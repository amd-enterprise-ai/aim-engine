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

package aimmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// runningDiscoveryJob returns inputs after one started attempt and the Job
// that attempt created for image, in the same whole second.
func runningDiscoveryJob(image string) (DiscoveryInputs, *batchv1.Job) {
	specHash := ComputeDiscoverySpecHash(image, nil, "", DiscoveryCommandVersion)
	owner := metav1.OwnerReference{
		APIVersion: "aim.eai.amd.com/v1alpha2", Kind: "AIMModel", Name: "dummy", UID: "model-uid", Controller: ptr.To(true),
	}
	attempt := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	in := DiscoveryInputs{
		Kind: DiscoveryKindAIMModel, ModelName: "dummy", ModelUID: "model-uid", Namespace: "team-a", Image: image, OwnerRef: owner,
		CurrentState: &aimv1alpha1.ModelDiscoveryState{Attempts: 1, LastAttemptTime: &attempt, SpecHash: specHash},
	}
	job := BuildDiscoveryJob(DiscoveryJobSpec{
		ModelName: "dummy", ModelUID: "model-uid", Namespace: "team-a", Image: image,
		SpecHash: specHash, OwnerRef: owner, Kind: DiscoveryKindAIMModel,
	})
	job.CreationTimestamp = attempt
	return in, job
}

func TestDecideDiscovery_RunningJobThatCannotPullFailsTheAttempt(t *testing.T) {
	t.Parallel()

	const image = "docker.io/amdenterpriseai/aim-missing:0.0.0"
	backOff := &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: `Back-off pulling image "` + image + `"`}
	tests := []struct {
		name       string
		waiting    *corev1.ContainerStateWaiting
		startedAgo time.Duration
		wantDelete bool
	}{
		{
			name:       "generic pull error within the grace period keeps waiting",
			waiting:    backOff,
			startedAgo: time.Minute,
		},
		{
			name: "rate-limited pull within the grace period keeps waiting",
			waiting: &corev1.ContainerStateWaiting{
				Reason:  "ErrImagePull",
				Message: "toomanyrequests: You have reached your pull rate limit. You may increase the limit by authenticating and upgrading",
			},
			startedAgo: time.Minute,
		},
		{
			name:       "generic pull error past the grace period",
			waiting:    backOff,
			startedAgo: discoveryPullRetryGrace + time.Minute,
			wantDelete: true,
		},
		{
			name: "auth error",
			waiting: &corev1.ContainerStateWaiting{
				Reason:  "ErrImagePull",
				Message: "pull access denied, repository does not exist or may require authorization",
			},
			startedAgo: time.Minute,
			wantDelete: true,
		},
		{
			name: "not-found error",
			waiting: &corev1.ContainerStateWaiting{
				Reason:  "ErrImagePull",
				Message: `failed to pull and unpack image "` + image + `": manifest unknown`,
			},
			startedAgo: time.Minute,
			wantDelete: true,
		},
		{
			name:    "container creating keeps waiting",
			waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in, job := runningDiscoveryJob(image)
			if tt.startedAgo > 0 {
				job.Status.StartTime = &metav1.Time{Time: time.Now().Add(-tt.startedAgo)}
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-x7k2p", Namespace: "team-a", Labels: map[string]string{"job-name": job.Name}},
				Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
					Name: discoveryContainerName, State: corev1.ContainerState{Waiting: tt.waiting},
				}}},
			}
			c := fake.NewClientBuilder().WithObjects(job, pod).Build()

			got := DecideDiscovery(context.Background(), c, in, FetchDiscoveryState(context.Background(), c, in))

			if !tt.wantDelete {
				if got.CompletedJobToDelete != nil || got.State.Attempts != 1 || got.Reason != "DiscoveryInProgress" {
					t.Fatalf("decision = %+v (attempts %d), want to keep waiting on the Job", got, got.State.Attempts)
				}
				return
			}
			if got.CompletedJobToDelete == nil || got.CompletedJobToDelete.Name != job.Name || got.DesiredJob != nil {
				t.Fatalf("decision deletes %v and creates %v, want Job %s deleted and none created",
					got.CompletedJobToDelete, got.DesiredJob, job.Name)
			}
			if got.State.Attempts != 2 || got.Reason != "DiscoveryImagePullFailed" {
				t.Fatalf("attempts = %d, reason = %q; want 2, DiscoveryImagePullFailed", got.State.Attempts, got.Reason)
			}
			for _, text := range []string{got.Message, got.State.LastFailureReason} {
				if !strings.Contains(text, image) || !strings.Contains(text, tt.waiting.Reason) || !strings.Contains(text, tt.waiting.Message) {
					t.Errorf("%q does not name the image, %s and the kubelet message", text, tt.waiting.Reason)
				}
			}

			// The next reconcile, with the Job gone, backs off and still says why.
			in.CurrentState = got.State
			next := DecideDiscovery(context.Background(), nil, in, DiscoveryFetch{})
			if next.DesiredJob != nil || next.Reason != reasonAwaitingDiscovery || !strings.Contains(next.Message, image) {
				t.Fatalf("next decision = %+v, want AwaitingDiscovery naming %s", next, image)
			}
		})
	}
}

