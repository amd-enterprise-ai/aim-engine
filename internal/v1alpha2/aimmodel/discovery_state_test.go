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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

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
