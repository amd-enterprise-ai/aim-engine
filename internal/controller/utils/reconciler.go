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

package controllerutils

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// degradationThreshold is the duration infrastructure errors can persist before
	// causing the component to degrade from Ready/Progressing to Degraded.
	// This grace period prevents status flapping due to transient network issues.
	degradationThreshold = 10 * time.Second

	// Condition type constants
	ConditionTypeDependenciesReachable = "DependenciesReachable"
	ConditionTypeAuthValid             = "AuthValid"
	ConditionTypeConfigValid           = "ConfigValid"
	ConditionTypeReady                 = "Ready"

	// Component condition suffix (e.g., "ModelReady", "TemplateReady")
	ComponentConditionSuffix = "Ready"

	// DependenciesReachable condition reasons
	ReasonDependenciesReachable     = "Reachable"
	ReasonDependenciesNotReachable  = "InfrastructureError"
	MessageDependenciesReachable    = "All dependencies are reachable"
	MessageDependenciesNotReachable = "Cannot reach dependencies"

	// AuthValid condition reasons
	ReasonAuthError  = "AuthError"
	ReasonAuthValid  = "AuthenticationValid"
	MessageAuthError = "Authentication or authorization failure"
	MessageAuthValid = "Authentication and authorization successful"

	// ConfigValid condition reasons
	ReasonInvalidSpec  = "InvalidSpec"
	ReasonMissingRef   = "ReferenceNotFound"
	ReasonConfigValid  = "ConfigurationValid"
	MessageInvalidSpec = "Configuration validation failed"
	MessageMissingRef  = "Referenced resource not found"
	MessageConfigValid = "Configuration is valid"

	// Ready condition reasons
	ReasonAllComponentsReady  = "AllComponentsReady"
	ReasonComponentsNotReady  = "ComponentsNotReady"
	ReasonProgressing         = "Progressing"
	MessageAllComponentsReady = "All components are ready"
	MessageComponentsNotReady = "Some components are not ready"
	MessageProgressing        = "Waiting for components to become ready"
	MessageInfraError         = "Infrastructure error - waiting for retry"
)

// PlanResult contains the desired state changes from the PlanResources phase.
type PlanResult struct {
	// toApply are objects to create or update via Server-Side Apply with owner references.
	// This is the default and most common case - owned resources will be garbage collected
	// when the owner is deleted.
	toApply []client.Object

	// toApplyWithoutOwnerRef are objects to create or update via Server-Side Apply without owner references.
	// Use this for shared resources or resources that should outlive the owner.
	toApplyWithoutOwnerRef []client.Object

	// toDelete are objects to delete
	toDelete []client.Object
}

// Apply adds an object to be applied with an owner reference (default behavior).
// The object will be garbage collected when the owner is deleted.
func (pr *PlanResult) Apply(obj client.Object) {
	pr.toApply = append(pr.toApply, obj)
}

// ApplyWithoutOwnerRef adds an object to be applied without an owner reference.
// Use this for shared resources or resources that should outlive the owner.
func (pr *PlanResult) ApplyWithoutOwnerRef(obj client.Object) {
	pr.toApplyWithoutOwnerRef = append(pr.toApplyWithoutOwnerRef, obj)
}

// Delete adds an object to be deleted
func (pr *PlanResult) Delete(obj client.Object) {
	pr.toDelete = append(pr.toDelete, obj)
}

// StateEngineDecision contains the state engine's analysis and reconciliation directives.
type StateEngineDecision struct {
	// ShouldApply is false if ConfigValid/AuthValid/DependenciesReachable is False
	ShouldApply bool

	// ShouldRequeue is true if infrastructure errors are present (triggers exponential backoff)
	ShouldRequeue bool

	// RequeueError is the error to return for controller-runtime requeue
	RequeueError error
}

