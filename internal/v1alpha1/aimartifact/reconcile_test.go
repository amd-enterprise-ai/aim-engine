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

package aimartifact

import (
	"context"
	"reflect"
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFoldStalledFilesystemHealth(t *testing.T) {
	const warning = "Filesystem may be stuck: 'du' exceeded 300s. Progress and stall detection paused."

	running := controllerutils.ComponentHealth{
		Component: "DownloadJob",
		State:     constants.AIMStatusProgressing,
		Reason:    "Running",
		Message:   "Job is running",
	}

	tests := []struct {
		name        string
		jobHealth   controllerutils.ComponentHealth
		progress    *aimv1alpha1.DownloadProgress
		wantState   constants.AIMStatus
		wantReason  string
		wantMessage string
	}{
		{
			name:        "no progress → unchanged",
			jobHealth:   running,
			progress:    nil,
			wantState:   constants.AIMStatusProgressing,
			wantReason:  "Running",
			wantMessage: "Job is running",
		},
		{
			name:        "empty message → unchanged",
			jobHealth:   running,
			progress:    &aimv1alpha1.DownloadProgress{Message: ""},
			wantState:   constants.AIMStatusProgressing,
			wantReason:  "Running",
			wantMessage: "Job is running",
		},
		{
			name:        "running job with warning → Degraded carrying the warning",
			jobHealth:   running,
			progress:    &aimv1alpha1.DownloadProgress{Message: warning},
			wantState:   constants.AIMStatusDegraded,
			wantReason:  aimv1alpha1.ArtifactReasonFilesystemStalled,
			wantMessage: warning,
		},
		{
			name:        "succeeded job → warning ignored (success wins)",
			jobHealth:   controllerutils.ComponentHealth{Component: "DownloadJob", State: constants.AIMStatusReady, Reason: "Complete", Message: "done"},
			progress:    &aimv1alpha1.DownloadProgress{Message: warning},
			wantState:   constants.AIMStatusReady,
			wantReason:  "Complete",
			wantMessage: "done",
		},
		{
			name:        "failed job → warning ignored (failure wins)",
			jobHealth:   controllerutils.ComponentHealth{Component: "DownloadJob", State: constants.AIMStatusFailed, Reason: "BackoffLimitExceeded", Message: "job failed"},
			progress:    &aimv1alpha1.DownloadProgress{Message: warning},
			wantState:   constants.AIMStatusFailed,
			wantReason:  "BackoffLimitExceeded",
			wantMessage: "job failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foldStalledFilesystemHealth(tt.jobHealth, tt.progress)
			if got.GetState() != tt.wantState {
				t.Errorf("state = %q, want %q", got.GetState(), tt.wantState)
			}
			if got.GetReason() != tt.wantReason {
				t.Errorf("reason = %q, want %q", got.GetReason(), tt.wantReason)
			}
			if got.GetMessage() != tt.wantMessage {
				t.Errorf("message = %q, want %q", got.GetMessage(), tt.wantMessage)
			}
		})
	}
}

func TestTypedS3DisallowedArtifactEnvironmentBlocksTransfer(t *testing.T) {
	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "models"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI: "s3://bucket/model",
			Size:      resource.MustParse("1Gi"),
			Env: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://artifact-proxy:8080"},
				{Name: "AWS_PROFILE", Value: "artifact-profile"},
			},
		},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		Artifact: &aimv1alpha1.AIMArtifactConfig{
			S3: &aimv1alpha1.S3ConnectionConfig{
				Endpoint: "https://s3.internal.example",
			},
		},
	}
	fetch := ArtifactFetchResult{
		artifact: artifact,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: config,
		},
	}

	obs := (&ArtifactReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact},
		fetch,
	)
	if obs.transferConfigErr == nil {
		t.Fatal("disallowed typed S3 artifact env was not rejected")
	}

	plan := (&ArtifactReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact},
		obs,
	)
	if len(plan.GetToApply()) != 0 ||
		len(plan.GetToApplyWithoutOwnerRef()) != 0 ||
		len(plan.GetToDelete()) != 0 {
		t.Fatalf(
			"invalid transfer config planned mutations: apply=%#v applyWithoutOwner=%#v delete=%#v",
			plan.GetToApply(),
			plan.GetToApplyWithoutOwnerRef(),
			plan.GetToDelete(),
		)
	}

	found := false
	for _, component := range obs.GetComponentHealth(context.Background(), nil) {
		if component.Component == "TransferConfiguration" &&
			component.GetReason() == "DisallowedS3ArtifactEnvironment" &&
			len(component.Errors) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("disallowed typed S3 artifact env was not surfaced as InvalidSpec health")
	}
}

