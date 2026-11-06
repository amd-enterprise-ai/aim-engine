#!/usr/bin/env python3
# MIT License
#
# Copyright (c) 2025 Advanced Micro Devices, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

"""Gate a pull request on every workflow its changed files trigger.

Evaluates the ``on.pull_request`` filters of each workflow in
``.github/workflows`` against the pull request's changed files, waits for the
latest run of every triggered workflow on the head commit, and exits non-zero
unless all of them pass.

Inputs come from the environment: ``GITHUB_REPOSITORY``, ``GITHUB_WORKFLOW_REF``,
``PR_NUMBER``, ``HEAD_SHA``, ``BASE_REF``, ``PR_ACTION``, and optionally
``POLL_INTERVAL_SECONDS`` and ``START_TIMEOUT_SECONDS``.
"""

import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

import yaml

WORKFLOWS_DIR = Path(".github/workflows")
DEFAULT_PR_TYPES = ("opened", "synchronize", "reopened")
PASSING_CONCLUSIONS = {"success", "neutral", "skipped"}
# GitHub evaluates path filters against only the first 300 changed files, so a
# larger pull request may not start a path-filtered workflow that matches.
PATH_FILTER_FILE_LIMIT = 300
MAX_CONSECUTIVE_API_ERRORS = 5


def as_list(value):
    return [value] if isinstance(value, str) else list(value or [])


def glob_to_regex(pattern):
    """Translate a GitHub Actions filter pattern into a regular expression."""
    out, i = [], 0
    while i < len(pattern):
        char = pattern[i]
        if pattern.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif pattern.startswith("**", i):
            out.append(".*")
            i += 2
        elif char == "*":
            out.append("[^/]*")
            i += 1
        elif char in "?+":
            # GitHub treats these as quantifiers on the preceding character.
            out.append(char)
            i += 1
        elif char == "[":
            end = pattern.index("]", i)
            out.append(pattern[i : end + 1])
            i = end + 1
        elif char == "\\" and i + 1 < len(pattern):
            out.append(re.escape(pattern[i + 1]))
            i += 2
        else:
            out.append(re.escape(char))
            i += 1
    return re.compile("".join(out))


def matches(value, patterns):
    """Apply filter patterns in order: a later ``!pattern`` excludes, a later pattern re-includes."""
    matched = False
    for pattern in as_list(patterns):
        negated = pattern.startswith("!")
        if glob_to_regex(pattern[1:] if negated else pattern).fullmatch(value):
            matched = not negated
    return matched


def pull_request_filters(workflow):
    """Return the workflow's ``on.pull_request`` filters, or None if pull requests do not trigger it."""
    # PyYAML implements YAML 1.1, which loads the bare key `on` as True.
    triggers = workflow.get("on", workflow.get(True))
    if isinstance(triggers, dict):
        if "pull_request" not in triggers:
            return None
        return triggers["pull_request"] or {}
    return {} if "pull_request" in as_list(triggers) else None


def is_triggered(filters, changed_files, base_ref, action):
    """Mirror GitHub's evaluation of ``on.pull_request`` filters for one pull request event."""
    if action not in as_list(filters.get("types", DEFAULT_PR_TYPES)):
        return False
    if "branches" in filters and not matches(base_ref, filters["branches"]):
        return False
    if "branches-ignore" in filters and matches(base_ref, filters["branches-ignore"]):
        return False
    if "paths" in filters:
        return any(matches(path, filters["paths"]) for path in changed_files)
    if "paths-ignore" in filters:
        return not all(matches(path, filters["paths-ignore"]) for path in changed_files)
    return True


def load_candidates(self_path):
    """Map each pull_request-triggered workflow file, except this gate, to its name and filters."""
    candidates = {}
    for path in sorted(WORKFLOWS_DIR.iterdir()):
        if path.suffix not in (".yml", ".yaml") or path.as_posix() == self_path:
            continue
        workflow = yaml.safe_load(path.read_text()) or {}
        filters = pull_request_filters(workflow)
        if filters is not None:
            candidates[path.as_posix()] = (workflow.get("name", path.as_posix()), filters)
    return candidates