// DomainReconciler is implemented by domain-specific logic for a CRD.
type DomainReconciler[T ObjectWithStatus[S], S StatusWithConditions, F any, Obs any] interface {
	// FetchRemoteState hits the API via client and returns the fetched objects.
	// Errors are captured in FetchResult types, not returned - this ensures ComposeState always runs.
	FetchRemoteState(ctx context.Context, c client.Client, reconcileCtx ReconcileContext[T]) F

	// ComposeState interprets the fetched objects into a meaningful observation.
	// All errors should be reflected in ComponentHealth via the observation, not returned.
	ComposeState(ctx context.Context, reconcileCtx ReconcileContext[T], fetched F) Obs

	// PlanResources must be pure: no client calls, just derive desired state changes based on the object + observed state.
	// Returns PlanResult with objects to apply/delete. Errors during planning should be rare (programming errors).
	PlanResources(ctx context.Context, reconcileCtx ReconcileContext[T], obs Obs) PlanResult
}

// StatusDecorator lets a reconciler *extend* status, but not replace it.
type StatusDecorator[T ObjectWithStatus[S], S StatusWithConditions, Obs any] interface {
	// DecorateStatus can set domain-specific status fields and optional conditions. It can be used to extend
	// and override the status and conditions that are set by the StateEngine.
	DecorateStatus(status S, cm *ConditionManager, obs Obs)
}

// ManualStatusController takes full ownership of status & conditions.
// When implemented, the StateEngine is NOT called.
type ManualStatusController[T ObjectWithStatus[S], S StatusWithConditions, Obs any] interface {
	// SetStatus mutates obj.Status via the ConditionManager based on observations.
	// Component health and errors are available via obs (if it implements ComponentHealthProvider).
	SetStatus(status S, cm *ConditionManager, obs Obs)
}

// Pipeline wires a domain reconciler with controller-runtime utilities.
type Pipeline[T ObjectWithStatus[S], S StatusWithConditions, F any, Obs any] struct {
	Client         client.Client
	StatusClient   client.StatusWriter // usually mgr.GetClient().Status()
	Recorder       record.EventRecorder
	Reconciler     DomainReconciler[T, S, F, Obs]
	Scheme         *runtime.Scheme
	ControllerName string
	Clientset      kubernetes.Interface // Optional: for health inspectors that need additional K8s API access
}

// GetKubernetesName returns the Kubernetes controller name (used in SetupWithManager's .Named()).
// Example: "model" -> "model-controller"
func (p *Pipeline[T, S, F, Obs]) GetKubernetesName() string {
	return p.ControllerName + "-controller"
}

// GetFullName returns the full AIM controller identifier (used for app.kubernetes.io/managed-by label).
// Example: "model" -> "aim-model-controller"
func (p *Pipeline[T, S, F, Obs]) GetFullName() string {
	return "aim-" + p.ControllerName + "-controller"
}

type ReconcileContext[T client.Object] struct {
	Object              T
	MergedRuntimeConfig FetchResult[*aimv1alpha1.AIMRuntimeConfigCommon]
}

