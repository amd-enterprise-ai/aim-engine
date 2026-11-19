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

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
)

func newLeaseClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestRunWithDiscoveryLock_ReturnsPipelineOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result ctrl.Result
		err    error
	}{
		{name: "pipeline error reaches the rate limiter", err: errors.New("cannot reach the registry")},
		{name: "pipeline requeue is kept", result: ctrl.Result{RequeueAfter: 5 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := runWithDiscoveryLock(context.Background(), newLeaseClient(t), func() (ctrl.Result, error) {
				return tt.result, tt.err
			})
			if got != tt.result || !errors.Is(err, tt.err) {
				t.Fatalf("runWithDiscoveryLock() = %+v, %v; want %+v, %v", got, err, tt.result, tt.err)
			}
		})
	}
}

func TestRunWithDiscoveryLock_RequeuesWhenLeaseHeldByAnotherReplica(t *testing.T) {
	t.Parallel()

	heldLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: discoverylock.DiscoveryLockName, Namespace: constants.GetOperatorNamespace()},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("other-operator-replica"),
			RenewTime:            ptr.To(metav1.NowMicro()),
			LeaseDurationSeconds: ptr.To(int32(discoverylock.DiscoveryLockDuration.Seconds())),
		},
	}
	// Cancelled so the lock wait ends at once instead of after its 30 s timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ran := false
	got, err := runWithDiscoveryLock(ctx, newLeaseClient(t, heldLease), func() (ctrl.Result, error) {
		ran = true
		return ctrl.Result{}, nil
	})
	if err != nil || got != (ctrl.Result{RequeueAfter: 2 * time.Second}) || ran {
		t.Fatalf("runWithDiscoveryLock() = %+v, %v, ran=%v; want RequeueAfter 2s, nil error, pipeline not run", got, err, ran)
	}
}
