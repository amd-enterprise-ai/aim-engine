"""Unit tests for the AcceleratorDetector partition labelling.

The production module is named with hyphens (detect-and-label.py) so it can't
be imported normally; load it by path via importlib.

Run with: python3 -m pytest config/accelerator-detector/scripts/
"""

import importlib.util
import json
import os

_SPEC = importlib.util.spec_from_file_location(
    "detect_and_label",
    os.path.join(os.path.dirname(__file__), "detect-and-label.py"),
)
mod = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(mod)


PREFIX = mod.PARTITION_LABEL_PREFIX


def _labels(lines):
    """Parse 'key=value' feature lines into a dict."""
    return dict(line.split("=", 1) for line in lines)


def test_spx_nps1_emits_default_plus_scheme():
    # 8-GPU MI300X in canonical unpartitioned SPX-NPS1: one entry per GPU.
    entries = [{"accelerator_type": "SPX", "memory_partition": "NPS1"} for _ in range(8)]
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels[f"{PREFIX}.SPX-NPS1"] == "8"
    assert labels[f"{PREFIX}.default"] == "8"


def test_cpx_nps4_emits_scheme_only_no_default():
    # 8-GPU CPX+NPS4 -> 64 schedulable partitions, no default sentinel.
    entries = [{"accelerator_type": "CPX", "memory_partition": "NPS4"} for _ in range(64)]
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels[f"{PREFIX}.CPX-NPS4"] == "64"
    assert f"{PREFIX}.default" not in labels


def test_no_partition_info_emits_default_only():
    # Radeon / amd-smi unavailable: only the hardware-agnostic sentinel.
    labels = _labels(mod.build_partition_lines(None, gpu_count=4))
    assert labels == {f"{PREFIX}.default": "4"}


def test_heterogeneous_node_emits_multiple_schemes_no_default():
    # 4 SPX-NPS1 + 4 CPX (32 partitions) at NPS1 on one node.
    entries = [{"accelerator_type": "SPX", "memory_partition": "NPS1"} for _ in range(4)]
    entries += [{"accelerator_type": "CPX", "memory_partition": "NPS1"} for _ in range(32)]
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels[f"{PREFIX}.SPX-NPS1"] == "4"
    assert labels[f"{PREFIX}.CPX-NPS1"] == "32"
    # A node mixing SPX with a partitioned scheme is not canonically unpartitioned.
    assert f"{PREFIX}.default" not in labels


def test_scheme_extraction_tolerates_key_drift_and_nesting():
    # compute_partition/memory keys, and nesting under a "partition" sub-object.
    assert mod._scheme_from_entry({"compute_partition": "cpx", "memory": "nps4"}) == "CPX-NPS4"
    assert mod._scheme_from_entry({"partition": {"accelerator_type": "SPX", "memory_partition": "NPS1"}}) == "SPX-NPS1"
    assert mod._scheme_from_entry({"accelerator_type": "SPX"}) is None  # missing memory axis


def test_scheme_extraction_treats_na_placeholders_as_missing():
    # amd-smi renders sibling-partition axes as "N/A" (and friends); these must
    # be treated as a missing axis, never as a literal "N/A-N/A" scheme (which
    # would also be an invalid k8s label key due to the '/').
    assert mod._scheme_from_entry({"accelerator_type": "N/A", "memory": "N/A"}) is None
    assert mod._scheme_from_entry({"accelerator_type": "CPX", "memory": "n/a"}) is None
    assert mod._scheme_from_entry({"accelerator_type": "", "memory": "NPS1"}) is None


def test_cpx_real_amdsmi_shape_carries_scheme_to_na_siblings():
    # Real ROCm 6.x CPX-NPS4 output: each physical GPU emits one primary row
    # (partition_id 0) carrying the scheme, then 7 sibling rows with N/A axes.
    # 8 GPUs -> 64 schedulable CPX-NPS4 partitions.
    entries = []
    for gpu in range(8):
        entries.append({"gpu_id": gpu * 8, "partition_id": "0",
                        "accelerator_type": "CPX", "memory": "NPS4"})
        for sib in range(1, 8):
            entries.append({"gpu_id": gpu * 8 + sib, "partition_id": str(sib),
                            "accelerator_type": "N/A", "memory": "N/A"})
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels[f"{PREFIX}.CPX-NPS4"] == "64"
    # No bogus N/A-N/A label, and a partitioned node carries no default sentinel.
    assert not any("N/A" in k for k in labels)
    assert f"{PREFIX}.default" not in labels