// Run executes the standard Fetch → Compose → Plan → StateEngine → Apply → Events → Status flow.
// It does NOT handle:
// - fetching the object from the API
// - deletion / finalizers
// Those remain in the controller's Reconcile.
func (p *Pipeline[T, S, F, Obs]) Run(ctx context.Context, obj T) error {
	// 1) Get current status pointer (will be mutated)
	status := obj.GetStatus() // S, e.g. *AIMServiceStatus

	reconcileCtx := ReconcileContext[T]{
		Object: obj,
	}

	name := DefaultRuntimeConfigName
	if r, ok := any(obj).(RuntimeConfigRefProvider); ok {
		if ref := r.GetRuntimeConfigRef(); ref.Name != "" {
			name = ref.Name
		}
	}
	reconcileCtx.MergedRuntimeConfig = FetchMergedRuntimeConfig(ctx, p.Client, name, obj.GetNamespace())

	// 2) Deep copy the entire object to capture old status for comparison
	oldObj, ok := obj.DeepCopyObject().(T)
	if !ok {
		return fmt.Errorf("DeepCopyObject returned unexpected type, expected %T", obj)
	}
	oldStatus := oldObj.GetStatus()
	oldConditions := append([]metav1.Condition(nil), status.GetConditions()...)

	// Condition manager from existing conditions
	cm := NewConditionManager(oldConditions)

	// === Phase 1: FetchRemoteState ===
	// Get all resources needed for observation. Errors are captured in FetchResult types.
	fetched := p.Reconciler.FetchRemoteState(ctx, p.Client, reconcileCtx)

	// === Phase 2: ComposeState ===
	// Interpret fetched resources into domain observations.
	// All errors (semantic and infrastructure) are reflected in ComponentHealth via observations.
	obs := p.Reconciler.ComposeState(ctx, reconcileCtx, fetched)

	// === Phase 3: PlanResources ===
	// Derive desired state changes based on observations (pure function, no client calls).
	planResult := p.Reconciler.PlanResources(ctx, reconcileCtx, obs)

	// === Phase 4: StateEngine ===
	// Analyze component health, categorize errors, set conditions, and decide reconciliation behavior.
	decision, stateErr := p.processStateEngine(ctx, obs, cm, status)
	if stateErr != nil {
		// State engine itself failed (programming error) - return immediately
		return fmt.Errorf("state engine failed: %w", stateErr)
	}

	// === Phase 5: Delete ===
	// Delete objects before applying new state (only if decision allows apply).
	// Aggregate errors to avoid silent failures.
	var deleteErrs []error
	if decision.ShouldApply && len(planResult.toDelete) > 0 {
		for _, objToDelete := range planResult.toDelete {
			if err := p.Client.Delete(ctx, objToDelete); client.IgnoreNotFound(err) != nil {
				gvk := objToDelete.GetObjectKind().GroupVersionKind()
				key := client.ObjectKeyFromObject(objToDelete)
				deleteErrs = append(deleteErrs, fmt.Errorf("delete failed for %s %s/%s: %w", gvk.Kind, key.Namespace, key.Name, err))
			}
		}
	}

	// === Phase 6: Apply ===
	// Use Server-Side Apply to create/update desired objects (only if decision allows).
	var applyErr error
	if decision.ShouldApply && len(deleteErrs) == 0 {
		// Propagate labels from the parent to the children
		PropagateLabelsForResult(reconcileCtx.Object, &planResult, reconcileCtx.MergedRuntimeConfig.Value)

		// Add standard controller labels to all resources
		controllerLabels := map[string]string{
			"app.kubernetes.io/managed-by":                                        p.GetFullName(),
			fmt.Sprintf("%s/%s.name", constants.AimLabelDomain, p.ControllerName): obj.GetName(),
		}
		ApplyControllerLabelsToResult(&planResult, controllerLabels)

		// Apply owned resources (with owner references)
		if len(planResult.toApply) > 0 {
			applyErr = ApplyDesiredState(ctx, p.Client, p.GetFullName(), p.Scheme, planResult.toApply, obj)
			if applyErr != nil {
				applyErr = fmt.Errorf("failed to apply owned resources: %w", applyErr)
			}
		}

		// Apply unowned resources (without owner references)
		if applyErr == nil && len(planResult.toApplyWithoutOwnerRef) > 0 {
			applyErr = ApplyDesiredState(ctx, p.Client, p.GetFullName(), p.Scheme, planResult.toApplyWithoutOwnerRef, nil)
			if applyErr != nil {
				applyErr = fmt.Errorf("failed to apply unowned resources: %w", applyErr)
			}
		}
	}

	// TODO: Handle deleteErrs and applyErr - should they feed back into state engine?
	// For now, if they occurred, we'll return them after status update
	var phaseErr error
	if len(deleteErrs) > 0 {
		phaseErr = fmt.Errorf("delete phase failed: %w", errors.Join(deleteErrs...))
	} else if applyErr != nil {
		phaseErr = fmt.Errorf("apply phase failed: %w", applyErr)
	}

	// === Phase 7: Update Conditions ===
	status.SetConditions(cm.Conditions())

	// === Phase 8: Emit Events and Logs ===
	transitions := DiffConditionTransitions(oldConditions, status.GetConditions())
	EmitConditionTransitions(p.Recorder, obj, transitions, cm)
	EmitRecurringEvents(p.Recorder, obj, cm)
	EmitConditionLogs(ctx, transitions, cm)
	EmitRecurringLogs(ctx, cm)

	// === Phase 9: Update Status ===
	// ALWAYS update status (even on errors) so users can see what went wrong
	if !equality.Semantic.DeepEqual(oldStatus, status) {
		if err := p.StatusClient.Update(ctx, obj); err != nil {
			// Conflict errors are expected during concurrent updates (e.g., when child resources
			// are being reconciled simultaneously). Log at debug level and return nil - the
			// controller will be requeued automatically due to the watch on the resource.
			if apierrors.IsConflict(err) {
				log.FromContext(ctx).V(1).Info("status update conflict, will retry on next reconcile")
				return nil
			}
			return fmt.Errorf("status update failed: %w", err)
		}
	}

	// === Phase 10: Return Decision ===
	// Return requeue error if infrastructure issues detected (triggers exponential backoff)
	if decision.ShouldRequeue {
		return decision.RequeueError
	}

	// Return phase errors (delete/apply failures)
	if phaseErr != nil {
		return phaseErr
	}

	return nil
}

