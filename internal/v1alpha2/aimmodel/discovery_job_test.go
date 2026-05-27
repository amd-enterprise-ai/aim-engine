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
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
)

func TestBuildDiscoveryJob_HasRequiredLabelsAndSpec(t *testing.T) {
	t.Parallel()

	spec := DiscoveryJobSpec{
		ModelName:        "qwen",
		ModelUID:         "uid-1",
		Namespace:        "team-a",
		Image:            "quay.io/amd/aim-qwen:0.10.0",
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "my-secret"}},
		ServiceAccount:   "aim-discovery",
		SpecHash:         "abcdef1234567890",
		Kind:             DiscoveryKindAIMModel,
		OwnerRef: metav1.OwnerReference{
			APIVersion: "aim.eai.amd.com/v1alpha2",
			Kind:       "AIMModel",
			Name:       "qwen",
			UID:        "uid-1",
		},
	}

	job := BuildDiscoveryJob(spec)

	if got := job.Labels["app.kubernetes.io/name"]; got != discoverylock.DiscoveryLabelName {
		t.Fatalf("labels[app.kubernetes.io/name] = %q, want %q (required for shared global cap)", got, discoverylock.DiscoveryLabelName)
	}
	if got := job.Labels[DiscoveryKindLabelKey]; got != DiscoveryKindAIMModel {
		t.Fatalf("labels[%s] = %q, want %q", DiscoveryKindLabelKey, got, DiscoveryKindAIMModel)
	}
	if got := job.Labels[LabelKeyDiscoveryTarget]; got != "qwen" {
		t.Fatalf("labels[%s] = %q, want %q", LabelKeyDiscoveryTarget, got, "qwen")
	}

	if job.Namespace != "team-a" {
		t.Fatalf("namespace = %q, want team-a", job.Namespace)
	}
	if len(job.OwnerReferences) != 1 || job.OwnerReferences[0].UID != "uid-1" {
		t.Fatalf("owner refs = %#v, want single owner UID=uid-1", job.OwnerReferences)
	}

	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("BackoffLimit = %v, want 0 (operator owns retry)", job.Spec.BackoffLimit)
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("TTLSecondsAfterFinished = nil, want set so completed jobs GC")
	}

	podSpec := job.Spec.Template.Spec
	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("RestartPolicy = %q, want Never", podSpec.RestartPolicy)
	}
	if len(podSpec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(podSpec.Containers))
	}
	c := podSpec.Containers[0]
	if c.Image != spec.Image {
		t.Fatalf("container image = %q, want %q", c.Image, spec.Image)
	}
	if c.Name != discoveryContainerName {
		t.Fatalf("container name = %q, want %q (parser relies on this)", c.Name, discoveryContainerName)
	}
	if len(c.Command) == 0 || c.Command[0] != "/bin/sh" {
		t.Fatalf("container command = %#v, want /bin/sh entry", c.Command)
	}
	if len(c.Args) != 1 || !strings.Contains(c.Args[0], "/workspace/aim-runtime/profiles") {
		t.Fatalf("container args = %#v, want script referencing profiles dir", c.Args)
	}
	if len(podSpec.ImagePullSecrets) != 1 || podSpec.ImagePullSecrets[0].Name != "my-secret" {
		t.Fatalf("pull secrets = %#v, want single 'my-secret'", podSpec.ImagePullSecrets)
	}
	if podSpec.ServiceAccountName != "aim-discovery" {
		t.Fatalf("serviceAccount = %q, want aim-discovery", podSpec.ServiceAccountName)
	}
}

func TestDiscoveryJobName_StableAndBounded(t *testing.T) {
	t.Parallel()

	longName := strings.Repeat("a", 120)
	name := discoveryJobName(DiscoveryKindAIMModel, longName, "abcdef1234567890")
	if len(name) > 63 {
		t.Fatalf("len(name) = %d, want <= 63", len(name))
	}
	if !strings.HasPrefix(name, discoveryJobPrefix) {
		t.Fatalf("name = %q, want prefix %q", name, discoveryJobPrefix)
	}

	again := discoveryJobName(DiscoveryKindAIMModel, longName, "abcdef1234567890")
	if name != again {
		t.Fatalf("discoveryJobName not deterministic: %q vs %q", name, again)
	}

	clusterName := discoveryJobName(DiscoveryKindAIMClusterModel, "qwen", "abcdef1234567890")
	modelScopedName := discoveryJobName(DiscoveryKindAIMModel, "qwen", "abcdef1234567890")
	if clusterName == modelScopedName {
		t.Fatalf("cluster vs namespaced names collide: %q", clusterName)
	}
}

