# MIT License
#
# Copyright (c) 2026 Advanced Micro Devices, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

"""Runs hack/upgrade-preflight.sh against a fake kubectl that serves canned lists."""

import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


HACK = Path(__file__).resolve().parent
SCRIPT = HACK / "upgrade-preflight.sh"
GUIDE = HACK.parent / "docs" / "docs" / "admin" / "upgrading.md"

# Serves <resource>.json from FAKE_CLUSTER for "get <resource> ...", and logs
# every call so tests can check that the preflight only reads.
FAKE_KUBECTL = """#!/bin/sh
echo "$*" >> "$FAKE_CLUSTER/calls.log"
case "$1" in
  api-resources) cat "$FAKE_CLUSTER/api-resources.txt" ;;
  get) cat "$FAKE_CLUSTER/$2.json" 2>/dev/null || echo '{"items": []}' ;;
  *) echo "fake kubectl: unexpected command $*" >&2; exit 1 ;;
esac
"""

API_RESOURCES = [
    "deployments.apps",
    "persistentvolumeclaims",
    "configmaps",
    "jobs.batch",
    "inferenceservices.serving.kserve.io",
    "httproutes.gateway.networking.k8s.io",
    "scaledobjects.keda.sh",
    "aimservices.aim.eai.amd.com",
    "aimmodels.aim.eai.amd.com",
    "aimclustermodels.aim.eai.amd.com",
    "aimservicetemplates.aim.eai.amd.com",
    "aimclusterservicetemplates.aim.eai.amd.com",
    "aimtemplatecaches.aim.eai.amd.com",
    "aimartifacts.aim.eai.amd.com",
    "aimprofiles.aim.eai.amd.com",
    "aimprofilecaches.aim.eai.amd.com",
]

SERVICES = "aimservices.aim.eai.amd.com"
MODELS = "aimmodels.aim.eai.amd.com"
CLUSTER_MODELS = "aimclustermodels.aim.eai.amd.com"
TEMPLATE_CACHES = "aimtemplatecaches.aim.eai.amd.com"
ARTIFACTS = "aimartifacts.aim.eai.amd.com"


def operator(image):
    return {
        "kind": "Deployment",
        "metadata": {"name": "aim-engine-controller-manager", "namespace": "aim-system"},
        "spec": {"template": {"spec": {"containers": [
            {"name": "manager", "image": image},
            {"name": "kube-rbac-proxy", "image": "kube-rbac-proxy:v0.1.0"},
        ]}}},
    }


def item(kind, name, namespace="team", **fields):
    metadata = {"name": name, "uid": "uid-" + name}
    if namespace:
        metadata["namespace"] = namespace
    metadata.update(fields.pop("metadata", {}))
    return {"kind": kind, "metadata": metadata, **fields}


def service(name, spec, pipeline="profile", **metadata):
    return item("AIMService", name, spec=spec, status={"reconcilerPipeline": pipeline}, metadata=metadata)


def owned_by(api_version, kind, name):
    return {"ownerReferences": [{"apiVersion": api_version, "kind": kind, "name": name, "uid": "uid-" + name}]}


