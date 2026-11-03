#!/usr/bin/env python3
"""
AcceleratorDetector NFD feature writer.

Detects hardware accelerators using aim-runtime and writes NFD feature
files following the value-in-key label pattern. Each hardware
identifier becomes a separate label key; the value carries the
accelerator count.

NFD's local source picks up the feature file and publishes the labels
on the node automatically.

For GPU nodes the detector also reads the current partition state from
`amd-smi partition --current --json` and publishes per-scheme labels under
the `partitioning-scheme.*` axis. The kernel-applied partition
state reported by amd-smi is the single source of truth; DCM labels are not
consulted. Partition detection is best-effort: any failure falls back to the
`partitioning-scheme.default` sentinel and never blocks model/family labels.

Environment variables:
  DETECT_TYPE        - "gpu", "cpu", or "all" (default: "gpu")
  ACCELERATOR_VENDOR - "AMD" or "NVIDIA" (default: "AMD"). Selects the detection
                       backend (AMD -> aim-runtime/amd-smi, NVIDIA -> nvidia-smi)
                       and is stamped onto the per-type vendor label axis.
  DETECT_INTERVAL    - seconds between re-detection cycles (default: 10)
  NODE_NAME          - node name for logging (injected via downward API)
"""

from __future__ import annotations

import json
import logging
import os
import re
import shutil
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

# Vendor axis. Each DaemonSet is vendor-specific and stamps its vendor onto a
# per-accelerator-type sub-axis so cross-vendor nodes (e.g. an NVIDIA GPU on an
# AMD EPYC host) carry one vendor key per type without conflict:
#   feature.node.kubernetes.io/aim-accelerator.vendor.GPU.NVIDIA=8
#   feature.node.kubernetes.io/aim-accelerator.vendor.CPU.AMD=1
# Consumers match on key existence (Exists), so vendor is a per-type label
# rather than a single node-wide one. Assumes one vendor (and one model) per
# accelerator type per node.
#
# AIMProfile.spec.acceleratorVendor consumes this axis to select both node
# affinity and the corresponding whole-device resource (amd.com/gpu or
# nvidia.com/gpu). Partition-specific AMD resource names remain a future
# extension of that resolver.
VENDOR_LABEL_INFIX = "vendor"
DEFAULT_VENDOR = "AMD"
NVIDIA_VENDOR = "NVIDIA"
# Vendors this detector knows how to handle. main() fails fast on anything else
# so a typo (e.g. "NVDA") can't silently route to the wrong detection backend.
SUPPORTED_VENDORS = (DEFAULT_VENDOR, NVIDIA_VENDOR)

# Model/family detection lives in the runtime's `detect-hardware` subcommand.
# The canonical way to reach it is the `aim-runtime` console script, but it is
# not present on every base image: aim-base:0.12 ships the runtime as source
# under /workspace without installing the console script. Invoking the
# entrypoint module by path reaches the exact same code (the entrypoint adds
# /workspace to sys.path and aim_runtime is already on PYTHONPATH), so we use it
# as a fallback. The partition axis (amd-smi) is unaffected by this.
ENTRYPOINT_PATH = "/workspace/entrypoint.py"

# Partition labels live in their OWN feature file (aim-accelerator-gpu-partition)
# rather than alongside the model/family labels. The model axis is driven by
# `detect-hardware` and the partition axis by `amd-smi`; the two sources fail
# independently, so keeping them in separate files gives each its own sticky
# lifecycle and guarantees a failure of one never clobbers the other. NFD merges
# all files in features.d/, so the published labels are identical to before.
PARTITION_FILE_KIND = "gpu-partition"

# Partition axis. A single value-in-key namespace carries both the
# canonical-unpartitioned sentinel and the explicit per-mode schemes so the
# AIMProfile resolver stays string-blind (Exists / DoesNotExist on a key).
PARTITION_LABEL_PREFIX = f"{LABEL_PREFIX}.partitioning-scheme"

# Hardware-agnostic sentinel meaning "canonical unpartitioned state, or this
# hardware doesn't expose partitioning at all". Load-bearing axis for
# cross-hardware unpartitioned matching (matches MI300X-SPX-NPS1 and Radeon).
PARTITION_DEFAULT = "default"