def test_leading_unattributable_rows_fall_back_to_default():
    # Sibling rows with no preceding primary partition cannot be attributed to
    # any scheme -> they are skipped and the node falls back to default.
    entries = [{"gpu_id": 0, "partition_id": "1", "accelerator_type": "N/A", "memory": "N/A"}]
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels == {f"{PREFIX}.default": "8"}


def test_entries_missing_axis_fall_back_to_default():
    # All entries unusable -> default sentinel rather than an empty axis.
    labels = _labels(mod.build_partition_lines([{"foo": "bar"}], gpu_count=8))
    assert labels == {f"{PREFIX}.default": "8"}


def test_normalize_handles_list_and_dict_shapes():
    assert mod._normalize_partition_entries([{"a": 1}]) == [{"a": 1}]
    assert mod._normalize_partition_entries({"gpu_0": {"a": 1}}) == [{"a": 1}]
    assert mod._normalize_partition_entries({"data": [{"a": 1}]}) == [{"a": 1}]
    assert mod._normalize_partition_entries("nonsense") == []


def test_normalize_handles_rocm6_current_partition_wrapper():
    # Real ROCm 6.x amd-smi `partition --current --json` shape: the per-GPU
    # list is wrapped under "current_partition", entries use memory/accelerator_type.
    raw = {
        "current_partition": [
            {"gpu_id": i, "memory": "NPS1", "accelerator_type": "SPX",
             "accelerator_profile_index": 0, "partition_id": str(i)}
            for i in range(8)
        ]
    }
    entries = mod._normalize_partition_entries(raw)
    assert len(entries) == 8
    labels = _labels(mod.build_partition_lines(entries, gpu_count=8))
    assert labels[f"{PREFIX}.SPX-NPS1"] == "8"
    assert labels[f"{PREFIX}.default"] == "8"


# ---------------------------------------------------------------------------
# detect_hardware: failure (None) vs confident-empty ([]) contract.
# This distinction is what stops node labels from flapping: a transient
# detection failure must NOT be mistaken for "this node has no accelerators".
# ---------------------------------------------------------------------------


class _FakeProc:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _with_run(fake_run, fn):
    """Run fn() with mod.subprocess.run temporarily replaced by fake_run."""
    orig = mod.subprocess.run
    mod.subprocess.run = fake_run
    try:
        return fn()
    finally:
        mod.subprocess.run = orig


def test_detect_hardware_none_on_nonzero_exit():
    # e.g. the observed `detect-hardware: No such command` on some images.
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=1, stderr="No such command"),
        lambda: mod.detect_hardware("gpu"),
    )
    assert out is None


def test_detect_hardware_none_on_timeout():
    def boom(*a, **k):
        raise mod.subprocess.TimeoutExpired(cmd="aim-runtime", timeout=120)

    assert _with_run(boom, lambda: mod.detect_hardware("gpu")) is None


def test_detect_hardware_none_on_bad_json():
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=0, stdout="not-json"),
        lambda: mod.detect_hardware("gpu"),
    )
    assert out is None


def test_detect_hardware_empty_list_is_confident_empty():
    # returncode 0 + valid empty array == "ran fine, genuinely nothing here".
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=0, stdout="[]"),
        lambda: mod.detect_hardware("cpu"),
    )
    assert out == []


