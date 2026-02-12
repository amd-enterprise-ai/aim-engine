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

# ── Status patching helper ──────────────────────────────────
patch_download_status() {
    _protocol="$1" _attempt="$2" _total="$3" _message="$4"
    [ -z "${ARTIFACT_NAME:-}" ] && return 0
    [ -z "${ARTIFACT_NAMESPACE:-}" ] && return 0
    kubectl patch aimartifact "$ARTIFACT_NAME" -n "$ARTIFACT_NAMESPACE" \
        --type=merge --subresource=status \
        -p "{\"status\":{\"download\":{\"protocol\":\"${_protocol}\",\"attempt\":${_attempt},\"totalAttempts\":${_total},\"protocolSequence\":\"${AIM_DOWNLOADER_PROTOCOL:-}\",\"message\":\"${_message}\"}}}" \
        2>/dev/null || true
}

# ── Apply protocol env vars ─────────────────────────────────
apply_protocol() {
    case "$1" in
        XET)
            export HF_HUB_DISABLE_XET=0
            export HF_HUB_ENABLE_HF_TRANSFER=0
            ;;
        HF_TRANSFER)
            export HF_HUB_DISABLE_XET=1
            export HF_HUB_ENABLE_HF_TRANSFER=1
            ;;
        HTTP)
            export HF_HUB_DISABLE_XET=1
            export HF_HUB_ENABLE_HF_TRANSFER=0
            ;;
        *)
            echo "WARNING: Unknown protocol '$1'" >&2
            return 1
            ;;
    esac
}

# ── Clean incomplete files (between protocol switches) ──────
clean_incomplete() {
    _dir="$TARGET_DIR/.cache/huggingface/download"
    if [ -d "$_dir" ]; then
        _count=$(find "$_dir" -name "*.incomplete" 2>/dev/null | wc -l)
        if [ "$_count" -gt 0 ]; then
            find "$_dir" -name "*.incomplete" -delete 2>/dev/null || true
            echo "Cleaned $_count incomplete file(s)"
        fi
    fi
}

# ── Download + verify ───────────────────────────────────────
do_hf_download() {
    echo "Environment: HF_HUB_DISABLE_XET=${HF_HUB_DISABLE_XET:-unset} HF_HUB_ENABLE_HF_TRANSFER=${HF_HUB_ENABLE_HF_TRANSFER:-unset}"

    # Simulation mode: simulate success/failure without network
    if [ -n "${AIM_DEBUG_SIMULATE_HF_DOWNLOAD:-}" ]; then
        _current_proto="${HF_HUB_DISABLE_XET:-0}:${HF_HUB_ENABLE_HF_TRANSFER:-0}"
        # Determine the protocol name from env vars
        case "$_current_proto" in
            0:0) _proto_name="XET" ;;
            1:1) _proto_name="HF_TRANSFER" ;;
            1:0) _proto_name="HTTP" ;;
            *)   _proto_name="UNKNOWN" ;;
        esac
        # Check if this protocol should fail
        _fail_protos="${AIM_DEBUG_SIMULATE_HF_FAIL_PROTOCOLS:-}"
        if echo ",$_fail_protos," | grep -q ",$_proto_name,"; then
            echo "[SIMULATE] Download FAILING for protocol $_proto_name"
            sleep "${AIM_DEBUG_SIMULATE_HF_DURATION:-2}"
            return 1
        fi
        echo "[SIMULATE] Download SUCCEEDING for protocol $_proto_name"
        sleep "${AIM_DEBUG_SIMULATE_HF_DURATION:-2}"
        # Write simulated data so progress monitor has something to measure
        dd if=/dev/zero of="$TARGET_DIR/simulated_data" bs=1024 count=100 2>/dev/null
        return 0
    fi

    hf download --local-dir "$TARGET_DIR" "$MODEL_PATH"
}



# ═══════════════════════════════════════════════════════════════
#  Main logic
# ═══════════════════════════════════════════════════════════════

if [ -z "${AIM_DOWNLOADER_PROTOCOL:-}" ]; then
    # ── Legacy mode: single attempt, use whatever env vars are set ──
    echo "Downloading from Hugging Face: $MODEL_PATH to $TARGET_DIR"
    do_hf_download
    exit 0
fi

# ── Protocol sequence mode ──────────────────────────────────
total=$(echo "$AIM_DOWNLOADER_PROTOCOL" | awk -F',' '{print NF}')
attempt=0
last_protocol=""
remaining="$AIM_DOWNLOADER_PROTOCOL"

while [ -n "$remaining" ]; do
    protocol="${remaining%%,*}"
    if [ "$remaining" = "$protocol" ]; then
        remaining=""
    else
        remaining="${remaining#*,}"
    fi
    
    attempt=$((attempt + 1))
    
    echo ""
    echo "════════════════════════════════════════════════════════════"
    echo "  DOWNLOAD ATTEMPT $attempt/$total"
    echo "  Protocol: $protocol"
    echo "  Model:    $MODEL_PATH"
    echo "════════════════════════════════════════════════════════════"
    echo ""
    
    patch_download_status "$protocol" "$attempt" "$total" "Downloading with $protocol"
    
    # Clean .incomplete files on protocol switch
    if [ -n "$last_protocol" ] && [ "$protocol" != "$last_protocol" ]; then
        echo "Protocol switch: $last_protocol -> $protocol"
        clean_incomplete
    fi
    
    if ! apply_protocol "$protocol"; then
        last_protocol="$protocol"
        continue
    fi
    
    if do_hf_download; then
        echo ""
        echo "────────────────────────────────────────────────────────"
        echo "  SUCCESS: $protocol (attempt $attempt/$total)"
        echo "────────────────────────────────────────────────────────"
        
        patch_download_status "$protocol" "$attempt" "$total" "Verifying integrity..."
        exit 0
    fi
    
    echo ""
    echo "────────────────────────────────────────────────────────"
    echo "  FAILED: $protocol (attempt $attempt/$total)"
    echo "────────────────────────────────────────────────────────"
    echo ""
    
    patch_download_status "$protocol" "$attempt" "$total" "Failed with $protocol"
    last_protocol="$protocol"
done

echo ""
echo "════════════════════════════════════════════════════════════"
echo "  ALL $total DOWNLOAD ATTEMPTS EXHAUSTED"
echo "  Sequence: $AIM_DOWNLOADER_PROTOCOL"
echo "════════════════════════════════════════════════════════════"

patch_download_status "${last_protocol}" "$attempt" "$total" "All $total attempts exhausted"
exit 1