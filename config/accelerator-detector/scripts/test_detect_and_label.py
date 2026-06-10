"""Unit tests for the AcceleratorDetector partition labelling.

The production module is named with hyphens (detect-and-label.py) so it can't
be imported normally; load it by path via importlib.

Run with: python3 -m pytest config/accelerator-detector/scripts/
"""

import importlib.util
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


def _run_cycle(detect_type, *, hw, partitions):
    """run_detection_cycle with detect_hardware/detect_partitions stubbed."""
    orig_detect, orig_part = mod.detect_hardware, mod.detect_partitions
    calls, orig_write = _record_writes()
    mod.detect_hardware = lambda dt: hw
    mod.detect_partitions = lambda: partitions
    try:
        wrote = mod.run_detection_cycle(detect_type)
    finally:
        mod.detect_hardware, mod.detect_partitions = orig_detect, orig_part
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
    assert files["gpu"] == {f"{mod.LABEL_PREFIX}.MI300X": "8", f"{mod.LABEL_PREFIX}.GPU": "8"}
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
    assert files["gpu"] == {f"{mod.LABEL_PREFIX}.MI300X": "8", f"{mod.LABEL_PREFIX}.GPU": "8"}
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
