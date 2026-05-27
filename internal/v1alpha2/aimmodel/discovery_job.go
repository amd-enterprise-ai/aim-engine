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
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
)

const (
	// DiscoveryCommandVersion is the contract version of the shell script the operator
	// injects into the discovery Job. Bumping this value forces a cache re-run for
	// every AIMModel / AIMClusterModel because it feeds into the discovery spec hash.
	DiscoveryCommandVersion = "v1"

	// DiscoveryKindLabelKey distinguishes v1alpha2 AIMModel-scoped discovery Jobs
	// from v1alpha1 AIMServiceTemplate ones while both still count against the same
	// global 10-Job budget (they share app.kubernetes.io/name=aim-discovery).
	DiscoveryKindLabelKey = constants.AimLabelDomain + "/discovery-kind"

	// DiscoveryKindAIMModel marks Jobs owned by v1alpha2 AIMModel resources.
	DiscoveryKindAIMModel = "aimmodel"

	// DiscoveryKindAIMClusterModel marks Jobs owned by v1alpha2 AIMClusterModel resources.
	DiscoveryKindAIMClusterModel = "aimclustermodel"

	// LabelKeyDiscoveryTarget identifies the AIMModel / AIMClusterModel that owns the Job.
	LabelKeyDiscoveryTarget = constants.AimLabelDomain + "/discovery-target"

	// discoveryContainerName is the name of the container that runs the discovery script.
	discoveryContainerName = "discovery"

	// discoveryJobTTLSeconds defines how long completed discovery Jobs stay around
	// before Kubernetes garbage-collects them.
	discoveryJobTTLSeconds = 300

	// discoveryJobBackoffLimit forces Kubernetes to fail the Job immediately so the
	// operator can own the retry/backoff policy instead.
	discoveryJobBackoffLimit = 0

	// DiscoveryRecordBegin and DiscoveryRecordEnd delimit base64-encoded profile YAML
	// blocks emitted by the in-image shell script.
	DiscoveryRecordBegin = "@@BEGIN "
	DiscoveryRecordEnd   = "@@END@@"

	// DiscoveryEnvPrefix is the prefix the in-image shell script uses to echo the
	// discovery-relevant environment variables (AIM_ID, AIM_BASE_IMAGE_REF).
	DiscoveryEnvPrefix = "@@ENV "

	// discoveryBeginSuffix closes the @@BEGIN header.
	discoveryBeginSuffix = "@@"

	// jobNameMaxLength is the Kubernetes object name upper bound.
	jobNameMaxLength = 63

	// discoveryJobPrefix is prepended to every generated discovery Job name.
	discoveryJobPrefix = "aim-disc-"

	// discoveryHashHexLen is the number of hex characters retained from the spec hash
	// when composing a Job name. Keeping this small leaves room for model names.
	discoveryHashHexLen = 8
)

// DiscoveryJobSpec encapsulates everything the operator needs to build a discovery Job.
type DiscoveryJobSpec struct {
	ModelName        string
	ModelUID         string
	Namespace        string
	Image            string
	ImagePullSecrets []corev1.LocalObjectReference
	ServiceAccount   string
	SpecHash         string
	OwnerRef         metav1.OwnerReference
	// Kind is one of DiscoveryKindAIMModel or DiscoveryKindAIMClusterModel; surfaced
	// as a label so we can filter without losing the global 10-Job count.
	Kind string
}

