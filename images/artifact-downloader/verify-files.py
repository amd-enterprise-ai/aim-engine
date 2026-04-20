# MIT License

# Copyright (c) 2026 Advanced Micro Devices, Inc.

# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:

# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.

# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

"""Post-download file presence verification and fsync.

Fetches the expected file list from HuggingFace (using the same library and
filter logic as check-hf-size.py), verifies every expected file exists on disk
with non-zero size, and fsyncs all files to ensure persistence on network
filesystems.
"""

import os
import sys
from fnmatch import fnmatch

from huggingface_hub import HfApi
from huggingface_hub.utils import GatedRepoError, RepositoryNotFoundError

MODEL_PATH = os.environ["MODEL_PATH"]
TARGET_DIR = os.environ["TARGET_DIR"]


def parse_patterns(env_var):
    raw = os.environ.get(env_var, "")
    return [p.strip() for p in raw.split(",") if p.strip()]


def apply_filter(siblings):
    include = parse_patterns("AIM_HF_INCLUDE")
    exclude = parse_patterns("AIM_HF_EXCLUDE")
    if include:
        siblings = [f for f in siblings if any(fnmatch(f.rfilename, p) for p in include)]
    if exclude:
        siblings = [f for f in siblings if not any(fnmatch(f.rfilename, p) for p in exclude)]
    return siblings


def fsync_path(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def fmt_size(n):
    """Human-readable size string. Returns '?' for None."""
    if n is None:
        return "?"
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if abs(n) < 1024:
            return f"{n:.1f} {unit}" if unit != "B" else f"{n} B"
        n /= 1024
    return f"{n:.1f} PB"


def safe_stat_size(path):
    """Return file size in bytes, or None on any error."""
    try:
        return os.path.getsize(path)
    except OSError:
        return None


def main():
    if os.environ.get("AIM_DEBUG_SIMULATE_HF_DOWNLOAD"):
        print("[SIMULATE] File verification PASSED")
        return

    try:
        info = HfApi().model_info(MODEL_PATH, files_metadata=True)
    except RepositoryNotFoundError:
        print(f"VERIFY FAILED: Repository not found: {MODEL_PATH}", file=sys.stderr)
        sys.exit(1)
    except GatedRepoError:
        print(f"VERIFY FAILED: Cannot access gated repo: {MODEL_PATH}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"VERIFY FAILED: Could not fetch model info: {e}", file=sys.stderr)
        sys.exit(1)

    siblings = apply_filter(info.siblings)

    errors = []
    synced = 0
    total_expected = 0
    total_actual = 0

    print(f"Verifying {len(siblings)} expected files in {TARGET_DIR}")
    print("─" * 72)

    for f in siblings:
        expected_size = f.size  # may be None for some repo types
        path = os.path.join(TARGET_DIR, f.rfilename)

        if expected_size is not None:
            total_expected += expected_size

        if not os.path.exists(path):
            errors.append(f"MISSING: {f.rfilename} (expected {fmt_size(expected_size)})")
            print(f"  MISSING  {f.rfilename}  expected={fmt_size(expected_size)}")
            continue

        actual_size = safe_stat_size(path)
        if actual_size is not None:
            total_actual += actual_size

        if actual_size == 0:
            errors.append(f"EMPTY: {f.rfilename} (expected {fmt_size(expected_size)})")
            print(f"  EMPTY    {f.rfilename}  expected={fmt_size(expected_size)}")
            continue

        if expected_size is not None and actual_size is not None and actual_size != expected_size:
            print(
                f"  WARN     {f.rfilename}  "
                f"size mismatch: expected={fmt_size(expected_size)}  actual={fmt_size(actual_size)}"
                f" (may be filesystem-related; deferring to integrity check)"
            )

        try:
            fsync_path(path)
        except OSError as e:
            print(f"  WARN: fsync failed for {f.rfilename}: {e}", file=sys.stderr)

        synced += 1
        print(f"  OK       {f.rfilename}  {fmt_size(actual_size)}")

    print("─" * 72)

    if errors:
        print(
            f"VERIFY FAILED: {len(errors)} file(s) with errors "
            f"(total expected {fmt_size(total_expected)}, "
            f"got {fmt_size(total_actual)} on disk):",
            file=sys.stderr,
        )
        for e in errors:
            print(f"  {e}", file=sys.stderr)
        sys.exit(1)

    # fsync directory entries so renames/creates are persisted
    for dirpath, _dirnames, _filenames in os.walk(TARGET_DIR):
        try:
            fsync_path(dirpath)
        except OSError as e:
            print(f"WARN: fsync failed for directory {dirpath}: {e}", file=sys.stderr)

    print(
        f"Verified {synced} files present and synced "
        f"(total {fmt_size(total_actual)}, expected {fmt_size(total_expected)})"
    )


if __name__ == "__main__":
    main()
