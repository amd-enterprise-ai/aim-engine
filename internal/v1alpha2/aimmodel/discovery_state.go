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
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// DiscoveryFetch is the raw state the reconciler pulls from the cluster for the
// discovery subsystem of an AIMModel/AIMClusterModel.
type DiscoveryFetch struct {
	Cache controllerutils.FetchResult[*corev1.ConfigMap]
	Job   controllerutils.FetchResult[*batchv1.Job]
}

// DiscoveryDecision is the composed output of the discovery state machine. Exactly
// one of {Catalog, DesiredJob} is set when we are making forward progress; both
// may be nil if we are waiting on an in-flight Job.
type DiscoveryDecision struct {
	// Catalog is the normalized catalog parsed out of a valid cache ConfigMap.
	// If nil, the caller must not attempt to build profiles yet.
	Catalog *aimprofile.DiscoveryCatalog

	// DesiredCache is a ConfigMap to apply (produced by scraping a successful Job).
	// Nil when the existing cache is already valid.
	DesiredCache *corev1.ConfigMap

	// DesiredJob is a Job to apply when we need a (new) discovery run and the
	// global concurrency cap permits it.
	DesiredJob *batchv1.Job

	// CompletedJobToDelete is a finished Job that can be garbage-collected once
	// its logs have been consumed into a cache.
	CompletedJobToDelete *batchv1.Job

	// RequeueAfter is non-zero when we explicitly want the controller to come
	// back (cap exhausted, awaiting Job progress, backoff).
	RequeueAfter time.Duration

	// State is the new status to stamp on the object, capturing attempts/backoff.
	State *aimv1alpha1.ModelDiscoveryState

	// Reason / Message describe the current discovery phase for status decoration.
	Reason  string
	Message string

	// BuildErr, if non-nil, should short-circuit profile generation: discovery
	// is broken for a reason the operator cannot retry through.
	BuildErr error

	// Progressing reports whether we should treat this as an in-flight async
	// process (awaiting discovery, awaiting job, backing off).
	Progressing bool
}

// DiscoveryInputs is everything callers must supply to resolve the discovery
// state for a single AIMModel or AIMClusterModel reconcile pass.
type DiscoveryInputs struct {
	Kind             string // DiscoveryKindAIMModel or DiscoveryKindAIMClusterModel
	ModelName        string
	ModelUID         string
	Namespace        string // cache/job namespace; for cluster models this is operator ns
	Image            string
	ImagePullSecrets []corev1.LocalObjectReference
	ServiceAccount   string
	OwnerRef         metav1.OwnerReference
	CurrentState     *aimv1alpha1.ModelDiscoveryState
	Clientset        kubernetes.Interface
}

// FetchDiscoveryState pulls the latest cache + Job for the model. It never returns
// an error; individual errors are captured in the FetchResult wrappers so that
// ComposeState can still make progress on the parts it has.
func FetchDiscoveryState(ctx context.Context, c client.Client, in DiscoveryInputs) DiscoveryFetch {
	cacheName, err := discoveryCacheName(in.ModelName)
	if err != nil {
		return DiscoveryFetch{Cache: controllerutils.FetchResult[*corev1.ConfigMap]{Error: err}}
	}
	cache := controllerutils.Fetch(ctx, c, client.ObjectKey{Name: cacheName, Namespace: in.Namespace}, &corev1.ConfigMap{})
	job := FetchDiscoveryJob(ctx, c, in.Kind, in.Namespace, in.ModelName)
	return DiscoveryFetch{Cache: cache, Job: job}
}

