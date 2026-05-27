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

package aimservice

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofile"
)

// profileResolutionShape enumerates the ADR 0006b authoring shapes the
// AIMService resolver normalises onto a single internal funnel. The
// image-shape variant (spec.model.image) was added after the original
// ADR to support the v1alpha2-first quick-start docs; it desugars to the
// model-selector shape once an AIMModel with the requested image is
// located (or auto-created).
type profileResolutionShape string

const (
	resolutionShapeNone           profileResolutionShape = ""
	resolutionShapeName           profileResolutionShape = "Name"
	resolutionShapeModelOnly      profileResolutionShape = "Model"
	resolutionShapeModelSelector  profileResolutionShape = "ModelAndSelector"
	resolutionShapeGlobalSelector profileResolutionShape = "Selector"
	// resolutionShapeModelImage is the v1alpha2 quick-start shape:
	// spec.model.image (and optionally spec.profile.selector). The
	// resolver looks up an existing AIMModel/AIMClusterModel with this
	// image, then desugars to ModelOnly/ModelSelector. When no match
	// exists the plan step creates a dedicated AIMModel owned by the
	// service.
	resolutionShapeModelImage profileResolutionShape = "ModelImage"
)

// profileResolution records what the resolver attempted and how the picks
// landed. The caller (FetchRemoteState) stamps `service.profile` /
// `service.clusterProfile` with the winner so the existing ComposeState path
// keeps treating those as the source of truth.
type profileResolution struct {
	shape profileResolutionShape

	// candidates captures the names considered after label + spec filtering
	// (selector path) or the single name attempted (name path). Used for
	// ambiguity diagnostics.
	candidates []candidateRef

	// ambiguous is true when more than one candidate survived ranking and
	// the resolver picked the alphabetical winner deterministically.
	ambiguous bool

	// notFoundReason carries the user-facing reason string when nothing
	// matched. Empty when a winner was selected. Used only for the
	// terminal "no profile matched the selector" case — transient infra
	// errors (List failures, RBAC) go through listErr below so the
	// component-health pipeline can light up DependenciesReachable=False
	// instead of misclassifying the issue as a user config error.
	notFoundReason  string
	notFoundMessage string

	// listErr is the wrapped error from a failed namespace or cluster
	// AIMProfile/AIMClusterProfile list, if any. The caller plumbs this
	// into the returned FetchResults' Error so the framework treats it as
	// an infrastructure dependency failure rather than ProfileNotFound.
	listErr error

	// needsAutoModel is true when the resolver picked the image shape and
	// no existing AIMModel/AIMClusterModel matches the requested
	// `spec.model.image`. The reconcile plan uses this to SSA-create a
	// dedicated v1alpha2 AIMModel owned by the AIMService; subsequent
	// reconciles re-enter via the watch fan-out once the model controller
	// has run discovery and its emitted AIMProfile becomes Ready.
	needsAutoModel bool

	// autoModelImage carries the image URI captured during shape detection
	// so the plan step doesn't have to re-derive it from spec on a path
	// where needsAutoModel may have been set independently.
	autoModelImage string
}

type candidateRef struct {
	name      string
	scope     aimv1alpha1.AIMResolutionScope
	namespace string
}

// resolveProfileCandidates runs the multi-mode resolver and returns
// populated `profile` / `clusterProfile` fetch results pointing at the
// winning candidate (or zero values when nothing matched). It also returns
// the profileResolution book-keeping used to surface diagnostics through
// the component health pipeline and event recorder.
//
// The four shapes ADR 0006b prescribes collapse onto a single funnel here:
//
//	By name:
//	  spec.profile.name → namespace fetch → cluster fetch.
//	By model only:
//	  spec.model.name desugars to selector.modelRef.name. Lists namespace
//	  AIMProfile then cluster AIMClusterProfile filtered by provenance
//	  labels + role=Deployable; ranks via SelectBestPtr.
//	By model + selector:
//	  Same as by-selector but with selector.modelRef.name desugared from
//	  spec.model.name when the user did not set selector.modelRef.
//	By selector only:
//	  CEL requires selector.aimId or selector.modelRef to be set so the
//	  watch fan-out has an O(1) index path; runtime applies the same label
//	  + spec filters and ranking pipeline.
func resolveProfileCandidates(
	ctx context.Context,
	c client.Client,
	recorder record.EventRecorder,
	service *aimv1alpha1.AIMService,
) (
	controllerutils.FetchResult[*aimv1alpha2.AIMProfile],
	controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile],
	profileResolution,
) {
	shape := resolutionShapeFor(service)
	res := profileResolution{shape: shape}

	switch shape {
	case resolutionShapeName:
		return resolveByName(ctx, c, service)
	case resolutionShapeModelOnly, resolutionShapeModelSelector, resolutionShapeGlobalSelector:
		return resolveBySelector(ctx, c, recorder, service, res)
	case resolutionShapeModelImage:
		return resolveByImage(ctx, c, recorder, service, res)
	default:
		// resolutionShapeNone: caller (CEL) rejected this combo, but if
		// we somehow land here at runtime, surface it as ProfileNotFound
		// with a clear message instead of panicking.
		res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
		res.notFoundMessage = "AIMService spec must set spec.profile.name, spec.profile.selector, spec.model.name, or spec.model.image"
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}
}

