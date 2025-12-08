/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package constants

import (
	"os"
	"sync"
)

const (
	// operatorNamespaceEnvVar is the environment variable the operator uses to determine its namespace.
	operatorNamespaceEnvVar = "AIM_SYSTEM_NAMESPACE"

	// DefaultRuntimeConfigName is the name of the default AIM runtime config
	DefaultRuntimeConfigName = "default"

	// MaxConcurrentDiscoveryJobs is the global limit for concurrent discovery jobs across all namespaces
	MaxConcurrentDiscoveryJobs = 10

	// AimLabelDomain is the base domain used for AIM-specific labels.
	AimLabelDomain = "aim.eai.amd.com"
)

// Shared condition reasons used across multiple resource types
const (
	// Image-related reasons (used by AIMModel, AIMService, AIMServiceTemplate)
	ReasonImagePullAuthFailure = "ImagePullAuthFailure"
	ReasonImageNotFound        = "ImageNotFound"
	ReasonImagePullBackOff     = "ImagePullBackOff"

	// Resource resolution/reference reasons (used by multiple types)
	ReasonNotFound = "NotFound"
	ReasonNotReady = "NotReady"
	ReasonCreating = "Creating"
	ReasonResolved = "Resolved"

	// Storage/PVC reasons (used by AIMModelCache, AIMService)
	ReasonPVCProvisioning = "PVCProvisioning"
	ReasonPVCBound        = "PVCBound"
	ReasonPVCNotBound     = "PVCNotBound"
	ReasonPVCPending      = "PVCPending"
	ReasonPVCLost         = "PVCLost"

	// Generic failure/retry reasons
	ReasonRetryBackoff = "RetryBackoff"
	ReasonFailed       = "Failed"
)

type AIMStatus string

const (
	AIMStatusPending      AIMStatus = "Pending"
	AIMStatusStarting     AIMStatus = "Starting"
	AIMStatusProgressing  AIMStatus = "Progressing"
	AIMStatusReady        AIMStatus = "Ready"
	AIMStatusRunning      AIMStatus = "Running"
	AIMStatusDegraded     AIMStatus = "Degraded"
	AIMStatusNotAvailable AIMStatus = "NotAvailable"
	AIMStatusFailed       AIMStatus = "Failed"
)

// AIMStatusPriority maps AIMStatus values to priority levels.
// Higher values indicate more desirable statuses for sorting and filtering.
var AIMStatusPriority = map[AIMStatus]int{
	AIMStatusRunning:      7,
	AIMStatusReady:        6,
	AIMStatusProgressing:  5,
	AIMStatusStarting:     4,
	AIMStatusPending:      3,
	AIMStatusDegraded:     2,
	AIMStatusNotAvailable: 1,
	AIMStatusFailed:       0,
}

func CompareAIMStatus(a AIMStatus, b AIMStatus) int {
	if AIMStatusPriority[a] > AIMStatusPriority[b] {
		return 1
	}
	return -1
}

var (
	operatorNamespaceOnce sync.Once
	operatorNamespace     string
)

// GetOperatorNamespace returns the namespace where the AIM operator runs.
// It reads the AIM_OPERATOR_NAMESPACE environment variable; if unset, it defaults to "aim-system".
func GetOperatorNamespace() string {
	operatorNamespaceOnce.Do(func() {
		if ns := os.Getenv(operatorNamespaceEnvVar); ns != "" {
			operatorNamespace = ns
			return
		}
		operatorNamespace = "aim-system"
	})
	return operatorNamespace
}

// AMD GPU node label keys
const (
	// NodeLabelAMDGPUDeviceID is the primary node label for AMD GPU device IDs (e.g., "74a1" for MI300X)
	NodeLabelAMDGPUDeviceID = "amd.com/gpu.device-id"

	// NodeLabelBetaAMDGPUDeviceID is the legacy/beta node label for AMD GPU device IDs
	NodeLabelBetaAMDGPUDeviceID = "beta.amd.com/gpu.device-id"
)
