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
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

// artifactWakeupState captures every artifact field a profile cache reads:
// ArtifactAdoptableBy inspects the owner references and spec, ComposeState keys off
// the aggregate status, and DecorateStatus republishes the PVC name plus the
// artifact's Ready condition. Spec fields are covered by generation. Progress
// accounting, size fields and the remaining conditions never reach the cache status.
//
// A field a cache reads but that is missing here becomes a wakeup the cache never gets,
// so extend this struct alongside anything new the reconcilers consume.
type artifactWakeupState struct {
	generation int64
	// The Delete event only fires once the artifact's finalizers clear, so this is the
	// earliest wakeup an adopter gets while it can still react.
	deleting    bool
	owners      string
	status      constants.AIMStatus
	pvc         string
	readyStatus metav1.ConditionStatus
	// The reason is republished as the cache's own condition reason. The message is not
	// compared: it tracks the reason closely enough to be redundant here, and it is the
	// field most likely to grow dynamic detail later.
	readyReason string
}

func artifactWakeupStateOf(artifact *aimv1alpha1.AIMArtifact) artifactWakeupState {
	state := artifactWakeupState{
		generation: artifact.Generation,
		deleting:   artifact.DeletionTimestamp != nil,
		status:     artifact.Status.Status,
		pvc:        artifact.Status.PersistentVolumeClaim,
	}

	ownerUIDs := make([]string, 0, len(artifact.OwnerReferences))
	for _, owner := range artifact.OwnerReferences {
		ownerUIDs = append(ownerUIDs, string(owner.UID))
	}
	slices.Sort(ownerUIDs)
	state.owners = strings.Join(ownerUIDs, "\x00")

	for _, condition := range artifact.Status.Conditions {
		if condition.Type == controllerutils.ConditionTypeReady {
			state.readyStatus = condition.Status
			state.readyReason = condition.Reason
			break
		}
	}

	return state
}

// ArtifactWakeupPredicate drops artifact updates that cannot change any cache's output.
//
// The downloader's progress monitor patches artifact status every few seconds for the
// whole download (PROGRESS_INTERVAL, 5s by default). Since one artifact is shared by
// every cache resolving the same source URI, and the cache controllers map an artifact
// event to all of them, each of those patches would otherwise list the namespace's
// caches and reconcile all of them.
//
// Comparing state rather than allow-listing transitions is deliberate: a missed wakeup
// is what stranded caches in Pending in the first place, so anything the reconcile
// consumes has to be part of the comparison. Note that this drops informer resyncs,
// whose old and new objects are identical.
//
// An object that cannot be asserted to *AIMArtifact is forwarded rather than dropped:
// the predicate cannot tell whether it matters. The map functions on the other side of
// this watch drop such objects instead, since they cannot map what they cannot inspect.
func ArtifactWakeupPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldArtifact, okOld := e.ObjectOld.(*aimv1alpha1.AIMArtifact)
			newArtifact, okNew := e.ObjectNew.(*aimv1alpha1.AIMArtifact)
			if !okOld || !okNew {
				return true
			}
			return artifactWakeupStateOf(oldArtifact) != artifactWakeupStateOf(newArtifact)
		},
	}
}
