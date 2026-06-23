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

package utils

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// PullPolicyForImage mirrors kubelet's own default-policy heuristic: when the
// image reference has no tag or carries the rolling `:latest` tag, opt into
// `PullAlways` so we never reuse an arbitrarily stale cached layer on a node.
// Versioned tags (e.g. `:v0.2.2`) are immutable enough in practice to keep
// `IfNotPresent`, which avoids hitting the registry on every pod start (and lets
// pre-loaded images, e.g. `kind load`, actually be used). Digest references
// (`@sha256:...`) are also treated as immutable. This is the same rule the K8s
// controller-manager applies when ImagePullPolicy is omitted; we have to do it
// ourselves because the Job and serving-container builders set it explicitly.
func PullPolicyForImage(image string) corev1.PullPolicy {
	switch {
	case image == "":
		return corev1.PullAlways
	case strings.Contains(image, "@sha256:"):
		return corev1.PullIfNotPresent
	}
	tag := ""
	if idx := strings.LastIndex(image, ":"); idx >= 0 && !strings.Contains(image[idx:], "/") {
		tag = image[idx+1:]
	}
	if tag == "" || tag == "latest" {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}