def gh_api_lines(endpoint, jq):
    result = subprocess.run(
        ["gh", "api", "--paginate", endpoint, "--jq", jq],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.splitlines()


def changed_files(repo, pr_number):
    # Renamed files count only under their new path: predicting a workflow that
    # GitHub does not start fails the gate, while one GitHub starts but this
    # script did not predict is still gated once its run appears.
    return gh_api_lines(f"repos/{repo}/pulls/{pr_number}/files?per_page=100", ".[].filename")


def latest_runs(repo, head_sha):
    """Return the newest pull_request run of each workflow file for the head commit."""
    latest = {}
    for line in gh_api_lines(
        f"repos/{repo}/actions/runs?event=pull_request&head_sha={head_sha}&per_page=100",
        ".workflow_runs[] | @json",
    ):
        run = json.loads(line)
        if run["path"] not in latest or run["id"] > latest[run["path"]]["id"]:
            latest[run["path"]] = run
    return latest


def describe(run):
    if run is None:
        return "not started"
    return run["conclusion"] if run["status"] == "completed" else run["status"]


def write_summary(candidates, tracked, runs):
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary_path:
        return
    lines = ["| Workflow | Result |", "| --- | --- |"]
    for path in tracked:
        name = candidates[path][0]
        run = runs.get(path)
        label = f"[{name}]({run['html_url']})" if run else name
        lines.append(f"| {label} | {describe(run)} |")
    untriggered = sorted(candidates[path][0] for path in candidates.keys() - set(tracked))
    if untriggered:
        lines += ["", f"Not triggered by this change: {', '.join(untriggered)}"]
    with open(summary_path, "a", encoding="utf-8") as summary:
        summary.write("\n".join(lines) + "\n")


def main():
    repo = os.environ["GITHUB_REPOSITORY"]
    head_sha = os.environ["HEAD_SHA"]
    # GITHUB_WORKFLOW_REF is "<owner>/<repo>/<workflow path>@<ref>".
    self_path = os.environ["GITHUB_WORKFLOW_REF"].split("@")[0].removeprefix(f"{repo}/")
    poll_seconds = int(os.environ.get("POLL_INTERVAL_SECONDS", "60"))
    start_timeout = int(os.environ.get("START_TIMEOUT_SECONDS", "600"))

    candidates = load_candidates(self_path)
    files = changed_files(repo, os.environ["PR_NUMBER"])
    too_many_files = len(files) > PATH_FILTER_FILE_LIMIT
    if too_many_files:
        print(
            f"::warning::{len(files)} changed files exceed GitHub's {PATH_FILTER_FILE_LIMIT}-file path filter "
            "limit; path-filtered workflows are required only if they start."
        )
    required = {
        path
        for path, (_, filters) in candidates.items()
        if is_triggered(filters, files, os.environ["BASE_REF"], os.environ["PR_ACTION"])
        and not (too_many_files and ("paths" in filters or "paths-ignore" in filters))
    }
    print(f"{len(files)} changed files trigger: {', '.join(sorted(candidates[p][0] for p in required)) or 'none'}")

    started = time.monotonic()
    api_errors = 0
    previous = None
    while True:
        try:
            runs = {path: run for path, run in latest_runs(repo, head_sha).items() if path in candidates}
            api_errors = 0
        except subprocess.CalledProcessError as err:
            api_errors += 1
            if api_errors >= MAX_CONSECUTIVE_API_ERRORS:
                raise
            print(f"::warning::Listing workflow runs failed, retrying: {err.stderr.strip()}")
            time.sleep(poll_seconds)
            continue

        tracked = sorted(required | runs.keys())
        state = {path: describe(runs.get(path)) for path in tracked}
        if state != previous:
            print(", ".join(f"{candidates[path][0]}: {state[path]}" for path in tracked))
            previous = state

        completed = {path for path, run in runs.items() if run["status"] == "completed"}
        failed = [path for path in tracked if path in completed and state[path] not in PASSING_CONCLUSIONS]
        not_started = [path for path in tracked if path not in runs]
        timed_out = bool(not_started) and time.monotonic() - started > start_timeout
        if failed or timed_out or set(tracked) <= completed:
            break
        time.sleep(poll_seconds)

    write_summary(candidates, tracked, runs)
    for path in failed:
        print(f"::error::{candidates[path][0]} concluded {state[path]}: {runs[path]['html_url']}")
    if timed_out:
        for path in not_started:
            print(f"::error::{candidates[path][0]} did not start within {start_timeout}s for {head_sha}")
    if failed or timed_out:
        return 1
    print("All triggered workflows passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