func TestTypedS3DisallowedAdapterEnvironmentIsInvalid(t *testing.T) {
	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "adapter", Namespace: "models"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:           aimv1alpha1.ArtifactTypeAdapter,
			SourceURI:      "s3://bucket/adapter",
			ParentArtifact: "parent",
			Env: []corev1.EnvVar{
				{Name: "AWS_WEB_IDENTITY_TOKEN_FILE", Value: "/artifact/token"},
			},
		},
	}
	fetch := ArtifactFetchResult{
		artifact: artifact,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{
			Value: &aimv1alpha1.AIMRuntimeConfigCommon{
				Artifact: &aimv1alpha1.AIMArtifactConfig{
					S3: &aimv1alpha1.S3ConnectionConfig{},
				},
			},
		},
	}

	obs := (&ArtifactReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact},
		fetch,
	)
	if obs.transferConfigErr == nil {
		t.Fatal("disallowed typed S3 adapter env was not rejected")
	}

	found := false
	for _, component := range obs.GetComponentHealth(context.Background(), nil) {
		if component.Component == "TransferConfiguration" &&
			component.GetReason() == "DisallowedS3ArtifactEnvironment" {
			found = true
		}
	}
	if !found {
		t.Fatal("adapter transfer configuration error was not surfaced")
	}
}

// TestFailedArtifactRecreatesDownloadJobWithCurrentCachingEnv pins that a
// Failed artifact retries once its failed download Job is gone (the Job's TTL
// removes it): the new Job reads the runtime config spec.caching.env as it is
// now, over the defaults and under the artifact's own spec.env.
func TestFailedArtifactRecreatesDownloadJobWithCurrentCachingEnv(t *testing.T) {
	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI: "hf://org/gated-model",
			Size:      resource.MustParse("1Mi"),
			Env:       []corev1.EnvVar{{Name: "HF_ENDPOINT", Value: "https://huggingface.example.com"}},
		},
		Status: aimv1alpha1.AIMArtifactStatus{Status: constants.AIMStatusFailed},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		Caching: &aimv1alpha1.AIMRuntimeConfigCachingConfig{
			Env: []corev1.EnvVar{
				{Name: "HF_TOKEN", Value: "added-after-the-failure"},
				{Name: "HF_ENDPOINT", Value: "https://ignored.example.com"},
			},
		},
	}
	jobGone := controllerutils.FetchResult[*batchv1.Job]{
		Error: apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, "model-download"),
	}
	fetch := ArtifactFetchResult{
		artifact:            artifact,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: config},
		cachePvc:            controllerutils.FetchResult[*corev1.PersistentVolumeClaim]{Value: &corev1.PersistentVolumeClaim{}},
		roleBinding:         controllerutils.FetchResult[*rbacv1.RoleBinding]{Value: &rbacv1.RoleBinding{}},
		downloadJob:         &jobGone,
	}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact}
	obs := (&ArtifactReconciler{}).ComposeState(context.Background(), reconcileCtx, fetch)

	for _, component := range obs.GetComponentHealth(context.Background(), nil) {
		for _, err := range component.Errors {
			switch controllerutils.CategorizeError(err).Category() {
			case controllerutils.ErrorCategoryInvalidSpec, controllerutils.ErrorCategoryAuth,
				controllerutils.ErrorCategoryMissingUpstreamDependency:
				t.Fatalf("component %s blocks apply: %v", component.Component, err)
			}
		}
	}

	plan := (&ArtifactReconciler{}).PlanResources(context.Background(), reconcileCtx, obs)
	var job *batchv1.Job
	for _, obj := range plan.GetToApply() {
		if j, ok := obj.(*batchv1.Job); ok {
			job = j
		}
	}
	if job == nil {
		t.Fatalf("Failed artifact without a download Job planned no new Job: %#v", plan.GetToApply())
	}
	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["HF_TOKEN"] != "added-after-the-failure" {
		t.Errorf("HF_TOKEN = %q, want the current runtime config spec.caching.env value", env["HF_TOKEN"])
	}
	if env["HF_ENDPOINT"] != "https://huggingface.example.com" {
		t.Errorf("HF_ENDPOINT = %q, want the artifact spec.env value", env["HF_ENDPOINT"])
	}
}