// resolutionShapeFor maps an AIMService spec to its ADR resolution shape.
// Shape detection runs ahead of any cluster I/O so the caller can short-
// circuit on `resolutionShapeNone` without burning a List.
func resolutionShapeFor(service *aimv1alpha1.AIMService) profileResolutionShape {
	if service == nil {
		return resolutionShapeNone
	}
	if service.Spec.Profile != nil && service.Spec.Profile.Name != "" {
		return resolutionShapeName
	}
	name, image := serviceModelAnchor(service)
	hasModelName := name != ""
	hasModelImage := image != ""
	hasSelector := service.Spec.Profile != nil && service.Spec.Profile.Selector != nil
	switch {
	case hasModelImage:
		// Image shape covers both the bare `spec.model.image` and
		// the `spec.model.image + spec.profile.selector` combination;
		// once we desugar to a model name the selector is composed
		// through the same composeServiceSelector path.
		return resolutionShapeModelImage
	case hasSelector && hasModelName:
		return resolutionShapeModelSelector
	case hasSelector:
		return resolutionShapeGlobalSelector
	case hasModelName:
		return resolutionShapeModelOnly
	default:
		return resolutionShapeNone
	}
}

// serviceModelAnchor returns the v1alpha2-resolver desugaring source for
// `selector.modelRef.name`. Exactly one of (name, image) is non-empty when
// the AIMService spec carries a model shortcut. Callers that only care
// about the name shape can use serviceModelName.
//
// The image shape is what makes the v1alpha2 quick-start docs work: a
// service authored with `spec.model.image` is dispatched onto the profile
// pipeline via the `aim.eai.amd.com/reconciler-pipeline: profile`
// annotation, the resolver looks up an AIMModel by spec.image, and falls
// through to the model-selector path once a match exists.
func serviceModelAnchor(service *aimv1alpha1.AIMService) (name, image string) {
	if service == nil || service.Spec.Model == nil {
		return "", ""
	}
	if service.Spec.Model.Name != nil && *service.Spec.Model.Name != "" {
		return *service.Spec.Model.Name, ""
	}
	if service.Spec.Model.Image != nil && *service.Spec.Model.Image != "" {
		return "", *service.Spec.Model.Image
	}
	return "", ""
}

// serviceModelName returns just the explicit-name component of the model
// anchor. Kept as a thin wrapper because composeServiceSelector and the
// watch fan-out only care about the named-anchor case (the image anchor
// is desugared via auto-created AIMModels before reaching the selector
// composition step).
func serviceModelName(service *aimv1alpha1.AIMService) string {
	name, _ := serviceModelAnchor(service)
	return name
}

// resolveByName implements shape 1: spec.profile.name. Namespace AIMProfile
// takes precedence, then cluster AIMClusterProfile. This mirrors the
// pre-PR-3 behaviour so the no-op case stays cheap.
func resolveByName(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
) (
	controllerutils.FetchResult[*aimv1alpha2.AIMProfile],
	controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile],
	profileResolution,
) {
	res := profileResolution{shape: resolutionShapeName}
	profileName := service.Spec.Profile.Name
	res.candidates = []candidateRef{{name: profileName, scope: aimv1alpha1.AIMResolutionScopeNamespace, namespace: service.Namespace}}

	nsResult := controllerutils.Fetch(ctx, c, client.ObjectKey{
		Namespace: service.Namespace,
		Name:      profileName,
	}, &aimv1alpha2.AIMProfile{})
	if nsResult.OK() && nsResult.Value != nil {
		return nsResult, controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}

	var clusterResult controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]
	if nsResult.IsNotFound() {
		clusterResult = controllerutils.Fetch(ctx, c, client.ObjectKey{Name: profileName}, &aimv1alpha2.AIMClusterProfile{})
		if clusterResult.OK() && clusterResult.Value != nil {
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{}, clusterResult, res
		}
	}

	if (nsResult.IsNotFound() || nsResult.Error == nil) && (clusterResult.Error == nil || clusterResult.IsNotFound()) {
		res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
		res.notFoundMessage = fmt.Sprintf("no AIMProfile or AIMClusterProfile named %q", profileName)
	}
	return nsResult, clusterResult, res
}

