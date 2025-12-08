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

package utils

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Kubernetes container status reasons
const (
	containerStatusReasonImagePullBackOff = "ImagePullBackOff"
	containerStatusReasonErrImagePull     = "ErrImagePull"
	containerStatusReasonImageNotFound    = "ImageNotFound"
)

// ImagePullErrorType categorizes image pull errors
type ImagePullErrorType string

const (
	ImagePullErrorAuth     ImagePullErrorType = "auth"
	ImagePullErrorNotFound ImagePullErrorType = "not-found"
	ImagePullErrorGeneric  ImagePullErrorType = "generic"
)

// ImageRegistryError wraps registry access errors with categorization
type ImageRegistryError struct {
	Type    ImagePullErrorType
	Message string
	Cause   error
}

func (e *ImageRegistryError) Error() string {
	return e.Message
}

func (e *ImageRegistryError) Unwrap() error {
	return e.Cause
}

// CategorizeRegistryError analyzes a registry error to determine its type
func CategorizeRegistryError(err error) ImagePullErrorType {
	if err == nil {
		return ImagePullErrorGeneric
	}

	errMsg := strings.ToLower(err.Error())

	// Check for authentication/authorization errors
	authIndicators := []string{
		"unauthorized",
		"authentication required",
		"authentication failed",
		"401",
		"403",
		"forbidden",
		"denied",
		"permission denied",
		"access denied",
		"credentials",
		"authentication",
	}
	for _, indicator := range authIndicators {
		if strings.Contains(errMsg, indicator) {
			return ImagePullErrorAuth
		}
	}

	// Check for not-found errors
	notFoundIndicators := []string{
		"not found",
		"404",
		"manifest unknown",
		"name unknown",
		"image not found",
		"no such",
	}
	for _, indicator := range notFoundIndicators {
		if strings.Contains(errMsg, indicator) {
			return ImagePullErrorNotFound
		}
	}

	return ImagePullErrorGeneric
}

// ImagePullError contains categorized information about an image pull failure
type ImagePullError struct {
	Type            ImagePullErrorType
	Container       string
	Reason          string // e.g., "ImagePullBackOff", "ErrImagePull"
	Message         string // Full error message from Kubernetes
	IsInitContainer bool
}

func CheckPodImagePullStatus(pod *corev1.Pod) *ImagePullError {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if err := checkContainerImagePullStatus(containerStatus, false); err != nil {
			return err
		}
	}
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		if err := checkContainerImagePullStatus(containerStatus, true); err != nil {
			return err
		}
	}
	return nil
}

func checkContainerImagePullStatus(containerStatus corev1.ContainerStatus, isInitContainer bool) *ImagePullError {
	if containerStatus.State.Waiting != nil {
		reason := containerStatus.State.Waiting.Reason
		if reason == containerStatusReasonImagePullBackOff || reason == containerStatusReasonErrImagePull {
			message := containerStatus.State.Waiting.Message
			// Create a simple error to categorize
			msgErr := &ImageRegistryError{Message: message}
			pullError := &ImagePullError{
				Type:            CategorizeRegistryError(msgErr),
				Container:       containerStatus.Name,
				Reason:          reason,
				Message:         message,
				IsInitContainer: isInitContainer,
			}
			return pullError
		}
	}
	return nil
}
