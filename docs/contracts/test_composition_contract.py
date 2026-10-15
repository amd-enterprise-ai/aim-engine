# Copyright © Advanced Micro Devices, Inc., or its affiliates.
# SPDX-License-Identifier: MIT
"""Check the review contract, not Kubernetes admission or composition resolution."""

import copy
import re
import unittest
from pathlib import Path

import jsonschema
import yaml


DOCS = Path(__file__).resolve().parents[1] / "docs" / "composition"
SCHEMA = yaml.safe_load((DOCS / "schema.yaml").read_text())
VALIDATOR = jsonschema.Draft202012Validator(SCHEMA)


def service(composition):
    return {
        "apiVersion": "aim.eai.amd.com/v1alpha2",
        "kind": "AIMService",
        "metadata": {"name": "example", "namespace": "team-a"},
        "spec": {"composition": composition},
    }


def runtime(composition):
    return {
        "apiVersion": "aim.eai.amd.com/v1alpha1",
        "kind": "AIMClusterRuntimeConfig",
        "metadata": {"name": "default"},
        "spec": {"composition": composition},
    }


def local_errors(obj):
    """Only local invariants JSON Schema cannot express; no source resolution."""
    errors = list(VALIDATOR.iter_errors(obj))
    if errors:
        return [str(error) for error in errors]

    composition = obj["spec"].get("composition", {})
    if obj["kind"] in ("AIMComposition", "AIMClusterComposition"):
        composition = obj["spec"]
    if obj["kind"] in ("AIMRuntimeConfig", "AIMClusterRuntimeConfig"):
        names = [p["name"] for p in composition.get("transportPresets", [])]
        if len(names) != len(set(names)):
            errors.append("duplicate transport preset name")
        return errors

    for name in ("main", "prefill", "decode"):
        role = composition.get(name, {})
        scaling = role.get("scaling")
        if scaling and scaling.get("minReplicas", 1) > scaling["maxReplicas"]:
            errors.append(f"{name}: minReplicas exceeds maxReplicas")
        for position in (role, role.get("worker", {})):
            env = position.get("launch", {}).get("containerEnv", [])
            names = [entry["name"] for entry in env]
            if len(names) != len(set(names)):
                errors.append(f"{name}: duplicate containerEnv name")
    return errors