// resolveByImage implements the v1alpha2 image shape: spec.model.image
// (with or without spec.profile.selector). It looks up existing
// AIMModel / AIMClusterModel resources whose `.spec.image` matches the
// requested image, then desugars to a model-ref selector and falls
// through to resolveBySelector so ranking + scope precedence stay
// identical to the by-name path.
//
// Three terminal outcomes:
//   - Exactly one matching model: desugar `selector.modelRef = {name,
//     scope}` from the match and call resolveBySelector. The caller sees
//     the same FetchResults it would see for spec.model.name.
//   - Zero matches: stamp `resolution.needsAutoModel=true` and the image
//     so the plan step SSA-creates a dedicated v1alpha2 AIMModel owned by
//     the AIMService. Surfaces ProfileNotFound with a "creating model;
//     waiting for discovery" message so users see the progress.
//   - Multiple matches: ProfileNotFound with a clear "pin via
//     spec.model.name" message. This mirrors the v1alpha1
//     ErrMultipleModelsFound shape — we never guess which of N models
//     the user intended.
//
// Namespace AIMModel wins over AIMClusterModel of the same image (mirrors
// the namespace-over-cluster precedence used everywhere else in the
// engine). A namespace+cluster pair with the same image is treated as the
// namespace match, not as an ambiguity.
func resolveByImage(
	ctx context.Context,
	c client.Client,
	recorder record.EventRecorder,
	service *aimv1alpha1.AIMService,
	res profileResolution,
) (
	controllerutils.FetchResult[*aimv1alpha2.AIMProfile],
	controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile],
	profileResolution,
) {
	logger := log.FromContext(ctx).WithName("resolver").WithValues(
		"service", service.Name, "namespace", service.Namespace, "shape", string(res.shape))

	_, image := serviceModelAnchor(service)
	res.autoModelImage = image

	matches, listErr := findModelsByImage(ctx, c, service.Namespace, image)
	if listErr != nil {
		// Treat List failures as infrastructure: surface via
		// FetchResult.Error so DependenciesReachable=False lights up
		// instead of misclassifying as ProfileNotFound.
		res.listErr = listErr
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Error: listErr},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}

	switch len(matches) {
	case 0:
		// No model yet — signal upward so the plan step creates a
		// dedicated v1alpha2 AIMModel for this service. Reuse
		// ProfileNotFound so the framework's terminal-user-config
		// reason aggregator surfaces a clear status; the message
		// distinguishes this from a stale `spec.model.name`.
		res.needsAutoModel = true
		res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
		res.notFoundMessage = fmt.Sprintf(
			"no AIMModel matches image %q; auto-creating a dedicated AIMModel for this service and waiting for discovery",
			image,
		)
		logger.V(1).Info("no AIMModel matches image; signalling auto-create",
			"image", image)
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	case 1:
		// Single match: desugar `selector.modelRef` and fall through
		// to the standard selector pipeline so ranking, namespace-
		// over-cluster precedence, and ambiguity-event emission stay
		// identical to the by-name shape. Deep-copying the AIMService
		// guarantees the in-memory spec mutation cannot leak back into
		// the caller (which still uses the original service for
		// status updates, watches, etc.) and that any user-supplied
		// selector overlay survives untouched.
		picked := matches[0]
		desugared := service.DeepCopy()
		desugared.Spec.Model = &aimv1alpha1.AIMServiceModel{
			Name: &picked.Name,
		}
		nsRes, clusterRes, finalRes := resolveBySelector(ctx, c, recorder, desugared, res)
		// Preserve the original shape (ModelImage) for diagnostics
		// while letting the rest of the resolution carry the
		// downstream-determined candidates / ambiguity flag.
		finalRes.shape = resolutionShapeModelImage
		return nsRes, clusterRes, finalRes
	default:
		// Multiple matches: don't guess. Mirror the v1alpha1
		// ErrMultipleModelsFound posture and tell the user how to
		// disambiguate (author the spec.model.name explicitly).
		names := make([]string, len(matches))
		for i, m := range matches {
			if m.Scope == aimv1alpha1.AIMResolutionScopeCluster {
				names[i] = fmt.Sprintf("%s (cluster)", m.Name)
			} else {
				names[i] = fmt.Sprintf("%s/%s (namespace)", service.Namespace, m.Name)
			}
		}
		res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
		res.notFoundMessage = fmt.Sprintf(
			"multiple AIMModels reference image %q (%s); pin one via spec.model.name to disambiguate",
			image, strings.Join(names, ", "),
		)
		logger.V(1).Info("multiple AIMModels match image; user must disambiguate",
			"image", image, "matches", names)
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}
}

// imageModelMatch is a thin tuple for findModelsByImage results so the
// caller can render scope-aware diagnostics on multi-match errors without
// re-fetching the underlying objects.
type imageModelMatch struct {
	Name  string
	Scope aimv1alpha1.AIMResolutionScope
}

// findModelsByImage lists AIMModel (in `namespace`) and AIMClusterModel
// (cluster-wide) resources whose `.spec.image` equals `image`. Uses the
// field indexers registered by the v1alpha2 model controllers (see
// internal/v1alpha2/controller/aimmodel_controller.go and
// internal/v1alpha2/controller/aimclustermodel_controller.go).
//
// When both a namespace and a cluster model have the same image, only the
// namespace match is returned — namespace-over-cluster precedence is the
// rule the rest of the engine uses, and treating it as an ambiguity here
// would force users to delete cluster models they may not control.
func findModelsByImage(
	ctx context.Context,
	c client.Client,
	namespace, image string,
) ([]imageModelMatch, error) {
	if image == "" {
		return nil, nil
	}

	var (
		out         []imageModelMatch
		nsMatched   bool
		clusterList aimv1alpha2.AIMClusterModelList
		nsList      aimv1alpha2.AIMModelList
	)

	if namespace != "" {
		if err := c.List(ctx, &nsList,
			client.InNamespace(namespace),
			client.MatchingFields{aimv1alpha1.ModelImageIndexKey: image},
		); err != nil {
			return nil, fmt.Errorf("list namespace AIMModels by image: %w", err)
		}
		for i := range nsList.Items {
			out = append(out, imageModelMatch{
				Name:  nsList.Items[i].Name,
				Scope: aimv1alpha1.AIMResolutionScopeNamespace,
			})
			nsMatched = true
		}
	}

	// Skip the cluster list when a namespace model already matched —
	// namespace precedence makes the cluster fallback irrelevant. This
	// also prevents the namespace + cluster pair from looking like an
	// ambiguity to the caller.
	if nsMatched {
		return out, nil
	}

	if err := c.List(ctx, &clusterList,
		client.MatchingFields{aimv1alpha1.ClusterModelImageIndexKey: image},
	); err != nil {
		return nil, fmt.Errorf("list AIMClusterModels by image: %w", err)
	}
	for i := range clusterList.Items {
		out = append(out, imageModelMatch{
			Name:  clusterList.Items[i].Name,
			Scope: aimv1alpha1.AIMResolutionScopeCluster,
		})
	}
	return out, nil
}