// errorCategories holds the results of error categorization from component health.
type errorCategories struct {
	hasInfra                bool
	hasAuth                 bool
	hasMissingDownstreamDep bool // Missing dependencies that will self-heal (e.g., resources being created)
	hasMissingUpstreamDep   bool // Missing dependencies that will not self-heal (e.g., required secret or config)
	hasInvalidSpec          bool
	infraErrors             []error
}

// categorizeComponentErrors collects and categorizes all errors from component health.
func categorizeComponentErrors(componentHealth []ComponentHealth) errorCategories {
	var result errorCategories
	for _, h := range componentHealth {
		for _, err := range h.Errors {
			if err == nil {
				continue
			}
			categorized := CategorizeError(err)
			switch categorized.Category() {
			case ErrorCategoryInfrastructure:
				result.hasInfra = true
				result.infraErrors = append(result.infraErrors, err)
			case ErrorCategoryMissingDownstreamDependency:
				// Missing dependencies are transient - resources being created, pods starting, etc.
				// These don't block Apply (we need to apply to create the missing resources)
				result.hasMissingDownstreamDep = true
			case ErrorCategoryAuth:
				result.hasAuth = true
			case ErrorCategoryMissingUpstreamDependency:
				result.hasMissingUpstreamDep = true
			case ErrorCategoryInvalidSpec:
				result.hasInvalidSpec = true
			case ErrorCategoryUnknown:
				// Unknown errors are treated as infrastructure errors (retriable)
				result.hasInfra = true
				result.infraErrors = append(result.infraErrors, err)
			}
		}
	}
	return result
}

// setDependenciesReachableCondition sets or clears the DependenciesReachable condition.
func setDependenciesReachableCondition(cm *ConditionManager, hasInfraError bool) {
	if hasInfraError {
		cm.Set(ConditionTypeDependenciesReachable, metav1.ConditionFalse, ReasonDependenciesNotReachable, MessageDependenciesNotReachable, AsError())
	} else if oldDepReachable := cm.Get(ConditionTypeDependenciesReachable); oldDepReachable != nil && oldDepReachable.Status == metav1.ConditionFalse {
		cm.Set(ConditionTypeDependenciesReachable, metav1.ConditionTrue, ReasonDependenciesReachable, MessageDependenciesReachable, AsInfo())
	}
}

// isInGracePeriod checks if we're within the grace period for infrastructure errors.
func isInGracePeriod(cm *ConditionManager, hasInfraError bool) bool {
	if !hasInfraError {
		return false
	}
	depReachable := cm.Get(ConditionTypeDependenciesReachable)
	if depReachable != nil && depReachable.Status == metav1.ConditionFalse {
		return isWithinGracePeriod(depReachable, degradationThreshold, cm.now())
	}
	return false
}

