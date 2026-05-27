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
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// Annotation + label keys stamped on the cache ConfigMap so the operator can
// decide at fetch-time whether a cache is still valid without re-parsing the payload.
const (
	// AnnotationDiscoverySpecHash is the model's discovery spec hash at the time
	// the cache was produced. A mismatch with the current hash triggers re-discovery.
	AnnotationDiscoverySpecHash = constants.AimLabelDomain + "/discovery-spec-hash"

	// AnnotationDiscoveryCommandVersion records the in-image script contract version
	// used to produce the cache. Informational — feeds into the spec hash already.
	AnnotationDiscoveryCommandVersion = constants.AimLabelDomain + "/discovery-command-version"

	// AnnotationDiscoverySourceImage is the image reference the cache was produced against.
	AnnotationDiscoverySourceImage = constants.AimLabelDomain + "/discovery-source-image"
)

func discoveryCacheName(modelName string) (string, error) {
	return utils.GenerateDerivedName(
		[]string{modelName, "discovery"},
		utils.WithHashSource(modelName, "discovery"),
	)
}

func DiscoveryCacheName(modelName string) (string, error) {
	return discoveryCacheName(modelName)
}

func childProfileSetName(modelName string) (string, error) {
	return utils.GenerateDerivedName(
		[]string{modelName, "profiles"},
		utils.WithHashSource(modelName, "profiles"),
	)
}

func ChildProfileSetName(modelName string) (string, error) {
	return childProfileSetName(modelName)
}

// DiscoveryCacheInput is everything the cache builder needs to produce a ConfigMap.
type DiscoveryCacheInput struct {
	Name      string
	Namespace string
	ModelUID  string
	ModelName string

	// Profiles is the map of in-image relative paths -> raw YAML bytes captured
	// from the discovery Job. The builder writes each entry as a separate
	// profiles.<flattened-path>.yaml data key.
	Profiles     map[string][]byte
	OrderedPaths []string

	// Discovery-time metadata persisted in metadata.json and as annotations.
	SourceImage             string
	AimID                   string
	BaseImage               string
	SourceSpecHash          string
	DiscoveryCommandVersion string
	DiscoveredAt            time.Time
}

// BuildDiscoveryCacheConfigMap materializes the ConfigMap that stores raw profile
// YAMLs plus a metadata.json sidecar. The returned object is unowned; callers are
// responsible for setting owner references.
func BuildDiscoveryCacheConfigMap(in DiscoveryCacheInput) (*corev1.ConfigMap, error) {
	if len(in.Profiles) == 0 {
		return nil, fmt.Errorf("discovery cache has no profiles")
	}

	data := make(map[string]string, len(in.Profiles)+1)
	profileIDs := make([]string, 0, len(in.Profiles))

	paths := in.OrderedPaths
	if len(paths) == 0 {
		paths = make([]string, 0, len(in.Profiles))
		for p := range in.Profiles {
			paths = append(paths, p)
		}
	}

	for _, relpath := range paths {
		raw, ok := in.Profiles[relpath]
		if !ok {
			continue
		}
		key := aimprofile.FlattenProfilePath(relpath)
		data[key] = string(raw)
		profileIDs = append(profileIDs, profileIDFromRelPath(relpath))
	}

	discoveredAt := in.DiscoveredAt
	if discoveredAt.IsZero() {
		discoveredAt = time.Now().UTC()
	}

	metadata := aimprofile.DiscoveryCacheMetadata{
		AimID:                   in.AimID,
		SourceImage:             in.SourceImage,
		BaseImage:               in.BaseImage,
		SourceSpecHash:          in.SourceSpecHash,
		DiscoveryCommandVersion: in.DiscoveryCommandVersion,
		DiscoveredAt:            discoveredAt.Format(time.RFC3339),
		ProfileIDs:              profileIDs,
	}
	metadataRaw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal discovery cache metadata: %w", err)
	}
	data[aimprofile.DiscoveryCacheMetadataKey] = string(metadataRaw)

	configMap := &corev1.ConfigMap{}
	configMap.Namespace = in.Namespace
	configMap.Name = in.Name
	configMap.Labels = map[string]string{
		constants.LabelK8sManagedBy: constants.LabelValueManagedBy,
	}
	configMap.Annotations = map[string]string{
		annotationModelUID:                in.ModelUID,
		annotationModelName:               in.ModelName,
		annotationModelNamespace:          in.Namespace,
		AnnotationDiscoverySpecHash:       in.SourceSpecHash,
		AnnotationDiscoveryCommandVersion: in.DiscoveryCommandVersion,
		AnnotationDiscoverySourceImage:    in.SourceImage,
	}
	configMap.Data = data
	return configMap, nil
}

// CacheHasMatchingSpecHash reports whether the given cache was produced with the
// supplied spec hash (i.e. is still valid for the current model spec).
func CacheHasMatchingSpecHash(cm *corev1.ConfigMap, specHash string) bool {
	if cm == nil || specHash == "" {
		return false
	}
	return cm.Annotations[AnnotationDiscoverySpecHash] == specHash
}

// CacheOwnedByModel reports whether the cache was created by the operator on
// behalf of this specific AIMModel / AIMClusterModel (same UID annotation).
func CacheOwnedByModel(cm *corev1.ConfigMap, modelUID string) bool {
	if cm == nil {
		return false
	}
	return cm.Annotations[annotationModelUID] == modelUID
}

// CacheOwnerReference returns the owner reference that should be stamped on
// the cache ConfigMap so it is garbage-collected alongside the model.
func CacheOwnerReference(apiVersion, kind, name, uid string) metav1.OwnerReference {
	controller := true
	blockOwnerDeletion := true
	return metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               name,
		UID:                toUID(uid),
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
}

func toUID(s string) types.UID { return types.UID(s) }

// profileIDFromRelPath returns the file stem for use as a human-facing profile ID
// in metadata.json (e.g. "HuggingFaceTB/SmolLM2-135M/cpu.yaml" -> "cpu").
func profileIDFromRelPath(relpath string) string {
	return strings.TrimSuffix(path.Base(relpath), aimprofile.ProfilesDataKeySuffix)
}