class CompositionContractTest(unittest.TestCase):
    def assert_valid(self, obj):
        self.assertEqual(local_errors(obj), [])

    def assert_invalid(self, obj):
        self.assertTrue(local_errors(obj), f"unexpectedly valid: {obj}")

    def test_schema_is_valid(self):
        jsonschema.Draft202012Validator.check_schema(SCHEMA)

    def test_all_manifest_examples(self):
        paths = sorted((DOCS / "examples").glob("*.yaml"))
        self.assertGreaterEqual(len(paths), 12)
        for path in paths:
            for index, obj in enumerate(yaml.safe_load_all(path.read_text()), 1):
                with self.subTest(path=path.name, document=index):
                    self.assert_valid(obj)

    def test_markdown_yaml_examples_and_spec_fragments(self):
        checked = 0
        for path in sorted(DOCS.glob("*.md")):
            for index, block in enumerate(
                re.findall(r"^```yaml\n(.*?)^```", path.read_text(), re.M | re.S),
                1,
            ):
                for obj in yaml.safe_load_all(block):
                    with self.subTest(path=path.name, block=index):
                        self.assertIsInstance(obj, dict)
                        # Guide snippets containing only spec are service specs.
                        if set(obj) == {"spec"}:
                            obj = {**service({}), **obj}
                        self.assert_valid(obj)
                        checked += 1
        self.assertGreaterEqual(checked, 12)

    def test_inference_and_omission_shapes(self):
        for composition in [
            {"mode": "Aggregated"},
            {"mode": "PrefillDecode"},
            {"main": {}},
            {"prefill": {}},
            {"decode": {}},
            {"decode": {"profile": {"selector": {}}}},
            {"mode": "PrefillDecode", "inheritSelectorDefaults": False},
            {"ref": {"kind": "AIMClusterComposition", "name": "local-pd"}},
        ]:
            with self.subTest(composition=composition):
                self.assert_valid(service(composition))

    def test_invalid_mode_union_and_nesting(self):
        for composition in [
            {},
            {"profileSelector": {"engine": "vllm"}},
            {"mode": "Disaggregated"},
            {"mode": "PrefillDecode", "main": {}},
            {"mode": "Aggregated", "decode": {}},
            {"main": {}, "prefill": {}},
            {"main": {}, "transport": {"preset": "local-kv"}},
            {"mode": "Aggregated", "transport": {"preset": "local-kv"}},
            {"main": {"template": {}}},
            {"main": {"composition": {"mode": "Aggregated"}}},
            {"main": {"profile": {}}},
            {"main": {"profile": {"selector": {}, "ref": {
                "kind": "AIMProfile", "name": "p",
            }}}},
            {"main": {"profile": {"name": "legacy-ref"}}},
            {"main": {"profile": {"selector": {"role": "base"}}}},
            {"main": {"profile": {"ref": {
                "kind": "AIMProfile", "name": "p", "namespace": "other",
            }}}},
            {"main": {"worker": {"replicas": 2}}},
            {"main": {"parallelism": {"tensor": 0}}},
            {"main": {"parallelism": {"data": 2}}},
            {"main": {}, "refreshToken": ""},
            {"main": {}, "inheritSelectorDefaults": None},
            {"main": {"launch": {"engineEnv": {"EXAMPLE": None}}}},
        ]:
            with self.subTest(composition=composition):
                self.assert_invalid(service(composition))

    def test_named_form_excludes_inline_topology(self):
        for field, value in [
            ("mode", "PrefillDecode"),
            ("placement", {"scope": "SameNode"}),
            ("transport", {"preset": "local-kv"}),
        ]:
            with self.subTest(field=field):
                self.assert_invalid(service({
                    "ref": {"kind": "AIMComposition", "name": "recipe"},
                    field: value,
                }))

    def test_model_shortcut_and_forbidden_legacy_fields(self):
        obj = service({"mode": "PrefillDecode"})
        obj["spec"]["model"] = {"name": "qwen3-32b"}
        self.assert_valid(obj)
        for field, value in [
            ("model", {"image": "registry/model:example"}),
            ("model", {"name": "qwen3-32b", "image": "registry/model:example"}),
            ("profile", {"name": "p"}),
            ("profileOverrides", {}),
            ("template", {}),
            ("overrides", {}),
            ("replicas", 1),
            ("minReplicas", 0),
            ("maxReplicas", 3),
            ("autoScaling", {}),
            ("resources", {}),
            ("env", []),
            ("scaleFromZero", {}),
            ("adapters", [{"kind": "AIMArtifact", "name": "adapter"}]),
            ("adapterRuntime", {}),
            ("adapterMode", "dynamic"),
            ("serving", {"mode": "PrefillDecode"}),
        ]:
            with self.subTest(field=field, value=value):
                changed = copy.deepcopy(obj)
                changed["spec"][field] = value
                self.assert_invalid(changed)

    def test_capacity_union_and_bounds(self):
        for role in [
            {"replicas": 0},
            {"replicas": 1, "scaling": {"minReplicas": 0, "maxReplicas": 2}},
            {"scaling": {}},
            {"scaling": {"maxReplicas": 3}},
            {"scaling": {"minReplicas": 0, "maxReplicas": 0}},
            {"scaling": {"minReplicas": 0, "maxReplicas": 2, "metrics": []}},
        ]:
            with self.subTest(role=role):
                self.assert_invalid(service({"decode": role}))
        self.assert_valid(service({"decode": {"scaling": {
            "minReplicas": 0, "maxReplicas": 2,
        }}}))
        obj = list(yaml.safe_load_all(
            (DOCS / "examples" / "09-launch-scaling.yaml").read_text()
        ))[0]
        obj["spec"]["composition"]["decode"]["scaling"]["minReplicas"] = 4
        self.assert_invalid(obj)

    def test_runtime_default_field_scope(self):
        for forbidden in [
            "modelId", "modelRef", "aimId", "profileId", "engineArgs", "role",
            "origin", "minimumType", "type", "rules", "fallbacks",
        ]:
            with self.subTest(field=forbidden):
                self.assert_invalid(runtime({
                    "selectorDefaults": {"common": {forbidden: "example"}},
                }))
        self.assert_valid(runtime({"selectorDefaults": {"decode": {}}}))
        self.assert_invalid(runtime({
            "transportPresets": [
                {"name": "local", "integration": "a"},
                {"name": "local", "integration": "b"},
            ],
        }))

    def test_cluster_recipe_reference_scope(self):
        obj = {
            "apiVersion": "aim.eai.amd.com/v1alpha2",
            "kind": "AIMClusterComposition",
            "metadata": {"name": "recipe"},
            "spec": {"main": {"profile": {"ref": {
                "kind": "AIMClusterProfile", "name": "p",
            }}}},
        }
        self.assert_valid(obj)
        obj["spec"]["main"]["profile"]["ref"]["kind"] = "AIMProfile"
        self.assert_invalid(obj)
        obj["kind"] = "AIMComposition"
        obj["metadata"]["namespace"] = "team-a"
        self.assert_valid(obj)
        obj["spec"]["ref"] = {"kind": "AIMComposition", "name": "other"}
        self.assert_invalid(obj)

    def test_environment_map_keys(self):
        obj = service({"main": {"launch": {"containerEnv": [
            {"name": "SETTING", "value": "a"},
            {"name": "SETTING", "value": "b"},
        ]}}})
        self.assert_invalid(obj)

    def test_validation_does_not_materialize_inherited_defaults(self):
        for composition in [
            {"ref": {"kind": "AIMComposition", "name": "recipe"}},
            {"mode": "PrefillDecode"},
        ]:
            obj = service(composition)
            before = copy.deepcopy(obj)
            self.assert_valid(obj)
            self.assertEqual(obj, before)

    def test_examples_are_indexed(self):
        index = (DOCS / "examples.md").read_text()
        for path in (DOCS / "examples").glob("*.yaml"):
            with self.subTest(path=path.name):
                self.assertIn(f"(examples/{path.name})", index)


if __name__ == "__main__":
    unittest.main()
