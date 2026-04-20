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

# ── Build --include/--exclude flags from env vars ────────────
build_filter_args() {
    _args=""
    _old_ifs="$IFS"; IFS=','
    set -f  # disable pathname expansion — patterns like */* must stay literal
    for _p in ${AIM_HF_INCLUDE:-}; do
        [ -n "$_p" ] && _args="$_args --include $_p"
    done
    for _p in ${AIM_HF_EXCLUDE:-}; do
        [ -n "$_p" ] && _args="$_args --exclude $_p"
    done
    set +f
    IFS="$_old_ifs"
    echo "$_args"
}

# ── Download + verify ───────────────────────────────────────
do_hf_download() {
    echo "Environment: HF_HUB_DISABLE_XET=${HF_HUB_DISABLE_XET:-unset} HF_HUB_ENABLE_HF_TRANSFER=${HF_HUB_ENABLE_HF_TRANSFER:-unset}"
    echo "Download filter: include=[${AIM_HF_INCLUDE:-}] exclude=[${AIM_HF_EXCLUDE:-}]"
    FILTER_ARGS=$(build_filter_args)

    # Simulation mode: shadow the real `hf` CLI with a shell function so
    # the rest of this function (set -f / exit-code capture / set +f /
    # return) runs exactly as in production. An early `return` here would
    # bypass the code path the original bug lived in, making the test
    # unable to catch a regression of that bug.
    if [ -n "${AIM_DEBUG_SIMULATE_HF_DOWNLOAD:-}" ]; then
        _current_proto="${HF_HUB_DISABLE_XET:-0}:${HF_HUB_ENABLE_HF_TRANSFER:-0}"
        case "$_current_proto" in
            0:0) _proto_name="XET" ;;
            1:1) _proto_name="HF_TRANSFER" ;;
            1:0) _proto_name="HTTP" ;;
            *)   _proto_name="UNKNOWN" ;;
        esac
        hf() {
            _fail_protos="${AIM_DEBUG_SIMULATE_HF_FAIL_PROTOCOLS:-}"
            _hang_protos="${AIM_DEBUG_SIMULATE_HF_HANG_PROTOCOLS:-}"
            # HANG mode: real python sleep that progress-monitor can pkill.
            # Lets tests exercise stall-kill + retry monitoring end-to-end.
            if echo ",$_hang_protos," | grep -q ",$_proto_name,"; then
                echo "[SIMULATE] Download HANGING for protocol $_proto_name"
                python -c "import time; time.sleep(${AIM_DEBUG_SIMULATE_HF_HANG_DURATION:-300})"
                return $?
            fi
            sleep "${AIM_DEBUG_SIMULATE_HF_DURATION:-2}"
            if echo ",$_fail_protos," | grep -q ",$_proto_name,"; then
                echo "[SIMULATE] Download FAILING for protocol $_proto_name (rc=137)"
                # Mimic a SIGKILL-style non-zero exit so the outer
                # exit-code propagation is under test.
                return 137
            fi
            echo "[SIMULATE] Download SUCCEEDING for protocol $_proto_name"
            dd if=/dev/zero of="$TARGET_DIR/simulated_data" bs=1024 count=100 2>/dev/null
            return 0
        }
    fi

    # Capture the hf CLI exit code explicitly so we dont return the result of set
    set -f  # prevent glob expansion of filter patterns (e.g. */*) during word splitting
    _hf_rc=0
    hf download --local-dir "$TARGET_DIR" $FILTER_ARGS "$MODEL_PATH" || _hf_rc=$?
    set +f
    return "$_hf_rc"
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