// DecideDiscovery evaluates the current cache / Job / state tuple and returns
// what the reconcile pipeline should do next: load a catalog, apply a new cache,
// create a Job, wait for backoff, or surface an error.
func DecideDiscovery(ctx context.Context, c client.Client, in DiscoveryInputs, fetch DiscoveryFetch) DiscoveryDecision {
	logger := log.FromContext(ctx).WithName("aimmodel-discovery")

	specHash := ComputeDiscoverySpecHash(in.Image, in.ImagePullSecrets, in.ServiceAccount, DiscoveryCommandVersion)
	nextState := cloneOrNewDiscoveryState(in.CurrentState)
	nextState.SpecHash = specHash

	cache := fetch.Cache.Value
	// If the cache exists and matches the current spec hash, load the catalog
	// straight from it — no Job required.
	if cache != nil && CacheHasMatchingSpecHash(cache, specHash) && aimprofile.HasRawProfileKeys(cache) {
		catalog, err := aimprofile.ParseDiscoveryCatalog(cache)
		if err != nil {
			logger.Error(err, "discovery cache is malformed, falling back to re-discovery", "cache", client.ObjectKeyFromObject(cache))
		} else {
			return DiscoveryDecision{
				Catalog: catalog,
				State:   nextState,
				Reason:  "DiscoveryCacheValid",
				Message: "Using cached discovery result",
			}
		}
	}

	job := fetch.Job.Value
	if job != nil {
		if !IsDiscoveryJobComplete(job) {
			// Job is still running; just wait.
			return DiscoveryDecision{
				State:        nextState,
				Progressing:  true,
				RequeueAfter: 5 * time.Second,
				Reason:       "DiscoveryInProgress",
				Message:      fmt.Sprintf("Discovery Job %s is still running", job.Name),
			}
		}

		if IsDiscoveryJobSucceeded(job) {
			// Pod logs should be available. Parse them and produce a cache.
			cm, parseErr := buildCacheFromJob(ctx, c, in, job, specHash)
			if parseErr != nil {
				nextState.Attempts++
				nextState.LastAttemptTime = nowPtr()
				nextState.LastFailureReason = "LogParseFailed"
				logger.Error(parseErr, "failed to parse discovery logs", "job", job.Name)
				return DiscoveryDecision{
					State:                nextState,
					Progressing:          true,
					CompletedJobToDelete: job,
					Reason:               "LogParseFailed",
					Message:              parseErr.Error(),
				}
			}
			// Attach owner so the cache is GC'd with the model.
			cm.OwnerReferences = []metav1.OwnerReference{in.OwnerRef}
			// Reset attempts on success.
			nextState.Attempts = 0
			nextState.LastFailureReason = ""
			nextState.LastAttemptTime = nil
			catalog, err := aimprofile.ParseDiscoveryCatalog(cm)
			if err != nil {
				return DiscoveryDecision{
					State:                nextState,
					CompletedJobToDelete: job,
					Progressing:          true,
					Reason:               "CacheParseFailed",
					Message:              err.Error(),
				}
			}
			return DiscoveryDecision{
				Catalog:              catalog,
				DesiredCache:         cm,
				CompletedJobToDelete: job,
				State:                nextState,
				Reason:               "DiscoverySucceeded",
				Message:              fmt.Sprintf("Discovered %d profiles", len(catalog.Profiles)),
			}
		}

		// Job failed: record the reason, bump attempts, let backoff gate the retry.
		reason := GetDiscoveryJobFailureReason(job)
		nextState.Attempts++
		nextState.LastAttemptTime = nowPtr()
		nextState.LastFailureReason = reason
		return DiscoveryDecision{
			State:                nextState,
			CompletedJobToDelete: job,
			Progressing:          true,
			RequeueAfter:         5 * time.Second,
			Reason:               "DiscoveryFailed",
			Message:              fmt.Sprintf("Discovery Job %s failed: %s", job.Name, reason),
		}
	}

	// No valid cache and no Job: decide whether to create one now (respecting backoff + cap).
	shouldCreate, backoffReason, backoffMsg := ShouldCreateDiscoveryJob(in.CurrentState, specHash, time.Now())
	if !shouldCreate {
		return DiscoveryDecision{
			State:        nextState,
			Progressing:  true,
			RequeueAfter: 5 * time.Second,
			Reason:       backoffReason,
			Message:      backoffMsg,
		}
	}

	active, err := discoverylock.CountActiveDiscoveryJobs(ctx, c)
	if err != nil {
		return DiscoveryDecision{
			State:        nextState,
			Progressing:  true,
			RequeueAfter: 5 * time.Second,
			Reason:       "DiscoveryCountFailed",
			Message:      err.Error(),
		}
	}
	if active >= constants.MaxConcurrentDiscoveryJobs {
		return DiscoveryDecision{
			State:        nextState,
			Progressing:  true,
			RequeueAfter: 5 * time.Second,
			Reason:       "DiscoveryCapReached",
			Message:      fmt.Sprintf("Waiting for discovery slot (%d/%d active)", active, constants.MaxConcurrentDiscoveryJobs),
		}
	}

	// Schedule a new Job.
	nextState.Attempts++
	nextState.LastAttemptTime = nowPtr()
	newJob := BuildDiscoveryJob(DiscoveryJobSpec{
		ModelName:        in.ModelName,
		ModelUID:         in.ModelUID,
		Namespace:        in.Namespace,
		Image:            in.Image,
		ImagePullSecrets: in.ImagePullSecrets,
		ServiceAccount:   in.ServiceAccount,
		SpecHash:         specHash,
		OwnerRef:         in.OwnerRef,
		Kind:             in.Kind,
	})
	return DiscoveryDecision{
		DesiredJob:  newJob,
		State:       nextState,
		Progressing: true,
		Reason:      "DiscoveryStarted",
		Message:     fmt.Sprintf("Started discovery Job %s", newJob.Name),
	}
}

