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

package aimservicetemplate

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	discoverylock "github.com/amd-enterprise-ai/aim-engine/internal/discovery/lock"
)

// CountActiveDiscoveryJobs counts the number of active (non-complete) discovery jobs
// across the whole cluster. The shared global cap applies equally to v1alpha1 template
// discovery and v1alpha2 model discovery.
func CountActiveDiscoveryJobs(ctx context.Context, c client.Client) (int, error) {
	return discoverylock.CountActiveDiscoveryJobs(ctx, c)
}

// WithDiscoveryLock acquires the shared discovery lock, runs the function, and releases.
func WithDiscoveryLock(ctx context.Context, c client.Client, timeout time.Duration, fn func() error) error {
	return discoverylock.WithDiscoveryLock(ctx, c, timeout, fn)
}

// NeedsDiscoveryLock returns true if the template might need to create a discovery job,
// meaning we should acquire the lock before running the pipeline.
func NeedsDiscoveryLock(status constants.AIMStatus, hasInlineModelSources bool) bool {
	if status == constants.AIMStatusReady {
		return false
	}
	if hasInlineModelSources {
		return false
	}
	return true
}
