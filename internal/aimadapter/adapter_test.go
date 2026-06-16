// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package aimadapter

import (
	"fmt"
	"testing"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const testAdapterPVC = "base-adapters-pvc"

func succeededJob(name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		},
	}
}

// withSyncedSubtree marks the per-service subtree-sync Job as succeeded so
// Compose reports SubtreeReady (the ISVC mount gate).
func withSyncedSubtree(deps Dependencies, svc *aimv1alpha1.AIMService) Dependencies {
	deps.SubtreeSyncJob = controllerutils.FetchResult[*batchv1.Job]{
		Value: succeededJob(SubtreeSyncJobName(svc)),
	}
	return deps
}

// adapterArtifact builds a Ready adapter artifact bound to the given parent.
func adapterArtifact(name, parent string) *aimv1alpha1.AIMArtifact {
	return &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			ParentArtifact: parent,
			ModelID:        "org/base",
			SourceURI:      "hf://org/" + name,
		},
		Status: aimv1alpha1.AIMArtifactStatus{
			Status:      constants.AIMStatusReady,
			AdapterPath: name,
		},
	}
}

// modelParent builds a model artifact named "base" with the given adapter disk PVC.
func modelParent(pvc string) *aimv1alpha1.AIMArtifact {
	return &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{Type: aimv1alpha1.ArtifactTypeModel, ModelID: "org/base"},
		Status:     aimv1alpha1.AIMArtifactStatus{AdapterPersistentVolumeClaim: pvc},
	}
}

// isvcWithContainer builds a minimal InferenceService with a single predictor
// container so AddVolumeMount has somewhere to attach the adapter disk.
func isvcWithContainer() *servingv1beta1.InferenceService {
	return &servingv1beta1.InferenceService{
		Spec: servingv1beta1.InferenceServiceSpec{
			Predictor: servingv1beta1.PredictorSpec{
				PodSpec: servingv1beta1.PodSpec{
					Containers: []corev1.Container{{Name: "main"}},
				},
			},
		},
	}
}

func serviceWithAdapters(names ...string) *aimv1alpha1.AIMService {
	refs := make([]aimv1alpha1.AIMServiceAdapterReference, 0, len(names))
	for _, n := range names {
		refs = append(refs, aimv1alpha1.AIMServiceAdapterReference{Name: n, Kind: aimv1alpha1.AdapterKindAIMArtifact})
	}
	return &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "default", UID: types.UID("svc-uid-1")},
		Spec:       aimv1alpha1.AIMServiceSpec{Adapters: refs},
	}
}

// statusAdapter builds a prior Downloaded per-adapter status entry (as it would
// appear on service.Status.Adapters from a previous reconcile).
func statusAdapter(name string) aimv1alpha1.AIMServiceAdapterStatus {
	return aimv1alpha1.AIMServiceAdapterStatus{
		Name:        name,
		AdapterPath: name,
		ModelID:     "org/base",
		State:       aimv1alpha1.AdapterStateDownloaded,
	}
}

// depsWith assembles Dependencies with a resolved parent and the given adapter
// artifacts so Compose can run end to end.
func depsWith(parent *aimv1alpha1.AIMArtifact, artifacts map[string]*aimv1alpha1.AIMArtifact) Dependencies {
	deps := Dependencies{
		AdapterArtifacts: map[string]controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{},
		StagingJobs:      map[string]controllerutils.FetchResult[*batchv1.Job]{},
	}
	if parent != nil {
		pf := controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{Value: parent}
		deps.ParentArtifact = &pf
	}
	for name, art := range artifacts {
		deps.AdapterArtifacts[name] = controllerutils.FetchResult[*aimv1alpha1.AIMArtifact]{Value: art}
	}
	return deps
}

func TestComposeHappyPath(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})

	st := Compose(svc, deps)

	if st.ConfigErr != nil {
		t.Fatalf("unexpected config error: %v", st.ConfigErr)
	}
	if st.AdapterDiskPVC != testAdapterPVC {
		t.Errorf("AdapterDiskPVC = %q, want %s", st.AdapterDiskPVC, testAdapterPVC)
	}
	if len(st.Adapters) != 1 || st.Adapters[0].State != aimv1alpha1.AdapterStatePending {
		t.Errorf("expected single pending adapter (no job yet), got %+v", st.Adapters)
	}
}

func TestComposeParentMismatch(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	// Adapter points at a different parent than the resolved base model.
	deps := depsWith(modelParent("pvc"), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "other-base"),
	})

	st := Compose(svc, deps)
	if st.ConfigErr == nil {
		t.Error("expected ParentArtifactMismatch config error")
	}
}

