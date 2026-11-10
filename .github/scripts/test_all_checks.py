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

import importlib.util
import unittest
from pathlib import Path

import yaml

spec = importlib.util.spec_from_file_location("all_checks", Path(__file__).with_name("all-checks.py"))
all_checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(all_checks)


class FilterPatternTest(unittest.TestCase):
    # Cases follow GitHub's "Filter pattern cheat sheet" for paths.
    CASES = {
        "docs/**": (["docs/README.md", "docs/a/b.md"], ["README.md", "docsx/a.md"]),
        "docs/*": (["docs/README.md"], ["docs/a/b.md"]),
        "docs/**/*.md": (["docs/README.md", "docs/a/b/c.md"], ["docs/a/b.txt"]),
        "**/docs/**": (["docs/hello.md", "dir/docs/my-file.txt"], ["dir/doc/file.txt"]),
        "**/README.md": (["README.md", "js/README.md"], ["README.mdx"]),
        "**/*src/**": (["a/src/app.js", "my-src/code/js/app.js"], ["src.js"]),
        "*.jsx?": (["page.js", "page.jsx"], ["page.jsxx", "src/page.js"]),
        "**.js": (["index.js", "js/index.js"], ["index.json"]),
        "go.mod": (["go.mod"], ["x/go.mod", "go.modx"]),
    }

    def test_patterns(self):
        for pattern, (included, excluded) in self.CASES.items():
            for path in included:
                self.assertTrue(all_checks.matches(path, [pattern]), f"{pattern} should match {path}")
            for path in excluded:
                self.assertFalse(all_checks.matches(path, [pattern]), f"{pattern} should not match {path}")

    def test_later_patterns_win(self):
        patterns = ["sub/**", "!sub/docs/**", "sub/docs/keep.md"]
        self.assertTrue(all_checks.matches("sub/main.go", patterns))
        self.assertFalse(all_checks.matches("sub/docs/guide.md", patterns))
        self.assertTrue(all_checks.matches("sub/docs/keep.md", patterns))


class TriggerTest(unittest.TestCase):
    def filters(self, text):
        return all_checks.pull_request_filters(yaml.safe_load(text))

    def test_trigger_forms(self):
        self.assertEqual(self.filters("on: pull_request"), {})
        self.assertEqual(self.filters("on: [push, pull_request]"), {})
        self.assertEqual(self.filters("on:\n  pull_request:\n"), {})
        self.assertEqual(self.filters("on:\n  pull_request:\n    paths: [docs/**]\n"), {"paths": ["docs/**"]})
        self.assertIsNone(self.filters("on:\n  push:\n    branches: [main]\n"))

    def test_paths(self):
        filters = {"paths": ["docs/**", "go.mod"]}
        self.assertTrue(all_checks.is_triggered(filters, ["api/x.go", "go.mod"], "main", "synchronize"))
        self.assertFalse(all_checks.is_triggered(filters, ["api/x.go"], "main", "synchronize"))

    def test_paths_ignore(self):
        filters = {"paths-ignore": ["docs/**"]}
        self.assertFalse(all_checks.is_triggered(filters, ["docs/a.md"], "main", "opened"))
        self.assertTrue(all_checks.is_triggered(filters, ["docs/a.md", "api/x.go"], "main", "opened"))

    def test_branches_and_types(self):
        self.assertTrue(all_checks.is_triggered({"branches": ["release/**"]}, ["a"], "release/v1", "opened"))
        self.assertFalse(all_checks.is_triggered({"branches": ["release/**"]}, ["a"], "main", "opened"))
        self.assertFalse(all_checks.is_triggered({"branches-ignore": ["main"]}, ["a"], "main", "opened"))
        self.assertFalse(all_checks.is_triggered({"types": ["labeled"]}, ["a"], "main", "synchronize"))
        self.assertTrue(all_checks.is_triggered({}, ["a"], "main", "reopened"))


if __name__ == "__main__":
    unittest.main()
