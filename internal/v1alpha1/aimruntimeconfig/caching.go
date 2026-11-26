// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
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

package aimruntimeconfig

import (
	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// CachingEnv returns the runtime config's spec.caching.env, or nil when none
// is set. The download Jobs (model download, size check, adapter download and
// adapter subtree sync) read only this field, never the runtime config
// spec.env, so inference settings and download credentials stay apart.
func CachingEnv(config *aimv1alpha1.AIMRuntimeConfigCommon) []corev1.EnvVar {
	if config == nil || config.Caching == nil {
		return nil
	}
	return config.Caching.Env
}