func TestComposeDuplicatePath(t *testing.T) {
	svc := serviceWithAdapters("lora-a", "lora-b")
	a := adapterArtifact("lora-a", "base")
	b := adapterArtifact("lora-b", "base")
	// Force both adapters to the same on-disk path.
	a.Status.AdapterPath = "collide"
	b.Status.AdapterPath = "collide"
	deps := depsWith(modelParent("pvc"), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": a, "lora-b": b,
	})

	st := Compose(svc, deps)
	if st.ConfigErr == nil {
		t.Error("expected DuplicateAdapterPath config error")
	}
}

func TestComposeMissingArtifact(t *testing.T) {
	svc := serviceWithAdapters("lora-missing")
	// No entry in AdapterArtifacts => treated as not found.
	deps := depsWith(modelParent("pvc"), nil)

	st := Compose(svc, deps)
	if st.ConfigErr != nil {
		t.Errorf("missing artifact must be transient, not a config error: %v", st.ConfigErr)
	}
	if len(st.Adapters) != 1 || st.Adapters[0].LastError != ReasonNotFound {
		t.Errorf("expected AdapterArtifactNotFound, got %+v", st.Adapters)
	}
	if st.Ready {
		t.Error("adapters must not be ready when one is missing")
	}
}

func TestComposeNoParentDisk(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(""), map[string]*aimv1alpha1.AIMArtifact{ // parent has no adapter PVC yet
		"lora-a": adapterArtifact("lora-a", "base"),
	})

	st := Compose(svc, deps)
	if st.AdapterDiskPVC != "" {
		t.Errorf("expected empty AdapterDiskPVC, got %q", st.AdapterDiskPVC)
	}
	if st.Ready {
		t.Error("adapters cannot be ready without a parent adapter disk")
	}
}

// Mountable (Ready) is gated on the subtree, NOT on downloads: a fully staged
// adapter with no subtree-sync Job is still not mountable.
func TestNotMountableWithoutSubtree(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})
	deps.StagingJobs["lora-a"] = controllerutils.FetchResult[*batchv1.Job]{
		Value: succeededJob(StagingJobName(svc, "lora-a")),
	}

	st := Compose(svc, deps)
	if st.SubtreeReady {
		t.Error("subtree must not be ready without a succeeded subtree-sync job")
	}
	if st.Ready {
		t.Error("adapters must not be mountable before the subtree exists, even when all downloaded")
	}
	if !st.AllStaged {
		t.Error("AllStaged should be true once the staging job succeeded")
	}
	if st.Adapters[0].State != aimv1alpha1.AdapterStateDownloaded {
		t.Errorf("adapter state = %q, want Downloaded", st.Adapters[0].State)
	}
}

// Once the subtree-sync Job succeeds the service is mountable (MountReady) even
// before downloads finish. Whether that mountable state gates the ISVC (Ready)
// then depends on the adapter mode: dynamic loads asynchronously (Ready ==
// mountable), while static must wait for downloads because the runtime
// enumerates the adapter directory once at launch.
func TestMountableWhenSubtreeEnsured(t *testing.T) {
	mkDeps := func(svc *aimv1alpha1.AIMService) Dependencies {
		return withSyncedSubtree(
			depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
				"lora-a": adapterArtifact("lora-a", "base"),
			}),
			svc,
		)
	}

	t.Run("dynamic is Ready once mountable", func(t *testing.T) {
		svc := serviceWithAdapters("lora-a")
		svc.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
		st := Compose(svc, mkDeps(svc))
		if !st.SubtreeReady {
			t.Error("expected SubtreeReady once the subtree-sync job succeeded")
		}
		if !st.MountReady {
			t.Errorf("expected MountReady once the subtree exists, got %+v", st)
		}
		if !st.Ready {
			t.Errorf("dynamic mode should gate on mountable only, got %+v", st)
		}
		if st.AllStaged {
			t.Error("AllStaged must be false while the adapter has not been staged yet")
		}
	})

	t.Run("static waits for downloads", func(t *testing.T) {
		svc := serviceWithAdapters("lora-a") // static is the default
		st := Compose(svc, mkDeps(svc))
		if !st.MountReady {
			t.Errorf("expected MountReady once the subtree exists, got %+v", st)
		}
		if st.Ready {
			t.Errorf("static mode must not be Ready until downloads complete, got %+v", st)
		}
	})
}