# AMD's stable name for the canonical-unpartitioned scheme on Instinct: SPX =
# "Single Partition X-celerator" (one compute partition), NPS1 = "1 NUMA Per
# Socket" (one memory region). When amd-smi reports this scheme we additionally
# emit the `default` sentinel. A future Instinct generation that renames its
# canonical-unpartitioned scheme adds one constant here; nothing else moves.
CANONICAL_DEFAULT_SCHEME = "SPX-NPS1"


def detection_command() -> list:
    """Resolve the base command used to run hardware detection.

    Prefer the `aim-runtime` console script when it is installed on PATH;
    otherwise fall back to executing the entrypoint module directly. The
    fallback keeps detection working on images that ship the runtime source
    without the console script (e.g. aim-base:0.12).
    """
    if shutil.which("aim-runtime"):
        return ["aim-runtime"]
    if os.path.exists(ENTRYPOINT_PATH):
        return [sys.executable, ENTRYPOINT_PATH]
    # Nothing resolvable; return the console-script name so detect_hardware()
    # surfaces a clear "command not found" failure.
    return ["aim-runtime"]


def detect_hardware(detect_type: str) -> list | None:
    """Run detect-hardware and return the parsed JSON output.

    Return value carries a crucial distinction:
      - a list (possibly empty) => detection SUCCEEDED. An empty list is a
        confident "this node has no such accelerator".
      - None => detection FAILED this cycle (tool missing, non-zero exit,
        timeout, or unparseable output). The caller MUST NOT treat this as
        "no hardware": overwriting a previously-good feature file with empty
        results on a transient failure is what makes the published node labels
        (and therefore every consumer's profile/model availability) flap.
    """
    cmd = detection_command() + ["detect-hardware", "--type", detect_type, "--format", "json"]
    logger.info("Running: %s", " ".join(cmd))
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode != 0:
            logger.error("detect-hardware failed (exit %d): %s", result.returncode, result.stderr.strip())
            if result.stdout.strip():
                logger.error("stdout: %s", result.stdout.strip())
            if result.stderr.strip():
                logger.error("stderr: %s", result.stderr.strip())
            return None
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
        return None
    except Exception as exc:
        logger.error("Failed to parse detect-hardware output: %s", exc)
        return None


# Canonical NVIDIA datacenter GPU tokens. nvidia-smi reports verbose product
# names ("NVIDIA H100 80GB HBM3", "NVIDIA A100-SXM4-80GB", "Tesla T4"); we reduce
# them to a stable token used as the label key and matched by AIMProfiles. Listed
# most-specific-first so e.g. GH200 wins over H200 and L40S over L40. A boundary
# check keeps A10 from matching inside A100.
_NVIDIA_MODEL_TOKENS = (
    "GH200", "H200", "H100", "B200", "A100", "A40", "A30", "A10",
    "L40S", "L40", "L4", "V100", "T4",
)

# Anything outside the k8s label-key alphabet collapses to '_' in the fallback.
_LABEL_UNSAFE = re.compile(r"[^A-Za-z0-9_.-]+")


def _normalize_nvidia_rtx_gpu_name(upper: str) -> str | None:
    """Normalize GeForce/workstation RTX product names to compact tokens.

    Consumer/workstation naming is messy and a moving target, so rather than
    enumerate every SKU we collapse the common shapes with patterns (anything
    unrecognized still falls through to the slug):
      GeForce RTX 4090          -> RTX4090
      GeForce RTX 4080 SUPER    -> RTX4080Super
      GeForce RTX 4070 Ti SUPER -> RTX4070TiSuper
      GeForce RTX 3090 Ti       -> RTX3090Ti
      RTX A6000                 -> RTXA6000   (workstation Ampere)
      RTX 6000 Ada Generation   -> RTX6000Ada (workstation Ada)
    """
    # Workstation Ada: "RTX 6000 ADA [GENERATION]" (check before the bare
    # GeForce pattern so the "ADA" suffix isn't dropped).
    m = re.search(r"(?<![A-Z0-9])RTX\s+(\d{3,4})\s+ADA(?![A-Z0-9])", upper)
    if m:
        return f"RTX{m.group(1)}Ada"
    # Workstation Ampere: "RTX A6000".
    m = re.search(r"(?<![A-Z0-9])RTX\s+A(\d{3,4})(?![A-Z0-9])", upper)
    if m:
        return f"RTXA{m.group(1)}"
    # GeForce consumer: "RTX 4090 [TI] [SUPER]".
    m = re.search(r"(?<![A-Z0-9])RTX\s+(\d{3,4})(\s+TI)?(\s+SUPER)?(?![A-Z0-9])", upper)
    if m:
        token = f"RTX{m.group(1)}"
        if m.group(2):
            token += "Ti"
        if m.group(3):
            token += "Super"
        return token
    return None