// BuildDiscoveryJob materializes the Kubernetes Job that runs the in-image profile
// walk. The command is a portable POSIX shell one-liner so it runs against any AIM
// image (alpine/busybox, distroless variants that ship a shell, etc.).
func BuildDiscoveryJob(spec DiscoveryJobSpec) *batchv1.Job {
	backoffLimit := int32(discoveryJobBackoffLimit)
	ttl := int32(discoveryJobTTLSeconds)

	command := DiscoveryShellCommand()

	allowPrivilegeEscalation := false
	runAsNonRoot := false

	labels := map[string]string{
		"app.kubernetes.io/name":       discoverylock.DiscoveryLabelName,
		"app.kubernetes.io/component":  constants.LabelValueComponentDiscovery,
		"app.kubernetes.io/managed-by": constants.LabelValueManagedByController,
		DiscoveryKindLabelKey:          spec.Kind,
		LabelKeyDiscoveryTarget:        spec.ModelName,
	}

	jobName := discoveryJobName(spec.Kind, spec.ModelName, spec.SpecHash)

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            jobName,
			Namespace:       spec.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{spec.OwnerRef},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						DiscoveryKindLabelKey:   spec.Kind,
						LabelKeyDiscoveryTarget: spec.ModelName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ImagePullSecrets:   spec.ImagePullSecrets,
					ServiceAccountName: spec.ServiceAccount,
					Containers: []corev1.Container{
						{
							Name:    discoveryContainerName,
							Image:   spec.Image,
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{command},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								RunAsNonRoot:             &runAsNonRoot,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    mustParseQuantity("50m"),
									corev1.ResourceMemory: mustParseQuantity("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    mustParseQuantity("500m"),
									corev1.ResourceMemory: mustParseQuantity("256Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	return job
}

// DiscoveryShellCommand returns the POSIX-shell script the operator injects as the
// Job's command. It walks /workspace/aim-runtime/profiles/{<AIM_ID>,general}/**.yaml,
// base64-encodes each file, and emits framed records plus AIM_ID / AIM_BASE_IMAGE_REF.
//
// The script is intentionally tolerant: missing directories are skipped, not errors.
func DiscoveryShellCommand() string {
	// The script must be valid busybox sh; avoid bash-isms (arrays, [[ ]], printf %q).
	// "tr -d '\n'" makes us resilient to base64 variants that wrap lines by default.
	//
	// We prepend a generous PATH so tools like find/base64/tr/sort resolve even
	// when the AIM image ships with a minimal PATH (e.g. alpine-based images that
	// set PATH=/bin:/). find, base64, and tr live in /usr/bin on most distros.
	const script = `set -eu
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:${PATH:-}"
BASE=/workspace/aim-runtime/profiles
AIM_ID_VAL="${AIM_ID:-}"
for prefix in "$AIM_ID_VAL" general; do
  [ -z "$prefix" ] && continue
  DIR="$BASE/$prefix"
  [ -d "$DIR" ] || continue
  find "$DIR" -type f -name "*.yaml" | LC_ALL=C sort | while IFS= read -r f; do
    rel=${f#"$BASE/"}
    printf '@@BEGIN %s@@\n' "$rel"
    base64 < "$f" | tr -d '\n'
    printf '\n@@END@@\n'
  done
done
printf '@@ENV AIM_ID=%s@@\n' "$AIM_ID_VAL"
printf '@@ENV AIM_BASE_IMAGE_REF=%s@@\n' "${AIM_BASE_IMAGE_REF:-}"
`
	return script
}

// ParsedDiscovery is the result of scraping pod logs from a discovery Job.
type ParsedDiscovery struct {
	// Profiles maps in-image relative path (e.g. "HuggingFaceTB/SmolLM2-135M/cpu-bf16.yaml")
	// to raw YAML bytes exactly as produced by the image.
	Profiles map[string][]byte
	// OrderedPaths is the deterministic order in which the image emitted the profiles.
	OrderedPaths []string
	// Env captures AIM_ID / AIM_BASE_IMAGE_REF as scraped from stdout.
	Env map[string]string
	// Warnings collects non-fatal per-record parse issues encountered while
	// scanning the logs (e.g. malformed base64 payloads, mismatched begin
	// markers). Callers should log these even when the overall parse
	// succeeded so operators can diagnose "why is profile X missing?"
	// without having to re-run the discovery Job at a higher log verbosity.
	Warnings []string
}

// ParseDiscoveryLogs parses the delimited stdout of a discovery Job container. It
// tolerates garbage lines between records (e.g. shell debug output) and returns
// only the well-formed @@BEGIN/@@END blocks it can decode.
func ParseDiscoveryLogs(reader io.Reader) (*ParsedDiscovery, error) {
	parsed := &ParsedDiscovery{
		Profiles: map[string][]byte{},
		Env:      map[string]string{},
	}

	// Scan line-by-line so we can accept very long base64 payloads. We'll enlarge
	// the buffer to a generous ceiling (profiles can exceed the default 64 KiB).
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<16), 8<<20)

	var (
		inRecord bool
		path     string
		payload  bytes.Buffer
	)

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case !inRecord && strings.HasPrefix(line, DiscoveryRecordBegin):
			rest := strings.TrimPrefix(line, DiscoveryRecordBegin)
			if !strings.HasSuffix(rest, discoveryBeginSuffix) {
				continue
			}
			path = strings.TrimSuffix(rest, discoveryBeginSuffix)
			if path == "" {
				continue
			}
			payload.Reset()
			inRecord = true
		case inRecord && line == DiscoveryRecordEnd:
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.String()))
			if err != nil {
				parsed.Warnings = append(parsed.Warnings,
					fmt.Sprintf("dropping malformed discovery record %q: base64 decode failed: %v", path, err))
				inRecord = false
				continue
			}
			if _, dup := parsed.Profiles[path]; !dup {
				parsed.OrderedPaths = append(parsed.OrderedPaths, path)
			}
			parsed.Profiles[path] = decoded
			inRecord = false
		case inRecord:
			payload.WriteString(line)
		case strings.HasPrefix(line, DiscoveryEnvPrefix):
			rest := strings.TrimPrefix(line, DiscoveryEnvPrefix)
			rest = strings.TrimSuffix(rest, discoveryBeginSuffix)
			key, value, ok := strings.Cut(rest, "=")
			if !ok {
				continue
			}
			parsed.Env[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan discovery logs: %w", err)
	}
	if len(parsed.Profiles) == 0 {
		return nil, fmt.Errorf("discovery logs contained no profile records")
	}

	return parsed, nil
}

