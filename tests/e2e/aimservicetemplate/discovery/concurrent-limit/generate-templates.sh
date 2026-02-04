#!/bin/bash
# Generate templates.yaml with a specified number of AIMServiceTemplate resources
#
# Usage: ./generate-templates.sh [count]
#   count: Number of templates to generate (default: 30)

set -e

COUNT=${1:-30}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_FILE="${SCRIPT_DIR}/templates.yaml"

echo "Generating ${COUNT} templates in ${OUTPUT_FILE}..."

# Clear the file
> "${OUTPUT_FILE}"

for i in $(seq -w 1 "$COUNT"); do
  # Add separator for all but the first
  if [ "$i" != "01" ]; then
    echo "---" >> "${OUTPUT_FILE}"
  fi

  cat >> "${OUTPUT_FILE}" <<EOF
apiVersion: aim.eai.amd.com/v1alpha1
kind: AIMServiceTemplate
metadata:
  name: test-template-concurrent-${i}
spec:
  modelName: test-model-concurrent
  hardware:
    gpu:
      requests: 1
      model: MI300X
EOF
done

echo "Generated ${COUNT} templates"