class UpgradePreflightTest(unittest.TestCase):
    def run_preflight(self, resources=None, api_resources=API_RESOURCES):
        objects = {
            "deployments": [operator("docker.io/amdenterpriseai/aim-engine:v0.2.7")],
            SERVICES: [service("ready", {"profile": {"name": "ready-profile"}})],
            MODELS: [item("AIMModel", "image-model", spec={"image": "aim:1.0.0"})],
        }
        objects.update(resources or {})
        with tempfile.TemporaryDirectory() as tmp:
            cluster = Path(tmp)
            fake_bin = cluster / "bin"
            fake_bin.mkdir()
            kubectl = fake_bin / "kubectl"
            kubectl.write_text(FAKE_KUBECTL)
            kubectl.chmod(0o755)
            (cluster / "api-resources.txt").write_text("\n".join(api_resources) + "\n")
            for resource, items in objects.items():
                (cluster / (resource + ".json")).write_text(json.dumps({"items": items}))
            env = {**os.environ, "FAKE_CLUSTER": tmp, "PATH": f"{fake_bin}{os.pathsep}{os.environ['PATH']}"}
            result = subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True, check=False)
            self.calls = (cluster / "calls.log").read_text().splitlines()
        for call in self.calls:
            self.assertIn(call.split()[0], ("get", "api-resources"), call)
        return result

    def assert_fails(self, result, check, obj, section):
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        line = f"FAIL [{check}] {obj}: "
        failures = [entry for entry in result.stdout.splitlines() if entry.startswith(line)]
        self.assertTrue(failures, f"no line starting with {line!r} in:\n{result.stdout}")
        for failure in failures:
            self.assertTrue(failure.endswith(f". See #{section}"), failure)

    def test_clean_cluster_passes(self):
        result = self.run_preflight()

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for check in ("minimum-version", "template-pipeline-services", "unservable-services",
                      "models-without-native-path", "template-objects", "template-finalizers",
                      "stale-owner-references"):
            self.assertIn(f"ok   [{check}]", result.stdout)
        self.assertIn("MANUAL [v1alpha1-clients]", result.stdout)
        self.assertIn("All blocking checks passed.", result.stdout)

    def test_minimum_version_rejects_an_older_operator(self):
        result = self.run_preflight({"deployments": [operator("docker.io/amdenterpriseai/aim-engine:v0.2.6")]})

        self.assert_fails(result, "minimum-version", "Deployment aim-system/aim-engine-controller-manager",
                          "minimum-version")
        self.assertIn("runs v0.2.6, upgrade to v0.2.7 or later first", result.stdout)

    def test_minimum_version_accepts_later_tags(self):
        for image in ("aim-engine:v0.2.10", "aim-engine:0.3.0", "registry.local:5000/aim-engine:v0.2.8-rc1",
                      "aim-engine:v0.2.7@sha256:0123"):
            with self.subTest(image=image):
                result = self.run_preflight({"deployments": [operator(image)]})
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def assert_asks_to_confirm_the_version(self, result, obj, problem):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(f"MANUAL [minimum-version] {obj}: {problem}, so confirm by hand that ", result.stdout)
        self.assertIn("v0.2.7 or later. See #minimum-version", result.stdout)
        self.assertNotIn("ok   [minimum-version]", result.stdout)

    def test_minimum_version_asks_to_confirm_an_unreadable_tag(self):
        for image in ("aim-engine@sha256:0123", "registry.local:5000/aim-engine", "aim-engine:latest"):
            with self.subTest(image=image):
                result = self.run_preflight({"deployments": [operator(image)]})
                self.assert_asks_to_confirm_the_version(
                    result, "Deployment aim-system/aim-engine-controller-manager",
                    f"cannot read a version from the image tag of {image}")

    def test_minimum_version_asks_to_confirm_without_the_operator_deployment(self):
        result = self.run_preflight({"deployments": []})

        self.assert_asks_to_confirm_the_version(result, "Deployment aim-engine-controller-manager",
                                                "not found in any namespace")

        unreconciled = item("AIMService", "old", spec={"profile": {"name": "p"}})
        result = self.run_preflight({"deployments": [], SERVICES: [unreconciled]})
        self.assert_fails(result, "minimum-version", "AIMService team/old", "minimum-version")

    def test_minimum_version_requires_reconciler_pipeline_status(self):
        unreconciled = item("AIMService", "old", spec={"profile": {"name": "p"}})

        result = self.run_preflight({SERVICES: [unreconciled]})

        self.assert_fails(result, "minimum-version", "AIMService team/old", "minimum-version")

    def test_template_pipeline_services(self):
        annotation = {"annotations": {"aim.eai.amd.com/reconciler-pipeline": "profile"}}
        cases = {
            "model name without opt-in": service("svc", {"model": {"name": "m"}}, pipeline="template"),
            "opt-in not yet reconciled": service("svc", {"model": {"name": "m"}}, pipeline="template", **annotation),
            "profile pipeline without opt-in": service("svc", {"model": {"name": "m"}}),
        }
        for name, svc in cases.items():
            with self.subTest(name):
                result = self.run_preflight({SERVICES: [svc]})
                self.assert_fails(result, "template-pipeline-services", "AIMService team/svc",
                                  "move-template-services")

        moved = service("svc", {"model": {"name": "m"}, "template": {"name": "t"}}, **annotation)
        self.assertEqual(self.run_preflight({SERVICES: [moved]}).returncode, 0)

    def test_unservable_services(self):
        cases = {
            "uses spec.model.custom": {"profile": {"name": "p"}, "model": {"custom": {"baseImage": "aim:1.0.0"}}},
            "uses spec.template without spec.model or spec.profile": {"template": {"name": "t"}},
        }
        annotation = {"annotations": {"aim.eai.amd.com/reconciler-pipeline": "profile"}}
        for problem, spec in cases.items():
            with self.subTest(problem):
                result = self.run_preflight({SERVICES: [service("svc", spec, **annotation)]})
                self.assert_fails(result, "unservable-services", "AIMService team/svc", "replace-unservable-services")
                self.assertIn(f"AIMService team/svc: {problem}.", result.stdout)

    def test_models_without_native_path(self):
        cases = {
            "uses spec.custom": {"spec": {"custom": {"baseImage": "aim-base:1.0.0"}}},
            "uses spec.customTemplates": {"spec": {"image": "aim:1.0.0", "customTemplates": [{"name": "t"}]}},
            "uses spec.modelSources": {"spec": {"modelSources": [{"sourceUri": "hf://org/model"}]}},
            "sets spec.aimId on an image model": {"spec": {"image": "aim:1.0.0", "aimId": "org/model"}},
            "was generated for an AIMService with spec.model.custom": {
                "spec": {"modelId": "org/model"},
                "metadata": {"annotations": {"aim.eai.amd.com/legacy-auto-custom-spec": "{}"}},
            },
            "sets spec.discovery.extractMetadata: false on an image model": {
                "spec": {"image": "aim:1.0.0", "discovery": {"extractMetadata": False}},
            },
        }
        for problem, fields in cases.items():
            with self.subTest(problem):
                result = self.run_preflight({MODELS: [item("AIMModel", "model", **fields)]})
                self.assert_fails(result, "models-without-native-path", "AIMModel team/model",
                                  "replace-models-without-a-native-path")
                self.assertIn(f"AIMModel team/model: {problem}.", result.stdout)

        cluster_model = item("AIMClusterModel", "cluster-model", namespace=None, spec={"custom": {}})
        result = self.run_preflight({CLUSTER_MODELS: [cluster_model]})
        self.assert_fails(result, "models-without-native-path", "AIMClusterModel cluster-model",
                          "replace-models-without-a-native-path")

    def test_template_objects(self):
        cases = {
            "aimservicetemplates.aim.eai.amd.com": item("AIMServiceTemplate", "template"),
            "aimclusterservicetemplates.aim.eai.amd.com": item("AIMClusterServiceTemplate", "template", namespace=None),
            TEMPLATE_CACHES: item("AIMTemplateCache", "template"),
        }
        for resource, obj in cases.items():
            with self.subTest(resource):
                result = self.run_preflight({resource: [obj]})
                name = obj["kind"] + (" team/template" if "namespace" in obj["metadata"] else " template")
                self.assert_fails(result, "template-objects", name, "delete-template-objects")

    def test_template_finalizers(self):
        legacy = service("svc", {"profile": {"name": "p"}},
                         finalizers=["aim.eai.amd.com/cache-cleanup", "aim.eai.amd.com/template-cache-cleanup"])
        cache = item("AIMTemplateCache", "cache", metadata={"finalizers": ["aim.eai.amd.com/artifactcleanup"]})

        result = self.run_preflight({SERVICES: [legacy], TEMPLATE_CACHES: [cache]})

        self.assert_fails(result, "template-finalizers", "AIMService team/svc", "delete-template-objects")
        self.assert_fails(result, "template-finalizers", "AIMTemplateCache team/cache", "delete-template-objects")
        self.assertNotIn("finalizer aim.eai.amd.com/cache-cleanup.", result.stdout)

    def test_stale_owner_references(self):
        resources = {
            "inferenceservices.serving.kserve.io": [
                item("InferenceService", "stale", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMService", "svc")),
                item("InferenceService", "current", metadata=owned_by("aim.eai.amd.com/v1alpha2", "AIMService", "svc")),
            ],
            "jobs.batch": [
                item("Job", "discovery", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMServiceTemplate", "t")),
            ],
            "aimprofiles.aim.eai.amd.com": [
                item("AIMProfile", "kept", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMClusterModelSource", "s")),
            ],
        }

        result = self.run_preflight(resources)

        section = "remove-stale-v1alpha1-owner-references"
        self.assert_fails(result, "stale-owner-references", "InferenceService team/stale", section)
        self.assert_fails(result, "stale-owner-references", "Job team/discovery", section)
        self.assertNotIn("team/current", result.stdout)
        self.assertNotIn("team/kept", result.stdout)

    def test_skips_resource_types_the_cluster_does_not_have(self):
        missing = {"scaledobjects.keda.sh", "httproutes.gateway.networking.k8s.io"}

        result = self.run_preflight(api_resources=[r for r in API_RESOURCES if r not in missing])

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse([call for call in self.calls if call.split()[1] in missing])

    def test_lists_dedicated_template_caches_with_sizes(self):
        resources = {
            TEMPLATE_CACHES: [
                item("AIMTemplateCache", "dedicated", spec={"mode": "Dedicated"}),
                item("AIMTemplateCache", "shared", spec={"mode": "Shared"}),
            ],
            ARTIFACTS: [
                item("AIMArtifact", "dedicated-weights", status={"allocatedSize": "120Gi"},
                     metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMTemplateCache", "dedicated")),
                item("AIMArtifact", "shared-weights", status={"allocatedSize": "80Gi"}),
            ],
        }

        result = self.run_preflight(resources)

        self.assertIn("INFO [dedicated-template-caches] AIMTemplateCache team/dedicated: "
                      "AIMArtifact dedicated-weights uses 120Gi", result.stdout)
        self.assertNotIn("shared-weights", result.stdout)

    def test_requires_jq(self):
        with tempfile.TemporaryDirectory() as tmp:
            kubectl = Path(tmp) / "kubectl"
            kubectl.write_text("#!/bin/sh\nexit 1\n")
            kubectl.chmod(0o755)
            result = subprocess.run([shutil.which("bash"), str(SCRIPT)], env={"PATH": tmp},
                                    capture_output=True, text=True, check=False)

        self.assertEqual(result.returncode, 2)
        self.assertIn("jq is required", result.stderr)

    def test_every_section_the_script_names_exists_in_the_guide(self):
        script = SCRIPT.read_text()
        sections = set(re.findall(r"See #([a-z0-9-]+)", script))
        sections |= set(re.findall(r"^\s*(?:report|manual) [a-z0-9-]+ ([a-z0-9-]+)", script, re.MULTILINE))
        headings = {
            re.sub(r"[^a-z0-9 -]", "", heading.strip().lower()).replace(" ", "-")
            for heading in re.findall(r"^#+ (.+)$", GUIDE.read_text(), re.MULTILINE)
        }

        self.assertTrue(sections)
        self.assertEqual(sections - headings, set())


if __name__ == "__main__":
    unittest.main()