def _normalize_nvidia_model(raw: str) -> str:
    """Reduce an nvidia-smi product name to a canonical label token.

    Recognized datacenter parts map to their short token (H100, A100, ...);
    consumer/workstation RTX cards are normalized by pattern (RTX4090, RTXA6000,
    ...) so "try it on a workstation GPU" produces clean labels too.
    Unrecognized names fall back to a sanitized, label-key-safe slug so the node
    is still labelled (and we log a warning so the token table can be extended).
    """
    upper = raw.strip().upper()
    for token in _NVIDIA_MODEL_TOKENS:
        if re.search(rf"(?<![A-Z0-9]){re.escape(token)}(?![A-Z0-9])", upper):
            return token
    rtx_token = _normalize_nvidia_rtx_gpu_name(upper)
    if rtx_token:
        return rtx_token
    slug = _LABEL_UNSAFE.sub("_", raw.strip()).strip("_.-")[:63] or "UNKNOWN"
    logger.warning("Unrecognized NVIDIA GPU name %r; using sanitized label %r", raw, slug)
    return slug


def detect_hardware_nvidia() -> list | None:
    """Detect NVIDIA GPUs via nvidia-smi and return detections in the common shape.

    Returns the same structure as detect_hardware():
      [{"accelerator_type": "GPU", "accelerator_model": <token>, "accelerator_count": n}, ...]

    Honors the same None-vs-[] contract that keeps labels from flapping:
      - None  => detection FAILED this cycle (nvidia-smi missing, non-zero exit,
                 timeout). Caller preserves the last-good feature file.
      - []    => detection SUCCEEDED and confidently found no GPUs.

    nvidia-smi and libnvidia-ml are injected into the container by the
    nvidia-container-toolkit (NVIDIA_VISIBLE_DEVICES=all +
    NVIDIA_DRIVER_CAPABILITIES=utility under the nvidia runtime), so no driver
    libraries are baked into the detector image.
    """
    cmd = ["nvidia-smi", "--query-gpu=name", "--format=csv,noheader"]
    logger.info("Running: %s", " ".join(cmd))
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    except FileNotFoundError:
        logger.error(
            "nvidia-smi not found; ensure the nvidia runtime injects it "
            "(NVIDIA_VISIBLE_DEVICES=all, NVIDIA_DRIVER_CAPABILITIES=utility)"
        )
        return None
    except subprocess.TimeoutExpired:
        logger.error("nvidia-smi timed out after 120s")
        return None

    if result.returncode != 0:
        logger.error("nvidia-smi failed (exit %d): %s", result.returncode, result.stderr.strip())
        return None

    counts: dict[str, int] = {}
    for line in result.stdout.splitlines():
        name = line.strip()
        if not name:
            continue
        model = _normalize_nvidia_model(name)
        counts[model] = counts.get(model, 0) + 1

    detections = [
        {"accelerator_type": "GPU", "accelerator_model": model, "accelerator_count": count}
        for model, count in sorted(counts.items())
    ]
    for det in detections:
        logger.info(
            "Detected: type=GPU model=%s count=%s",
            det["accelerator_model"], det["accelerator_count"],
        )
    if not detections:
        logger.warning("nvidia-smi returned no GPUs")
    return detections


