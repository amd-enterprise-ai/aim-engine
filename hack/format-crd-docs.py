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

"""Keep crd-ref-docs prose unwrapped while retaining Markdown structure."""

import re
import sys
from pathlib import Path


LIST_ITEM = re.compile(r'(?:[-*+] |\d+[.)] |"[^"]*"\s*(?:-|:))')
# Qualified label keys distinguish configuration examples from prose such as
# "Note:" or a wrapped sentence ending in "AcceleratorPartitioningMode:".
MAPPING = re.compile(r"^[A-Za-z0-9_./\\{}-]*[./][A-Za-z0-9_./\\{}-]*:\s")
MARKDOWN_LIST = re.compile(r"^(\s*)(?:[-*+] |\d+[.)] )")
BLOCK = re.compile(r"^(?:\s|#|>|[-*+] |\d+[.)] |---|___|\*\*\*)")
BREAK = re.compile(r"<br\s*/?>")


def format_description(text):
    parts = BREAK.split(text.strip())
    result = ""
    in_list = False
    after_mapping = False
    in_options = False
    for part in parts:
        value = part.strip()
        if not value:
            continue
        item = bool(LIST_ITEM.match(value))
        list_end = in_list and not item and part == part.lstrip()
        mapping = bool(MAPPING.match(value))
        options_end = in_options and not item and result.endswith(".") and value[0].isupper()
        separator = "<br />" if item or list_end or mapping or after_mapping or options_end else " "
        result += (separator if result else "") + value
        after_mapping = mapping
        if item or list_end:
            in_list = item and not value.startswith('"')
        if item or options_end:
            in_options = item and value.startswith('"')
    return result


def format_markdown(text):
    output = []
    paragraph = []
    fence = None
    list_indent = None

    def flush():
        if paragraph:
            output.append(" ".join(paragraph))
            paragraph.clear()

    for line in text.splitlines():
        stripped = line.strip()
        if fence:
            output.append(line)
            if stripped.startswith(fence):
                fence = None
        elif stripped.startswith(("```", "~~~")):
            flush()
            fence = stripped[:3]
            list_indent = None
            output.append(line)
        elif line.startswith("|"):
            flush()
            cells = re.split(r"(?<!\\)\|", line)
            if len(cells) >= 4:
                cells[2] = " " + format_description(cells[2]) + " "
                # Validation entries retain their deliberate list separators.
                cells[-2] = re.sub(r"(?:\s*<br\s*/?>)+\s*$", " ", cells[-2])
            output.append("|".join(cells))
            list_indent = None
        elif MARKDOWN_LIST.match(line):
            flush()
            list_indent = len(MARKDOWN_LIST.match(line).group(1))
            output.append(line)
        elif stripped and list_indent is not None and len(line) - len(line.lstrip()) > list_indent:
            output[-1] += " " + stripped
        elif not stripped or BLOCK.match(line):
            flush()
            output.append(line)
            list_indent = None
        else:
            paragraph.append(stripped)
            list_indent = None
    flush()
    return "\n".join(output).rstrip() + "\n"


if __name__ == "__main__":
    for filename in sys.argv[1:]:
        path = Path(filename)
        path.write_text(format_markdown(path.read_text(encoding="utf-8")), encoding="utf-8")