func buildCacheFromJob(ctx context.Context, c client.Client, in DiscoveryInputs, job *batchv1.Job, specHash string) (*corev1.ConfigMap, error) {
	raw, err := ReadDiscoveryPodLogs(ctx, c, in.Clientset, job)
	if err != nil {
		return nil, fmt.Errorf("read discovery logs: %w", err)
	}
	parsed, err := ParseDiscoveryLogs(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse discovery logs: %w", err)
	}
	// Surface non-fatal per-record parse warnings (e.g. malformed base64
	// payloads) so operators have a breadcrumb when a profile is missing
	// from the catalog. The discovery succeeds overall — the dropped
	// records are intentionally non-fatal — but silent drops were a
	// recurring "where did my profile go?" diagnostic gap.
	if len(parsed.Warnings) > 0 {
		logger := log.FromContext(ctx).WithName("aimmodel-discovery")
		for _, w := range parsed.Warnings {
			logger.Info("discovery log parse warning", "model", in.ModelName, "warning", w)
		}
	}

	cacheName, err := discoveryCacheName(in.ModelName)
	if err != nil {
		return nil, err
	}

	cm, err := BuildDiscoveryCacheConfigMap(DiscoveryCacheInput{
		Name:                    cacheName,
		Namespace:               in.Namespace,
		ModelUID:                in.ModelUID,
		ModelName:               in.ModelName,
		Profiles:                parsed.Profiles,
		OrderedPaths:            parsed.OrderedPaths,
		SourceImage:             in.Image,
		AimID:                   parsed.Env["AIM_ID"],
		BaseImage:               parsed.Env["AIM_BASE_IMAGE_REF"],
		SourceSpecHash:          specHash,
		DiscoveryCommandVersion: DiscoveryCommandVersion,
	})
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func cloneOrNewDiscoveryState(in *aimv1alpha1.ModelDiscoveryState) *aimv1alpha1.ModelDiscoveryState {
	if in == nil {
		return &aimv1alpha1.ModelDiscoveryState{}
	}
	return in.DeepCopy()
}

func nowPtr() *metav1.Time {
	now := metav1.NewTime(time.Now())
	return &now
}
