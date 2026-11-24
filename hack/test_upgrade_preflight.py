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
# every call so tests can check that the preflight only reads. With
# <resource>.error, it fails like a forbidden list: the error on stderr, an
# empty list on stdout and exit 1.
FAKE_KUBECTL = """#!/bin/sh
echo "$*" >> "$FAKE_CLUSTER/calls.log"
case "$1" in
  api-resources) cat "$FAKE_CLUSTER/api-resources.txt" ;;
  get)
    if [ -f "$FAKE_CLUSTER/$2.error" ]; then
      cat "$FAKE_CLUSTER/$2.error" >&2
      echo '{"items": []}'
      exit 1
    fi
    cat "$FAKE_CLUSTER/$2.json" 2>/dev/null || echo '{"items": []}' ;;
  *) echo "fake kubectl: unexpected command $*" >&2; exit 1 ;;
esac
"""

API_RESOURCES = [
    "deployments.apps",
    "pods",
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
TEMPLATE_CRDS = ["aimservicetemplates.aim.eai.amd.com", "aimclusterservicetemplates.aim.eai.amd.com", TEMPLATE_CACHES]
ARTIFACTS = "aimartifacts.aim.eai.amd.com"
INFERENCE_SERVICES = "inferenceservices.serving.kserve.io"
PODS = "pods"
CRDS = "customresourcedefinitions"
KEEP = {"helm.sh/resource-policy": "keep"}
FORBIDDEN = 'Error from server (Forbidden): {} is forbidden: User "restricted" cannot list resource'


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


def service(name, spec, pipeline="profile", profile_cache_ready="True", **metadata):
    # v0.2.7 reports ProfileCacheReady only when the profile has model sources.
    conditions = [] if profile_cache_ready is None else [{"type": "ProfileCacheReady", "status": profile_cache_ready}]
    status = {"reconcilerPipeline": pipeline, "conditions": conditions}
    return item("AIMService", name, spec=spec, status=status, metadata=metadata)


def owned_by(api_version, kind, name):
    return {"ownerReferences": [{"apiVersion": api_version, "kind": kind, "name": name, "uid": "uid-" + name}]}


def inference_service(service_name, profile_label=True):
    metadata = owned_by("aim.eai.amd.com/v1alpha2", "AIMService", service_name)
    if profile_label:
        metadata["labels"] = {"aim.eai.amd.com/profile": "profile"}
    return item("InferenceService", service_name + "-isvc", metadata=metadata)


def predictor_pod(service_name, revision="new", profile_label=True, phase="Running", **status):
    labels = {"serving.kserve.io/inferenceservice": service_name + "-isvc"}
    if profile_label:
        labels["aim.eai.amd.com/profile"] = "profile"
    return item("Pod", f"{service_name}-isvc-predictor-{revision}", metadata={"labels": labels},
                status={"phase": phase, **status})


def crd(name, served=("v1alpha1",), annotations=None):
    versions = [{"name": version, "served": version in served} for version in ("v1alpha1", "v1alpha2")]
    return item("CustomResourceDefinition", name, namespace=None, spec={"versions": versions},
                metadata={"annotations": annotations or {}})


def pod(name, claim, phase="Running", namespace="team"):
    volumes = [{"name": "config", "configMap": {"name": "c"}},
               {"name": "weights", "persistentVolumeClaim": {"claimName": claim}}]
    return item("Pod", name, namespace=namespace, spec={"volumes": volumes}, status={"phase": phase})


class UpgradePreflightTest(unittest.TestCase):
    def run_preflight(self, resources=None, api_resources=API_RESOURCES, errors=()):
        objects = {
            CRDS: [crd(SERVICES, served=("v1alpha1", "v1alpha2"))],
            "deployments": [operator("docker.io/amdenterpriseai/aim-engine:v0.2.7")],
            SERVICES: [service("ready", {"profile": {"name": "ready-profile"}})],
            INFERENCE_SERVICES: [inference_service("ready")],
            PODS: [predictor_pod("ready")],
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
            for resource in errors:
                (cluster / (resource + ".error")).write_text(FORBIDDEN.format(resource) + "\n")
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
        result = self.run_preflight({SERVICES: [moved], INFERENCE_SERVICES: [inference_service("svc")],
                                     PODS: [predictor_pod("svc")]})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_template_pipeline_services_waits_until_the_move_finishes(self):
        annotation = {"annotations": {"aim.eai.amd.com/reconciler-pipeline": "profile"}}
        moved = {SERVICES: [service("svc", {"model": {"name": "m"}}, **annotation)],
                 INFERENCE_SERVICES: [inference_service("svc")], PODS: [predictor_pod("svc")]}
        old_pod = predictor_pod("svc", "old", profile_label=False)
        cases = {
            "InferenceService not rewritten": ("(InferenceService svc-isvc still runs the template)",
                                               {INFERENCE_SERVICES: [inference_service("svc", profile_label=False)]}),
            "old pod serving, new pod Pending": ("(predictor pod svc-isvc-predictor-old still runs the template)",
                                                 {PODS: [old_pod, predictor_pod("svc", phase="Pending")]}),
        }
        for name, (problem, resources) in cases.items():
            with self.subTest(name):
                result = self.run_preflight({**moved, **resources})
                self.assert_fails(result, "template-pipeline-services", "AIMService team/svc", "move-template-services")
                self.assertIn(f"AIMService team/svc: has not finished moving {problem}.", result.stdout)

        for name, pods in {"rolled out": [predictor_pod("svc")], "scaled to zero": [],
                           "old pod finished": [predictor_pod("svc", "old", profile_label=False, phase="Failed")],
                           "pod of another InferenceService": [predictor_pod("other", "old", profile_label=False)],
                           }.items():
            with self.subTest(name):
                result = self.run_preflight({**moved, PODS: pods})
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_template_pipeline_services_passes_unhealthy_profile_services(self):
        crash_looping = {"containerStatuses": [{"name": "kserve-container", "ready": False,
                                                "state": {"waiting": {"reason": "CrashLoopBackOff"}}}]}
        cases = {
            "ProfileNotFound, no InferenceService": (
                {SERVICES: [service("svc", {"profile": {"name": "missing"}}, profile_cache_ready=None)], PODS: []}, False),
            "Pending on GPUs": ({PODS: [predictor_pod("svc", phase="Pending")]}, True),
            "crash-looping": ({PODS: [predictor_pod("svc", **crash_looping)]}, True),
            "healthy, profile without model sources": (
                {SERVICES: [service("svc", {"profile": {"name": "p"}}, profile_cache_ready=None)]}, True),
            "profile cache not Ready": (
                {SERVICES: [service("svc", {"profile": {"name": "p"}}, profile_cache_ready="False")], PODS: []}, True),
        }
        for name, (resources, has_isvc) in cases.items():
            with self.subTest(name):
                base = {SERVICES: [service("svc", {"profile": {"name": "p"}})], PODS: [predictor_pod("svc")],
                        INFERENCE_SERVICES: [inference_service("svc")] if has_isvc else []}
                result = self.run_preflight({**base, **resources})
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("ok   [template-pipeline-services]", result.stdout)

    def test_template_pipeline_services_accepts_an_inference_service_the_migration_tool_preserved(self):
        def preserved(generation="3"):
            return service("svc", {"model": {"name": "m"}}, generation=3, annotations={
                "aim.eai.amd.com/reconciler-pipeline": "profile",
                "aim.eai.amd.com/migration-preserve-inference-service-uid": "uid-svc-isvc",
                "aim.eai.amd.com/migration-preserve-inference-service-generation": generation})
        old_pod = predictor_pod("svc", "old", profile_label=False)
        unlabelled = {INFERENCE_SERVICES: [inference_service("svc", profile_label=False)], PODS: [old_pod]}

        rolling_out = [old_pod, predictor_pod("svc", "next", profile_label=False, phase="Pending")]
        for name, pods in {"serving": [old_pod], "rolling out": rolling_out}.items():
            with self.subTest(name):
                result = self.run_preflight({SERVICES: [preserved()], **unlabelled, PODS: pods})
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

        result = self.run_preflight({**unlabelled, SERVICES: [preserved("2")]})
        self.assert_fails(result, "template-pipeline-services", "AIMService team/svc", "move-template-services")
        self.assertIn("AIMService team/svc: has not finished moving (InferenceService svc-isvc still runs the template).",
                      result.stdout)

    def test_template_pipeline_services_fails_while_a_pod_mounts_a_template_cache_pvc(self):
        resources = {
            ARTIFACTS: [
                item("AIMArtifact", "dedicated-weights", status={"persistentVolumeClaim": "dedicated-pvc"},
                     metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMTemplateCache", "dedicated")),
                item("AIMArtifact", "shared-weights", status={"persistentVolumeClaim": "shared-pvc"}),
            ],
            PODS: [
                pod("old-predictor", "dedicated-pvc"),
                pod("finished-download", "dedicated-pvc", phase="Succeeded"),
                pod("same-claim-name", "dedicated-pvc", namespace="other"),
                pod("new-predictor", "shared-pvc"),
            ],
        }

        result = self.run_preflight(resources)

        self.assert_fails(result, "template-pipeline-services", "Pod team/old-predictor", "move-template-services")
        self.assertIn("Pod team/old-predictor: mounts PVC dedicated-pvc of AIMArtifact dedicated-weights,"
                      " which AIMTemplateCache dedicated owns.", result.stdout)
        for name in ("team/finished-download", "other/same-claim-name", "team/new-predictor"):
            self.assertNotIn(name, result.stdout)

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
        model_owned = {"ownerReferences": [
            owned_by("aim.eai.amd.com/v1alpha2", "AIMService", "svc")["ownerReferences"][0],
            owned_by("aim.eai.amd.com/v1alpha1", "AIMModel", "m")["ownerReferences"][0],
        ]}
        resources = {
            INFERENCE_SERVICES: [
                inference_service("ready"),
                item("InferenceService", "stale", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMService", "svc")),
                item("InferenceService", "current", metadata=owned_by("aim.eai.amd.com/v1alpha2", "AIMService", "svc")),
            ],
            "jobs.batch": [
                item("Job", "discovery", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMServiceTemplate", "t")),
            ],
            "aimprofiles.aim.eai.amd.com": [
                item("AIMProfile", "derived", metadata=model_owned),
                item("AIMProfile", "kept", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMClusterModelSource", "s")),
            ],
            MODELS: [
                item("AIMModel", "service-model", spec={"image": "aim:1.0.0"},
                     metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMService", "svc")),
            ],
        }

        result = self.run_preflight(resources)

        section = "remove-stale-v1alpha1-owner-references"
        self.assert_fails(result, "stale-owner-references", "InferenceService team/stale", section)
        self.assertIn(
            "InferenceService team/stale: is owned by v1alpha1 AIMService svc; rewrite the reference with:"
            " kubectl patch inferenceservice stale -n team --type json -p"
            """ '[{"op":"replace","path":"/metadata/ownerReferences/0/apiVersion","""
            """"value":"aim.eai.amd.com/v1alpha2"}]'.""",
            result.stdout)
        self.assertIn("""AIMProfile team/derived: is owned by v1alpha1 AIMModel m; rewrite the reference with:"""
                      """ kubectl patch aimprofile derived -n team --type json -p"""
                      """ '[{"op":"replace","path":"/metadata/ownerReferences/1/apiVersion",""", result.stdout)
        self.assert_fails(result, "stale-owner-references", "AIMModel team/service-model", section)
        self.assert_fails(result, "stale-owner-references", "Job team/discovery", section)
        self.assertIn(
            "Job team/discovery: is owned by v1alpha1 AIMServiceTemplate t; delete it with its owner, or remove the"
            """ reference with: kubectl patch job discovery -n team --type json -p"""
            """ '[{"op":"remove","path":"/metadata/ownerReferences/0"}]'.""",
            result.stdout)
        self.assertNotIn("team/current", result.stdout)
        self.assertNotIn("team/kept", result.stdout)

    def test_a_failed_list_fails_the_checks_that_read_it(self):
        cases = {
            "aimservicetemplates.aim.eai.amd.com": ["template-objects", "template-settings"],
            TEMPLATE_CACHES: ["template-objects", "template-finalizers", "dedicated-template-caches"],
            INFERENCE_SERVICES: ["template-pipeline-services", "stale-owner-references"],
            "jobs.batch": ["stale-owner-references"],
            PODS: ["template-pipeline-services"],
            ARTIFACTS: ["template-pipeline-services", "stale-owner-references", "dedicated-template-caches"],
        }
        for resource, checks in cases.items():
            with self.subTest(resource):
                result = self.run_preflight(errors=[resource])
                for check in checks:
                    self.assert_fails(result, check, resource, "run-the-upgrade-preflight")
                    self.assertIn(f"FAIL [{check}] {resource}: cannot list it: {FORBIDDEN.format(resource)}.",
                                  result.stdout)
                    self.assertNotIn(f"ok   [{check}]", result.stdout)

    def test_an_upgraded_cluster_prints_one_line(self):
        scrubbed = item("AIMService", "svc", spec={"model": {"name": "m"}}, status={"status": "Running"})
        for name, aimservice_crd in {
            "v1alpha1 unserved": crd(SERVICES, served=("v1alpha2",)),
            "v1alpha1 removed": item("CustomResourceDefinition", SERVICES, namespace=None,
                                     spec={"versions": [{"name": "v1alpha2", "served": True}]}),
        }.items():
            with self.subTest(name):
                result = self.run_preflight({CRDS: [aimservice_crd], SERVICES: [scrubbed]})

                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(result.stdout.splitlines(), [
                    "Already upgraded: the AIMService CRD no longer serves aim.eai.amd.com/v1alpha1,"
                    " so there is nothing to check."])

    def test_kept_template_crds_turn_template_findings_into_manual_lines(self):
        kept = [crd(SERVICES, served=("v1alpha1", "v1alpha2"))]
        kept += [crd(name, annotations=KEEP) for name in TEMPLATE_CRDS]
        resources = {
            CRDS: kept,
            TEMPLATE_CACHES: [item("AIMTemplateCache", "cache",
                                   metadata={"finalizers": ["aim.eai.amd.com/artifactcleanup"]})],
            ARTIFACTS: [item("AIMArtifact", "weights",
                             metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMTemplateCache", "cache"))],
        }

        result = self.run_preflight(resources)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("FAIL", result.stdout)
        self.assertIn("helm.sh/resource-policy=keep, so template objects don't block", result.stdout)
        for check, obj in (("template-objects", "AIMTemplateCache team/cache"),
                           ("template-finalizers", "AIMTemplateCache team/cache"),
                           ("stale-owner-references", "AIMArtifact team/weights")):
            lines = [line for line in result.stdout.splitlines() if line.startswith(f"MANUAL [{check}] {obj}: ")]
            self.assertTrue(lines, f"no MANUAL [{check}] line for {obj} in:\n{result.stdout}")
            self.assertTrue(all(line.endswith(". See #remove-the-template-crds") for line in lines), lines)

        blocking = {
            "unmoved service": ({SERVICES: [service("svc", {"model": {"name": "m"}}, pipeline="template")]},
                                "template-pipeline-services", "AIMService team/svc", "move-template-services"),
            "mounted template cache PVC": (
                {ARTIFACTS: [item("AIMArtifact", "weights", status={"persistentVolumeClaim": "weights-pvc"},
                                  metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMTemplateCache", "cache"))],
                 PODS: [pod("old-predictor", "weights-pvc")]},
                "template-pipeline-services", "Pod team/old-predictor", "move-template-services"),
            "v1alpha1 AIMService owner": (
                {"configmaps": [
                    item("ConfigMap", "c", metadata=owned_by("aim.eai.amd.com/v1alpha1", "AIMService", "s"))]},
                "stale-owner-references", "ConfigMap team/c", "remove-stale-v1alpha1-owner-references"),
        }
        for name, (extra, check, obj, section) in blocking.items():
            with self.subTest(name):
                self.assert_fails(self.run_preflight({**resources, **extra}), check, obj, section)

        partly_kept = {**resources, CRDS: kept[:-1] + [crd(TEMPLATE_CACHES)]}
        self.assert_fails(self.run_preflight(partly_kept), "template-objects", "AIMTemplateCache team/cache",
                          "delete-template-objects")

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

    def test_lists_template_settings_the_move_drops(self):
        moved = {"annotations": {"aim.eai.amd.com/reconciler-pipeline": "profile"}}
        generated = service("generated", {"model": {"name": "m"}}, **moved)
        generated["status"]["resolvedTemplate"] = {"name": "generated"}
        kept = [crd(SERVICES, served=("v1alpha1", "v1alpha2"))]
        kept += [crd(name, annotations=KEEP) for name in TEMPLATE_CRDS]
        resources = {
            CRDS: kept,
            SERVICES: [
                service("hand-written", {"model": {"name": "m"}, "template": {"name": "custom"}}, **moved),
                generated,
                service("overrides", {"model": {"name": "m"}, "overrides": {"metric": "latency"}}, **moved),
            ],
            "aimservicetemplates.aim.eai.amd.com": [
                item("AIMServiceTemplate", "custom", namespace="other", spec={"customProfile": {}}),
                item("AIMServiceTemplate", "custom", spec={"env": [{"name": "A", "value": "1"}],
                                                           "resources": {"limits": {"cpu": "4"}}}),
            ],
            "aimclusterservicetemplates.aim.eai.amd.com": [
                item("AIMClusterServiceTemplate", "generated", namespace=None, spec={"env": [{"name": "A"}]},
                     metadata=owned_by("aim.eai.amd.com/v1alpha2", "AIMClusterModel", "m")),
            ],
        }

        result = self.run_preflight(resources)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("INFO [template-settings] AIMService team/hand-written: its AIMServiceTemplate custom sets env,"
                      " resources, which the profile pipeline ignores. See #move-template-services", result.stdout)
        self.assertIn("INFO [template-settings] AIMService team/overrides: sets spec.overrides, which the profile"
                      " pipeline ignores. See #move-template-services", result.stdout)
        self.assertNotIn("team/generated", result.stdout)

    def test_guide_link_has_the_form_the_release_stamps_with_its_tag(self):
        # compile-release.yaml replaces /blob/main/ on this line with the release tag.
        self.assertIn('\nguide="https://github.com/amd-enterprise-ai/aim-engine/blob/main/docs/docs/admin/upgrading.md"\n',
                      SCRIPT.read_text())

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
        sections |= set(re.findall(r"^\s*(?:report|manual)\w* \S+ ([a-z0-9-]+)", script, re.MULTILINE))
        headings = {
            re.sub(r"[^a-z0-9 -]", "", heading.strip().lower()).replace(" ", "-")
            for heading in re.findall(r"^#+ (.+)$", GUIDE.read_text(), re.MULTILINE)
        }

        self.assertTrue(sections)
        self.assertEqual(sections - headings, set())


if __name__ == "__main__":
    unittest.main()