// In static mode, once every declared adapter is Downloaded the service becomes
// Ready (the runtime will see them all at launch).
func TestStaticReadyWhenAllStaged(t *testing.T) {
	svc := serviceWithAdapters("lora-a") // static is the default
	deps := withSyncedSubtree(
		depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
			"lora-a": adapterArtifact("lora-a", "base"),
		}),
		svc,
	)
	deps.StagingJobs["lora-a"] = controllerutils.FetchResult[*batchv1.Job]{
		Value: succeededJob(StagingJobName(svc, "lora-a")),
	}

	st := Compose(svc, deps)
	if !st.AllStaged {
		t.Errorf("AllStaged should be true once the staging job succeeded, got %+v", st)
	}
	if !st.Ready {
		t.Errorf("static mode should be Ready once all adapters are staged, got %+v", st)
	}
}

// A profile that does not advertise the LoRA feature must reject the service's
// adapters up front rather than staging bytes the runtime would silently ignore.
func TestComposeRejectsProfileWithoutAdapterFeature(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := withSyncedSubtree(
		depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
			"lora-a": adapterArtifact("lora-a", "base"),
		}),
		svc,
	)
	no := false
	deps.ProfileSupportsAdapters = &no

	st := Compose(svc, deps)
	if st.ConfigErr == nil {
		t.Fatal("expected ConfigErr when the profile does not advertise adapter support")
	}
	if st.Ready {
		t.Errorf("must not be Ready when config is invalid, got %+v", st)
	}
}

// When the profile advertises the feature (and nil = unknown also allowed), the
// service proceeds normally.
func TestComposeAllowsProfileWithAdapterFeature(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	deps := withSyncedSubtree(
		depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
			"lora-a": adapterArtifact("lora-a", "base"),
		}),
		svc,
	)
	yes := true
	deps.ProfileSupportsAdapters = &yes

	st := Compose(svc, deps)
	if st.ConfigErr != nil {
		t.Fatalf("unexpected ConfigErr: %v", st.ConfigErr)
	}
	if !st.Ready {
		t.Errorf("expected Ready (dynamic, mountable, feature supported), got %+v", st)
	}
}

// A terminal base-model resolution error (e.g. multiple model sources) surfaces
// as a config error on the adapter state.
func TestComposeSurfacesParentResolutionError(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})
	deps.ParentResolutionErr = fmt.Errorf("multiple model sources")

	st := Compose(svc, deps)
	if st.ConfigErr == nil {
		t.Fatal("expected ConfigErr from ParentResolutionErr")
	}
}

func TestHealthStates(t *testing.T) {
	t.Run("config error is failed", func(t *testing.T) {
		h := Health(State{ConfigErr: fmt.Errorf("bad")}, 1)
		if h.State != constants.AIMStatusFailed {
			t.Errorf("state = %q, want Failed", h.State)
		}
	})
	t.Run("no disk is progressing", func(t *testing.T) {
		h := Health(State{}, 1)
		if h.State != constants.AIMStatusProgressing || h.Reason != ReasonParentLacksDisk {
			t.Errorf("got state=%q reason=%q, want Progressing/%s", h.State, h.Reason, ReasonParentLacksDisk)
		}
	})
	t.Run("subtree not ready is progressing", func(t *testing.T) {
		h := Health(State{AdapterDiskPVC: "pvc"}, 1)
		if h.State != constants.AIMStatusProgressing || h.Reason != ReasonSubtreeProvisioning {
			t.Errorf("got state=%q reason=%q, want Progressing/%s", h.State, h.Reason, ReasonSubtreeProvisioning)
		}
	})
	t.Run("ready while staging once subtree exists", func(t *testing.T) {
		st := State{
			AdapterDiskPVC: "pvc",
			SubtreeReady:   true,
			Ready:          true,
			Adapters:       []Observation{{Name: "a", State: aimv1alpha1.AdapterStatePending}},
		}
		h := Health(st, 1)
		if h.State != constants.AIMStatusReady || h.Reason != ReasonStaging {
			t.Errorf("got state=%q reason=%q, want Ready/%s", h.State, h.Reason, ReasonStaging)
		}
	})
	t.Run("ready when all staged", func(t *testing.T) {
		st := State{
			AdapterDiskPVC: "pvc",
			SubtreeReady:   true,
			AllStaged:      true,
			Ready:          true,
			Adapters:       []Observation{{Name: "a", State: aimv1alpha1.AdapterStateDownloaded}},
		}
		h := Health(st, 1)
		if h.State != constants.AIMStatusReady || h.Reason != ReasonStaged {
			t.Errorf("got state=%q reason=%q, want Ready/%s", h.State, h.Reason, ReasonStaged)
		}
	})
}

