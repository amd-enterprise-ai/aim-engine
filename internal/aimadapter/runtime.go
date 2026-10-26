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
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	runtimeModelsResponseLimit    = 1 << 20 // 1 MiB
	runtimeRequestTimeout         = 2 * time.Second
	runtimeObservationTimeout     = 3 * time.Second
	runtimeMaxConcurrentRequests  = 8
	runtimePollBaseInterval       = 30 * time.Second
	runtimePollJitterRangeSeconds = 11 // deterministic -5s..+5s
)

// RuntimeModelClient returns the model IDs currently served by one predictor
// replica. The interface keeps the observer deterministic and unit-testable.
type RuntimeModelClient interface {
	ListModels(ctx context.Context, pod *corev1.Pod) ([]string, error)
}

// HTTPRuntimeModelClient queries vLLM's OpenAI-compatible /v1/models endpoint
// directly on the predictor pod. Direct pod access observes each replica
// independently; querying the Service would sample an arbitrary replica.
type HTTPRuntimeModelClient struct {
	Client *http.Client
}

// NewHTTPRuntimeModelClient returns a reusable client for all AIMService
// reconciles. The shared client and default transport retain idle connections
// between polls while the per-observation context still bounds total latency.
func NewHTTPRuntimeModelClient() HTTPRuntimeModelClient {
	return HTTPRuntimeModelClient{
		Client: &http.Client{Timeout: runtimeRequestTimeout},
	}
}

func (c HTTPRuntimeModelClient) ListModels(ctx context.Context, pod *corev1.Pod) ([]string, error) {
	if pod == nil || pod.Status.PodIP == "" {
		return nil, fmt.Errorf("predictor pod has no IP")
	}

	httpClient := c.Client
	if httpClient == nil {
		httpClient = NewHTTPRuntimeModelClient().Client
	}

	url := "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(constants.DefaultHTTPPort)) + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("query models: HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, runtimeModelsResponseLimit)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

// RuntimeObservation is a point-in-time aggregate across all ready predictor
// replicas. Complete is deliberately strict: status transitions are only made
// when every serving replica answered successfully, preventing a timeout from
// being misreported as an unloaded adapter.
type RuntimeObservation struct {
	Complete       bool
	ObservedAt     metav1.Time
	Replicas       int
	FailedReplicas int
	Failures       []string
	LoadedReplicas map[string]int
}

// ObserveRuntimeModels queries each ready serving replica once. Requests are
// concurrent but bounded, and the entire observation has a short deadline so a
// slow runtime cannot hold a reconciliation worker indefinitely.
func ObserveRuntimeModels(
	ctx context.Context,
	modelClient RuntimeModelClient,
	pods *corev1.PodList,
) RuntimeObservation {
	eligible := readyServingPods(pods)
	observation := RuntimeObservation{
		Replicas:       len(eligible),
		LoadedReplicas: map[string]int{},
	}
	if len(eligible) == 0 {
		return observation
	}
	if modelClient == nil {
		modelClient = HTTPRuntimeModelClient{}
	}

	observationCtx, cancel := context.WithTimeout(ctx, runtimeObservationTimeout)
	defer cancel()

	type result struct {
		models []string
		err    error
	}
	jobs := make(chan *corev1.Pod)
	results := make(chan result, len(eligible))

	workers := min(len(eligible), runtimeMaxConcurrentRequests)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for pod := range jobs {
				models, err := modelClient.ListModels(observationCtx, pod)
				results <- result{models: models, err: err}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, pod := range eligible {
			select {
			case jobs <- pod:
			case <-observationCtx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	responses := 0
	for replica := range results {
		responses++
		if replica.err != nil {
			observation.FailedReplicas++
			if len(observation.Failures) < 3 {
				observation.Failures = append(observation.Failures, replica.err.Error())
			}
			continue
		}
		seen := make(map[string]struct{}, len(replica.models))
		for _, modelID := range replica.models {
			if _, duplicate := seen[modelID]; duplicate {
				continue
			}
			seen[modelID] = struct{}{}
			observation.LoadedReplicas[modelID]++
		}
	}

	// A cancelled producer may enqueue fewer jobs than there are eligible pods.
	observation.FailedReplicas += len(eligible) - responses
	if observation.FailedReplicas == 0 {
		observation.Complete = true
		observation.ObservedAt = metav1.Now()
	}
	return observation
}

func readyServingPods(pods *corev1.PodList) []*corev1.Pod {
	if pods == nil {
		return nil
	}

	ready := make([]*corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil ||
			pod.Status.Phase != corev1.PodRunning ||
			pod.Status.PodIP == "" ||
			!podReady(pod) {
			continue
		}
		ready = append(ready, pod)
	}
	return ready
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ShouldObserveRuntime reports whether status has reached a disk-side state
// that the runtime can meaningfully confirm. The reconcile that first records
// Downloaded is followed by either the status watch or the scheduled poll.
func ShouldObserveRuntime(service *aimv1alpha1.AIMService) bool {
	for _, adapter := range service.Status.Adapters {
		if adapter.State == aimv1alpha1.AdapterStateDownloaded ||
			adapter.State == aimv1alpha1.AdapterStateLoaded {
			return true
		}
	}
	return false
}

// RuntimePollRequeueAfter requests polling only after at least one adapter is
// staged. Pod and Job watches drive earlier convergence. A deterministic jitter
// spreads services across an 11-second window without introducing test
// nondeterminism or mutable random state.
func RuntimePollRequeueAfter(service *aimv1alpha1.AIMService, state State) time.Duration {
	hasDownloadedAdapter := false
	for _, adapter := range state.Adapters {
		if adapter.State == aimv1alpha1.AdapterStateDownloaded {
			hasDownloadedAdapter = true
			break
		}
	}
	if !hasDownloadedAdapter {
		return 0
	}

	key := string(service.UID)
	if key == "" {
		key = service.Namespace + "/" + service.Name
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	jitterSeconds := int64(hash.Sum32()%runtimePollJitterRangeSeconds) - 5
	return runtimePollBaseInterval + time.Duration(jitterSeconds)*time.Second
}
