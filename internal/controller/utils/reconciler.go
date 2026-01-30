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
	ctrl "sigs.k8s.io/controller-runtime"
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

	// RequeueAfter signals to the controller that reconciliation should be retried
	// after the specified duration. Use this when the reconciler cannot proceed
	// (e.g., blocked by a rate limit) but should retry later.
	RequeueAfter time.Duration
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

// GetToApply returns the objects to be applied with owner references (for testing)
func (pr *PlanResult) GetToApply() []client.Object {
	return pr.toApply
}

// GetToApplyWithoutOwnerRef returns the objects to be applied without owner references (for testing)
func (pr *PlanResult) GetToApplyWithoutOwnerRef() []client.Object {
	return pr.toApplyWithoutOwnerRef
}

// GetToDelete returns the objects to be deleted (for testing)
func (pr *PlanResult) GetToDelete() []client.Object {
	return pr.toDelete
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

// InfrastructureError represents retriable infrastructure failures (network, API server, etc.).
// It provides a stable error type for controller-runtime's exponential backoff, while preserving
// detailed error information for logging and debugging.
type InfrastructureError struct {
	// Count is the number of infrastructure errors encountered
	Count int
	// Errors contains the detailed error information
	Errors []error
}

func (e InfrastructureError) Error() string {
	if e.Count == 1 {
		return "infrastructure error (1 failure)"
	}
	return fmt.Sprintf("infrastructure errors (%d failures)", e.Count)
}

func (e InfrastructureError) Unwrap() []error {
	return e.Errors
}

// IsReconciliationPaused returns true if the resource has the reconciliation-paused annotation set to "true".
// When paused, the controller skips all reconciliation logic and returns immediately.
func IsReconciliationPaused(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[constants.AnnotationReconciliationPaused] == "true"
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
func (p *Pipeline[T, S, F, Obs]) Run(ctx context.Context, obj T) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// === Pre-check: Skip reconciliation if paused ===
	if IsReconciliationPaused(obj) {
		logger.V(1).Info("Reconciliation paused, skipping",
			"annotation", constants.AnnotationReconciliationPaused)
		return ctrl.Result{}, nil
	}

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
		return ctrl.Result{}, fmt.Errorf("DeepCopyObject returned unexpected type, expected %T", obj)
	}
	oldStatus := oldObj.GetStatus()
	oldConditions := append([]metav1.Condition(nil), oldStatus.GetConditions()...)

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
		return ctrl.Result{}, fmt.Errorf("state engine failed: %w", stateErr)
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

	// === Phase 7: Handle Apply/Delete Errors ===
	// Apply/delete failures are treated as infrastructure errors (retriable).
	// Set DependenciesReachable=False to indicate the operator cannot reach the API server
	// or lacks permissions to perform the operation.
	var phaseErr error
	if len(deleteErrs) > 0 {
		phaseErr = InfrastructureError{Count: len(deleteErrs), Errors: deleteErrs}
		cm.Set(ConditionTypeDependenciesReachable, metav1.ConditionFalse, ReasonDependenciesNotReachable, fmt.Sprintf("Failed to delete resources: %v", deleteErrs[0]), AsError())
	} else if applyErr != nil {
		phaseErr = InfrastructureError{Count: 1, Errors: []error{applyErr}}
		cm.Set(ConditionTypeDependenciesReachable, metav1.ConditionFalse, ReasonDependenciesNotReachable, fmt.Sprintf("Failed to apply resources: %v", applyErr), AsError())
	}

	// === Phase 8: Update Conditions ===
	status.SetConditions(cm.Conditions())

	// === Phase 9: Emit Events and Logs ===
	transitions := DiffConditionTransitions(oldConditions, status.GetConditions())
	EmitConditionTransitions(p.Recorder, obj, transitions, cm)
	EmitRecurringEvents(p.Recorder, obj, cm)
	EmitConditionLogs(ctx, transitions, cm)
	EmitRecurringLogs(ctx, cm)

	// === Phase 10: Update Status ===
	// ALWAYS update status (even on errors) so users can see what went wrong
	if !equality.Semantic.DeepEqual(oldStatus, status) {
		if err := p.StatusClient.Update(ctx, obj); err != nil {
			// Conflict errors are expected during concurrent updates (e.g., when child resources
			// are being reconciled simultaneously). Log at debug level and return nil - the
			// controller will be requeued automatically due to the watch on the resource.
			if apierrors.IsConflict(err) {
				log.FromContext(ctx).V(1).Info("status update conflict, will retry on next reconcile")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("status update failed: %w", err)
		}
	}

	// === Phase 11: Return Decision ===
	// Return requeue error if infrastructure issues detected (triggers exponential backoff)
	if decision.ShouldRequeue {
		return ctrl.Result{}, decision.RequeueError
	}

	// Return phase errors (delete/apply failures)
	if phaseErr != nil {
		return ctrl.Result{}, phaseErr
	}

	// === Phase 12: Return PlanResult RequeueAfter ===
	// If the reconciler requested a requeue (e.g., blocked by rate limit), honor it
	if planResult.RequeueAfter > 0 {
		return ctrl.Result{RequeueAfter: planResult.RequeueAfter}, nil
	}

	return ctrl.Result{}, nil
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

// setComponentConditions sets or preserves per-component conditions.
// During the grace period, component conditions with infrastructure errors preserve their previous status
// to prevent flapping between Ready/Progressing and Degraded due to transient network issues.
func setComponentConditions(cm *ConditionManager, componentHealth []ComponentHealth, withinGracePeriod bool) {
	for _, h := range componentHealth {
		conditionType := h.Component + ComponentConditionSuffix
		state := h.GetState()
		reason := h.GetReason()
		message := h.GetMessage()

		// Grace period: preserve previous state for components with infra errors
		if hasComponentInfrastructureErrors(h) && withinGracePeriod {
			if oldCond := cm.Get(conditionType); oldCond != nil {
				_, condStatus, obsLevel := preserveGracePeriodState(oldCond.Status)
				cm.Set(conditionType, condStatus, reason, message, obsLevel)
				continue
			}
		}

		cm.Set(conditionType, statusToCondition(state), reason, message, statusToObsLevel(state))
	}
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

// deriveStatusAndSetReadyCondition analyzes all component conditions, derives the root status,
// and sets the Ready condition. This should be called after DecorateStatus to ensure all
// conditions (including domain-specific ones) are considered.
//
// Returns the derived root status.
func deriveStatusAndSetReadyCondition(cm *ConditionManager, cats errorCategories, componentHealth []ComponentHealth) constants.AIMStatus {
	// Check for error category conditions first (highest priority)
	if status, handled := handleErrorCategories(cm, cats); handled {
		return status
	}

	// Build lookup maps for component states and dependency types
	componentStates, componentDepTypes := buildComponentMaps(componentHealth)

	// Scan all component conditions and aggregate results
	scanResult := scanComponentConditions(cm, componentStates, componentDepTypes)

	// Set Ready condition based on scan results
	setReadyConditionFromScan(cm, scanResult, cats)

	// Infrastructure errors take precedence if we're not ready
	if cats.hasInfra && scanResult.worstStatus != constants.AIMStatusReady {
		return constants.AIMStatusDegraded
	}

	return scanResult.worstStatus
}

// handleErrorCategories checks for error category conditions and sets Ready=False if found.
// Returns (status, true) if handled, (empty, false) if not.
func handleErrorCategories(cm *ConditionManager, cats errorCategories) (constants.AIMStatus, bool) {
	if !cats.hasAuth && !cats.hasInvalidSpec && !cats.hasMissingUpstreamDep {
		return "", false
	}

	var reason, message string
	switch {
	case cats.hasAuth:
		reason, message = ReasonAuthError, MessageAuthError
	case cats.hasInvalidSpec:
		reason, message = ReasonInvalidSpec, MessageInvalidSpec
	case cats.hasMissingUpstreamDep:
		reason, message = ReasonMissingRef, MessageMissingRef
	}
	cm.Set(ConditionTypeReady, metav1.ConditionFalse, reason, message, AsError())
	return constants.AIMStatusFailed, true
}

// buildComponentMaps creates lookup maps for component states and dependency types.
func buildComponentMaps(componentHealth []ComponentHealth) (map[string]constants.AIMStatus, map[string]DependencyType) {
	componentStates := make(map[string]constants.AIMStatus)
	componentDepTypes := make(map[string]DependencyType)
	for _, h := range componentHealth {
		componentStates[h.Component] = h.GetState()
		componentDepTypes[h.Component] = h.DependencyType
	}
	return componentStates, componentDepTypes
}

// componentScanResult holds the aggregated results from scanning component conditions.
type componentScanResult struct {
	allReady            bool
	worstStatus         constants.AIMStatus
	firstErrorComponent string
	firstErrorReason    string
	firstErrorMessage   string
}

// scanComponentConditions scans all component Ready conditions and aggregates their status.
func scanComponentConditions(cm *ConditionManager, componentStates map[string]constants.AIMStatus, componentDepTypes map[string]DependencyType) componentScanResult {
	result := componentScanResult{
		allReady:    true,
		worstStatus: constants.AIMStatusReady,
	}

	// Note: cm.Conditions() allocates a new slice for encapsulation, but this is acceptable
	// since the number of conditions is typically small (< 10) and this is called once per reconcile.
	allConditions := cm.Conditions()
	for _, cond := range allConditions {
		if !isComponentCondition(cond.Type) {
			continue
		}

		componentName := strings.TrimSuffix(cond.Type, ComponentConditionSuffix)
		componentStatus := deriveComponentStatus(cond.Status, componentName, componentStates, componentDepTypes)

		// Process the component status
		processComponentStatus(cond, componentName, componentStatus, &result)

		// Keep the worst status
		if constants.CompareAIMStatus(componentStatus, result.worstStatus) < 0 {
			result.worstStatus = componentStatus
		}
	}

	return result
}

// isComponentCondition returns true if the condition type is a component Ready condition.
func isComponentCondition(condType string) bool {
	if condType == ConditionTypeReady ||
		condType == ConditionTypeDependenciesReachable ||
		condType == ConditionTypeAuthValid ||
		condType == ConditionTypeConfigValid {
		return false
	}
	return strings.HasSuffix(condType, ComponentConditionSuffix)
}

// deriveComponentStatus derives the component's status based on condition status and available metadata.
func deriveComponentStatus(condStatus metav1.ConditionStatus, componentName string, componentStates map[string]constants.AIMStatus, componentDepTypes map[string]DependencyType) constants.AIMStatus {
	switch condStatus {
	case metav1.ConditionTrue:
		return constants.AIMStatusReady
	case metav1.ConditionFalse:
		// For not-ready components, check if the original state should be preserved.
		// Preserve specific states that indicate:
		// - Error states (Failed/Degraded/NotAvailable): these are more specific than Pending/Progressing
		// - Pending state: component is explicitly waiting for external resources (e.g., pod scheduling)
		// Otherwise, derive from dependency type for correct semantics (Upstream→Pending, Downstream→Progressing).
		if originalState, ok := componentStates[componentName]; ok {
			if isErrorState(originalState) || originalState == constants.AIMStatusPending {
				return originalState
			}
		}
		return deriveStatusFromDependencyType(componentDepTypes[componentName])
	case metav1.ConditionUnknown:
		// Unknown status: use original if available and specific, otherwise derive from dependency type
		if originalState, ok := componentStates[componentName]; ok {
			if isErrorState(originalState) || originalState == constants.AIMStatusPending {
				return originalState
			}
		}
		// Fallback: upstream → Pending, downstream/unspecified → Progressing
		if componentDepTypes[componentName] == DependencyTypeUpstream {
			return constants.AIMStatusPending
		}
		return constants.AIMStatusProgressing
	default:
		return constants.AIMStatusProgressing
	}
}

// processComponentStatus updates the scan result based on the component's condition and status.
func processComponentStatus(cond metav1.Condition, componentName string, componentStatus constants.AIMStatus, result *componentScanResult) {
	if componentStatus != constants.AIMStatusReady {
		result.allReady = false
		// Track first component in actual error state (Failed, Degraded, NotAvailable).
		// Normal progression states (Progressing, Pending) don't count as errors.
		if result.firstErrorComponent == "" && isErrorState(componentStatus) {
			result.firstErrorComponent = componentName
			result.firstErrorReason = cond.Reason
			result.firstErrorMessage = cond.Message
		}
	}
}

// setReadyConditionFromScan sets the Ready condition based on the scan results.
func setReadyConditionFromScan(cm *ConditionManager, result componentScanResult, cats errorCategories) {
	if result.allReady {
		// When all components are ready, use the standard aggregated reason/message.
		// Don't inherit from individual components to avoid leaking implementation details
		// (e.g., "PodsReady" / "Pods are running" from InferenceServicePodsReady).
		cm.Set(ConditionTypeReady, metav1.ConditionTrue, ReasonAllComponentsReady, MessageAllComponentsReady, AsInfo())
		return
	}

	// Determine reason for Ready=False
	reason, message, obsLevel := determineReadyFalseReason(result, cats)
	cm.Set(ConditionTypeReady, metav1.ConditionFalse, reason, message, obsLevel)
}

// determineReadyFalseReason determines the reason, message, and observability level for Ready=False.
func determineReadyFalseReason(result componentScanResult, cats errorCategories) (string, string, ObservabilityOption) {
	if result.firstErrorComponent != "" {
		reason := result.firstErrorReason
		message := fmt.Sprintf("Component %s is not ready: %s", result.firstErrorComponent, result.firstErrorMessage)
		return reason, message, AsError()
	}
	if cats.hasInfra {
		return ReasonDependenciesNotReachable, MessageInfraError, AsError()
	}
	return ReasonProgressing, MessageProgressing, AsInfo()
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

	// Set or preserve per-component conditions.
	// During grace period (degradationThreshold after infra errors start), component conditions
	// preserve their previous status to prevent flapping. The preserved conditions will be used
	// by deriveStatusAndSetReadyCondition to calculate the root status.
	withinGracePeriod := isInGracePeriod(cm, cats.hasInfra)
	setComponentConditions(cm, componentHealth, withinGracePeriod)

	// Set error category conditions (AuthValid, ConfigValid)
	setErrorCategoryConditions(cm, cats)

	// Allow StatusDecorator to extend/override and add domain-specific conditions
	if dec, ok := any(p.Reconciler).(StatusDecorator[T, S, Obs]); ok {
		dec.DecorateStatus(status, cm, obs)
	}

	// Derive root status and set Ready condition after DecorateStatus has had a chance to add conditions.
	// This ensures all conditions (including domain-specific ones) are considered.
	derivedStatus := deriveStatusAndSetReadyCondition(cm, cats, componentHealth)
	status.SetStatus(string(derivedStatus))

	// Determine behavior
	if cats.hasInfra {
		infraErr := InfrastructureError{Count: len(cats.infraErrors), Errors: cats.infraErrors}
		return StateEngineDecision{ShouldApply: false, ShouldRequeue: true, RequeueError: infraErr}, nil
	}
	// Block apply if auth, invalid spec, or missing upstream dependencies
	shouldApply := !cats.hasAuth && !cats.hasInvalidSpec && !cats.hasMissingUpstreamDep
	return StateEngineDecision{ShouldApply: shouldApply, ShouldRequeue: false}, nil
}

// deriveStatusFromDependencyType derives the status for a not-ready component based on its dependency type.
// Upstream dependencies not ready → Pending (waiting for user to provide them)
// Downstream dependencies not ready → Progressing (controller is creating them)
// Unspecified dependencies → Progressing (for backward compatibility and flexibility)
func deriveStatusFromDependencyType(depType DependencyType) constants.AIMStatus {
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

// isErrorState returns true if the status represents an actual error condition,
// not just normal progression (Progressing, Pending) or readiness.
func isErrorState(status constants.AIMStatus) bool {
	switch status {
	case constants.AIMStatusFailed, constants.AIMStatusDegraded, constants.AIMStatusNotAvailable:
		return true
	default:
		return false
	}
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