// ReadDiscoveryPodLogs fetches the stdout of the first successful pod of a completed
// discovery Job. It returns the raw bytes so a caller can parse them (and keep the
// bytes for debugging if parsing fails).
func ReadDiscoveryPodLogs(ctx context.Context, c client.Client, clientset kubernetes.Interface, job *batchv1.Job) ([]byte, error) {
	pod, err := findSuccessfulDiscoveryPod(ctx, c, job)
	if err != nil {
		return nil, err
	}
	return streamDiscoveryPodLogs(ctx, clientset, pod)
}

func findSuccessfulDiscoveryPod(ctx context.Context, c client.Client, job *batchv1.Job) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods,
		client.InNamespace(job.Namespace),
		client.MatchingLabels{"job-name": job.Name},
	); err != nil {
		return nil, fmt.Errorf("list pods for discovery job: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods found for discovery job %s/%s", job.Namespace, job.Name)
	}
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodSucceeded {
			return &pods.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no successful pod for discovery job %s/%s", job.Namespace, job.Name)
}

func streamDiscoveryPodLogs(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod) ([]byte, error) {
	if clientset == nil {
		return nil, fmt.Errorf("clientset is nil; cannot stream pod logs")
	}
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: discoveryContainerName,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("stream pod logs %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	defer func() { _ = stream.Close() }()
	return io.ReadAll(stream)
}

// IsDiscoveryJobComplete is a thin alias over the generic IsJobComplete.
func IsDiscoveryJobComplete(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// IsDiscoveryJobSucceeded reports whether the Job has a Complete=True condition.
func IsDiscoveryJobSucceeded(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// IsDiscoveryJobFailed reports whether the Job has a Failed=True condition.
func IsDiscoveryJobFailed(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// GetDiscoveryJobFailureReason extracts a human-readable failure message from a Job.
func GetDiscoveryJobFailureReason(job *batchv1.Job) string {
	if job == nil {
		return ""
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			if c.Message != "" {
				return c.Message
			}
			if c.Reason != "" {
				return c.Reason
			}
			return "unknown failure"
		}
	}
	return ""
}

// FetchDiscoveryJob returns the most recently created discovery Job for the given model,
// or nil if none exist.
func FetchDiscoveryJob(ctx context.Context, c client.Client, kind, namespace, modelName string) controllerutils.FetchResult[*batchv1.Job] {
	var jobs batchv1.JobList
	opts := []client.ListOption{
		client.MatchingLabels{
			DiscoveryKindLabelKey:   kind,
			LabelKeyDiscoveryTarget: modelName,
		},
	}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.List(ctx, &jobs, opts...); err != nil {
		return controllerutils.FetchResult[*batchv1.Job]{Error: err}
	}
	if len(jobs.Items) == 0 {
		return controllerutils.FetchResult[*batchv1.Job]{}
	}
	sort.Slice(jobs.Items, func(i, j int) bool {
		return jobs.Items[i].CreationTimestamp.After(jobs.Items[j].CreationTimestamp.Time)
	})
	return controllerutils.FetchResult[*batchv1.Job]{Value: &jobs.Items[0]}
}

// ComputeDiscoverySpecHash hashes the model spec fields whose change must invalidate
// a cached discovery: image, pullSecrets, serviceAccountName, plus the in-image
// script contract version.
func ComputeDiscoverySpecHash(image string, pullSecrets []corev1.LocalObjectReference, serviceAccount, commandVersion string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, "image\x00"+image)
	secretNames := make([]string, 0, len(pullSecrets))
	for _, s := range pullSecrets {
		secretNames = append(secretNames, s.Name)
	}
	sort.Strings(secretNames)
	_, _ = io.WriteString(h, "\x00pullSecrets")
	for _, n := range secretNames {
		_, _ = io.WriteString(h, "\x00"+n)
	}
	_, _ = io.WriteString(h, "\x00sa\x00"+serviceAccount)
	_, _ = io.WriteString(h, "\x00cmd\x00"+commandVersion)
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

// ShouldCreateDiscoveryJob returns true if the operator should create (or recreate)
// a discovery Job now. Returns (false, reason, message) with a human-readable
// explanation when backoff applies.
func ShouldCreateDiscoveryJob(state *aimv1alpha1.ModelDiscoveryState, currentSpecHash string, now time.Time) (bool, string, string) {
	if state == nil || state.Attempts == 0 {
		return true, "", ""
	}
	if state.SpecHash != "" && state.SpecHash != currentSpecHash {
		return true, "", ""
	}
	if state.LastAttemptTime != nil && state.Attempts > 0 {
		backoff := calculateDiscoveryBackoff(state.Attempts)
		next := state.LastAttemptTime.Add(backoff)
		if now.Before(next) {
			remaining := next.Sub(now).Round(time.Second)
			return false, "AwaitingDiscovery", fmt.Sprintf("Waiting %s before retry (attempt %d)", remaining, state.Attempts+1)
		}
	}
	return true, "", ""
}

// calculateDiscoveryBackoff uses exponential backoff: base * 2^(attempts-1), capped at max.
//
// attempts is capped well before the bit shift width so we never wrap.
// Pre-fix, attempts ≥ 64 made `int64(1) << (attempts-1)` set the sign
// bit (or wrap to 0), producing a negative or zero multiplier — and
// therefore a 0 (or negative) backoff — right when we most wanted to
// back off. We cap shift at 32 which keeps `base * multiplier` safely
// inside int64 for any realistic base, while still vastly exceeding
// DiscoveryMaxBackoffSeconds (60 << 32 ≈ 8000 years), so the
// observable saturated-backoff window is unchanged.
func calculateDiscoveryBackoff(attempts int32) time.Duration {
	if attempts <= 0 {
		return 0
	}
	const maxShift = 32
	shift := uint(attempts - 1)
	if shift > maxShift {
		shift = maxShift
	}
	multiplier := int64(1) << shift
	secs := int64(constants.DiscoveryBaseBackoffSeconds) * multiplier
	if secs > int64(constants.DiscoveryMaxBackoffSeconds) {
		secs = int64(constants.DiscoveryMaxBackoffSeconds)
	}
	return time.Duration(secs) * time.Second
}

func mustParseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(fmt.Sprintf("parse quantity %q: %v", s, err))
	}
	return q
}

// discoveryJobName composes a kubernetes-valid Job name from a hash fragment and model name.
func discoveryJobName(kind, modelName, specHash string) string {
	hash := specHash
	if len(hash) > discoveryHashHexLen {
		hash = hash[:discoveryHashHexLen]
	}
	kindPrefix := "m"
	if kind == DiscoveryKindAIMClusterModel {
		kindPrefix = "cm"
	}
	reserved := len(discoveryJobPrefix) + len(kindPrefix) + 1 + 1 + len(hash) // prefix + kind + '-' + name + '-' + hash
	maxName := jobNameMaxLength - reserved
	name := modelName
	if maxName > 0 && len(name) > maxName {
		name = name[:maxName]
	}
	return fmt.Sprintf("%s%s-%s-%s", discoveryJobPrefix, kindPrefix, name, hash)
}