def detect_partitions() -> list | None:
    """Read current GPU partition state from `amd-smi partition --current --json`.

    Returns a list of per-partition entries (each a dict with at least a
    compute type and a memory partition), or None when no usable partition
    information could be obtained. None is the signal to fall back to the
    `partitioning-scheme.default` sentinel for this cycle.

    Best-effort by design: a missing/old amd-smi, a non-partitionable
    GPU (Radeon), a timeout, or an unparseable shape all return None without
    raising. Partition detection must never take down model/family labelling.
    Every fallback path logs *what happened* and *what we did about it* so an
    operator reading detector logs can tell the cases apart.
    """
    cmd = ["amd-smi", "partition", "--current", "--json"]
    logger.info("Running: %s", " ".join(cmd))
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    except FileNotFoundError:
        logger.info(
            "amd-smi not found on PATH; treating node as non-partitionable "
            "-> publishing partitioning-scheme.default only"
        )
        return None
    except subprocess.TimeoutExpired:
        logger.warning(
            "amd-smi partition timed out after 60s; falling back to "
            "partitioning-scheme.default for this cycle"
        )
        return None

    if result.returncode != 0:
        logger.info(
            "amd-smi partition unavailable (exit %d); treating node as "
            "non-partitionable -> publishing partitioning-scheme.default only",
            result.returncode,
        )
        if result.stderr.strip():
            logger.warning("amd-smi partition stderr: %s", result.stderr.strip())
        return None

    try:
        parsed = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        logger.warning(
            "Failed to parse amd-smi partition JSON (%s); falling back to "
            "partitioning-scheme.default. Raw (truncated): %s",
            exc, result.stdout.strip()[:300],
        )
        return None

    entries = _normalize_partition_entries(parsed)
    if not entries:
        logger.warning(
            "amd-smi partition returned no recognizable per-GPU entries; "
            "falling back to partitioning-scheme.default. Raw (truncated): %s",
            result.stdout.strip()[:300],
        )
        return None
    return entries


def _normalize_partition_entries(parsed) -> list:
    """Flatten amd-smi partition output into a list of per-GPU dicts.

    amd-smi's JSON shape varies across ROCm versions. Observed shapes:
      - a top-level list of GPU objects;
      - a wrapper dict whose list lives under one of several keys. ROCm 6.x
        emits `{"current_partition": [ {gpu_id, accelerator_type, memory}, ...]}`;
      - a dict keyed by GPU id (`{"gpu_0": {...}, ...}`).
    Parse defensively, accept any of these, and ignore anything unrecognized.
    """
    if isinstance(parsed, list):
        return [e for e in parsed if isinstance(e, dict)]
    if isinstance(parsed, dict):
        # Prefer an explicit wrapper key holding the per-GPU list. Keep this
        # list permissive: amd-smi has used several names across versions.
        for key in ("current_partition", "current_partitions", "partitions", "gpus", "data", "partition"):
            value = parsed.get(key)
            if isinstance(value, list):
                return [e for e in value if isinstance(e, dict)]
        # Last resort: a dict keyed by GPU id whose values are the entries.
        return [v for v in parsed.values() if isinstance(v, dict)]
    return []


# amd-smi renders "no value here" as one of these placeholders (case-insensitive)
# rather than omitting the key. Treat them as a missing axis, never as a literal
# scheme name. Critically, "N/A" also contains '/', which would make an invalid
# Kubernetes label key (feature.node.kubernetes.io/...partitioning-scheme.N/A-N/A).
_AXIS_SENTINELS = {"", "n/a", "na", "none", "null", "nil", "unknown", "-", "--"}

# Partition-row marker keys. A row that carries any of these is recognizably a
# per-partition record from amd-smi (even when its axis values are sentinels),
# as opposed to unrelated JSON we should skip.
_PARTITION_ROW_KEYS = (
    "partition_id", "gpu_id", "accelerator_type", "compute_partition",
    "memory", "memory_partition", "accelerator_profile_index",
)


def _clean_axis(value) -> str | None:
    """Normalize one axis value, mapping amd-smi placeholders to None."""
    if value is None:
        return None
    text = str(value).strip()
    if text.lower() in _AXIS_SENTINELS:
        return None
    return text.upper()


def _partition_source(entry: dict) -> dict:
    """Return the dict that actually holds the axis fields (handles nesting)."""
    nested = entry.get("partition")
    return nested if isinstance(nested, dict) else entry


def _looks_like_partition_row(entry: dict) -> bool:
    """True when the entry is recognizably an amd-smi partition record.

    Used to tell a legitimate sibling partition (axes reported as N/A) apart
    from unrelated/garbage JSON. Sibling rows still carry partition_id/gpu_id.
    """
    if not isinstance(entry, dict):
        return False
    source = _partition_source(entry)
    return any(k in source for k in _PARTITION_ROW_KEYS)