// resolveBySelector implements shapes 2-4. It composes the selector from
// spec.profile.selector (or an empty selector for shape 2), desugars
// spec.model.name into selector.modelRef.name, forces role=Deployable, and
// runs the same label + spec filter / SelectBestPtr ranking pipeline used
// by AIMProfileSet so the two surfaces stay in lock-step.
func resolveBySelector(
	ctx context.Context,
	c client.Client,
	recorder record.EventRecorder,
	service *aimv1alpha1.AIMService,
	res profileResolution,
) (
	controllerutils.FetchResult[*aimv1alpha2.AIMProfile],
	controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile],
	profileResolution,
) {
	logger := log.FromContext(ctx).WithName("resolver").WithValues(
		"service", service.Name, "namespace", service.Namespace, "shape", string(res.shape))
	selector := composeServiceSelector(service)

	provenance, scope, err := aimprofile.ProvenanceLabelSelector(selector)
	if err != nil {
		res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
		res.notFoundMessage = fmt.Sprintf("compile provenance selector: %v", err)
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}

	// Sticky binding short-circuit. The default binding model is
	// sticky-once-bound: once status.resolvedProfile is set, keep
	// honouring that profile as long as it still exists and still
	// matches the current selector. This protects running services from
	// silently switching profiles when an unrelated AIMModel lands in
	// the same namespace and contributes a higher-ranked candidate.
	//
	// The user opts out for one reconcile by setting the
	// constants.AnnotationForceRebind annotation to any non-empty value,
	// or permanently by leaving the annotation in place.
	keepNS, keepCluster, sticky := evaluateStickyBinding(ctx, c, service, selector)
	if sticky.honored {
		switch {
		case keepNS != nil:
			res.candidates = []candidateRef{{
				name: keepNS.Name, scope: aimv1alpha1.AIMResolutionScopeNamespace, namespace: keepNS.Namespace,
			}}
			logger.V(1).Info("honouring sticky profile binding",
				"profile", keepNS.Name, "scope", "Namespace")
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: keepNS},
				controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
		case keepCluster != nil:
			res.candidates = []candidateRef{{
				name: keepCluster.Name, scope: aimv1alpha1.AIMResolutionScopeCluster,
			}}
			logger.V(1).Info("honouring sticky profile binding",
				"profile", keepCluster.Name, "scope", "Cluster")
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
				controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: keepCluster}, res
		}
	}

	var (
		nsCandidates      []aimv1alpha2.AIMProfile
		clusterCandidates []aimv1alpha2.AIMClusterProfile
	)
	if scope != aimprofile.SelectorScopeCluster {
		var list aimv1alpha2.AIMProfileList
		opts := []client.ListOption{client.InNamespace(service.Namespace)}
		if selector.AimId != "" {
			opts = append(opts, client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: selector.AimId})
		}
		if !provenance.Empty() {
			opts = append(opts, client.MatchingLabelsSelector{Selector: provenance})
		}
		if err := c.List(ctx, &list, opts...); err != nil {
			// Treat as infrastructure failure, not ProfileNotFound:
			// the API/RBAC outage is transient and the framework's
			// DependenciesReachable=False path handles retry +
			// reporting better than a terminal user-config reason.
			res.listErr = fmt.Errorf("list namespace AIMProfiles: %w", err)
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Error: res.listErr},
				controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
		}
		nsCandidates = filterNamespaceProfilesBySpec(list.Items, selector)
	}
	if scope != aimprofile.SelectorScopeNamespace {
		var list aimv1alpha2.AIMClusterProfileList
		opts := []client.ListOption{}
		if selector.AimId != "" {
			opts = append(opts, client.MatchingFields{aimv1alpha2.ProfileAimIdIndexKey: selector.AimId})
		}
		if !provenance.Empty() {
			opts = append(opts, client.MatchingLabelsSelector{Selector: provenance})
		}
		if err := c.List(ctx, &list, opts...); err != nil {
			res.listErr = fmt.Errorf("list cluster AIMClusterProfiles: %w", err)
			return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
				controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Error: res.listErr}, res
		}
		clusterCandidates = filterClusterProfilesBySpec(list.Items, selector)
	}

	// Namespace candidates win over cluster candidates: if both sets are
	// non-empty, pick the best namespace profile. This mirrors the rest of
	// the engine (AIMProfileSet, runtimeconfig merge, model resolution) and
	// keeps namespace overrides effective.
	if len(nsCandidates) > 0 {
		sortNamespaceCandidates(nsCandidates)
		winner := bestNamespaceCandidate(nsCandidates)
		res.candidates = candidateRefsFromNamespace(nsCandidates)
		// Ambiguity here means "the ranker had to coin-flip on
		// alphabetical name to pick a winner", not just "more than
		// one profile matched the selector". When the ranker breaks
		// the tie cleanly (e.g. metric=latency beats
		// metric=throughput), the warning would be noise — the user
		// has no reason to narrow their selector.
		tied := namespaceCandidatesTiedWith(winner, nsCandidates)
		if len(tied) > 1 {
			res.ambiguous = true
			// Pass the actual SelectBestPtr winner's name (not
			// candidates[0]) into the event so users see the
			// profile we are about to use; status-based ranking
			// can promote a later candidate above the alphabetical
			// first.
			maybeEmitAmbiguous(recorder, service, logger, winner.Name, candidateRefsFromNamespace(tied))
		}
		maybeEmitRebound(recorder, service, logger, sticky, winner.Name)
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{Value: winner},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
	}
	if len(clusterCandidates) > 0 {
		sortClusterCandidates(clusterCandidates)
		winner := bestClusterCandidate(clusterCandidates)
		res.candidates = candidateRefsFromCluster(clusterCandidates)
		tied := clusterCandidatesTiedWith(winner, clusterCandidates)
		if len(tied) > 1 {
			res.ambiguous = true
			maybeEmitAmbiguous(recorder, service, logger, winner.Name, candidateRefsFromCluster(tied))
		}
		maybeEmitRebound(recorder, service, logger, sticky, winner.Name)
		return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
			controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{Value: winner}, res
	}

	res.notFoundReason = aimv1alpha1.AIMServiceReasonProfileNotFound
	res.notFoundMessage = "no AIMProfile or AIMClusterProfile matched the selector"
	return controllerutils.FetchResult[*aimv1alpha2.AIMProfile]{},
		controllerutils.FetchResult[*aimv1alpha2.AIMClusterProfile]{}, res
}

