#!/usr/bin/env python3
"""
AcceleratorDetector NFD feature writer.

Detects hardware accelerators using aim-runtime and writes NFD feature
files following the value-in-key label pattern. Each hardware
identifier becomes a separate label key; the value carries the
accelerator count.

NFD's local source picks up the feature file and publishes the labels
on the node automatically.

Environment variables:
  DETECT_TYPE      - "gpu", "cpu", or "all" (default: "gpu")
  DETECT_INTERVAL  - seconds between re-detection cycles (default: 300)
  NODE_NAME        - node name for logging (injected via downward API)
"""

import json
import logging
import os
import subprocess
import sys
import time

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger("accelerator-detector")

FEATURES_DIR = "/nfd-features"
# Each DaemonSet writes its own feature file so CPU and GPU detectors can run
# concurrently on the same node without clobbering each other. NFD merges
# labels across all files in features.d/.
FEATURE_FILE_TEMPLATE = "aim-accelerator-{}"
LABEL_PREFIX = "feature.node.kubernetes.io/aim-accelerator"
HEALTH_FILE = "/tmp/healthy"


def detect_hardware(detect_type: str) -> list:
    """Run aim-runtime detect-hardware and return the parsed JSON output."""
    cmd = ["aim-runtime", "detect-hardware", "--type", detect_type, "--format", "json"]
    logger.info("Running: %s", " ".join(cmd))
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode != 0:
            logger.error("detect-hardware failed (exit %d): %s", result.returncode, result.stderr.strip())
            if result.stdout.strip():
                logger.error("stdout: %s", result.stdout.strip())
            if result.stderr.strip():
                logger.error("stderr: %s", result.stderr.strip())
            return []
        detections = json.loads(result.stdout)
        for det in detections:
            logger.info(
                "Detected: type=%s model=%s count=%s",
                det.get("accelerator_type", "?"),
                det.get("accelerator_model", "?"),
                det.get("accelerator_count", "?"),
            )
        if not detections:
            logger.warning("detect-hardware returned empty results")
            logger.debug("Raw output: %s", result.stdout.strip())
        return detections
    except subprocess.TimeoutExpired:
        logger.error("detect-hardware timed out after 120s")
        return []
    except (json.JSONDecodeError, Exception) as exc:
        logger.error("Failed to parse detect-hardware output: %s", exc)
        return []


def build_feature_lines(detections: list) -> list:
    """Convert detection results into NFD feature file lines.

    Each detection dict has: accelerator_type, accelerator_model, accelerator_count.
    We produce one label per detection with the accelerator count as value.
    """
    lines = []
    seen = set()
    for det in detections:
        model = det.get("accelerator_model")
        count = det.get("accelerator_count", 0)
        if not model or model in seen:
            continue
        seen.add(model)
        lines.append(f"{LABEL_PREFIX}.{model}={count}")

    if lines:
        logger.info("Labels: %s", ", ".join(lines))

    return lines


def write_feature_file(lines: list, detect_type: str) -> None:
    """Atomically write the NFD feature file.

    Writes to a dot-prefixed temp file then renames, per NFD docs, to
    avoid race conditions with the NFD worker. The filename is namespaced
    by detect_type so the CPU and GPU DaemonSets can co-exist on the same
    node without clobbering each other's features.
    """
    filename = FEATURE_FILE_TEMPLATE.format(detect_type)
    target = os.path.join(FEATURES_DIR, filename)
    tmp_path = os.path.join(FEATURES_DIR, f".{filename}")

    content = "# Written by aim-accelerator-detector\n"
    if lines:
        content += "\n".join(lines) + "\n"

    try:
        with open(tmp_path, "w") as f:
            f.write(content)
        os.rename(tmp_path, target)
        logger.info("Wrote %d label(s) to %s", len(lines), target)
    except OSError as exc:
        logger.error("Failed to write feature file: %s", exc)


def main():
    detect_type = os.environ.get("DETECT_TYPE", "gpu")
    interval = int(os.environ.get("DETECT_INTERVAL", "300"))
    node_name = os.environ.get("NODE_NAME", "unknown")

    logger.info(
        "Starting accelerator-detector on node=%s type=%s interval=%ds",
        node_name, detect_type, interval,
    )

    while True:
        detections = detect_hardware(detect_type)
        lines = build_feature_lines(detections)
        if lines:
            write_feature_file(lines, detect_type)
        else:
            logger.warning("No accelerators detected")
            write_feature_file([], detect_type)

        open(HEALTH_FILE, "w").close()

        logger.info("Sleeping %ds before next detection cycle", interval)
        time.sleep(interval)


if __name__ == "__main__":
    main()