def _scheme_from_entry(entry: dict) -> str | None:
    """Extract the `{COMPUTE}-{MEMORY}` scheme name from one partition entry.

    Tolerates the key-name drift between amd-smi versions (accelerator_type vs
    compute_partition; memory vs memory_partition), nesting under a `partition`
    sub-object, and placeholder axis values (N/A and friends). Returns None when
    either axis is missing or a placeholder.
    """
    source = _partition_source(entry)
    compute = (
        _clean_axis(source.get("accelerator_type"))
        or _clean_axis(source.get("compute_partition"))
        or _clean_axis(source.get("compute"))
    )
    memory = (
        _clean_axis(source.get("memory_partition"))
        or _clean_axis(source.get("memory"))
    )
    if not compute or not memory:
        return None
    return f"{compute}-{memory}"


def build_partition_lines(entries: list | None, gpu_count: int) -> list:
    """Convert partition entries into NFD feature lines on the partition axis.

    Emits, under PARTITION_LABEL_PREFIX:
      - one `partitioning-scheme.{C}-{M}={count}` per distinct scheme present,
        where count is the number of schedulable units in that bucket (one per
        partition entry);
      - `partitioning-scheme.default={n}` when the node is in the canonical
        unpartitioned scheme (SPX-NPS1, alongside the explicit label) or when
        no usable partition info was obtained (Radeon / errors / missing
        tooling), where n is the physical GPU count.

    The resolver consumes label *keys* (Exists / DoesNotExist); values are
    informational counts only.
    """
    # No usable partition info -> only the hardware-agnostic sentinel.
    if not entries:
        logger.info(
            "No partition info; publishing %s.%s=%d (non-partitionable or "
            "amd-smi unavailable)",
            PARTITION_LABEL_PREFIX, PARTITION_DEFAULT, gpu_count,
        )
        return [f"{PARTITION_LABEL_PREFIX}.{PARTITION_DEFAULT}={gpu_count}"]

    # amd-smi reports the scheme only on each physical GPU's PRIMARY partition
    # row (partition_id 0); the remaining sibling partitions of that GPU are
    # listed with N/A axes. They belong to the same scheme, so carry the most
    # recent scheme forward and count every sibling toward it. This makes
    # CPX-NPS4 on 8 GPUs report 64 schedulable units, not 8.
    scheme_counts: dict[str, int] = {}
    current_scheme: str | None = None
    siblings = 0
    skipped = 0
    for entry in entries:
        scheme = _scheme_from_entry(entry)
        if scheme is not None:
            current_scheme = scheme
            scheme_counts[scheme] = scheme_counts.get(scheme, 0) + 1
        elif current_scheme is not None and _looks_like_partition_row(entry):
            scheme_counts[current_scheme] += 1
            siblings += 1
        else:
            skipped += 1

    if siblings:
        logger.info(
            "Attributed %d sibling partition row(s) (amd-smi reports the scheme "
            "only on each GPU's primary partition) to their parent scheme",
            siblings,
        )
    if skipped:
        logger.warning(
            "Skipped %d unrecognized partition entr(y/ies) with no scheme and "
            "no preceding primary partition to attribute them to",
            skipped,
        )

    if not scheme_counts:
        logger.warning(
            "amd-smi partition entries carried no usable scheme; falling back "
            "to %s.%s=%d", PARTITION_LABEL_PREFIX, PARTITION_DEFAULT, gpu_count,
        )
        return [f"{PARTITION_LABEL_PREFIX}.{PARTITION_DEFAULT}={gpu_count}"]

    lines = [
        f"{PARTITION_LABEL_PREFIX}.{scheme}={count}"
        for scheme, count in sorted(scheme_counts.items())
    ]

    # Canonical-unpartitioned state: emit the cross-hardware sentinel alongside
    # the explicit SPX-NPS1 label so a profile can match either by name or via
    # `default`. Only when SPX-NPS1 is the ONLY scheme on the node (a node that
    # mixes SPX with a partitioned scheme is not "canonically unpartitioned").
    if set(scheme_counts) == {CANONICAL_DEFAULT_SCHEME}:
        default_count = scheme_counts[CANONICAL_DEFAULT_SCHEME]
        lines.append(f"{PARTITION_LABEL_PREFIX}.{PARTITION_DEFAULT}={default_count}")
        logger.info(
            "Partition schemes: %s=%d (+%s sentinel)",
            CANONICAL_DEFAULT_SCHEME, default_count, PARTITION_DEFAULT,
        )
    else:
        logger.info(
            "Partition schemes: %s",
            ", ".join(f"{s}={c}" for s, c in sorted(scheme_counts.items())),
        )

    return lines


