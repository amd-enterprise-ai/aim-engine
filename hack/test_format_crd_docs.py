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


spec = importlib.util.spec_from_file_location(
    "format_crd_docs", Path(__file__).with_name("format-crd-docs.py")
)
formatter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(formatter)


class FormatCRDDocsTest(unittest.TestCase):
    def test_paragraphs(self):
        self.assertEqual(
            formatter.format_markdown("# Type\n\nFirst line\ncontinues here.\n\nNext paragraph.\n"),
            "# Type\n\nFirst line continues here.\n\nNext paragraph.\n",
        )

    def test_markdown_structure(self):
        text = "# Type\n\n- First item\n- Second item\n\n```yaml\nfoo:\n  bar: baz\n```\n\n    code\n"
        self.assertEqual(formatter.format_markdown(text), text)

    def test_wrapped_list_items(self):
        self.assertEqual(
            formatter.format_markdown("  - First item\n    continues here.\n  - Second item.\n"),
            "  - First item continues here.\n  - Second item.\n",
        )

    def test_table_prose_and_escaped_pipe(self):
        self.assertEqual(
            formatter.format_markdown("| `name` | A name<br />with a \\| character. | | Optional <br /> |\n"),
            "| `name` | A name with a \\| character. | | Optional |\n",
        )

    def test_description_lists(self):
        self.assertEqual(
            formatter.format_description("Origins:<br />  - discovered: from<br />    an image.<br />  - derived: from a set.<br />Defaults apply<br />when omitted."),
            "Origins:<br />- discovered: from an image.<br />- derived: from a set.<br />Defaults apply when omitted.",
        )

    def test_option_lists(self):
        self.assertEqual(
            formatter.format_description('Modes:<br />"" - default.<br />"partitioned" - any<br />partition.'),
            'Modes:<br />"" - default.<br />"partitioned" - any partition.',
        )

    def test_mapping_example(self):
        self.assertEqual(
            formatter.format_description("Example:<br />feature.node.kubernetes.io/model: Exists<br />Matches the<br />selected model."),
            "Example:<br />feature.node.kubernetes.io/model: Exists<br />Matches the selected model.",
        )

    def test_colon_in_prose(self):
        self.assertEqual(
            formatter.format_description("Matching works as follows:<br />the controller selects a profile."),
            "Matching works as follows: the controller selects a profile.",
        )
        self.assertEqual(
            formatter.format_description("The meaning depends on<br />AcceleratorPartitioningMode: each unit is<br />one slice."),
            "The meaning depends on AcceleratorPartitioningMode: each unit is one slice.",
        )

    def test_idempotent(self):
        text = "| `mode` | Modes:<br />- First.<br />- Second. | | Enum <br />Optional <br /> |\n\nA wrapped\nparagraph.\n"
        formatted = formatter.format_markdown(text)
        self.assertEqual(formatter.format_markdown(formatted), formatted)


if __name__ == "__main__":
    unittest.main()