func TestComputeDiscoverySpecHash_StableForEquivalentInputs(t *testing.T) {
	t.Parallel()

	h1 := ComputeDiscoverySpecHash(
		"quay.io/amd/aim-qwen:0.10.0",
		[]corev1.LocalObjectReference{{Name: "b"}, {Name: "a"}},
		"sa",
		DiscoveryCommandVersion,
	)
	h2 := ComputeDiscoverySpecHash(
		"quay.io/amd/aim-qwen:0.10.0",
		[]corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}},
		"sa",
		DiscoveryCommandVersion,
	)
	if h1 != h2 {
		t.Fatalf("hash must be order-independent for pull secrets: %q vs %q", h1, h2)
	}

	hDifferentImage := ComputeDiscoverySpecHash(
		"quay.io/amd/aim-qwen:0.11.0",
		[]corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}},
		"sa",
		DiscoveryCommandVersion,
	)
	if hDifferentImage == h1 {
		t.Fatalf("hash should change when image tag changes")
	}

	hNewCmd := ComputeDiscoverySpecHash(
		"quay.io/amd/aim-qwen:0.10.0",
		[]corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}},
		"sa",
		"v2",
	)
	if hNewCmd == h1 {
		t.Fatalf("hash should change when command version changes (forces cache rebuild)")
	}
}

func TestParseDiscoveryLogs_HandlesMultipleProfilesAndEnv(t *testing.T) {
	t.Parallel()

	yamlA := []byte("aim_id: qwen/qwen3\nprofile_id: a\n")
	yamlB := []byte("aim_id: qwen/qwen3\nprofile_id: b\n")

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "some junk line that should be ignored\n")
	fmt.Fprintf(&buf, "%sqwen/qwen3/a.yaml%s\n", DiscoveryRecordBegin, discoveryBeginSuffix)
	fmt.Fprintln(&buf, base64.StdEncoding.EncodeToString(yamlA))
	fmt.Fprintln(&buf, DiscoveryRecordEnd)
	fmt.Fprintf(&buf, "%sqwen/qwen3/b.yaml%s\n", DiscoveryRecordBegin, discoveryBeginSuffix)
	fmt.Fprintln(&buf, base64.StdEncoding.EncodeToString(yamlB))
	fmt.Fprintln(&buf, DiscoveryRecordEnd)
	fmt.Fprintf(&buf, "%sAIM_ID=qwen/qwen3%s\n", DiscoveryEnvPrefix, discoveryBeginSuffix)
	fmt.Fprintf(&buf, "%sAIM_BASE_IMAGE_REF=quay.io/base:1%s\n", DiscoveryEnvPrefix, discoveryBeginSuffix)

	parsed, err := ParseDiscoveryLogs(&buf)
	if err != nil {
		t.Fatalf("ParseDiscoveryLogs() err = %v", err)
	}
	if len(parsed.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(parsed.Profiles))
	}
	if string(parsed.Profiles["qwen/qwen3/a.yaml"]) != string(yamlA) {
		t.Fatalf("profile a = %q, want %q", parsed.Profiles["qwen/qwen3/a.yaml"], yamlA)
	}
	if got := parsed.Env["AIM_ID"]; got != "qwen/qwen3" {
		t.Fatalf("env AIM_ID = %q, want qwen/qwen3", got)
	}
	if got := parsed.Env["AIM_BASE_IMAGE_REF"]; got != "quay.io/base:1" {
		t.Fatalf("env AIM_BASE_IMAGE_REF = %q, want quay.io/base:1", got)
	}
	if len(parsed.OrderedPaths) != 2 || parsed.OrderedPaths[0] != "qwen/qwen3/a.yaml" {
		t.Fatalf("OrderedPaths = %#v, want a.yaml first", parsed.OrderedPaths)
	}
}

func TestParseDiscoveryLogs_EmptyIsError(t *testing.T) {
	t.Parallel()
	_, err := ParseDiscoveryLogs(strings.NewReader("no records here\nlalala\n"))
	if err == nil {
		t.Fatal("expected error when no profile records were emitted")
	}
}

func TestParseDiscoveryLogs_BadBase64IsSkippedNotFatal(t *testing.T) {
	t.Parallel()

	goodYAML := []byte("aim_id: qwen\n")

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%sqwen/bad.yaml%s\n", DiscoveryRecordBegin, discoveryBeginSuffix)
	fmt.Fprintln(&buf, "not-base64!@#$")
	fmt.Fprintln(&buf, DiscoveryRecordEnd)
	fmt.Fprintf(&buf, "%sqwen/good.yaml%s\n", DiscoveryRecordBegin, discoveryBeginSuffix)
	fmt.Fprintln(&buf, base64.StdEncoding.EncodeToString(goodYAML))
	fmt.Fprintln(&buf, DiscoveryRecordEnd)

	parsed, err := ParseDiscoveryLogs(&buf)
	if err != nil {
		t.Fatalf("ParseDiscoveryLogs() err = %v", err)
	}
	if _, ok := parsed.Profiles["qwen/bad.yaml"]; ok {
		t.Fatalf("malformed base64 profile should be dropped, not included")
	}
	if string(parsed.Profiles["qwen/good.yaml"]) != string(goodYAML) {
		t.Fatalf("good profile = %q, want %q", parsed.Profiles["qwen/good.yaml"], goodYAML)
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("expected a warning describing the dropped malformed record, got none")
	}
	if !strings.Contains(parsed.Warnings[0], "qwen/bad.yaml") {
		t.Fatalf("warning %q should reference the dropped profile path", parsed.Warnings[0])
	}
}