// TestFailedSizeCheckIsRecreatedWithCurrentCachingEnv pins the same retry for
// an artifact whose size check failed before any download started.
func TestFailedSizeCheckIsRecreatedWithCurrentCachingEnv(t *testing.T) {
	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team-a"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: "hf://org/gated-model"},
		Status:     aimv1alpha1.AIMArtifactStatus{Status: constants.AIMStatusFailed},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		Caching: &aimv1alpha1.AIMRuntimeConfigCachingConfig{
			Env: []corev1.EnvVar{{Name: "HF_TOKEN", Value: "added-after-the-failure"}},
		},
	}
	jobGone := controllerutils.FetchResult[*batchv1.Job]{
		Error: apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, "model-check-size"),
	}
	fetch := ArtifactFetchResult{
		artifact:            artifact,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: config},
		roleBinding:         controllerutils.FetchResult[*rbacv1.RoleBinding]{Value: &rbacv1.RoleBinding{}},
		checkSizeJob:        &jobGone,
	}
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact}
	obs := (&ArtifactReconciler{}).ComposeState(context.Background(), reconcileCtx, fetch)

	plan := (&ArtifactReconciler{}).PlanResources(context.Background(), reconcileCtx, obs)
	var job *batchv1.Job
	for _, obj := range plan.GetToApply() {
		if j, ok := obj.(*batchv1.Job); ok && j.Labels[constants.LabelKeyComponent] == "check-size" {
			job = j
		}
	}
	if job == nil {
		t.Fatalf("Failed artifact without a size-check Job planned no new one: %#v", plan.GetToApply())
	}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "HF_TOKEN" && e.Value == "added-after-the-failure" {
			return
		}
	}
	t.Errorf("size-check Job env = %v, want the current runtime config spec.caching.env HF_TOKEN", job.Spec.Template.Spec.Containers[0].Env)
}

func TestPlanResourcesDoesNotReplaceExistingDownloadJobWhenTrustChanges(t *testing.T) {
	oldImage := aimv1alpha1.DefaultDownloadImage
	aimv1alpha1.DefaultDownloadImage = "example.test/downloader:v1"
	t.Cleanup(func() { aimv1alpha1.DefaultDownloadImage = oldImage })

	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "team-a"},
		Spec: aimv1alpha1.AIMArtifactSpec{
			SourceURI: "hf://org/model",
			Size:      resource.MustParse("1Mi"),
		},
	}
	config := &aimv1alpha1.AIMRuntimeConfigCommon{
		AdditionalCABundleRef: &aimv1alpha1.AdditionalCABundleReference{
			Name: "company-ca",
			Key:  "ca.crt",
		},
	}
	oldJob := buildDownloadJob(artifact, config, artifact.Spec.Size.Value(), true)
	jobFetch := controllerutils.FetchResult[*batchv1.Job]{Value: oldJob}
	obs := ArtifactObservation{ArtifactFetchResult: ArtifactFetchResult{
		artifact:            artifact,
		mergedRuntimeConfig: controllerutils.FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]{Value: config},
		additionalCABundle: controllerutils.FetchResult[*corev1.ConfigMap]{
			Value: &corev1.ConfigMap{Data: map[string]string{"ca.crt": "new-certificates"}},
		},
		downloadJob: &jobFetch,
	}}

	plan := (&ArtifactReconciler{}).PlanResources(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact},
		obs,
	)
	if len(plan.GetToDelete()) != 0 {
		t.Fatalf("trust snapshot replaced existing download Job: %#v", plan.GetToDelete())
	}
}

