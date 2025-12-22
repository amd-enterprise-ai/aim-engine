#!/bin/sh
# Model download script
# Downloads a model from a source URI and performs cleanup.
#
# Environment variables:
#   AIM_DEBUG_CAUSE_FAILURE - If set, causes the script to fail (for testing)
#   MOUNT_PATH - Path where the model will be downloaded
#   SOURCE_URI - URI of the model to download

# Bail out if AIM_DEBUG_CAUSE_FAILURE is set
if [ -n "${AIM_DEBUG_CAUSE_FAILURE:-}" ]; then
	echo "AIM_DEBUG_CAUSE_FAILURE is set, bailing out"
	exit 1
fi

# Set umask so downloaded files are readable by others
umask 0022


## Record size before download
size_before=$(du -sb "$MOUNT_PATH" 2>/dev/null | cut -f1)
echo "Size before download: $size_before bytes"

# Download the model
python /storage-initializer/scripts/initializer-entrypoint "$SOURCE_URI" "$MOUNT_PATH"
download_exit_code=$?

# Check if download command failed
if [ $download_exit_code -ne 0 ]; then
	echo "ERROR: storage-initializer exited with code $download_exit_code"
	exit $download_exit_code
fi

# Verify that files were actually downloaded by checking size increase
size_after=$(du -sb "$MOUNT_PATH" 2>/dev/null | cut -f1)
echo "Size after download: $size_after bytes"

size_increase=$((size_after - size_before))
echo "Size increase: $size_increase bytes"

# Require at least 1KB of new data (to avoid false positives from metadata/dirs)
if [ $size_increase -lt 1024 ]; then
	echo "ERROR: Model download failed - no data was downloaded (size increase: $size_increase bytes)"
	exit 1
fi

echo "Model download successful: $size_increase bytes downloaded"