func TestPlanSyncsSubtreeThenStages(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})
	st := Compose(svc, deps)

	var plan controllerutils.PlanResult
	Plan(&plan, svc, deps, st, nil)

	var sawSync, sawStage bool
	for _, obj := range plan.GetToApply() {
		job, ok := obj.(*batchv1.Job)
		if !ok {
			continue
		}
		switch job.Name {
		case SubtreeSyncJobName(svc):
			sawSync = true
		case StagingJobName(svc, "lora-a"):
			sawStage = true
		}
	}
	if !sawSync {
		t.Error("Plan must emit the subtree-sync job when the declared set has not been synced yet")
	}
	if !sawStage {
		t.Error("Plan must emit a staging job for a ready, undownloaded adapter")
	}
}

// Once the current set has been synced (recorded on status) and the Job is gone,
// Plan must not re-create it — otherwise it would loop forever.
func TestPlanSkipsSyncWhenAlreadySynced(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})
	// Mark the declared set as already synced on status.
	svc.Status.AdapterSubtreeSyncKey = desiredAdapterKey(svc)
	st := Compose(svc, deps)

	var plan controllerutils.PlanResult
	Plan(&plan, svc, deps, st, nil)

	for _, obj := range plan.GetToApply() {
		if job, ok := obj.(*batchv1.Job); ok && job.Name == SubtreeSyncJobName(svc) {
			t.Error("Plan must not re-emit the subtree-sync job once the set is recorded as synced")
		}
	}
}

// Removing an adapter changes the desired key, so a fresh sync Job is planned to
// prune the dropped directory.
func TestPlanReSyncsOnAdapterRemoval(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})
	// Status reflects a previously-synced set that included a now-removed adapter.
	twoAdapters := serviceWithAdapters("lora-a", "lora-b")
	svc.Status.AdapterSubtreeSyncKey = desiredAdapterKey(twoAdapters)
	st := Compose(svc, deps)

	var plan controllerutils.PlanResult
	Plan(&plan, svc, deps, st, nil)

	var sawSync bool
	for _, obj := range plan.GetToApply() {
		if job, ok := obj.(*batchv1.Job); ok && job.Name == SubtreeSyncJobName(svc) {
			sawSync = true
		}
	}
	if !sawSync {
		t.Error("Plan must re-emit the subtree-sync job when the declared set changed (removal)")
	}
}