// statusToCondition maps AIMStatus to metav1.ConditionStatus.
func statusToCondition(state constants.AIMStatus) metav1.ConditionStatus {
	switch state {
	case constants.AIMStatusReady:
		return metav1.ConditionTrue
	default:
		// Not ready = False. The Reason field explains why (Progressing, Failed, etc.)
		// Unknown is reserved for when we can't determine state (e.g., infra errors)
		return metav1.ConditionFalse
	}
}

// statusToObsLevel maps AIMStatus to ObservabilityOption.
func statusToObsLevel(state constants.AIMStatus) ObservabilityOption {
	switch state {
	case constants.AIMStatusFailed, constants.AIMStatusDegraded, constants.AIMStatusNotAvailable:
		return AsError()
	default:
		return AsInfo()
	}
}

// setComponentConditions sets per-component conditions and returns the actual states used.
func setComponentConditions(cm *ConditionManager, componentHealth []ComponentHealth, withinGracePeriod bool) []constants.AIMStatus {
	actualStates := make([]constants.AIMStatus, len(componentHealth))

	for i, h := range componentHealth {
		conditionType := h.Component + ComponentConditionSuffix
		state := h.GetState()
		reason := h.GetReason()
		message := h.GetMessage()

		// Grace period: preserve previous state for components with infra errors
		if hasComponentInfrastructureErrors(h) && withinGracePeriod {
			if oldCond := cm.Get(conditionType); oldCond != nil {
				state, condStatus, obsLevel := preserveGracePeriodState(oldCond.Status)
				cm.Set(conditionType, condStatus, reason, message, obsLevel)
				actualStates[i] = state
				continue
			}
		}

		cm.Set(conditionType, statusToCondition(state), reason, message, statusToObsLevel(state))
		actualStates[i] = state
	}
	return actualStates
}

// preserveGracePeriodState returns the state to preserve during grace period based on old condition.
func preserveGracePeriodState(oldStatus metav1.ConditionStatus) (constants.AIMStatus, metav1.ConditionStatus, ObservabilityOption) {
	switch oldStatus {
	case metav1.ConditionTrue:
		return constants.AIMStatusReady, metav1.ConditionTrue, AsInfo()
	case metav1.ConditionUnknown:
		return constants.AIMStatusProgressing, metav1.ConditionUnknown, AsInfo()
	default:
		return constants.AIMStatusDegraded, metav1.ConditionFalse, AsError()
	}
}

// setErrorCategoryConditions sets AuthValid and ConfigValid conditions based on error categories.
func setErrorCategoryConditions(cm *ConditionManager, cats errorCategories) {
	if cats.hasAuth {
		cm.Set(ConditionTypeAuthValid, metav1.ConditionFalse, ReasonAuthError, MessageAuthError, AsError())
	} else if oldAuthValid := cm.Get(ConditionTypeAuthValid); oldAuthValid != nil && oldAuthValid.Status == metav1.ConditionFalse {
		cm.Set(ConditionTypeAuthValid, metav1.ConditionTrue, ReasonAuthValid, MessageAuthValid, AsInfo())
	}

	if cats.hasInvalidSpec {
		cm.Set(ConditionTypeConfigValid, metav1.ConditionFalse, ReasonInvalidSpec, MessageInvalidSpec, AsError())
	} else if cats.hasMissingUpstreamDep {
		cm.Set(ConditionTypeConfigValid, metav1.ConditionFalse, ReasonMissingRef, MessageMissingRef, AsError())
	} else if oldConfigValid := cm.Get(ConditionTypeConfigValid); oldConfigValid != nil && oldConfigValid.Status == metav1.ConditionFalse {
		cm.Set(ConditionTypeConfigValid, metav1.ConditionTrue, ReasonConfigValid, MessageConfigValid, AsInfo())
	}
}