// composeServiceSelector builds the effective ProfileSelector the resolver
// runs filtering against. It applies the two iteration-1 invariants:
//   - selector.role is forced to Deployable (CEL forbids users from setting
//     it; we still stamp here as a defence in depth).
//   - spec.model.name desugars to selector.modelRef.name when the user did
//     not explicitly author a modelRef. Scope defaults to Auto so the
//     resolver tries namespace then cluster, just like AIMProfileSet.
func composeServiceSelector(service *aimv1alpha1.AIMService) aimv1alpha1.ProfileSelector {
	var selector aimv1alpha1.ProfileSelector
	if service.Spec.Profile != nil && service.Spec.Profile.Selector != nil {
		selector = *service.Spec.Profile.Selector.DeepCopy()
	}
	selector.Role = aimv1alpha1.ProfileSelectorRoleDeployable
	if modelName := serviceModelName(service); modelName != "" && selector.ModelRef == nil {
		selector.ModelRef = &aimv1alpha1.ProfileSelectorModelRef{
			Name:  modelName,
			Scope: aimv1alpha1.ProfileSelectorScopeAuto,
		}
	}
	return selector
}

// filterNamespaceProfilesBySpec applies the spec-side selector filters
// (aimId, precision, acceleratorModel, ...) to a label-filtered list and
// drops manual-selection-only and overlay profiles. The label filter (role,
// source-model[-scope]) has already been applied at List time via the
// provenance label selector.
//
// A user can explicitly target a profile by spec.profile.name even when
// manualSelectionOnly is true; the selector path implicitly excludes them
// because they should not be auto-selected.
//
// All overlays are excluded regardless of owning service: they are PRIVATE
// to the AIMService that owns them and reached only through the dedicated
// overlay lookup path. Excluding self-owned overlays too is what prevents
// recursive self-selection on selector-driven services that also set
// spec.profileOverrides.
func filterNamespaceProfilesBySpec(
	profiles []aimv1alpha2.AIMProfile,
	selector aimv1alpha1.ProfileSelector,
) []aimv1alpha2.AIMProfile {
	out := make([]aimv1alpha2.AIMProfile, 0, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		if p.Spec.ManualSelectionOnly {
			continue
		}
		if _, isOverlay := p.Annotations[AnnotationOverlayService]; isOverlay {
			continue
		}
		candidate := aimprofile.ProfileCopyCandidate{
			Name:   p.Name,
			Spec:   p.Spec.AIMProfileSpecCommon,
			Status: p.Status,
		}
		ok, err := aimprofile.MatchesProfileCopySelector(candidate, selector)
		if err != nil || !ok {
			continue
		}
		out = append(out, *p)
	}
	return out
}

func filterClusterProfilesBySpec(
	profiles []aimv1alpha2.AIMClusterProfile,
	selector aimv1alpha1.ProfileSelector,
) []aimv1alpha2.AIMClusterProfile {
	out := make([]aimv1alpha2.AIMClusterProfile, 0, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		if p.Spec.ManualSelectionOnly {
			continue
		}
		candidate := aimprofile.ProfileCopyCandidate{
			Name:   p.Name,
			Spec:   p.Spec.AIMProfileSpecCommon,
			Status: p.Status,
		}
		ok, err := aimprofile.MatchesProfileCopySelector(candidate, selector)
		if err != nil || !ok {
			continue
		}
		out = append(out, *p)
	}
	return out
}

// sortNamespaceCandidates orders profiles so utils.SelectBestPtr produces a
// stable ranking on ties (status > primary > type hierarchy > version), and
// alphabetical name is the final tie-break for the deterministic-winner
// guarantee.
func sortNamespaceCandidates(profiles []aimv1alpha2.AIMProfile) {
	sort.SliceStable(profiles, func(i, j int) bool {
		return profileLess(&profiles[i].Spec.AIMProfileSpecCommon, &profiles[j].Spec.AIMProfileSpecCommon,
			profiles[i].Status.Version, profiles[j].Status.Version,
			profiles[i].Name, profiles[j].Name)
	})
}

func sortClusterCandidates(profiles []aimv1alpha2.AIMClusterProfile) {
	sort.SliceStable(profiles, func(i, j int) bool {
		return profileLess(&profiles[i].Spec.AIMProfileSpecCommon, &profiles[j].Spec.AIMProfileSpecCommon,
			profiles[i].Status.Version, profiles[j].Status.Version,
			profiles[i].Name, profiles[j].Name)
	})
}