func TestBuildSubtreeSyncJobContract(t *testing.T) {
	svc := serviceWithAdapters("lora-a", "lora-b")
	job := BuildSubtreeSyncJob(svc, modelParent(testAdapterPVC), testAdapterPVC, nil)
	container := job.Spec.Template.Spec.Containers[0]

	if container.Command[0] != "/adapter-subtree-sync.sh" {
		t.Errorf("command = %v, want /adapter-subtree-sync.sh", container.Command)
	}
	env := map[string]string{}
	for _, e := range container.Env {
		env[e.Name] = e.Value
	}
	if env["SERVICE_ID"] != string(svc.UID) {
		t.Errorf("SERVICE_ID = %q, want %q", env["SERVICE_ID"], svc.UID)
	}
	if env["ADAPTER_PVC_ROOT"] != constants.AIMAdapterPVCRoot {
		t.Errorf("ADAPTER_PVC_ROOT = %q, want %q", env["ADAPTER_PVC_ROOT"], constants.AIMAdapterPVCRoot)
	}
	if env["KEEP_ADAPTER_PATHS"] != "lora-a,lora-b" {
		t.Errorf("KEEP_ADAPTER_PATHS = %q, want sorted keep-list lora-a,lora-b", env["KEEP_ADAPTER_PATHS"])
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != testAdapterPVC {
		t.Error("subtree-sync job must mount the adapter disk PVC read-write")
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly {
		t.Error("subtree-sync job must mount the adapter disk read-write (it creates/prunes dirs)")
	}
}

func TestStagingJobNameDeterministicAndScoped(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	n1 := StagingJobName(svc, "lora-a")
	n2 := StagingJobName(svc, "lora-a")
	if n1 != n2 {
		t.Errorf("staging job name not deterministic: %q != %q", n1, n2)
	}
	if StagingJobName(svc, "lora-b") == n1 {
		t.Error("different adapters must yield different job names")
	}

	other := serviceWithAdapters("lora-a")
	other.UID = types.UID("svc-uid-2")
	if StagingJobName(other, "lora-a") == n1 {
		t.Error("different service UIDs must yield different job names")
	}
}

func TestBuildStagingJobContract(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	artifact := adapterArtifact("lora-a", "base")
	ad := Observation{Name: "lora-a", AdapterPath: "lora-a", ModelID: "org/base", SourceURI: "hf://org/lora-a"}

	job := BuildStagingJob(svc, artifact, testAdapterPVC, ad, nil)
	container := job.Spec.Template.Spec.Containers[0]

	if container.Command[0] != "/adapter-stage.sh" {
		t.Errorf("command = %v, want /adapter-stage.sh", container.Command)
	}
	if len(container.Args) != 1 || container.Args[0] != "hf://org/lora-a" {
		t.Errorf("args = %v, want [hf://org/lora-a]", container.Args)
	}
	env := map[string]string{}
	for _, e := range container.Env {
		env[e.Name] = e.Value
	}
	if env["SERVICE_ID"] != string(svc.UID) {
		t.Errorf("SERVICE_ID = %q, want %q", env["SERVICE_ID"], svc.UID)
	}
	if env["ADAPTER_PATH"] != "lora-a" {
		t.Errorf("ADAPTER_PATH = %q, want lora-a", env["ADAPTER_PATH"])
	}
	if env["ADAPTER_PVC_ROOT"] != constants.AIMAdapterPVCRoot {
		t.Errorf("ADAPTER_PVC_ROOT = %q, want %q", env["ADAPTER_PVC_ROOT"], constants.AIMAdapterPVCRoot)
	}
	// Non-root HF/XET clients need a writable cache dir; without these the XET
	// backend fails with "Permission denied" against $HOME/.cache.
	if env["HF_HOME"] != "/tmp/.hf" {
		t.Errorf("HF_HOME = %q, want /tmp/.hf", env["HF_HOME"])
	}
	if env["TMPDIR"] != "/tmp/" {
		t.Errorf("TMPDIR = %q, want /tmp/", env["TMPDIR"])
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != testAdapterPVC {
		t.Error("staging job must mount the adapter disk PVC")
	}
}

func TestAddVolumeMountReadOnlySubPath(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	isvc := isvcWithContainer()
	AddVolumeMount(isvc, svc, testAdapterPVC)

	if len(isvc.Spec.Predictor.Volumes) != 1 {
		t.Fatalf("expected one adapter volume, got %d", len(isvc.Spec.Predictor.Volumes))
	}
	vol := isvc.Spec.Predictor.Volumes[0]
	if vol.PersistentVolumeClaim == nil || !vol.PersistentVolumeClaim.ReadOnly {
		t.Error("adapter volume must be read-only")
	}
	mounts := isvc.Spec.Predictor.Containers[0].VolumeMounts
	if len(mounts) != 1 {
		t.Fatalf("expected one mount, got %d", len(mounts))
	}
	m := mounts[0]
	if m.MountPath != constants.AIMAdapterMountPath {
		t.Errorf("mount path = %q, want %q", m.MountPath, constants.AIMAdapterMountPath)
	}
	if m.SubPath != string(svc.UID) {
		t.Errorf("subPath = %q, want service UID %q", m.SubPath, svc.UID)
	}
	if !m.ReadOnly {
		t.Error("adapter mount must be read-only")
	}

	// Default (static) service emits the source/mode/cap envs but no refresh interval.
	env := envMap(isvc.Spec.Predictor.Containers[0].Env)
	if env[constants.EnvAIMAdapterSource] != constants.AIMAdapterMountPath {
		t.Errorf("AIM_ADAPTER_SOURCE = %q, want %q", env[constants.EnvAIMAdapterSource], constants.AIMAdapterMountPath)
	}
	if env[constants.EnvAIMAdapterMode] != string(aimv1alpha1.AdapterModeStatic) {
		t.Errorf("AIM_ADAPTER_MODE = %q, want %q (default static)", env[constants.EnvAIMAdapterMode], aimv1alpha1.AdapterModeStatic)
	}
	if env[constants.EnvAIMAdapterMaxCount] != "8" || env[constants.EnvAIMAdapterMaxCPUCount] != "16" || env[constants.EnvAIMAdapterMaxRank] != "32" {
		t.Errorf("unexpected MAX_* caps: count=%q cpu=%q rank=%q", env[constants.EnvAIMAdapterMaxCount], env[constants.EnvAIMAdapterMaxCPUCount], env[constants.EnvAIMAdapterMaxRank])
	}
	if _, ok := env[constants.EnvAIMAdapterRefreshInterval]; ok {
		t.Error("static mode must not set AIM_ADAPTER_REFRESH_INTERVAL")
	}
}

func TestAddVolumeMountDynamicSetsRefreshInterval(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	isvc := isvcWithContainer()
	AddVolumeMount(isvc, svc, testAdapterPVC)

	env := envMap(isvc.Spec.Predictor.Containers[0].Env)
	if env[constants.EnvAIMAdapterMode] != string(aimv1alpha1.AdapterModeDynamic) {
		t.Errorf("AIM_ADAPTER_MODE = %q, want dynamic", env[constants.EnvAIMAdapterMode])
	}
	if env[constants.EnvAIMAdapterRefreshInterval] != "30" {
		t.Errorf("AIM_ADAPTER_REFRESH_INTERVAL = %q, want 30", env[constants.EnvAIMAdapterRefreshInterval])
	}
}

func envMap(envs []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(envs))
	for _, e := range envs {
		m[e.Name] = e.Value
	}
	return m
}

func TestIsActive(t *testing.T) {
	if IsActive(serviceWithAdapters()) {
		t.Error("no mode, no spec adapters and no status adapters must be inactive")
	}
	if !IsActive(serviceWithAdapters("a")) {
		t.Error("declared adapters must keep the engine active")
	}
	svc := serviceWithAdapters() // empty spec
	svc.Status.Adapters = []aimv1alpha1.AIMServiceAdapterStatus{statusAdapter("a")}
	if !IsActive(svc) {
		t.Error("status-carried adapters (pending removal) must keep the engine active")
	}

	// Dynamic mode keeps the engine active even with zero adapters, so the subtree
	// is provisioned and the disk mounted at zero (add/remove never restarts).
	dynEmpty := serviceWithAdapters()
	dynEmpty.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	if !dynEmpty.Spec.AdaptersEnabled() {
		t.Error("dynamic mode must report AdaptersEnabled at zero adapters")
	}
	if !IsActive(dynEmpty) {
		t.Error("dynamic mode must keep the engine active at zero adapters")
	}

	// Static (the default) with no adapters needs no disk: nothing to mount.
	staticEmpty := serviceWithAdapters()
	staticEmpty.Spec.AdapterMode = aimv1alpha1.AdapterModeStatic
	if staticEmpty.Spec.AdaptersEnabled() {
		t.Error("static mode with no adapters must not report AdaptersEnabled")
	}
	if IsActive(staticEmpty) {
		t.Error("static mode with no adapters must be inactive")
	}
}

// An adapter-mode service with zero adapters still provisions its subtree (so the
// read-only subPath mount binds) and, once synced, is mountable and stable — the
// whole point of decoupling the mount from the list length.
func TestComposeAdapterModeEmptyProvisionsSubtree(t *testing.T) {
	svc := serviceWithAdapters() // empty spec
	svc.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	deps := depsWith(modelParent(testAdapterPVC), nil)

	// Before the first sync: disk resolved but subtree not yet ready, so a
	// (non-empty) desired key drives a subtree-sync Job and the ISVC waits.
	st := Compose(svc, deps)
	if st.ConfigErr != nil {
		t.Fatalf("unexpected config error: %v", st.ConfigErr)
	}
	if st.DesiredKey == "" {
		t.Error("empty-set desired key must be non-empty so the first sync runs")
	}
	if st.SubtreeReady || st.Ready {
		t.Error("subtree must not be ready before the first sync succeeds")
	}
	var plan controllerutils.PlanResult
	Plan(&plan, svc, deps, st, nil)
	sawSync := false
	for _, obj := range plan.GetToApply() {
		if job, ok := obj.(*batchv1.Job); ok && job.Name == SubtreeSyncJobName(svc) {
			sawSync = true
		}
	}
	if !sawSync {
		t.Error("Plan must emit a subtree-sync Job to mkdir the subtree at zero adapters")
	}

	// After the sync succeeds the subtree is mountable and the mount is added.
	st = Compose(svc, withSyncedSubtree(deps, svc))
	if !st.SubtreeReady || !st.Ready {
		t.Errorf("subtree must be mountable after sync (SubtreeReady=%v Ready=%v)", st.SubtreeReady, st.Ready)
	}
	isvc := isvcWithContainer()
	AddVolumeMount(isvc, svc, st.AdapterDiskPVC)
	if len(isvc.Spec.Predictor.Containers[0].VolumeMounts) != 1 {
		t.Error("adapter disk must be mounted even with zero adapters declared")
	}
}

// Removing an adapter keeps it visible as Deleting until the prune sync lands, so
// the removal is observable in status.adapters.
func TestComposeMarksRemovedAdapterDeleting(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Status.Adapters = []aimv1alpha1.AIMServiceAdapterStatus{
		statusAdapter("lora-a"),
		statusAdapter("lora-b"),
	}
	// Status key still reflects the two-adapter set => prune not yet confirmed.
	svc.Status.AdapterSubtreeSyncKey = desiredAdapterKey(serviceWithAdapters("lora-a", "lora-b"))
	deps := depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})

	st := Compose(svc, deps)

	byName := map[string]Observation{}
	for _, ad := range st.Adapters {
		byName[ad.Name] = ad
	}
	if _, ok := byName["lora-a"]; !ok {
		t.Error("declared adapter lora-a must remain present")
	}
	b, ok := byName["lora-b"]
	if !ok || b.State != aimv1alpha1.AdapterStateDeleting {
		t.Errorf("removed adapter lora-b must be marked Deleting, got %+v (ok=%v)", b, ok)
	}
	// A removal in progress must not flip AllStaged false for the kept set.
	if st.ConfigErr != nil {
		t.Errorf("removal must not be a config error: %v", st.ConfigErr)
	}
}