// setReadyCondition sets the overall Ready condition based on all component conditions and error categories.
// This should be called after DecorateStatus to ensure all conditions are considered.
func setReadyCondition(cm *ConditionManager, componentHealth []ComponentHealth, cats errorCategories) {
	// First check component health from GetComponentHealth
	allReady, firstFailed, readyReason, readyMessage := analyzeComponentReadiness(componentHealth)

	// Then check all component-specific Ready conditions that may have been added by DecorateStatus
	// These are conditions with names ending in "Ready" (e.g., "ModelCachesReady")
	allConditions := cm.Conditions()
	for _, cond := range allConditions {
		if cond.Type == ConditionTypeReady {
			continue // Skip the overall Ready condition itself
		}
		if strings.HasSuffix(cond.Type, ComponentConditionSuffix) {
			if cond.Status != metav1.ConditionTrue {
				allReady = false
				// Use the first failed component condition for the error message
				if firstFailed == nil {
					firstFailed = &ComponentHealth{
						Component: strings.TrimSuffix(cond.Type, ComponentConditionSuffix),
						State:     constants.AIMStatusFailed,
						Reason:    cond.Reason,
						Message:   cond.Message,
					}
				}
			}
		}
	}

	if allReady && !cats.hasAuth && !cats.hasMissingUpstreamDep && !cats.hasInvalidSpec {
		// Use component-specific reason/message if available, otherwise use generic
		reason := readyReason
		message := readyMessage
		if reason == "" {
			reason = ReasonAllComponentsReady
		}
		if message == "" {
			message = MessageAllComponentsReady
		}
		cm.Set(ConditionTypeReady, metav1.ConditionTrue, reason, message, AsInfo())
		return
	}

	reason, message, obsLevel := determineReadyFalseReason(cats, firstFailed)
	cm.Set(ConditionTypeReady, metav1.ConditionFalse, reason, message, obsLevel)
}

// analyzeComponentReadiness checks if all components are ready and finds the first failed component.
// When all are ready, it returns the reason/message from the last component with a non-empty reason
// (typically the most specific one, like "AllTemplatesReady" from a template aggregator).
func analyzeComponentReadiness(componentHealth []ComponentHealth) (allReady bool, firstFailed *ComponentHealth, readyReason, readyMessage string) {
	allReady = true
	for i := range componentHealth {
		h := &componentHealth[i]
		state := h.GetState()
		if state != constants.AIMStatusReady {
			allReady = false
		}
		if firstFailed == nil && (state == constants.AIMStatusFailed || state == constants.AIMStatusDegraded || state == constants.AIMStatusNotAvailable) {
			firstFailed = h
		}
		// Capture reason/message from ready components (last one wins, as it's typically most specific)
		if state == constants.AIMStatusReady && h.Reason != "" {
			readyReason = h.Reason
			readyMessage = h.Message
		}
	}
	return allReady, firstFailed, readyReason, readyMessage
}

// determineReadyFalseReason determines the reason/message for Ready=False condition.
func determineReadyFalseReason(cats errorCategories, firstFailed *ComponentHealth) (string, string, ObservabilityOption) {
	switch {
	case cats.hasAuth:
		return ReasonAuthError, MessageAuthError, AsError()
	case cats.hasInvalidSpec:
		return ReasonInvalidSpec, MessageInvalidSpec, AsError()
	case cats.hasMissingUpstreamDep:
		return ReasonMissingRef, MessageMissingRef, AsError()
	case firstFailed != nil:
		return firstFailed.GetReason(), fmt.Sprintf("Component %s is not ready: %s", firstFailed.Component, firstFailed.GetMessage()), AsError()
	case cats.hasInfra:
		return ReasonDependenciesNotReachable, MessageInfraError, AsError()
	default:
		return ReasonProgressing, MessageProgressing, AsInfo()
	}
}