func TestFetchRemoteStateRewritesArtifactPVCOwnerReferences(t *testing.T) {
	artifact := &aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: "weights", Namespace: "models", UID: types.UID("artifact-uid")},
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:        aimv1alpha1.ArtifactTypeModel,
			SourceURI:   "hf://org/weights",
			Size:        resource.MustParse("10Mi"),
			AdapterDisk: &aimv1alpha1.AIMAdapterDisk{Size: resource.MustParse("1Gi")},
		},
	}
	storageClass := "nfs"
	pvcSpec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")},
		},
		StorageClassName: &storageClass,
	}
	labels := map[string]string{"keep": "label"}
	annotations := map[string]string{"keep": "annotation"}
	owner := metav1.OwnerReference{
		APIVersion:         "aim.eai.amd.com/v1alpha1",
		Kind:               "AIMArtifact",
		Name:               artifact.Name,
		UID:                artifact.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	other := metav1.OwnerReference{
		APIVersion:         "example.com/v1",
		Kind:               "Widget",
		Name:               "other",
		UID:                types.UID("other-uid"),
		Controller:         ptr.To(false),
		BlockOwnerDeletion: ptr.To(false),
	}
	newPVC := func(name string) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       artifact.Namespace,
				ResourceVersion: "1",
				Labels:          labels,
				Annotations:     annotations,
				OwnerReferences: []metav1.OwnerReference{owner, other},
			},
			Spec: pvcSpec,
		}
	}
	cachePVC := newPVC(GenerateCachePvcName(artifact))
	adapterPVC := newPVC(GenerateAdapterPvcName(artifact))
	downloadJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            getDownloadJobName(artifact),
			Namespace:       artifact.Namespace,
			ResourceVersion: "1",
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}
	reaperJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            generateAdapterReaperJobName(artifact),
			Namespace:       artifact.Namespace,
			ResourceVersion: "1",
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "aim-engine-artifact-status-updater",
			Namespace:       artifact.Namespace,
			ResourceVersion: "1",
			OwnerReferences: []metav1.OwnerReference{owner},
		},
	}

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, batchv1.AddToScheme, rbacv1.AddToScheme,
		aimv1alpha1.AddToScheme, aimv1alpha2.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("add to scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		cachePVC, adapterPVC, downloadJob, reaperJob, roleBinding,
	).Build()
	reconciler := &ArtifactReconciler{}
	ctx := context.Background()
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMArtifact]{Object: artifact}

	fetch := reconciler.FetchRemoteState(ctx, c, reconcileCtx)
	if fetch.cachePvc.Error != nil || (fetch.adapterPvc != nil && fetch.adapterPvc.Error != nil) {
		t.Fatalf("rewrite failed: cache %v adapter %v", fetch.cachePvc.Error, fetch.adapterPvc.Error)
	}

	wantArtifactOwner := owner
	wantArtifactOwner.APIVersion = "aim.eai.amd.com/v1alpha2"
	assertPVC := func(name string) {
		t.Helper()
		got := &corev1.PersistentVolumeClaim{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: artifact.Namespace, Name: name}, got); err != nil {
			t.Fatalf("get pvc %s: %v", name, err)
		}
		if !reflect.DeepEqual(got.OwnerReferences, []metav1.OwnerReference{wantArtifactOwner, other}) {
			t.Errorf("%s owner references = %#v", name, got.OwnerReferences)
		}
		if !reflect.DeepEqual(got.Spec, pvcSpec) || !reflect.DeepEqual(got.Labels, labels) || !reflect.DeepEqual(got.Annotations, annotations) {
			t.Errorf("%s spec, labels, or annotations changed", name)
		}
	}
	assertPVC(cachePVC.Name)
	assertPVC(adapterPVC.Name)

	assertUntouched := func(obj client.Object) {
		t.Helper()
		got := obj.DeepCopyObject().(client.Object)
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), got); err != nil {
			t.Fatalf("get %s: %v", obj.GetName(), err)
		}
		if got.GetResourceVersion() != "1" {
			t.Errorf("%s resourceVersion = %s, want 1", obj.GetName(), got.GetResourceVersion())
		}
		refs := got.GetOwnerReferences()
		if len(refs) != 1 || refs[0].APIVersion != "aim.eai.amd.com/v1alpha1" || refs[0].UID != artifact.UID {
			t.Errorf("%s owner references = %#v", obj.GetName(), refs)
		}
	}
	assertUntouched(downloadJob)
	assertUntouched(reaperJob)
	assertUntouched(roleBinding)

	cacheVersion := resourceVersionOf(t, ctx, c, cachePVC)
	adapterVersion := resourceVersionOf(t, ctx, c, adapterPVC)
	if err := reconciler.FetchRemoteState(ctx, c, reconcileCtx).cachePvc.Error; err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if got := resourceVersionOf(t, ctx, c, cachePVC); got != cacheVersion {
		t.Errorf("second fetch changed cache PVC resourceVersion from %s to %s", cacheVersion, got)
	}
	if got := resourceVersionOf(t, ctx, c, adapterPVC); got != adapterVersion {
		t.Errorf("second fetch changed adapter PVC resourceVersion from %s to %s", adapterVersion, got)
	}
}

func resourceVersionOf(t *testing.T, ctx context.Context, c client.Client, obj client.Object) string {
	t.Helper()
	got := obj.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), got); err != nil {
		t.Fatalf("get %s: %v", obj.GetName(), err)
	}
	return got.GetResourceVersion()
}