// Once the prune sync for the reduced set succeeds, the removed adapter is
// dropped from status and the new synced key is recorded.
func TestComposeDropsRemovedAdapterAfterPrune(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Status.Adapters = []aimv1alpha1.AIMServiceAdapterStatus{
		statusAdapter("lora-a"),
		statusAdapter("lora-b"),
	}
	svc.Status.AdapterSubtreeSyncKey = desiredAdapterKey(serviceWithAdapters("lora-a", "lora-b"))
	deps := withSyncedSubtree(
		depsWith(modelParent(testAdapterPVC), map[string]*aimv1alpha1.AIMArtifact{
			"lora-a": adapterArtifact("lora-a", "base"),
		}),
		svc,
	)

	st := Compose(svc, deps)

	for _, ad := range st.Adapters {
		if ad.Name == "lora-b" {
			t.Errorf("removed adapter lora-b must be dropped once its prune sync succeeded, got %+v", ad)
		}
	}

	status := &aimv1alpha1.AIMServiceStatus{}
	DecorateStatus(status, st)
	if len(status.Adapters) != 1 || status.Adapters[0].Name != "lora-a" {
		t.Errorf("status must list only lora-a after prune, got %+v", status.Adapters)
	}
	if status.AdapterSubtreeSyncKey != desiredAdapterKey(svc) {
		t.Errorf("synced key = %q, want one-adapter key %q", status.AdapterSubtreeSyncKey, desiredAdapterKey(svc))
	}
}

