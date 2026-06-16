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

package aimartifact

import (
	"context"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
)

// AdapterReclaimInterval is the cadence at which a model artifact with an adapter
// disk launches a reaper Job to reclaim subtrees of deleted services.
const AdapterReclaimInterval = 15 * time.Minute

// isAdapter reports whether an artifact is a LoRA adapter definition.
func isAdapter(mc *aimv1alpha1.AIMArtifact) bool {
	return mc.Spec.Type == aimv1alpha1.ArtifactTypeAdapter
}

// resolveAdapterPath returns the on-disk directory name for an adapter. The value
// is frozen on status at first resolution; the MVP defaults to metadata.name
// (namespace-unique, so collision-proof). adapterPathTemplate is not supported yet.
func resolveAdapterPath(mc *aimv1alpha1.AIMArtifact) string {
	if mc.Status.AdapterPath != "" {
		return mc.Status.AdapterPath
	}
	return mc.Name
}

// fetchAdapterState fetches the dependencies of a type=adapter artifact: the merged
// runtime config and the referenced parent model artifact. Adapters never create a
// cache PVC or download/check-size jobs.
func (r *ArtifactReconciler) fetchAdapterState(
	ctx context.Context,
	c client.Client,
	mc *aimv1alpha1.AIMArtifact,
) ArtifactFetchResult {
	runtimeConfigRef := mc.GetRuntimeConfigRef()
	result := ArtifactFetchResult{
		artifact:            mc,
		mergedRuntimeConfig: aimruntimeconfig.FetchMergedRuntimeConfig(ctx, c, runtimeConfigRef.Name, mc.Namespace),
	}

	if mc.Spec.ParentArtifact != "" {
		parent := controllerutils.Fetch(
			ctx, c,
			client.ObjectKey{Name: mc.Spec.ParentArtifact, Namespace: mc.Namespace},
			&aimv1alpha1.AIMArtifact{},
		)
		result.parentArtifact = &parent
	}

	return result
}

// composeAdapterState interprets the fetched adapter dependencies.
func composeAdapterState(fetch ArtifactFetchResult) ArtifactObservation {
	obs := ArtifactObservation{ArtifactFetchResult: fetch}
	mc := fetch.artifact
	obs.adapterPath = resolveAdapterPath(mc)

	if fetch.parentArtifact != nil && fetch.parentArtifact.OK() && fetch.parentArtifact.Value != nil {
		parent := fetch.parentArtifact.Value
		ref := aimv1alpha1.CreateResolvedReference(parent)
		obs.parentResolved = &ref
		obs.parentModelID = parent.Spec.ModelID
	}

	return obs
}

// getAdapterComponentHealth reports component health for a type=adapter artifact.
// The adapter becomes Ready once its lineage (parent model + adapter disk) is
// validated. Source bytes are validated lazily at service-stage time, so a bad
// source surfaces on the consuming service rather than here (MVP).
func (obs ArtifactObservation) getAdapterComponentHealth() []controllerutils.ComponentHealth {
	health := []controllerutils.ComponentHealth{
		obs.mergedRuntimeConfig.ToUpstreamComponentHealth("RuntimeConfig", aimruntimeconfig.GetRuntimeConfigHealth),
	}

	switch {
	case obs.parentArtifact == nil:
		// CEL requires parentArtifact on adapters; defensive only.
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusFailed,
			Reason:         aimv1alpha1.ArtifactReasonParentNotFound,
			Message:        "spec.parentArtifact is required for adapter artifacts",
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	case obs.parentArtifact.IsNotFound():
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusPending,
			Reason:         aimv1alpha1.ArtifactReasonParentNotFound,
			Message:        "parent model artifact " + obs.artifact.Spec.ParentArtifact + " not found",
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	case obs.parentArtifact.HasError():
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusPending,
			Reason:         aimv1alpha1.ArtifactReasonParentNotFound,
			Message:        "failed to read parent model artifact: " + obs.parentArtifact.Error.Error(),
			Errors:         []error{obs.parentArtifact.Error},
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	case obs.parentArtifact.Value.Spec.Type == aimv1alpha1.ArtifactTypeAdapter:
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusFailed,
			Reason:         aimv1alpha1.ArtifactReasonParentNotModel,
			Message:        "parentArtifact " + obs.artifact.Spec.ParentArtifact + " is not a model artifact",
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	case obs.parentArtifact.Value.Spec.AdapterDisk == nil:
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusPending,
			Reason:         aimv1alpha1.ArtifactReasonParentLacksAdapterDisk,
			Message:        "parent model artifact " + obs.artifact.Spec.ParentArtifact + " has no adapterDisk",
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	default:
		health = append(health, controllerutils.ComponentHealth{
			Component:      "AdapterParent",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.ArtifactReasonAdapterValidated,
			Message:        "adapter lineage validated against parent " + obs.artifact.Spec.ParentArtifact,
			DependencyType: controllerutils.DependencyTypeUpstream,
		})
	}

	return health
}