func TestShouldCreateDiscoveryJob_BackoffAndSpecHashInvalidation(t *testing.T) {
	t.Parallel()

	hash := "abc"
	now := time.Now()

	// First attempt: always proceed.
	ok, reason, _ := ShouldCreateDiscoveryJob(nil, hash, now)
	if !ok || reason != "" {
		t.Fatalf("first attempt: ok=%v reason=%q, want true/empty", ok, reason)
	}

	// Spec hash changed: proceed even during backoff window.
	state := &aimv1alpha1.ModelDiscoveryState{
		Attempts:        1,
		LastAttemptTime: &metav1.Time{Time: now.Add(-5 * time.Second)},
		SpecHash:        "previous",
	}
	ok, _, _ = ShouldCreateDiscoveryJob(state, hash, now)
	if !ok {
		t.Fatal("spec hash change must override backoff and force retry")
	}

	// Inside backoff, same hash: do not proceed, return reason.
	state = &aimv1alpha1.ModelDiscoveryState{
		Attempts:        1,
		LastAttemptTime: &metav1.Time{Time: now.Add(-5 * time.Second)},
		SpecHash:        hash,
	}
	ok, reason, msg := ShouldCreateDiscoveryJob(state, hash, now)
	if ok {
		t.Fatal("inside backoff window, must wait")
	}
	if reason == "" || !strings.Contains(msg, "attempt") {
		t.Fatalf("reason/msg = %q/%q, want populated", reason, msg)
	}

	// Past the first backoff window (60s base * 2^0), proceed.
	state = &aimv1alpha1.ModelDiscoveryState{
		Attempts:        1,
		LastAttemptTime: &metav1.Time{Time: now.Add(-time.Duration(constants.DiscoveryBaseBackoffSeconds+5) * time.Second)},
		SpecHash:        hash,
	}
	ok, _, _ = ShouldCreateDiscoveryJob(state, hash, now)
	if !ok {
		t.Fatal("past first backoff window, must retry")
	}
}

func TestCalculateDiscoveryBackoff_ExponentialAndCapped(t *testing.T) {
	t.Parallel()

	if d := calculateDiscoveryBackoff(0); d != 0 {
		t.Fatalf("attempts=0 backoff = %v, want 0", d)
	}

	base := time.Duration(constants.DiscoveryBaseBackoffSeconds) * time.Second
	if d := calculateDiscoveryBackoff(1); d != base {
		t.Fatalf("attempts=1 backoff = %v, want %v", d, base)
	}
	if d := calculateDiscoveryBackoff(2); d != 2*base {
		t.Fatalf("attempts=2 backoff = %v, want %v", d, 2*base)
	}

	max := time.Duration(constants.DiscoveryMaxBackoffSeconds) * time.Second
	if d := calculateDiscoveryBackoff(20); d != max {
		t.Fatalf("attempts=20 backoff = %v, want cap=%v", d, max)
	}
}

// TestCalculateDiscoveryBackoff_OverflowSaturates guards against the
// int64 shift overflow at attempts ≥ 64 that would have wrapped the
// multiplier to zero and returned a 0 backoff (instant retry) at the
// point we most want to back off. The function must saturate at
// DiscoveryMaxBackoffSeconds instead.
func TestCalculateDiscoveryBackoff_OverflowSaturates(t *testing.T) {
	t.Parallel()
	max := time.Duration(constants.DiscoveryMaxBackoffSeconds) * time.Second
	for _, attempts := range []int32{63, 64, 100, 1024, 1 << 30} {
		if d := calculateDiscoveryBackoff(attempts); d != max {
			t.Fatalf("attempts=%d backoff = %v, want cap=%v (overflow must saturate)", attempts, d, max)
		}
	}
}

func TestIsDiscoveryJob_Conditions(t *testing.T) {
	t.Parallel()

	running := &batchv1.Job{}
	if IsDiscoveryJobComplete(running) || IsDiscoveryJobSucceeded(running) || IsDiscoveryJobFailed(running) {
		t.Fatal("running job must report none of complete/succeeded/failed")
	}

	succeeded := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobComplete, Status: corev1.ConditionTrue,
	}}}}
	if !IsDiscoveryJobSucceeded(succeeded) || !IsDiscoveryJobComplete(succeeded) {
		t.Fatal("succeeded job must report complete+succeeded")
	}
	if IsDiscoveryJobFailed(succeeded) {
		t.Fatal("succeeded job must not report failed")
	}

	failed := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "too many retries",
	}}}}
	if !IsDiscoveryJobFailed(failed) || !IsDiscoveryJobComplete(failed) {
		t.Fatal("failed job must report failed+complete")
	}
	if IsDiscoveryJobSucceeded(failed) {
		t.Fatal("failed job must not report succeeded")
	}
	if got := GetDiscoveryJobFailureReason(failed); got != "too many retries" {
		t.Fatalf("failure reason = %q, want %q", got, "too many retries")
	}
}