// Dropping the LAST adapter: the engine stays active (status carries Deleting),
// plans the empty-set prune sync (but no staging), then clears status once pruned.
func TestComposeDropToZeroDeletingThenPruned(t *testing.T) {
	svc := serviceWithAdapters() // empty spec
	svc.Spec.AdapterMode = aimv1alpha1.AdapterModeDynamic
	svc.Status.Adapters = []aimv1alpha1.AIMServiceAdapterStatus{
		statusAdapter("lora-a"),
	}
	svc.Status.AdapterSubtreeSyncKey = desiredAdapterKey(serviceWithAdapters("lora-a"))
	deps := depsWith(modelParent(testAdapterPVC), nil)

	// Before prune: lora-a shown Deleting; an empty-set prune sync is planned and
	// no staging job is created for the adapter being removed.
	st := Compose(svc, deps)
	if len(st.Adapters) != 1 || st.Adapters[0].State != aimv1alpha1.AdapterStateDeleting {
		t.Fatalf("expected lora-a Deleting while dropping to zero, got %+v", st.Adapters)
	}
	var plan controllerutils.PlanResult
	Plan(&plan, svc, deps, st, nil)
	var sawSync, sawStage bool
	for _, obj := range plan.GetToApply() {
		job, ok := obj.(*batchv1.Job)
		if !ok {
			continue
		}
		switch job.Name {
		case SubtreeSyncJobName(svc):
			sawSync = true
		case StagingJobName(svc, "lora-a"):
			sawStage = true
		}
	}
	if !sawSync {
		t.Error("Plan must emit the empty-set subtree-sync job to prune the last adapter")
	}
	if sawStage {
		t.Error("Plan must not stage an adapter that is being deleted")
	}

	// After the prune succeeds: adapter status is cleared, but the subtree stays
	// mountable (Dynamic mode keeps the mount even at zero adapters). The synced
	// key advances to the empty-set key — a non-empty, stable value — rather than
	// "" (never-synced), so the sync Job does not re-run forever.
	deps = withSyncedSubtree(deps, svc)
	st = Compose(svc, deps)
	if len(st.Adapters) != 0 {
		t.Errorf("expected no adapters after the final prune, got %+v", st.Adapters)
	}
	if !st.SubtreeReady {
		t.Error("subtree must stay mountable at zero adapters in Dynamic mode")
	}
	status := &aimv1alpha1.AIMServiceStatus{}
	DecorateStatus(status, st)
	if len(status.Adapters) != 0 {
		t.Errorf("status adapters must be empty after drop-to-zero prune, got %+v", status.Adapters)
	}
	emptyKey := desiredAdapterKey(svc)
	if emptyKey == "" {
		t.Fatal("empty-set key must be non-empty to disambiguate from never-synced")
	}
	if status.AdapterSubtreeSyncKey != emptyKey {
		t.Errorf("synced key must advance to the empty-set key %q, got %q", emptyKey, status.AdapterSubtreeSyncKey)
	}
}