def test_detect_hardware_parses_detections():
    payload = '[{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]'
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=0, stdout=payload),
        lambda: mod.detect_hardware("gpu"),
    )
    assert out == [{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]


# ---------------------------------------------------------------------------
# detect_hardware: per-model detections from the --verbose output, so nodes
# that mix GPU models are labelled with every model instead of the first GPU's.
# ---------------------------------------------------------------------------


def _gpu(device_id, model):
    return {"device_id": device_id, "model": model, "vram_total": 1, "vram_used": 0}


def _verbose_gpu_payload(gpus, model="MI350P"):
    return json.dumps([{
        "accelerator_type": "gpu",
        "accelerator_model": model,
        "accelerator_count": len(gpus),
        "gpu_info": gpus,
    }])


def _recording_run(*responses):
    """fake subprocess.run returning `responses` in order and recording argv."""
    calls = []

    def run(cmd, *a, **k):
        calls.append(cmd)
        return responses[min(len(calls), len(responses)) - 1]

    return run, calls


def test_detect_hardware_requests_verbose_output():
    run, calls = _recording_run(_FakeProc(stdout="[]"))
    _with_run(run, lambda: mod.detect_hardware("gpu"))
    assert calls[0][-1] == "--verbose"


def test_detect_hardware_splits_mixed_gpu_models():
    gpus = [_gpu("0x75a8", "MI350P")] * 4 + [_gpu("0x7551", "R9700")] * 2
    out = _with_run(
        lambda *a, **k: _FakeProc(stdout=_verbose_gpu_payload(gpus)),
        lambda: mod.detect_hardware("gpu"),
    )
    assert out == [
        {"accelerator_type": "GPU", "accelerator_model": "MI350P", "accelerator_count": 4},
        {"accelerator_type": "GPU", "accelerator_model": "R9700", "accelerator_count": 2},
    ]


def test_unrecognized_gpu_marks_node_as_mixed():
    # The runtime's single-model output would drop the whole node here because
    # the first card's device ID is unknown. The device plugin still offers the
    # unknown card as amd.com/gpu, so the node counts as having two models.
    gpus = [_gpu("0x1234", None)] + [_gpu("0x74a1", "MI300X")] * 7
    out = _with_run(
        lambda *a, **k: _FakeProc(stdout=_verbose_gpu_payload(gpus, model=None)),
        lambda: mod.detect_hardware("gpu"),
    )
    assert out == [
        {"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 7},
        {"accelerator_type": "GPU", "accelerator_model": "", "device_id": "0x1234", "accelerator_count": 1},
    ]
    lines = mod.build_feature_lines(out, vendor="AMD")
    assert f"{mod.LABEL_PREFIX}.MI300X=7" in lines
    assert f"{mod.LABEL_PREFIX}.{mod.MIXED_MODELS_LABEL_INFIX}.GPU=2" in lines
    assert not any("0x1234" in line for line in lines)
    assert mod.gpu_unit_count(out) == 7


def test_unrecognized_gpus_of_one_device_id_count_as_one_model():
    gpus = [_gpu("0x1234", None)] * 2 + [_gpu("0x74a1", "MI300X")] * 6
    out = _with_run(
        lambda *a, **k: _FakeProc(stdout=_verbose_gpu_payload(gpus, model=None)),
        lambda: mod.detect_hardware("gpu"),
    )
    assert f"{mod.LABEL_PREFIX}.{mod.MIXED_MODELS_LABEL_INFIX}.GPU=2" in mod.build_feature_lines(out, vendor="AMD")


def test_only_unrecognized_gpus_publish_no_labels():
    out = _with_run(
        lambda *a, **k: _FakeProc(stdout=_verbose_gpu_payload([_gpu("0x1234", None)], model=None)),
        lambda: mod.detect_hardware("gpu"),
    )
    assert mod.build_feature_lines(out, vendor="AMD") == []
    assert mod.gpu_unit_count(out) == 0


def test_detect_hardware_normalizes_verbose_cpu_entry():
    payload = json.dumps([{
        "accelerator_type": "cpu",
        "accelerator_model": "EPYC_9965",
        "accelerator_count": 192,
        "cpu_info": {"model_name": "AMD EPYC 9965"},
        "cpu_cores": 192,
    }])
    out = _with_run(
        lambda *a, **k: _FakeProc(stdout=payload),
        lambda: mod.detect_hardware("cpu"),
    )
    assert out == [{"accelerator_type": "CPU", "accelerator_model": "EPYC_9965", "accelerator_count": 192}]


# ---------------------------------------------------------------------------
# detection_command: resolve aim-runtime, falling back to the entrypoint module.
# aim-base:0.12 ships the runtime source without the `aim-runtime` console
# script, so the detector must fall back to `python3 /workspace/entrypoint.py`.
# ---------------------------------------------------------------------------


def _with_resolution(which_result, entrypoint_exists, fn):
    """Run fn() with shutil.which and os.path.exists stubbed for resolution."""
    orig_which, orig_exists = mod.shutil.which, mod.os.path.exists
    mod.shutil.which = lambda _name: which_result
    mod.os.path.exists = lambda path: entrypoint_exists if path == mod.ENTRYPOINT_PATH else orig_exists(path)
    try:
        return fn()
    finally:
        mod.shutil.which, mod.os.path.exists = orig_which, orig_exists


def test_detection_command_prefers_console_script():
    # When aim-runtime is installed on PATH, use it directly.
    cmd = _with_resolution("/usr/local/bin/aim-runtime", True, mod.detection_command)
    assert cmd == ["aim-runtime"]


def test_detection_command_falls_back_to_entrypoint():
    # No console script (e.g. aim-base:0.12) but the entrypoint source exists.
    cmd = _with_resolution(None, True, mod.detection_command)
    assert cmd == [mod.sys.executable, mod.ENTRYPOINT_PATH]


def test_detection_command_last_resort_is_console_script():
    # Neither resolvable: return the console-script name for a clear failure.
    cmd = _with_resolution(None, False, mod.detection_command)
    assert cmd == ["aim-runtime"]


# ---------------------------------------------------------------------------
# run_detection_cycle / run_partition_cycle: the model axis (detect-hardware)
# and the partition axis (amd-smi) are detected and published INDEPENDENTLY,
# into separate feature files, so a failure of one never clobbers the other.
# ---------------------------------------------------------------------------


def _record_writes():
    """Replace write_feature_file with a recorder; returns (calls, restore)."""
    calls = []
    orig = mod.write_feature_file
    mod.write_feature_file = lambda lines, detect_type: calls.append((list(lines), detect_type))
    return calls, orig


def _run_cycle(detect_type, *, hw, partitions, vendor="AMD"):
    """run_detection_cycle with the detection backends stubbed.

    `hw` stands in for whichever model backend the vendor selects
    (detect_hardware for AMD, detect_hardware_nvidia for NVIDIA); `partitions`
    stands in for detect_partitions (consulted only on the AMD GPU path).
    """
    orig_detect = mod.detect_hardware
    orig_nvidia = mod.detect_hardware_nvidia
    orig_part = mod.detect_partitions
    calls, orig_write = _record_writes()
    mod.detect_hardware = lambda dt: hw
    mod.detect_hardware_nvidia = lambda: hw
    mod.detect_partitions = lambda: partitions
    try:
        wrote = mod.run_detection_cycle(detect_type, vendor)
    finally:
        mod.detect_hardware = orig_detect
        mod.detect_hardware_nvidia = orig_nvidia
        mod.detect_partitions = orig_part
        mod.write_feature_file = orig_write
    return wrote, calls


def _files(calls):
    """Map feature-file kind -> parsed labels dict."""
    return {dt: _labels(lines) for lines, dt in calls}


def test_cycle_preserves_both_files_when_all_detection_fails():
    # detect-hardware None AND amd-smi unavailable (None) -> nothing usable;
    # both the model and partition files are preserved (no writes).
    wrote, calls = _run_cycle("gpu", hw=None, partitions=None)
    assert wrote is False
    assert calls == []


def test_cycle_writes_empty_on_confident_no_hardware():
    # detect_hardware -> [] (succeeded, nothing found) DOES publish an empty
    # model file. CPU type has no partition axis.
    wrote, calls = _run_cycle("cpu", hw=[], partitions=None)
    assert wrote is True
    assert calls == [([], "cpu")]


def test_cycle_clears_partition_on_confident_no_gpu():
    # detect-hardware succeeded and confidently found no GPUs ([]) while amd-smi
    # has no signal -> BOTH the model and partition files are cleared (empty),
    # not preserved. This stops stale partition labels lingering after a GPU is
    # removed. (A FAILED detection (None) stays sticky instead; see other tests.)
    wrote, calls = _run_cycle("gpu", hw=[], partitions=None)
    assert wrote is True
    files = _files(calls)
    assert files["gpu"] == {}
    assert files[mod.PARTITION_FILE_KIND] == {}


def test_model_and_partition_go_to_separate_files():
    # Full success: model/family labels in aim-accelerator-gpu, partition labels
    # in aim-accelerator-gpu-partition. They must NOT share a file.
    hw = [{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]
    parts = [{"accelerator_type": "CPX", "memory": "NPS4"} for _ in range(64)]
    wrote, calls = _run_cycle("gpu", hw=hw, partitions=parts)
    assert wrote is True
    files = _files(calls)
    assert files["gpu"] == {
        f"{mod.LABEL_PREFIX}.MI300X": "8",
        f"{mod.LABEL_PREFIX}.GPU": "8",
        f"{mod.LABEL_PREFIX}.vendor.GPU.AMD": "8",
    }
    assert files[mod.PARTITION_FILE_KIND] == {f"{PREFIX}.CPX-NPS4": "64"}


def test_partition_published_even_when_detect_hardware_missing():
    # THE DECOUPLING: detect-hardware missing (None) -> model file preserved,
    # but amd-smi still reports the scheme -> partition file IS written. This is
    # what unblocks partition work on images that lack detect-hardware.
    parts = [{"accelerator_type": "CPX", "memory": "NPS4"} for _ in range(64)]
    wrote, calls = _run_cycle("gpu", hw=None, partitions=parts)
    assert wrote is True
    files = _files(calls)
    assert "gpu" not in files  # model file preserved, not written
    assert files[mod.PARTITION_FILE_KIND] == {f"{PREFIX}.CPX-NPS4": "64"}


def test_partition_default_sentinel_uses_hint_when_no_entries():
    # amd-smi gave nothing but detect-hardware knows GPUs exist -> default=count.
    hw = [{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]
    wrote, calls = _run_cycle("gpu", hw=hw, partitions=None)
    assert wrote is True
    files = _files(calls)
    assert files["gpu"] == {
        f"{mod.LABEL_PREFIX}.MI300X": "8",
        f"{mod.LABEL_PREFIX}.GPU": "8",
        f"{mod.LABEL_PREFIX}.vendor.GPU.AMD": "8",
    }
    assert files[mod.PARTITION_FILE_KIND] == {f"{PREFIX}.default": "8"}


def test_partition_cycle_preserves_when_no_signal():
    # No amd-smi entries and no GPU-count hint -> preserve the partition file.
    calls, orig_write = _record_writes()
    orig_part = mod.detect_partitions
    mod.detect_partitions = lambda: None
    try:
        wrote = mod.run_partition_cycle(0)
    finally:
        mod.detect_partitions = orig_part
        mod.write_feature_file = orig_write
    assert wrote is False
    assert calls == []


# ---------------------------------------------------------------------------
# Vendor axis: build_feature_lines stamps a per-accelerator-type vendor label
# from the DaemonSet's ACCELERATOR_VENDOR env, so cross-vendor nodes carry one
# vendor key per type with no conflict.
# ---------------------------------------------------------------------------


def test_vendor_label_emitted_per_family_amd_gpu():
    hw = [{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]
    labels = _labels(mod.build_feature_lines(hw, vendor="AMD"))
    assert labels[f"{mod.LABEL_PREFIX}.vendor.GPU.AMD"] == "8"


def test_vendor_label_emitted_per_family_amd_cpu():
    hw = [{"accelerator_type": "CPU", "accelerator_model": "EPYC_9965", "accelerator_count": 192}]
    labels = _labels(mod.build_feature_lines(hw, vendor="AMD"))
    assert labels[f"{mod.LABEL_PREFIX}.vendor.CPU.AMD"] == "192"


def test_vendor_label_emitted_per_family_nvidia():
    hw = [{"accelerator_type": "GPU", "accelerator_model": "H100", "accelerator_count": 8}]
    labels = _labels(mod.build_feature_lines(hw, vendor="NVIDIA"))
    assert labels[f"{mod.LABEL_PREFIX}.vendor.GPU.NVIDIA"] == "8"
    # Vendor count tracks the family count.
    assert labels[f"{mod.LABEL_PREFIX}.GPU"] == "8"


def test_no_vendor_lines_when_no_detections():
    # Confident-empty: no families -> no vendor labels (and an empty file).
    assert mod.build_feature_lines([], vendor="AMD") == []


# ---------------------------------------------------------------------------
# NVIDIA model normalization: nvidia-smi product names -> canonical label tokens.
# ---------------------------------------------------------------------------


def test_normalize_nvidia_known_models():
    cases = {
        "NVIDIA H100 80GB HBM3": "H100",
        "NVIDIA H100 PCIe": "H100",
        "NVIDIA A100-SXM4-80GB": "A100",
        "NVIDIA A100 80GB PCIe": "A100",
        "NVIDIA H200": "H200",
        "NVIDIA GH200 480GB": "GH200",
        "NVIDIA B200": "B200",
        "NVIDIA L40S": "L40S",
        "NVIDIA L40": "L40",
        "NVIDIA L4": "L4",
        "NVIDIA A10": "A10",
        "Tesla V100-SXM2-16GB": "V100",
        "Tesla T4": "T4",
    }
    for raw, expected in cases.items():
        assert mod._normalize_nvidia_model(raw) == expected, raw


def test_normalize_nvidia_boundary_avoids_a10_in_a100():
    # The A10 token must not match inside "A100".
    assert mod._normalize_nvidia_model("NVIDIA A100 80GB PCIe") == "A100"


def test_normalize_nvidia_consumer_and_workstation():
    # Consumer/workstation RTX cards normalize by pattern so "try it on a
    # workstation GPU" produces clean labels (not slugs).
    cases = {
        "NVIDIA GeForce RTX 4090": "RTX4090",
        "NVIDIA GeForce RTX 4080 SUPER": "RTX4080Super",
        "NVIDIA GeForce RTX 4070 Ti SUPER": "RTX4070TiSuper",
        "NVIDIA GeForce RTX 3090 Ti": "RTX3090Ti",
        "NVIDIA GeForce RTX 3060": "RTX3060",
        "NVIDIA GeForce RTX 2080 Ti": "RTX2080Ti",
        "NVIDIA RTX A6000": "RTXA6000",
        "NVIDIA RTX A4000": "RTXA4000",
        "NVIDIA RTX 6000 Ada Generation": "RTX6000Ada",
    }
    for raw, expected in cases.items():
        assert mod._normalize_nvidia_model(raw) == expected, raw


def test_normalize_nvidia_consumer_labels_are_valid_keys():
    for raw in ("NVIDIA GeForce RTX 4070 Ti SUPER", "NVIDIA RTX 6000 Ada Generation"):
        tok = mod._normalize_nvidia_model(raw)
        assert tok and all(c.isalnum() or c in "-_." for c in tok), raw


def test_normalize_nvidia_unknown_is_sanitized_label_key():
    slug = mod._normalize_nvidia_model("NVIDIA Wonder GPU 9000!!")
    # Valid k8s label-key characters only, and non-empty.
    assert slug and all(c.isalnum() or c in "-_." for c in slug)


# ---------------------------------------------------------------------------
# detect_hardware_nvidia: same None-vs-[] contract as the AMD path, plus
# aggregation of nvidia-smi per-GPU name lines into model counts.
# ---------------------------------------------------------------------------


def test_detect_hardware_nvidia_aggregates_counts():
    out = _with_run(
        lambda *a, **k: _FakeProc(
            returncode=0,
            stdout="NVIDIA H100 80GB HBM3\nNVIDIA H100 80GB HBM3\nNVIDIA H100 PCIe\n",
        ),
        mod.detect_hardware_nvidia,
    )
    assert out == [{"accelerator_type": "GPU", "accelerator_model": "H100", "accelerator_count": 3}]


def test_detect_hardware_nvidia_none_on_nonzero_exit():
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=9, stderr="driver/library mismatch"),
        mod.detect_hardware_nvidia,
    )
    assert out is None


def test_detect_hardware_nvidia_none_on_timeout():
    def boom(*a, **k):
        raise mod.subprocess.TimeoutExpired(cmd="nvidia-smi", timeout=120)

    assert _with_run(boom, mod.detect_hardware_nvidia) is None


def test_detect_hardware_nvidia_none_when_missing():
    def missing(*a, **k):
        raise FileNotFoundError("nvidia-smi")

    assert _with_run(missing, mod.detect_hardware_nvidia) is None


def test_detect_hardware_nvidia_empty_is_confident_empty():
    out = _with_run(
        lambda *a, **k: _FakeProc(returncode=0, stdout="\n"),
        mod.detect_hardware_nvidia,
    )
    assert out == []


# ---------------------------------------------------------------------------
# NVIDIA detection cycle: model + family + vendor labels in the model file, and
# (MIG deferred) only the partitioning-scheme.default sentinel in the partition
# file. amd-smi is never consulted on the NVIDIA path.
# ---------------------------------------------------------------------------


def test_nvidia_cycle_emits_model_family_vendor_and_default_partition():
    hw = [{"accelerator_type": "GPU", "accelerator_model": "H100", "accelerator_count": 8}]
    # partitions stub is intentionally a real scheme to prove it's ignored on NVIDIA.
    parts = [{"accelerator_type": "CPX", "memory": "NPS4"} for _ in range(64)]
    wrote, calls = _run_cycle("gpu", hw=hw, partitions=parts, vendor="NVIDIA")
    assert wrote is True
    files = _files(calls)
    assert files["gpu"] == {
        f"{mod.LABEL_PREFIX}.H100": "8",
        f"{mod.LABEL_PREFIX}.GPU": "8",
        f"{mod.LABEL_PREFIX}.vendor.GPU.NVIDIA": "8",
    }
    # MIG not parsed yet -> default sentinel only, derived from the GPU count.
    assert files[mod.PARTITION_FILE_KIND] == {f"{PREFIX}.default": "8"}


def test_nvidia_cycle_clears_partition_on_confident_no_gpu():
    wrote, calls = _run_cycle("gpu", hw=[], partitions=None, vendor="NVIDIA")
    assert wrote is True
    files = _files(calls)
    assert files["gpu"] == {}
    assert files[mod.PARTITION_FILE_KIND] == {}


def test_build_feature_lines_mixed_models_sum_family():
    # Two GPU models on one node: each model keeps its own key; the family and
    # vendor counts sum across them. (No vendor.*.AMD leaks on the NVIDIA path.)
    hw = [
        {"accelerator_type": "GPU", "accelerator_model": "H100", "accelerator_count": 4},
        {"accelerator_type": "GPU", "accelerator_model": "A100", "accelerator_count": 4},
    ]
    labels = _labels(mod.build_feature_lines(hw, vendor="NVIDIA"))
    assert labels[f"{mod.LABEL_PREFIX}.H100"] == "4"
    assert labels[f"{mod.LABEL_PREFIX}.A100"] == "4"
    assert labels[f"{mod.LABEL_PREFIX}.GPU"] == "8"
    assert labels[f"{mod.LABEL_PREFIX}.vendor.GPU.NVIDIA"] == "8"
    assert not any(k.endswith(".AMD") for k in labels)


MIXED_GPU_KEY = f"{mod.LABEL_PREFIX}.mixed-models.GPU"


def test_build_feature_lines_marks_mixed_gpu_models():
    hw = [
        {"accelerator_type": "GPU", "accelerator_model": "MI350P", "accelerator_count": 4},
        {"accelerator_type": "GPU", "accelerator_model": "R9700", "accelerator_count": 2},
    ]
    labels = _labels(mod.build_feature_lines(hw))
    assert labels[MIXED_GPU_KEY] == "2"


def test_build_feature_lines_single_gpu_model_has_no_mixed_marker():
    hw = [{"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8}]
    assert MIXED_GPU_KEY not in _labels(mod.build_feature_lines(hw))


def test_build_feature_lines_gpu_plus_cpu_model_is_not_mixed():
    # A CPU model label on a GPU node is a different accelerator type, not a
    # second GPU model.
    hw = [
        {"accelerator_type": "GPU", "accelerator_model": "MI300X", "accelerator_count": 8},
        {"accelerator_type": "CPU", "accelerator_model": "EPYC_ZEN5", "accelerator_count": 1},
    ]
    assert not any(".mixed-models." in k for k in _labels(mod.build_feature_lines(hw)))


# ---------------------------------------------------------------------------
# Env parsing: ACCELERATOR_VENDOR must fail fast on typos (so it can't silently
# route to the wrong backend), and DETECT_INTERVAL must not crash the loop.
# ---------------------------------------------------------------------------


def test_resolve_vendor_normalizes_known_values():
    assert mod.resolve_vendor("amd") == "AMD"
    assert mod.resolve_vendor("nvidia") == "NVIDIA"
    assert mod.resolve_vendor(" NVIDIA ") == "NVIDIA"
    assert mod.resolve_vendor(None) == "AMD"
    assert mod.resolve_vendor("") == "AMD"


def test_resolve_vendor_rejects_typos():
    raised = False
    try:
        mod.resolve_vendor("NVDA")
    except ValueError:
        raised = True
    assert raised, "a vendor typo must fail fast, not silently fall back to AMD"


def test_resolve_interval_guard():
    assert mod.resolve_interval("30") == 30
    assert mod.resolve_interval("abc") == 10  # unparseable -> default
    assert mod.resolve_interval("0") == 10    # non-positive -> default
    assert mod.resolve_interval("-5") == 10
    assert mod.resolve_interval(None) == 10
