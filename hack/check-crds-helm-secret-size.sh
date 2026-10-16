#!/usr/bin/env bash
#
# Estimate the Kubernetes Secret size of a Helm CRDs release.
#
# Helm stores a release as Secret data["release"] = base64(gzip(json(release))).
# Kubernetes rejects the Secret when len("release") + len(that value) exceeds
# 1 MiB. Python's gzip does not match Go's compressor, so the gzip step uses
# compress/gzip at BestCompression, which is what Helm uses.
#
# The chart templates must be static YAML. This check does not render {{ }}.
#
# Usage:
#   hack/check-crds-helm-secret-size.sh CHART_DIR
#   hack/check-crds-helm-secret-size.sh --match-secret [--context CTX] CHART_DIR
#
# --match-secret reads sh.helm.release.v1.<release>.v1 and reuses its
# timestamps, status, and description so the computed size can be compared
# with the Secret Kubernetes stored.

set -euo pipefail

LIMIT=1048576
RELEASE_NAME="aim-engine-crds"
NAMESPACE="default"
KUBE_CONTEXT="${KUBE_CONTEXT:-}"
MATCH_SECRET=0

usage() {
    echo "Usage: $0 [--match-secret] [--context CTX] [--namespace NS] [--release NAME] CHART_DIR" >&2
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --match-secret) MATCH_SECRET=1; shift ;;
        --context) KUBE_CONTEXT="${2:-}"; shift 2 ;;
        --namespace) NAMESPACE="${2:-}"; shift 2 ;;
        --release) RELEASE_NAME="${2:-}"; shift 2 ;;
        -h|--help) usage ;;
        --) shift; break ;;
        -*) echo "unknown flag: $1" >&2; usage ;;
        *) break ;;
    esac
done

CHART_DIR="${1:-}"
[[ -n "${CHART_DIR}" && -f "${CHART_DIR}/Chart.yaml" && -d "${CHART_DIR}/templates" ]] || usage

if grep -R -q '{{' "${CHART_DIR}/templates"; then
    echo "templates contain '{{'; this check only measures static YAML" >&2
    exit 2
fi

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

FIRST_DEPLOYED="2026-01-01T00:00:00Z"
LAST_DEPLOYED="2026-01-01T00:00:00Z"
DESCRIPTION="Install complete"
STATUS="deployed"
LIVE_SIZE=""

if [[ "${MATCH_SECRET}" -eq 1 ]]; then
    kubectl_args=()
    if [[ -n "${KUBE_CONTEXT}" ]]; then
        kubectl_args+=(--context "${KUBE_CONTEXT}")
    fi
    kubectl "${kubectl_args[@]}" -n "${NAMESPACE}" get secret "sh.helm.release.v1.${RELEASE_NAME}.v1" -o json > "${WORK_DIR}/secret.json"
    eval "$(python3 - "${WORK_DIR}/secret.json" <<'PY'
import base64, gzip, json, shlex, sys
secret = json.load(open(sys.argv[1]))
stored = base64.b64decode(secret["data"]["release"])
release = json.loads(gzip.decompress(base64.b64decode(stored)))
info = release["info"]
vals = {
    "FIRST_DEPLOYED": info["first_deployed"],
    "LAST_DEPLOYED": info["last_deployed"],
    "DESCRIPTION": info.get("description", ""),
    "STATUS": info.get("status", ""),
    "NAMESPACE": release.get("namespace", ""),
    "RELEASE_NAME": release["name"],
    "LIVE_SIZE": str(len(b"release") + len(stored)),
}
for key, value in vals.items():
    print(f"{key}={shlex.quote(value)}")
PY
)"
fi

python3 - "${CHART_DIR}" "${WORK_DIR}/release.json" "${RELEASE_NAME}" "${NAMESPACE}" "${FIRST_DEPLOYED}" "${LAST_DEPLOYED}" "${DESCRIPTION}" "${STATUS}" <<'PY'
import base64, json, re, sys
from pathlib import Path

chart_dir, out_path, release_name, namespace, first, last, description, status = sys.argv[1:]

