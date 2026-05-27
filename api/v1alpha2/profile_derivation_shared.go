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

package v1alpha2

import (
	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
)

// All profile-derivation types are hosted in v1alpha1 so that AIMModelSpec
// (also in v1alpha1, owning the v1alpha2 superset) can reference them without
// creating an import cycle. The aliases below let existing v1alpha2 callers
// keep using `aimv1alpha2.AIMProfileSetSpec` etc. while the canonical type
// definition lives in v1alpha1.

const (
	ProfileSetAimIdIndexKey     = aimv1alpha1.ProfileSetAimIdIndexKey
	ProfileSetSourceRefIndexKey = aimv1alpha1.ProfileSetSourceRefIndexKey
)

// ProfileVersionPolicy controls which matched profile versions a derivation request may copy.
type ProfileVersionPolicy = aimv1alpha1.ProfileVersionPolicy

const (
	ProfileVersionPolicyPinned = aimv1alpha1.ProfileVersionPolicyPinned
	ProfileVersionPolicyLatest = aimv1alpha1.ProfileVersionPolicyLatest
	ProfileVersionPolicyAll    = aimv1alpha1.ProfileVersionPolicyAll
)

// ProfileSourceRef identifies an alternate discovery cache source for derivation.
type ProfileSourceRef = aimv1alpha1.ProfileSourceRef

// ProfileSelector narrows the source profiles selected for derivation.
type ProfileSelector = aimv1alpha1.ProfileSelector

// ProfileOverrides mutates selected source profiles when creating derived copies.
type ProfileOverrides = aimv1alpha1.ProfileOverrides

// AIMProfileSetSpec defines the desired state of AIMProfileSet and is also reused by AIMModel.profileCopy.
type AIMProfileSetSpec = aimv1alpha1.AIMProfileSetSpec

// AIMProfileSetStatus defines the observed state of AIMProfileSet / AIMClusterProfileSet.
type AIMProfileSetStatus = aimv1alpha1.AIMProfileSetStatus

// ManagedProfileCounts summarizes managed derivative profiles.
type ManagedProfileCounts = aimv1alpha1.ManagedProfileCounts

// DiscoveryCacheReference identifies the cached discovery catalog produced from image inspection.
type DiscoveryCacheReference = aimv1alpha1.DiscoveryCacheReference

// DiscoveredProfileCounts summarizes the profiles found during image discovery.
type DiscoveredProfileCounts = aimv1alpha1.DiscoveredProfileCounts

// ProfileSetReference identifies the profile set synthesized by a model for derivation flows.
type ProfileSetReference = aimv1alpha1.ProfileSetReference

// ModelDiscoveryState tracks the Kubernetes Job that inspects an AIM image and
// writes its profile YAMLs into the discovery cache ConfigMap.
type ModelDiscoveryState = aimv1alpha1.ModelDiscoveryState