func TestDecideDiscovery_JobPastItsDeadlineBacksOff(t *testing.T) {
	t.Parallel()

	in, job := runningDiscoveryJob("docker.io/amdenterpriseai/aim-dummy:0.2.0")
	const deadlineMessage = "Job was active longer than specified deadline"
	job.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonDeadlineExceeded, Message: deadlineMessage,
	}}

	got := DecideDiscovery(context.Background(), nil, in, DiscoveryFetch{Job: controllerutils.FetchResult[*batchv1.Job]{Value: job}})
	if got.CompletedJobToDelete != job || got.Reason != "DiscoveryFailed" || got.State.Attempts != 2 ||
		got.State.LastFailureReason != deadlineMessage {
		t.Fatalf("decision = %+v (state %+v), want the failed Job deleted, attempt counted, reason DiscoveryFailed", got, got.State)
	}

	in.CurrentState = got.State
	next := DecideDiscovery(context.Background(), nil, in, DiscoveryFetch{})
	backoff := calculateDiscoveryBackoff(got.State.Attempts)
	if next.DesiredJob != nil || next.Reason != reasonAwaitingDiscovery ||
		next.RequeueAfter > backoff || next.RequeueAfter < backoff-time.Second || !strings.Contains(next.Message, deadlineMessage) {
		t.Fatalf("next decision = %+v, want AwaitingDiscovery for about %v naming the deadline", next, backoff)
	}
}

func TestDecideDiscovery_CountsAFailedJobOnce(t *testing.T) {
	t.Parallel()

	in, job := runningDiscoveryJob("docker.io/amdenterpriseai/aim-dummy:0.2.0")
	job.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "too many retries",
	}}
	fetch := DiscoveryFetch{Job: controllerutils.FetchResult[*batchv1.Job]{Value: job}}

	first := DecideDiscovery(context.Background(), nil, in, fetch)
	// The status write re-enqueues the model while the cache still has the deleted Job.
	in.CurrentState = first.State
	second := DecideDiscovery(context.Background(), nil, in, fetch)

	if first.State.Attempts != 2 || second.State.Attempts != 2 ||
		!second.State.LastAttemptTime.Equal(first.State.LastAttemptTime) {
		t.Fatalf("attempts %d then %d (last attempt %v then %v), want the failure counted once",
			first.State.Attempts, second.State.Attempts, first.State.LastAttemptTime, second.State.LastAttemptTime)
	}
}

func TestDecideDiscovery_WaitingRequeuesWithoutCountingAnAttempt(t *testing.T) {
	t.Parallel()

	image := "docker.io/amdenterpriseai/aim-dummy:0.2.0"
	base := time.Duration(constants.DiscoveryBaseBackoffSeconds) * time.Second
	tests := []struct {
		name        string
		blocked     bool
		state       *aimv1alpha1.ModelDiscoveryState
		wantReason  string
		wantRequeue time.Duration
	}{
		{
			name:        "image metadata blocks the model",
			blocked:     true,
			wantReason:  "AwaitingImageMetadata",
			wantRequeue: base,
		},
		{
			name: "backoff after a failed attempt requeues at the retry time",
			state: &aimv1alpha1.ModelDiscoveryState{
				Attempts:        1,
				LastAttemptTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)},
				SpecHash:        ComputeDiscoverySpecHash(image, nil, "", DiscoveryCommandVersion),
			},
			wantReason:  "AwaitingDiscovery",
			wantRequeue: base - 10*time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := DiscoveryInputs{
				ModelName: "dummy", Kind: DiscoveryKindAIMModel, Namespace: "team-a", Image: image,
				CurrentState: tt.state, BlockedByImageMetadata: tt.blocked,
			}
			wantAttempts := int32(0)
			if tt.state != nil {
				wantAttempts = tt.state.Attempts
			}
			got := DecideDiscovery(context.Background(), nil, in, DiscoveryFetch{})
			if got.DesiredJob != nil || got.State.Attempts != wantAttempts || got.Reason != tt.wantReason ||
				got.RequeueAfter > tt.wantRequeue || got.RequeueAfter < tt.wantRequeue-time.Second {
				t.Fatalf("decision = %+v (attempts %d), want no Job, %d attempts, %s, requeue about %v",
					got, got.State.Attempts, wantAttempts, tt.wantReason, tt.wantRequeue)
			}
		})
	}
}
