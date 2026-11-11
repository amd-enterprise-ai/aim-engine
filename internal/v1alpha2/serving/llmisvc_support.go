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

package serving

import (
	"fmt"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
)

const (
	llmInferenceServiceResource       = "llminferenceservices"
	llmInferenceServiceConfigResource = "llminferenceserviceconfigs"
)

// LLMInferenceServiceSupport records whether AIM Engine manages KServe
// LLMInferenceService resources in this cluster. It is resolved once at
// operator startup: controller-runtime cannot watch a kind whose CRD is not
// installed, so installing the CRDs later requires an operator restart.
type LLMInferenceServiceSupport struct {
	// Enabled is true when the operator was started with LLMInferenceService
	// support and the API server serves both the serving.kserve.io/v1alpha2
	// LLMInferenceService and LLMInferenceServiceConfig kinds.
	Enabled bool

	// KServeNamespace is the namespace KServe's LLMInferenceService controller
	// falls back to when resolving spec.baseRefs that do not exist in the
	// service's own namespace. AIMClusterProfiles project their
	// LLMInferenceServiceConfigs there so any namespace can reference them.
	KServeNamespace string
}

// DetectLLMInferenceServiceAPI reports whether the API server serves both the
// LLMInferenceService and LLMInferenceServiceConfig kinds at
// serving.kserve.io/v1alpha2. A missing group version is a confident "not
// installed" (false, nil); any other discovery failure is returned so the
// caller does not mistake a transient API error for an absent CRD.
func DetectLLMInferenceServiceAPI(client discovery.DiscoveryInterface) (bool, error) {
	groupVersion := kservev1alpha2.SchemeGroupVersion.String()
	resources, err := client.ServerResourcesForGroupVersion(groupVersion)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("discover %s resources: %w", groupVersion, err)
	}

	served := map[string]bool{}
	for _, resource := range resources.APIResources {
		served[resource.Name] = true
	}
	return served[llmInferenceServiceResource] && served[llmInferenceServiceConfigResource], nil
}