// processStateEngine analyzes component health, categorizes errors, sets conditions, and decides behavior.
func (p *Pipeline[T, S, F, Obs]) processStateEngine(
	ctx context.Context,
	obs Obs,
	cm *ConditionManager,
	status S,
) (StateEngineDecision, error) {
	// Extract component health from observation
	var componentHealth []ComponentHealth
	// Try the extended interface with clientset support first
	if healthProviderWithClientset, ok := any(obs).(interface {
		GetComponentHealth(context.Context, kubernetes.Interface) []ComponentHealth
	}); ok && p.Clientset != nil {
		componentHealth = healthProviderWithClientset.GetComponentHealth(ctx, p.Clientset)
	} else if healthProvider, ok := any(obs).(ComponentHealthProvider); ok {
		// Fallback to standard interface
		componentHealth = healthProvider.GetComponentHealth()
	}

	// Categorize errors
	cats := categorizeComponentErrors(componentHealth)

	// Manual mode: reconciler owns status & conditions
	if manual, ok := any(p.Reconciler).(ManualStatusController[T, S, Obs]); ok {
		manual.SetStatus(status, cm, obs)
		if cats.hasInfra {
			return StateEngineDecision{ShouldApply: false, ShouldRequeue: true, RequeueError: errors.Join(cats.infraErrors...)}, nil
		}
		return StateEngineDecision{ShouldApply: true, ShouldRequeue: false}, nil
	}

	// Set DependenciesReachable condition
	setDependenciesReachableCondition(cm, cats.hasInfra)

	// Set per-component conditions
	withinGracePeriod := isInGracePeriod(cm, cats.hasInfra)
	setComponentConditions(cm, componentHealth, withinGracePeriod)

	// Set error category conditions (AuthValid, ConfigValid)
	setErrorCategoryConditions(cm, cats)

	// Allow StatusDecorator to extend/override and add domain-specific conditions
	if dec, ok := any(p.Reconciler).(StatusDecorator[T, S, Obs]); ok {
		dec.DecorateStatus(status, cm, obs)
	}

	// Set overall Ready condition after DecorateStatus has had a chance to add conditions
	// This ensures all conditions (including domain-specific ones) are considered
	setReadyCondition(cm, componentHealth, cats)

	// Set root status from all conditions after DecorateStatus
	// This ensures the status reflects all domain-specific conditions
	derivedStatus := deriveStatusFromConditions(cm, cats, componentHealth)
	status.SetStatus(string(derivedStatus))

	// Determine behavior
	if cats.hasInfra {
		return StateEngineDecision{ShouldApply: false, ShouldRequeue: true, RequeueError: errors.Join(cats.infraErrors...)}, nil
	}
	return StateEngineDecision{ShouldApply: !cats.hasAuth && !cats.hasInvalidSpec, ShouldRequeue: false}, nil
}

// deriveStatusFromConditions derives the root status by examining all component Ready conditions.
// This is called after DecorateStatus to ensure all conditions (including domain-specific ones) are considered.
func deriveStatusFromConditions(cm *ConditionManager, cats errorCategories, componentHealth []ComponentHealth) constants.AIMStatus {
	// Check for error category conditions first (highest priority)
	if cats.hasAuth || cats.hasInvalidSpec || cats.hasMissingUpstreamDep {
		return constants.AIMStatusFailed
	}

	// Build a map of component names to their dependency types for quick lookup
	componentDepTypes := make(map[string]DependencyType)
	for _, h := range componentHealth {
		componentDepTypes[h.Component] = h.DependencyType
	}

	// Check all component Ready conditions
	worstStatus := constants.AIMStatusReady
	allConditions := cm.Conditions()

	for _, cond := range allConditions {
		// Skip non-component conditions
		if cond.Type == ConditionTypeReady ||
			cond.Type == ConditionTypeDependenciesReachable ||
			cond.Type == ConditionTypeAuthValid ||
			cond.Type == ConditionTypeConfigValid {
			continue
		}

		// Check component Ready conditions (conditions ending with "Ready")
		if strings.HasSuffix(cond.Type, ComponentConditionSuffix) {
			// Extract component name from condition type (remove "Ready" suffix)
			componentName := strings.TrimSuffix(cond.Type, ComponentConditionSuffix)
			depType := componentDepTypes[componentName]

			var componentStatus constants.AIMStatus
			switch cond.Status {
			case metav1.ConditionTrue:
				componentStatus = constants.AIMStatusReady
			case metav1.ConditionFalse:
				// Derive status based on dependency type
				componentStatus = deriveStatusFromDependencyType(depType, cond.Reason)
			case metav1.ConditionUnknown:
				// Unknown status: upstream → Pending, downstream/unspecified → Progressing
				if depType == DependencyTypeUpstream {
					componentStatus = constants.AIMStatusPending
				} else {
					// Treat downstream and unspecified as Progressing
					componentStatus = constants.AIMStatusProgressing
				}
			}

			// Keep the worst status
			if constants.CompareAIMStatus(componentStatus, worstStatus) < 0 {
				worstStatus = componentStatus
			}
		}
	}

	// Infrastructure errors take precedence if we're not ready
	if cats.hasInfra && worstStatus != constants.AIMStatusReady {
		return constants.AIMStatusDegraded
	}

	return worstStatus
}