def build_feature_lines(detections: list, vendor: str = DEFAULT_VENDOR) -> list:
    """Convert detection results into NFD feature file lines.

    Each detection dict has: accelerator_type, accelerator_model, accelerator_count.
    We emit three layers of labels so AIMProfiles can target a specific model, a
    generic family, or a vendor:

      - Per-model:  feature.node.kubernetes.io/aim-accelerator.{MODEL}={count}
                    e.g. aim-accelerator.MI300X=8, aim-accelerator.EPYC_ZEN5=1
      - Per-family: feature.node.kubernetes.io/aim-accelerator.{TYPE}={count}
                    e.g. aim-accelerator.GPU=8,    aim-accelerator.CPU=1
      - Per-vendor: feature.node.kubernetes.io/aim-accelerator.vendor.{TYPE}.{VENDOR}={count}
                    e.g. aim-accelerator.vendor.GPU.NVIDIA=8

    Family labels are required for AIM images whose embedded profile YAML
    declares accelerator_model: CPU / GPU (the family name) rather than a
    specific architecture. Without the family label, the v1alpha2 model
    matcher (internal/v1alpha2/aimprofile/node_match.go) finds no nodes for
    such profiles and reports them as unsupported.

    The vendor axis is keyed per accelerator type (one vendor per type per node)
    so a cross-vendor node never has conflicting vendor labels. Vendor comes from
    the DaemonSet's ACCELERATOR_VENDOR env, not from detection output.

    Family (and vendor) counts sum across all model entries of the same type
    (e.g. a hypothetical mixed MI300X+MI325X node aggregates to GPU=12).
    """
    seen_models = set()
    family_counts: dict[str, int] = {}
    model_lines: list[str] = []

    for det in detections:
        model = det.get("accelerator_model")
        family = det.get("accelerator_type")
        count = det.get("accelerator_count", 0)
        if not model:
            continue
        if model not in seen_models:
            seen_models.add(model)
            model_lines.append(f"{LABEL_PREFIX}.{model}={count}")
        if family:
            family_counts[family] = family_counts.get(family, 0) + count

    family_lines = [
        f"{LABEL_PREFIX}.{family}={count}"
        for family, count in sorted(family_counts.items())
    ]
    vendor_lines = []
    if vendor:
        vendor_lines = [
            f"{LABEL_PREFIX}.{VENDOR_LABEL_INFIX}.{family}.{vendor}={count}"
            for family, count in sorted(family_counts.items())
        ]
    lines = model_lines + family_lines + vendor_lines

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


def gpu_unit_count(detections: list) -> int:
    """Sum physical GPU units across GPU detections (for the default sentinel)."""
    total = 0
    for det in detections:
        if det.get("accelerator_type") == "GPU":
            total += det.get("accelerator_count", 0) or 0
    return total


