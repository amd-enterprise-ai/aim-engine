#!/bin/sh
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

set -eu

URL="$1"
TARGET_DIR="$2"
MODEL_PATH="${URL#hf://}"


# Simulation mode: skip real verification
if [ -n "${AIM_DEBUG_SIMULATE_HF_DOWNLOAD:-}" ]; then
    echo "[SIMULATE] Verification PASSED"
    exit 0
fi

echo "Verifying download (this may take a while for large models)..."
hf cache verify \
    --local-dir "$TARGET_DIR" \
    --fail-on-missing-files \
    "$MODEL_PATH"
echo "Download complete and verified"
echo "Size of HF_HOME: $(du -sh "${HF_HOME:-$HOME/.cache/huggingface}" 2>/dev/null || echo 'N/A')"