// profileLess returns true when a should rank ahead of b. Used by the
// stable sort that runs before SelectBestPtr so the rank order matches
// the AIMProfileSet ranking semantics and the alphabetical-name
// tie-break.
//
// Tier order (lower-numbered tier wins; higher tiers only fire on a
// tie at every earlier tier):
//  1. Primary           — `true` beats `false`. Image-author / OCI
//     recommendedDeployments fallback intent dominates.
//  2. Type              — optimized > general > preview > unoptimized.
//  3. AcceleratorModel  — well-known order from
//     utils.AcceleratorModelPreferenceOrder (MI325X > MI300X > ... ,
//     R9700 > W7900). Unknown / empty ties at the bottom.
//  4. Metric            — latency > throughput. Mirrors v1alpha1's
//     long-standing default; opinionated but stable.
//  5. Precision         — fp4 > int4 > fp8 > int8 > fp16 > bf16 > fp32.
//     Smaller bit-width preferred for performance; fp > bf > int for
//     accuracy. Mirrors v1alpha1.
//  6. AcceleratorCount  — smaller count wins. Resource-conservation
//     default: when tp1 and tp2 both ship and both are Ready, tp1
//     leaves more cluster capacity for other workloads. A zero count
//     means "no requirement" and ties at the bottom of this tier.
//  7. AcceleratorType   — gpu > cpu. Deep tie-break that effectively
//     never fires (a GPU and a CPU profile rarely tie at every earlier
//     tier); guards the truly-tied case where both Ready profiles ship
//     for the same model. Users wanting CPU explicitly should set
//     selector.acceleratorType=cpu.
//  8. Version           — higher semver wins; empty ties low.
//  9. Name              — alphabetical, deterministic last resort.
func profileLess(a, b *aimv1alpha2.AIMProfileSpecCommon, aVer, bVer, aName, bName string) bool {
	if cmp := profileRankCompareIgnoringName(a, b, aVer, bVer); cmp != 0 {
		return cmp < 0
	}
	return aName < bName
}

// profileRankCompareIgnoringName returns -1 / 0 / +1 reflecting whether
// a ranks ahead of, equal to, or behind b across every tier of the
// profile ranker EXCEPT the alphabetical-name last resort. A zero
// result means the two profiles are indistinguishable by the ranker on
// every meaningful dimension; only the deterministic-winner guarantee
// (alphabetical name) separates them.
//
// Callers use this to detect "true" ambiguity for the
// ProfileSelectorAmbiguous event: an event is only meaningful when the
// resolver had to coin-flip on name to break a real tie, not just
// because the selector happened to match multiple profiles that the
// ranker then separated cleanly.
func profileRankCompareIgnoringName(a, b *aimv1alpha2.AIMProfileSpecCommon, aVer, bVer string) int {
	if a.Primary != b.Primary {
		if a.Primary {
			return -1
		}
		return 1
	}
	if rank := profileTypeRank(a.Type) - profileTypeRank(b.Type); rank != 0 {
		return signOf(rank)
	}
	if rank := acceleratorModelRank(a.AcceleratorModel) - acceleratorModelRank(b.AcceleratorModel); rank != 0 {
		return signOf(rank)
	}
	if rank := metricRank(a.Metric) - metricRank(b.Metric); rank != 0 {
		return signOf(rank)
	}
	if rank := precisionRank(a.Precision) - precisionRank(b.Precision); rank != 0 {
		return signOf(rank)
	}
	if a.AcceleratorCount != b.AcceleratorCount {
		// Zero treated as "no explicit requirement"; loses to any
		// concrete count so a well-specified profile beats an
		// unspecified one on tie.
		switch {
		case a.AcceleratorCount == 0:
			return 1
		case b.AcceleratorCount == 0:
			return -1
		case a.AcceleratorCount < b.AcceleratorCount:
			return -1
		default:
			return 1
		}
	}
	if a.AcceleratorType != b.AcceleratorType {
		switch {
		case a.AcceleratorType == aimv1alpha1.AcceleratorTypeGPU:
			return -1
		case b.AcceleratorType == aimv1alpha1.AcceleratorTypeGPU:
			return 1
		}
	}
	if cmp := compareProfileVersions(aVer, bVer); cmp != 0 {
		return -cmp // compareProfileVersions returns +1 when a>b (preferred), invert for the ahead/behind convention.
	}
	return 0
}

func signOf(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// acceleratorModelRank returns the rank index of an accelerator model
// in the shared utils.AcceleratorModelPreferenceOrder list. Unknown or
// empty values return a large sentinel so they tie at the bottom and
// fall through to the next ranking tier.
func acceleratorModelRank(model string) int {
	return utils.PreferenceScore(model, acceleratorModelPrefMap)
}

func metricRank(metric aimv1alpha1.AIMMetric) int {
	return utils.PreferenceScore(string(metric), metricPrefMap)
}

func precisionRank(precision aimv1alpha1.AIMPrecision) int {
	return utils.PreferenceScore(string(precision), precisionPrefMap)
}

// Cached preference maps. Built once at package init from the shared
// lists in internal/utils so v1alpha2 picks up edits there without
// duplication.
var (
	acceleratorModelPrefMap = utils.MakePreferenceMap(utils.AcceleratorModelPreferenceOrder)
	metricPrefMap           = utils.MakePreferenceMap(utils.MetricPreferenceOrder)
	precisionPrefMap        = utils.MakePreferenceMap(utils.PrecisionPreferenceOrder)
)

func profileTypeRank(t aimv1alpha1.AIMProfileType) int {
	switch t {
	case aimv1alpha1.AIMProfileTypeOptimized:
		return 0
	case aimv1alpha1.AIMProfileTypeGeneral:
		return 1
	case aimv1alpha1.AIMProfileTypePreview:
		return 2
	case aimv1alpha1.AIMProfileTypeUnoptimized:
		return 3
	default:
		return 4
	}
}

// compareProfileVersions returns 1 if a>b, -1 if a<b, 0 on tie. Empty
// versions sort to the bottom so any usable version is preferred over the
// unknown.
func compareProfileVersions(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	default:
		if a > b {
			return 1
		}
		return -1
	}
}

