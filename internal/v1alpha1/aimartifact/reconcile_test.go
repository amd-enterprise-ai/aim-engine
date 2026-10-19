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
	"testing"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