// A transient parent-resolution gap (the parent's status carries no adapter PVC
// this cycle) must not blank the mount: Compose falls back to the PVC last
// persisted on the service status.
func TestComposeStickyAdapterDiskPVC(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Status.AdapterDiskPersistentVolumeClaim = "sticky-pvc"
	deps := depsWith(modelParent(""), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})

	st := Compose(svc, deps)
	if st.AdapterDiskPVC != "sticky-pvc" {
		t.Errorf("AdapterDiskPVC = %q, want sticky fallback sticky-pvc", st.AdapterDiskPVC)
	}
}

// The freshly resolved parent PVC always wins over the sticky status value.
func TestComposeFreshPVCWinsOverSticky(t *testing.T) {
	svc := serviceWithAdapters("lora-a")
	svc.Status.AdapterDiskPersistentVolumeClaim = "stale-pvc"
	deps := depsWith(modelParent("fresh-pvc"), map[string]*aimv1alpha1.AIMArtifact{
		"lora-a": adapterArtifact("lora-a", "base"),
	})

	st := Compose(svc, deps)
	if st.AdapterDiskPVC != "fresh-pvc" {
		t.Errorf("AdapterDiskPVC = %q, want freshly resolved fresh-pvc", st.AdapterDiskPVC)
	}
}

// DecorateStatus persists the resolved PVC and never clears a previously stored
// value on a transient gap (the sticky source for the fallback above).
func TestDecorateStatusPersistsAdapterDiskPVC(t *testing.T) {
	status := &aimv1alpha1.AIMServiceStatus{}
	DecorateStatus(status, State{AdapterDiskPVC: "pvc-1"})
	if status.AdapterDiskPersistentVolumeClaim != "pvc-1" {
		t.Errorf("AdapterDiskPersistentVolumeClaim = %q, want pvc-1", status.AdapterDiskPersistentVolumeClaim)
	}

	// An empty resolution must not clear the previously persisted value.
	DecorateStatus(status, State{})
	if status.AdapterDiskPersistentVolumeClaim != "pvc-1" {
		t.Errorf("sticky PVC must not be cleared on an empty cycle, got %q", status.AdapterDiskPersistentVolumeClaim)
	}
}

// PreserveExistingMount reports when the adapter wiring can't be resolved this
// cycle, so callers skip re-applying an ISVC that would drop the mount.
func TestPreserveExistingMount(t *testing.T) {
	tests := []struct {
		name     string
		mode     aimv1alpha1.AIMAdapterMode
		adapters []string
		pvc      string
		want     bool
	}{
		{name: "enabled but PVC unresolved", adapters: []string{"a"}, pvc: "", want: true},
		{name: "enabled and PVC resolved", adapters: []string{"a"}, pvc: "pvc", want: false},
		{name: "not enabled (static, no adapters)", adapters: nil, pvc: "", want: false},
		{name: "dynamic at zero adapters, PVC unresolved", mode: aimv1alpha1.AdapterModeDynamic, adapters: nil, pvc: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := serviceWithAdapters(tt.adapters...)
			if tt.mode != "" {
				svc.Spec.AdapterMode = tt.mode
			}
			if got := PreserveExistingMount(svc, State{AdapterDiskPVC: tt.pvc}); got != tt.want {
				t.Errorf("PreserveExistingMount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecorateStatusMirrorsObservations(t *testing.T) {
	st := State{
		Adapters: []Observation{
			{Name: "a", AdapterPath: "a", ModelID: "org/base", State: aimv1alpha1.AdapterStateDownloaded},
			{Name: "b", AdapterPath: "b", State: aimv1alpha1.AdapterStatePending, LastError: ReasonNotFound},
		},
	}
	status := &aimv1alpha1.AIMServiceStatus{}
	DecorateStatus(status, st)

	if len(status.Adapters) != 2 {
		t.Fatalf("expected 2 status adapters, got %d", len(status.Adapters))
	}
	if status.Adapters[0].State != aimv1alpha1.AdapterStateDownloaded {
		t.Errorf("adapter a state = %q, want Downloaded", status.Adapters[0].State)
	}
	if status.Adapters[1].LastError != ReasonNotFound {
		t.Errorf("adapter b lastError = %q, want %s", status.Adapters[1].LastError, ReasonNotFound)
	}
}