def run_detection_cycle(detect_type: str, vendor: str = DEFAULT_VENDOR) -> bool:
    """Run one detect-and-publish cycle.

    The model/family axis and the partition axis are detected and published
    INDEPENDENTLY:

      - Model/family labels come from `aim-runtime detect-hardware` and are
        written to the `aim-accelerator-{type}` feature file.
      - Partition labels (GPU only) come straight from `amd-smi partition` and
        are written to a separate `aim-accelerator-gpu-partition` file.

    Decoupling matters because the two sources fail independently: some
    accelerator base images ship without `detect-hardware` (it postdates the
    0.11 tag), yet `amd-smi` is present and can still report the partition
    scheme. Gating partitions behind detect-hardware (as an addendum to a
    successful GPU model detection) would suppress the partition axis on exactly
    those images. Separate files also give each axis its own sticky lifecycle so
    a transient failure of one never drops the other's labels.

    Preserving on failure is the load-bearing behaviour: detect_hardware()
    returns None for a transient failure (tool missing, non-zero exit, timeout,
    bad JSON). Overwriting the feature file with empty results in that case
    would drop this node's labels until the next good cycle, flapping every
    consumer's profile/model availability. An empty *list* (confident "no
    hardware") is distinct and is published as an empty file.

    Returns True if any feature file was (re)written this cycle.
    """
    wrote = False

    # Model/family axis. Backend is vendor-specific: NVIDIA -> nvidia-smi,
    # everything else -> aim-runtime detect-hardware. Sticky: None => preserve
    # last-good.
    if vendor == NVIDIA_VENDOR:
        detections = detect_hardware_nvidia()
    else:
        detections = detect_hardware(detect_type)
    if detections is None:
        logger.warning(
            "Model detection failed this cycle; preserving last-good feature "
            "file %s (not overwriting with empty results)",
            FEATURE_FILE_TEMPLATE.format(detect_type),
        )
    else:
        lines = build_feature_lines(detections, vendor)
        if lines:
            write_feature_file(lines, detect_type)
        else:
            # Detection succeeded and confidently found nothing (empty list);
            # safe to publish an empty file for this node.
            logger.warning("No accelerators detected")
            write_feature_file([], detect_type)
        wrote = True

    # Partition axis, GPU only and independent of the model axis.
    if detect_type == "gpu":
        gpu_count_hint = gpu_unit_count(detections) if detections else 0
        # A successful, GPU-free model result ([] -> hint 0) is a CONFIDENT "no
        # GPUs on this node". In that case clear the partition axis too rather
        # than preserving stale labels (e.g. a GPU that was physically removed).
        # A FAILED detection (None) is not confident and must stay sticky, so
        # only flag confidence when detections is not None.
        confident_no_gpu = detections is not None and gpu_count_hint == 0
        # AMD reads the scheme from amd-smi; NVIDIA partitioning (MIG) is not yet
        # parsed, so NVIDIA GPU nodes report only the default sentinel.
        if vendor == NVIDIA_VENDOR:
            partition_written = publish_nvidia_default_partition_labels(
                gpu_count_hint, clear_when_no_signal=confident_no_gpu
            )
        else:
            partition_written = run_partition_cycle(
                gpu_count_hint, clear_when_no_signal=confident_no_gpu
            )
        if partition_written:
            wrote = True

    return wrote


def run_partition_cycle(gpu_count_hint: int, clear_when_no_signal: bool = False) -> bool:
    """Detect GPU partitions via amd-smi and publish the partition-axis labels.

    Independent of detect-hardware: the partition scheme comes entirely from
    `amd-smi partition` and needs no device-id/model table. Sticky on transient
    failure so an amd-smi hiccup never drops the partition labels.

    `gpu_count_hint` (the physical GPU count from detect-hardware, or 0 when that
    is unavailable) is only consulted for the no-entries fallback: when amd-smi
    reports a real per-GPU scheme the counts derive from the entries themselves.

    `clear_when_no_signal` flips the no-signal behaviour from "preserve last-good"
    to "publish an empty file". The caller sets it when detect-hardware
    confidently reported no GPUs, so the partition axis is cleared alongside the
    model axis instead of leaving stale labels behind.

    Returns True if the partition feature file was (re)written, False if it was
    preserved because no usable partition signal was available this cycle.
    """
    entries = detect_partitions()
    if entries:
        write_feature_file(build_partition_lines(entries, gpu_count_hint), PARTITION_FILE_KIND)
        return True
    if gpu_count_hint > 0:
        # amd-smi gave nothing usable (e.g. a non-partitionable Radeon) but we
        # know GPUs are present -> publish the hardware-agnostic default sentinel.
        write_feature_file(build_partition_lines(None, gpu_count_hint), PARTITION_FILE_KIND)
        return True
    if clear_when_no_signal:
        # Confident no-GPU and amd-smi has nothing -> clear the partition axis.
        logger.info(
            "No GPUs detected and no partition signal; clearing partition "
            "feature file %s", FEATURE_FILE_TEMPLATE.format(PARTITION_FILE_KIND),
        )
        write_feature_file([], PARTITION_FILE_KIND)
        return True
    # No partition entries and no GPU-count signal (e.g. detect-hardware missing
    # AND amd-smi unavailable) -> can't characterise the node this cycle.
    # Preserve the last-good partition file rather than clobbering it.
    logger.warning(
        "Partition detection produced no usable signal; preserving last-good "
        "feature file %s", FEATURE_FILE_TEMPLATE.format(PARTITION_FILE_KIND),
    )
    return False