def chart_value(text, key):
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or ":" not in stripped:
            continue
        name, value = stripped.split(":", 1)
        if name.strip() == key:
            return value.strip().strip("'").strip('"')
    raise SystemExit(f"Chart.yaml is missing {key}")

def go_string(value):
    out = ['"']
    for ch in value:
        code = ord(ch)
        if ch == '"':
            out.append('\\"')
        elif ch == "\\":
            out.append("\\\\")
        elif ch == "\b":
            out.append("\\b")
        elif ch == "\f":
            out.append("\\f")
        elif ch == "\n":
            out.append("\\n")
        elif ch == "\r":
            out.append("\\r")
        elif ch == "\t":
            out.append("\\t")
        elif ch in "<>&" or code < 0x20 or ch in "\u2028\u2029":
            out.append("\\u%04x" % code)
        else:
            out.append(ch)
    out.append('"')
    return "".join(out)

chart = Path(chart_dir)
meta_text = (chart / "Chart.yaml").read_text()
meta = {key: chart_value(meta_text, key) for key in ("name", "version", "description", "apiVersion", "appVersion", "type")}
templates = sorted(p for p in (chart / "templates").iterdir() if p.is_file() and not p.name.startswith("_"))
if not templates:
    raise SystemExit("chart has no templates")

manifests = []
template_json = []
for path in templates:
    raw = path.read_bytes()
    text = raw.decode()
    docs = [part for part in re.split(r"(?m)^---\n", text) if part.strip()]
    source = f"{meta['name']}/{path.relative_to(chart).as_posix()}"
    manifests.extend(f"---\n# Source: {source}\n{doc}" for doc in docs)
    template_json.append(
        '{"name":' + go_string(path.relative_to(chart).as_posix()) +
        ',"data":' + go_string(base64.b64encode(raw).decode()) + "}"
    )

encoded = (
    '{"name":' + go_string(release_name) +
    ',"info":{"first_deployed":' + go_string(first) +
    ',"last_deployed":' + go_string(last) +
    ',"deleted":' + go_string("") +
    ',"description":' + go_string(description) +
    ',"status":' + go_string(status) +
    '},"chart":{"metadata":{"name":' + go_string(meta["name"]) +
    ',"version":' + go_string(meta["version"]) +
    ',"description":' + go_string(meta["description"]) +
    ',"apiVersion":' + go_string(meta["apiVersion"]) +
    ',"appVersion":' + go_string(meta["appVersion"]) +
    ',"type":' + go_string(meta["type"]) +
    '},"lock":null,"templates":[' + ",".join(template_json) +
    '],"values":null,"schema":null,"files":null},"manifest":' +
    go_string("".join(manifests)) +
    ',"version":1,"namespace":' + go_string(namespace) + "}"
)
Path(out_path).write_bytes(encoded.encode())
PY

cat > "${WORK_DIR}/gzip.go" <<'EOF'
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	payload, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		panic(err)
	}
	if _, err = writer.Write(payload); err != nil {
		panic(err)
	}
	if err = writer.Close(); err != nil {
		panic(err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	secretSize := len("release") + len(encoded)
	fmt.Printf("%d\n", secretSize)
}
EOF

SECRET_SIZE="$(go run "${WORK_DIR}/gzip.go" "${WORK_DIR}/release.json")"
HEADROOM="$((LIMIT - SECRET_SIZE))"

echo "secret data bytes: ${SECRET_SIZE}"
echo "limit:             ${LIMIT}"
echo "headroom:          ${HEADROOM}"

if [[ -n "${LIVE_SIZE}" ]]; then
    echo "kubernetes secret: ${LIVE_SIZE}"
    if [[ "${SECRET_SIZE}" != "${LIVE_SIZE}" ]]; then
        echo "computed size does not match the Secret stored in Kubernetes" >&2
        exit 1
    fi
    echo "match: kubernetes Secret size"
fi

if (( SECRET_SIZE > LIMIT )); then
    echo "Helm release Secret would be rejected" >&2
    exit 1
fi
