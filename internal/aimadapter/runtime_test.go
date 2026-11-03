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
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

type fakeRuntimeModelClient struct {
	models map[string][]string
	errs   map[string]error
}

func (f fakeRuntimeModelClient) ListModels(_ context.Context, pod *corev1.Pod) ([]string, error) {
	if err := f.errs[pod.Name]; err != nil {
		return nil, err
	}
	return f.models[pod.Name], nil
}

func readyRuntimePod(name, ip string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: ip,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
}

func TestObserveRuntimeModelsAggregatesReadyReplicas(t *testing.T) {
	notReady := readyRuntimePod("not-ready", "10.0.0.3")
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	pods := &corev1.PodList{Items: []corev1.Pod{
		readyRuntimePod("replica-a", "10.0.0.1"),
		readyRuntimePod("replica-b", "10.0.0.2"),
		notReady,
	}}
	client := fakeRuntimeModelClient{models: map[string][]string{
		"replica-a": {"base", "lora-a", "lora-a"},
		"replica-b": {"base", "lora-a", "lora-b"},
	}}

	got := ObserveRuntimeModels(context.Background(), client, pods)

	if !got.Complete {
		t.Fatalf("observation should be complete: %+v", got)
	}
	if got.Replicas != 2 || got.FailedReplicas != 0 {
		t.Fatalf("replica counts = %d total, %d failed; want 2, 0", got.Replicas, got.FailedReplicas)
	}
	if got.LoadedReplicas["base"] != 2 ||
		got.LoadedReplicas["lora-a"] != 2 ||
		got.LoadedReplicas["lora-b"] != 1 {
		t.Fatalf("unexpected model aggregation: %+v", got.LoadedReplicas)
	}
	if got.ObservedAt.IsZero() {
		t.Fatal("complete observation must carry ObservedAt")
	}
}

func TestObserveRuntimeModelsIsIncompleteWhenAnyReplicaFails(t *testing.T) {
	pods := &corev1.PodList{Items: []corev1.Pod{
		readyRuntimePod("replica-a", "10.0.0.1"),
		readyRuntimePod("replica-b", "10.0.0.2"),
	}}
	client := fakeRuntimeModelClient{
		models: map[string][]string{"replica-a": {"base", "lora-a"}},
		errs:   map[string]error{"replica-b": errors.New("timeout")},
	}

	got := ObserveRuntimeModels(context.Background(), client, pods)

	if got.Complete {
		t.Fatalf("partial observation must not be complete: %+v", got)
	}
	if got.Replicas != 2 || got.FailedReplicas != 1 {
		t.Fatalf("replica counts = %d total, %d failed; want 2, 1", got.Replicas, got.FailedReplicas)
	}
	if len(got.Failures) != 1 || got.Failures[0] != "timeout" {
		t.Fatalf("failure diagnostics = %v, want [timeout]", got.Failures)
	}
	if !got.ObservedAt.IsZero() {
		t.Fatal("partial observation must not advance ObservedAt")
	}
}

func TestDecorateStatusFoldsRuntimeStateWithoutChurn(t *testing.T) {
	firstObservation := metav1.NewTime(time.Unix(100, 0))
	status := aimv1alpha1.AIMServiceStatus{
		Adapters: []aimv1alpha1.AIMServiceAdapterStatus{{
			Name:        "lora-a",
			AdapterPath: "lora-a",
			ModelID:     "org/lora-a",
			State:       aimv1alpha1.AdapterStateDownloaded,
		}},
	}
	state := State{
		Adapters: []Observation{{
			Name:        "lora-a",
			AdapterPath: "lora-a",
			ModelID:     "org/lora-a",
			State:       aimv1alpha1.AdapterStateDownloaded,
		}},
		Runtime: RuntimeObservation{
			Complete:       true,
			ObservedAt:     firstObservation,
			Replicas:       2,
			LoadedReplicas: map[string]int{"lora-a": 2},
		},
	}

	DecorateStatus(&status, state)
	adapter := status.Adapters[0]
	if adapter.State != aimv1alpha1.AdapterStateLoaded || adapter.LoadedReplicas != "2/2" {
		t.Fatalf("adapter status = %+v, want Loaded 2/2", adapter)
	}
	if adapter.LastObserved == nil || !adapter.LastObserved.Equal(&firstObservation) {
		t.Fatalf("LastObserved = %v, want %v", adapter.LastObserved, firstObservation)
	}

	// An identical successful poll must preserve the timestamp so the status
	// write does not hot-loop through the AIMService watch.
	state.Runtime.ObservedAt = metav1.NewTime(time.Unix(200, 0))
	DecorateStatus(&status, state)
	adapter = status.Adapters[0]
	if adapter.LastObserved == nil || !adapter.LastObserved.Equal(&firstObservation) {
		t.Fatalf("stable observation advanced LastObserved to %v", adapter.LastObserved)
	}

	// A partial poll preserves the last confirmed state.
	state.Runtime = RuntimeObservation{Replicas: 2, FailedReplicas: 1}
	DecorateStatus(&status, state)
	adapter = status.Adapters[0]
	if adapter.State != aimv1alpha1.AdapterStateLoaded || adapter.LoadedReplicas != "2/2" {
		t.Fatalf("partial poll discarded confirmed state: %+v", adapter)
	}

	// A later complete snapshot may safely report the adapter unloaded.
	unloadedObservation := metav1.NewTime(time.Unix(300, 0))
	state.Runtime = RuntimeObservation{
		Complete:       true,
		ObservedAt:     unloadedObservation,
		Replicas:       2,
		LoadedReplicas: map[string]int{},
	}
	DecorateStatus(&status, state)
	adapter = status.Adapters[0]
	if adapter.State != aimv1alpha1.AdapterStateDownloaded || adapter.LoadedReplicas != "0/2" {
		t.Fatalf("adapter status = %+v, want Downloaded 0/2", adapter)
	}
	if adapter.LastObserved == nil || !adapter.LastObserved.Equal(&unloadedObservation) {
		t.Fatalf("unload LastObserved = %v, want %v", adapter.LastObserved, unloadedObservation)
	}
}

func TestRuntimePollingEligibilityAndJitter(t *testing.T) {
	service := &aimv1alpha1.AIMService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-a",
			Namespace: "ns",
			UID:       types.UID("uid-a"),
		},
		Status: aimv1alpha1.AIMServiceStatus{
			Adapters: []aimv1alpha1.AIMServiceAdapterStatus{{
				Name:  "lora-a",
				State: aimv1alpha1.AdapterStateDownloaded,
			}},
		},
	}
	if !ShouldObserveRuntime(service) {
		t.Fatal("Downloaded status should enable runtime observation")
	}

	state := State{Adapters: []Observation{{
		Name:  "lora-a",
		State: aimv1alpha1.AdapterStateDownloaded,
	}}}
	got := RuntimePollRequeueAfter(service, state)
	if got < 25*time.Second || got > 35*time.Second {
		t.Fatalf("poll interval = %s, want deterministic jitter in [25s, 35s]", got)
	}
	if got != RuntimePollRequeueAfter(service, state) {
		t.Fatal("poll jitter must be deterministic for one service")
	}

	state.Adapters[0].State = aimv1alpha1.AdapterStateFailed
	if got := RuntimePollRequeueAfter(service, state); got != 0 {
		t.Fatalf("failed adapter poll interval = %s, want disabled", got)
	}
}