def publish_nvidia_default_partition_labels(gpu_count_hint: int, clear_when_no_signal: bool = False) -> bool:
    """Write the NVIDIA partition feature file (always the `default` sentinel).

    Unlike the amd-smi path, this never detects a real partition scheme: MIG
    geometry is not yet parsed, so it only writes the hardware-agnostic
    `partitioning-scheme.default` label (count = physical GPUs) — mirroring how
    non-partitionable AMD hardware (Radeon) is handled, and keeping
    partition-blind (unpartitioned) profiles matching NVIDIA nodes today.

    TODO(MIG): parse `nvidia-smi mig -lgip` / `-lgi` and emit per-instance
    `partitioning-scheme.<profile>` labels, analogous to the amd-smi path.

    Writes the feature file when GPUs are present (or clears it when
    `clear_when_no_signal` is set and none are), returning True. Returns False
    without writing when there is no signal, preserving the last-good file.
    """
    if gpu_count_hint > 0:
        write_feature_file(build_partition_lines(None, gpu_count_hint), PARTITION_FILE_KIND)
        return True
    if clear_when_no_signal:
        logger.info(
            "No NVIDIA GPUs detected; clearing partition feature file %s",
            FEATURE_FILE_TEMPLATE.format(PARTITION_FILE_KIND),
        )
        write_feature_file([], PARTITION_FILE_KIND)
        return True
    logger.warning(
        "No NVIDIA GPU signal this cycle; preserving last-good partition "
        "feature file %s", FEATURE_FILE_TEMPLATE.format(PARTITION_FILE_KIND),
    )
    return False


def resolve_vendor(raw: str | None) -> str:
    """Normalize and validate the ACCELERATOR_VENDOR env value.

    Fails fast (ValueError) on anything outside SUPPORTED_VENDORS so a typo like
    "NVDA" can't silently fall through to the AMD detection backend on an NVIDIA
    node, producing empty/nonsense labels while the pod stays healthy.
    """
    vendor = (raw or DEFAULT_VENDOR).strip().upper() or DEFAULT_VENDOR
    if vendor not in SUPPORTED_VENDORS:
        raise ValueError(
            f"Unsupported ACCELERATOR_VENDOR={raw!r}; must be one of {list(SUPPORTED_VENDORS)}"
        )
    return vendor


def resolve_interval(raw: str | None, default: int = 10) -> int:
    """Parse DETECT_INTERVAL, falling back to a sane default on bad input.

    A malformed value would otherwise crash the detection loop on startup with
    an opaque traceback; instead log and continue at the default cadence.
    """
    try:
        value = int(raw)
    except (TypeError, ValueError):
        logger.warning("Invalid DETECT_INTERVAL=%r; using %ds", raw, default)
        return default
    if value <= 0:
        logger.warning("Non-positive DETECT_INTERVAL=%r; using %ds", raw, default)
        return default
    return value


def main():
    detect_type = os.environ.get("DETECT_TYPE", "gpu")
    try:
        vendor = resolve_vendor(os.environ.get("ACCELERATOR_VENDOR"))
    except ValueError as exc:
        logger.error("%s", exc)
        sys.exit(1)
    interval = resolve_interval(os.environ.get("DETECT_INTERVAL"))
    node_name = os.environ.get("NODE_NAME", "unknown")

    logger.info(
        "Starting accelerator-detector on node=%s type=%s vendor=%s interval=%ds",
        node_name, detect_type, vendor, interval,
    )

    while True:
        run_detection_cycle(detect_type, vendor)

        # Refresh health unconditionally: a detector waiting out a transient
        # hardware-tool hiccup is still alive and must not be liveness-killed.
        open(HEALTH_FILE, "w").close()

        logger.info("Sleeping %ds before next detection cycle", interval)
        time.sleep(interval)


if __name__ == "__main__":
    main()