// decorateAdapterStatus sets adapter-specific status fields (path, parent lineage).
func decorateAdapterStatus(
	status *aimv1alpha1.AIMArtifactStatus,
	_ *controllerutils.ConditionManager,
	obs ArtifactObservation,
) {
	// Freeze the adapter path at first resolution.
	if status.AdapterPath == "" && obs.adapterPath != "" {
		status.AdapterPath = obs.adapterPath
	}

	if obs.parentResolved != nil {
		status.ResolvedParent = obs.parentResolved
		status.ParentModelID = obs.parentModelID
	}

	// Adapter artifacts never allocate a cache PVC; keep the field clear.
	status.PersistentVolumeClaim = ""
}

// generateAdapterReaperJobName returns the deterministic reaper Job name for a
// model artifact's adapter disk.
func generateAdapterReaperJobName(mc *aimv1alpha1.AIMArtifact) string {
	name, _ := utils.GenerateDerivedName([]string{mc.Name, "adapter-reap"}, utils.WithHashSource(mc.UID))
	return name
}

// listLiveAdapterServiceIDs returns the subtree IDs (AIMService UIDs) of services
// in the namespace that own an adapter subtree. The reaper keeps these and
// removes any other subtree it finds on the disk. A service owns a subtree
// whenever it needs the adapter disk (spec.AdaptersEnabled(): adapters declared,
// or dynamic mode — where even at zero declared adapters the subtree exists and
// is mounted read-only into a running pod). Keying on AdaptersEnabled rather than
// the adapter list length is what prevents a dynamic service that drops to zero
// from having its live mount reaped.
//
// The error is propagated (not swallowed) so the caller can tell "the list
// failed" from "no live services": an empty keep-list on the reaper means delete
// every subtree, so a transient List error must skip the reaper, not reap.
func listLiveAdapterServiceIDs(ctx context.Context, c client.Client, namespace string) ([]string, error) {
	var services aimv1alpha1.AIMServiceList
	if err := c.List(ctx, &services, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(services.Items))
	for i := range services.Items {
		svc := &services.Items[i]
		if svc.Spec.AdaptersEnabled() {
			ids = append(ids, string(svc.UID))
		}
	}
	return ids, nil
}

// buildAdapterReaperJob builds the periodic subtree-reclaim Job owned by the model
// artifact. It mounts the adapter disk RW and removes subtrees of services no
// longer present (plus crash-orphaned staging dirs).
func buildAdapterReaperJob(
	mc *aimv1alpha1.AIMArtifact,
	adapterPVC string,
	keepServiceIDs []string,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *batchv1.Job {
	image := resolveDownloadImage(mc, runtimeConfig)

	labels := map[string]string{
		constants.LabelKeyCacheName: mc.Name,
		constants.LabelKeyComponent: "adapter-reap",
	}

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateAdapterReaperJobName(mc),
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(1)),
			TTLSecondsAfterFinished: ptr.To(int32(60 * 5)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: mc.Spec.ImagePullSecrets,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To(int64(1000)),
						RunAsGroup:   ptr.To(int64(1000)),
						RunAsNonRoot: ptr.To(true),
						FSGroup:      ptr.To(int64(1000)),
					},
					Containers: []corev1.Container{
						{
							Name:            "adapter-reap",
							Image:           image,
							ImagePullPolicy: pullPolicyForImage(image),
							Command:         []string{"/adapter-reap.sh"},
							Env: []corev1.EnvVar{
								{Name: "ADAPTER_PVC_ROOT", Value: constants.AIMAdapterPVCRoot},
								{Name: "KEEP_SERVICE_IDS", Value: strings.Join(keepServiceIDs, ",")},
								// Grace guard: never reap a subtree younger than 2x the
								// reclaim interval, so a stale keep-list can't delete a
								// freshly-created subtree.
								{Name: "MIN_AGE_SECONDS", Value: strconv.Itoa(int(2 * AdapterReclaimInterval.Seconds()))},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      constants.VolumeAdapterDisk,
									MountPath: constants.AIMAdapterPVCRoot,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: constants.VolumeAdapterDisk,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: adapterPVC,
								},
							},
						},
					},
				},
			},
		},
	}
}