func bestNamespaceCandidate(profiles []aimv1alpha2.AIMProfile) *aimv1alpha2.AIMProfile {
	return utils.SelectBestPtr(profiles, func(p *aimv1alpha2.AIMProfile) constants.AIMStatus {
		return p.Status.GetAIMStatus()
	})
}

func bestClusterCandidate(profiles []aimv1alpha2.AIMClusterProfile) *aimv1alpha2.AIMClusterProfile {
	return utils.SelectBestPtr(profiles, func(p *aimv1alpha2.AIMClusterProfile) constants.AIMStatus {
		return p.Status.GetAIMStatus()
	})
}

func candidateRefsFromNamespace(profiles []aimv1alpha2.AIMProfile) []candidateRef {
	out := make([]candidateRef, len(profiles))
	for i, p := range profiles {
		out[i] = candidateRef{name: p.Name, scope: aimv1alpha1.AIMResolutionScopeNamespace, namespace: p.Namespace}
	}
	return out
}

func candidateRefsFromCluster(profiles []aimv1alpha2.AIMClusterProfile) []candidateRef {
	out := make([]candidateRef, len(profiles))
	for i, p := range profiles {
		out[i] = candidateRef{name: p.Name, scope: aimv1alpha1.AIMResolutionScopeCluster}
	}
	return out
}

// namespaceCandidatesTiedWith returns the slice of profiles whose rank
// is indistinguishable from the winner at every ranker tier except
// the alphabetical-name tie-break. The result always contains at least
// the winner itself.
//
// Inputs are assumed to be already sorted by sortNamespaceCandidates,
// so equal-rank candidates form a contiguous prefix starting at the
// winner; we still scan defensively in case the winner came from a
// status-priority promotion inside SelectBestPtr that pushed it out of
// position 0.
func namespaceCandidatesTiedWith(winner *aimv1alpha2.AIMProfile, profiles []aimv1alpha2.AIMProfile) []aimv1alpha2.AIMProfile {
	if winner == nil {
		return nil
	}
	out := make([]aimv1alpha2.AIMProfile, 0, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		if profileRankCompareIgnoringName(&p.Spec.AIMProfileSpecCommon, &winner.Spec.AIMProfileSpecCommon,
			p.Status.Version, winner.Status.Version) == 0 {
			out = append(out, *p)
		}
	}
	return out
}

func clusterCandidatesTiedWith(winner *aimv1alpha2.AIMClusterProfile, profiles []aimv1alpha2.AIMClusterProfile) []aimv1alpha2.AIMClusterProfile {
	if winner == nil {
		return nil
	}
	out := make([]aimv1alpha2.AIMClusterProfile, 0, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		if profileRankCompareIgnoringName(&p.Spec.AIMProfileSpecCommon, &winner.Spec.AIMProfileSpecCommon,
			p.Status.Version, winner.Status.Version) == 0 {
			out = append(out, *p)
		}
	}
	return out
}

// maybeEmitAmbiguous records a Warning event when multiple candidates
// tied with the winner at every ranker tier and the resolver had to
// fall back to the alphabetical-name deterministic-winner guarantee to
// pick one. The event is purely informational so users know they can
// disambiguate by narrowing the selector or picking by name directly;
// it is intentionally NOT emitted when the ranker had a meaningful
// reason (Primary, Type, metric, precision, count, etc.) to choose
// the winner over the other matched profiles.
//
// Pass the actual winner's name so the event labels the profile we
// are about to use, even when status priority promotes a candidate
// above the alphabetically-first one. Safe on a nil recorder (covers
// tests that don't wire one in).
func maybeEmitAmbiguous(recorder record.EventRecorder, service *aimv1alpha1.AIMService, logger logr.Logger, winner string, candidates []candidateRef) {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	logger.Info("equally-ranked profiles tied for selection; picking winner by alphabetical name",
		"winner", winner, "tied", names)
	if recorder == nil || service == nil {
		return
	}
	recorder.Eventf(service, corev1.EventTypeWarning, aimv1alpha1.AIMServiceReasonProfileSelectorAmbiguous,
		"Multiple profiles tied at every ranker tier; picking %q by alphabetical name (tied: %v). Narrow the selector or set spec.profile.name to disambiguate.",
		winner, names)
}

// stickyBindingDecision summarises the outcome of evaluateStickyBinding.
// One of three end states applies on any given reconcile:
//
//  1. honored=true  → resolver short-circuits, keeps the existing binding.
//  2. honored=false, previousName!="", rebindReason!="" → fresh rank is
//     required and we know why. maybeEmitRebound uses this for the
//     ProfileRebound event message when the new winner differs from the
//     prior binding.
//  3. honored=false, previousName=="" → first-time binding; emit no
//     ProfileRebound event (the framework's ProfileResolved event covers
//     the new binding's lifecycle entry).
type stickyBindingDecision struct {
	honored      bool
	forceRebind  bool
	previousName string
	rebindReason string
}