// deriveStatusFromDependencyType derives the status for a not-ready component based on its dependency type.
// Upstream dependencies not ready → Pending (waiting for user to provide them)
// Downstream dependencies not ready → Progressing (controller is creating them)
func deriveStatusFromDependencyType(depType DependencyType, reason string) constants.AIMStatus {
	// First check for explicit error states in the reason
	switch {
	case strings.Contains(reason, "Failed"):
		return constants.AIMStatusFailed
	case strings.Contains(reason, "Degraded"):
		return constants.AIMStatusDegraded
	}

	// Then derive based on dependency type
	switch depType {
	case DependencyTypeUpstream:
		// Upstream dependencies not ready → Pending
		return constants.AIMStatusPending
	case DependencyTypeDownstream, DependencyTypeUnspecified:
		// Downstream dependencies being created → Progressing
		// Treat unspecified as downstream for flexibility (conditions added in DecorateStatus, etc.)
		return constants.AIMStatusProgressing
	default:
		return constants.AIMStatusProgressing
	}
}

// aggregateActualComponentStates returns the "worst" status from the actual component states.
// This is used when we've overridden derived states (e.g., during grace period).
func aggregateActualComponentStates(states []constants.AIMStatus) constants.AIMStatus {
	if len(states) == 0 {
		return constants.AIMStatusReady
	}

	worst := constants.AIMStatusReady
	for _, state := range states {
		// CompareAIMStatus returns > 0 if first arg is better (higher priority)
		// We want the worst status, so we check if state < worst (returns < 0)
		if constants.CompareAIMStatus(state, worst) < 0 {
			worst = state
		}
	}
	return worst
}

// hasComponentInfrastructureErrors checks if a component has any infrastructure errors
func hasComponentInfrastructureErrors(h ComponentHealth) bool {
	for _, err := range h.Errors {
		if err == nil {
			continue
		}
		categorized := CategorizeError(err)
		if categorized.Category() == ErrorCategoryInfrastructure {
			return true
		}
	}
	return false
}

// isWithinGracePeriod checks if a condition has been in its current state for less than the threshold.
// This function is testable by accepting currentTime as a parameter.
func isWithinGracePeriod(cond *metav1.Condition, threshold time.Duration, currentTime time.Time) bool {
	if cond == nil {
		return false
	}
	duration := currentTime.Sub(cond.LastTransitionTime.Time)
	return duration < threshold
}

// StatusWithConditions is a constraint for status types that have Conditions.
type StatusWithConditions interface {
	GetConditions() []metav1.Condition
	SetConditions([]metav1.Condition)
	SetStatus(string)
}

// ObjectWithStatus is a constraint for objects that have a Status field with conditions.
type ObjectWithStatus[S StatusWithConditions] interface {
	runtime.Object
	client.Object
	GetStatus() S
}