// evaluateStickyBinding decides whether to honor the AIMService's existing
// status.resolvedProfile or fall through to a fresh rank-and-pick.
//
// Sticky-keep returns (profile, _, {honored:true}) or (_, clusterProfile,
// {honored:true}) and the caller short-circuits. Sticky-rebind returns
// (nil, nil, {honored:false, previousName, rebindReason}); the caller
// continues to the full ranker, and maybeEmitRebound surfaces the
// transition to the user when the winner ends up different from the
// previous binding.
//
// The aim.eai.amd.com/force-rebind annotation overrides stickiness for
// this reconcile: forceRebind=true is reported even if the previous
// binding would still be valid.
func evaluateStickyBinding(
	ctx context.Context,
	c client.Client,
	service *aimv1alpha1.AIMService,
	selector aimv1alpha1.ProfileSelector,
) (*aimv1alpha2.AIMProfile, *aimv1alpha2.AIMClusterProfile, stickyBindingDecision) {
	if service == nil || service.Status.ResolvedProfile == nil {
		return nil, nil, stickyBindingDecision{}
	}
	prev := service.Status.ResolvedProfile
	decision := stickyBindingDecision{previousName: prev.Name}

	if service.Annotations[constants.AnnotationForceRebind] != "" {
		decision.forceRebind = true
		decision.rebindReason = fmt.Sprintf("%s annotation present", constants.AnnotationForceRebind)
		return nil, nil, decision
	}

	switch prev.Scope {
	case aimv1alpha1.AIMResolutionScopeCluster:
		var p aimv1alpha2.AIMClusterProfile
		if err := c.Get(ctx, client.ObjectKey{Name: prev.Name}, &p); err != nil {
			if apierrors.IsNotFound(err) {
				decision.rebindReason = "previously-bound AIMClusterProfile no longer exists"
				return nil, nil, decision
			}
			// Transient infra error: prefer re-ranking fresh so a
			// stale cached binding doesn't outlive a real outage.
			decision.rebindReason = fmt.Sprintf("could not fetch previously-bound AIMClusterProfile: %v", err)
			return nil, nil, decision
		}
		if !boundClusterStillMatches(&p, selector) {
			decision.rebindReason = "previously-bound profile no longer matches the current selector"
			return nil, nil, decision
		}
		decision.honored = true
		return nil, &p, decision
	default:
		// AIMResolutionScopeNamespace (and zero-value scope, treated
		// as namespace for safety on legacy status records).
		key := client.ObjectKey{Namespace: prev.Namespace, Name: prev.Name}
		if key.Namespace == "" {
			key.Namespace = service.Namespace
		}
		var p aimv1alpha2.AIMProfile
		if err := c.Get(ctx, key, &p); err != nil {
			if apierrors.IsNotFound(err) {
				decision.rebindReason = "previously-bound AIMProfile no longer exists"
				return nil, nil, decision
			}
			decision.rebindReason = fmt.Sprintf("could not fetch previously-bound AIMProfile: %v", err)
			return nil, nil, decision
		}
		if !boundNamespaceStillMatches(&p, selector) {
			decision.rebindReason = "previously-bound profile no longer matches the current selector"
			return nil, nil, decision
		}
		decision.honored = true
		return &p, nil, decision
	}
}

// boundNamespaceStillMatches mirrors filterNamespaceProfilesBySpec for a
// single previously-bound profile: same manual-only / overlay exclusions
// and the same selector predicate. Kept as a tiny helper so the sticky
// path and the freshly-listed path can't drift on what "matches" means.
func boundNamespaceStillMatches(p *aimv1alpha2.AIMProfile, selector aimv1alpha1.ProfileSelector) bool {
	if p == nil || p.Spec.ManualSelectionOnly {
		return false
	}
	if _, isOverlay := p.Annotations[AnnotationOverlayService]; isOverlay {
		return false
	}
	candidate := aimprofile.ProfileCopyCandidate{
		Name:   p.Name,
		Spec:   p.Spec.AIMProfileSpecCommon,
		Status: p.Status,
	}
	ok, err := aimprofile.MatchesProfileCopySelector(candidate, selector)
	return err == nil && ok
}

func boundClusterStillMatches(p *aimv1alpha2.AIMClusterProfile, selector aimv1alpha1.ProfileSelector) bool {
	if p == nil || p.Spec.ManualSelectionOnly {
		return false
	}
	candidate := aimprofile.ProfileCopyCandidate{
		Name:   p.Name,
		Spec:   p.Spec.AIMProfileSpecCommon,
		Status: p.Status,
	}
	ok, err := aimprofile.MatchesProfileCopySelector(candidate, selector)
	return err == nil && ok
}

// maybeEmitRebound emits a Normal ProfileRebound event when the resolver
// landed on a different profile than the prior status.resolvedProfile.
// The event message includes the previous and new profile names plus the
// rebind trigger (selector mismatch, profile gone, force-rebind, ...) so
// operators can audit why their service's binding changed.
//
// Silent (no event) on:
//   - first-time binding (no prior binding to compare against)
//   - sticky path (caller short-circuited before this is reached)
//   - winner==previous (the ranker re-ranked but landed on the same
//     profile, e.g. when the rebind was triggered by a transient infra
//     glitch on the sticky-fetch path)
func maybeEmitRebound(
	recorder record.EventRecorder,
	service *aimv1alpha1.AIMService,
	logger logr.Logger,
	sticky stickyBindingDecision,
	winnerName string,
) {
	if sticky.previousName == "" || winnerName == "" || sticky.previousName == winnerName {
		return
	}
	reason := sticky.rebindReason
	if reason == "" {
		reason = "ranker chose a different profile"
	}
	logger.Info("rebinding AIMService to a different profile",
		"previous", sticky.previousName, "new", winnerName, "reason", reason, "forceRebind", sticky.forceRebind)
	if recorder == nil || service == nil {
		return
	}
	recorder.Eventf(service, corev1.EventTypeNormal, aimv1alpha1.AIMServiceReasonProfileRebound,
		"Profile binding changed: was %q, now %q (reason: %s)", sticky.previousName, winnerName, reason)
}
