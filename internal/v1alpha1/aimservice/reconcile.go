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
	"slices"
	"strings"
	"time"

	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func inferenceServiceComponentHealth(
	inferenceService controllerutils.FetchResult[*servingv1beta1.InferenceService],
	inferenceServicePods *controllerutils.FetchResult[*corev1.PodList],
	predictorDeployment controllerutils.FetchResult[*appsv1.Deployment],
) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "InferenceService",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	if !inferenceService.OK() {
		if inferenceService.IsNotFound() {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
			health.Message = "InferenceService not found"
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = "FetchError"
		health.Message = inferenceService.Error.Error()
		health.Errors = []error{inferenceService.Error}
		return health
	}

	isvc := inferenceService.Value
	if isvc == nil {
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
		health.Message = "InferenceService not found"
		return health
	}

	// KServe v0.20 reports the InferenceService Ready while old pods keep the
	// predictor Deployment available, so a rollout is checked first.
	if inferenceServiceReady(inferenceService) && !deploymentRollingOut(predictorDeployment.Value) {
		health.State = constants.AIMStatusReady
		health.Reason = aimv1alpha1.AIMServiceReasonRuntimeReady
		health.Message = "InferenceService is ready"
		return health
	}

	// If pods are still serving traffic, keep status running.
	if servingPodsHealth(&health, "InferenceService", "Predictor", inferenceServicePods, predictorDeployment,
		podCreationRefusalDetail(isvc)) {
		return health
	}

	// Checked before the generation guard: in RawDeployment mode KServe copies
	// the observedGeneration of the predictor Deployment, which also changes
	// when the replica count changes, and the refusal is current either way.
	if refusal, refused := predictorPodCreationFailure(isvc); refused {
		// A pod that exists can still become ready. After a spec fix, for
		// example, the Deployment keeps the refused old ReplicaSet, and so the
		// refusal, until the pod of the new ReplicaSet is ready.
		if starting := activePodCount(inferenceServicePods); starting > 0 {
			health.State = constants.AIMStatusProgressing
			health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
			health.Message = fmt.Sprintf("InferenceService has %d starting pod(s); "+
				"the predictor Deployment cannot create a pod: %s", starting, refusal)
			return health
		}
		health.State = constants.AIMStatusFailed
		health.Reason = aimv1alpha1.AIMServiceReasonPodCreationFailed
		health.Message = "The predictor Deployment cannot create a pod: " + refusal
		return health
	}

	if isvc.Generation <= 0 || isvc.Status.ObservedGeneration != isvc.Generation {
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
		health.Message = "InferenceService has not observed its current generation"
		return health
	}

	health.State = constants.AIMStatusProgressing
	health.Reason = aimv1alpha1.AIMServiceReasonCreatingRuntime
	health.Message = "InferenceService is not ready"
	return health
}

// servingPodsHealth fills health when ready pods keep the service running
// although KServe does not report the serving resource ready, or its
// Deployment still rolls out. While previous-revision pods remain, ready pods
// may all be old ones, so they keep the service running without proving the
// rollout ready: the verdict is Degraded RolloutStalled once the rollout
// stalls, Progressing RolloutInProgress until then, and Ready RuntimeScaling
// when the Deployment has finished rolling out. kind names the KServe resource
// and workload the Deployment in messages; refusalDetail is appended to them.
// It reports false when no pod is ready.
func servingPodsHealth(
	health *controllerutils.ComponentHealth,
	kind, workload string,
	pods *controllerutils.FetchResult[*corev1.PodList],
	deployment controllerutils.FetchResult[*appsv1.Deployment],
	refusalDetail string,
) bool {
	if pods == nil || !pods.OK() || pods.Value == nil {
		return false
	}
	readyPodCount := 0
	for _, pod := range pods.Value.Items {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				readyPodCount++
				break
			}
		}
	}
	if readyPodCount == 0 {
		return false
	}
	if deployment.HasError() && !deployment.IsNotFound() {
		health.State = constants.AIMStatusFailed
		health.Reason = "FetchError"
		health.Message = deployment.Error.Error()
		health.Errors = []error{deployment.Error}
		return true
	}
	if d := deployment.Value; deploymentRollingOut(d) {
		health.Serving = true
		if stall, userFixable := rolloutStallDetail(d, pods.Value.Items); stall != "" {
			health.State = constants.AIMStatusDegraded
			health.Reason = aimv1alpha1.AIMServiceReasonRolloutStalled
			health.UserFixable = userFixable
			health.Message = fmt.Sprintf("%s rollout of Deployment %s is stalled: %s; %d pod(s) are ready and serving%s",
				workload, d.Name, stall, readyPodCount, refusalDetail)
			return true
		}
		health.State = constants.AIMStatusProgressing
		health.Reason = aimv1alpha1.AIMServiceReasonRolloutInProgress
		health.Message = fmt.Sprintf(
			"%s rollout in progress: %d of %d pods are on the new revision, %d pod(s) are ready and serving%s",
			workload,
			d.Status.UpdatedReplicas,
			d.Status.Replicas,
			readyPodCount,
			pendingPodDetail(pods.Value.Items),
		) + refusalDetail
		return true
	}
	health.State = constants.AIMStatusReady
	health.Reason = aimv1alpha1.AIMServiceReasonRuntimeScaling
	health.Message = fmt.Sprintf("%s has %d ready pod(s), scaling in progress", kind, readyPodCount) + refusalDetail
	return true
}

// deploymentRollingOut reports whether some of the Deployment's pods are not
// yet on its current revision.
func deploymentRollingOut(deployment *appsv1.Deployment) bool {
	return deployment != nil && deployment.Status.UpdatedReplicas < deployment.Status.Replicas
}

// deploymentReasonFailedCreate is the reason of the Deployment ReplicaFailure
// condition when the API server refuses a Pod. Not exported by client-go.
const deploymentReasonFailedCreate = "FailedCreate"

// predictorPodCreationFailure returns the refusal message when the predictor
// Deployment cannot create a Pod. In RawDeployment mode KServe copies the
// Deployment ReplicaFailure condition into PredictorReady, so the message is
// the text of the API server or of the admission policy.
func predictorPodCreationFailure(isvc *servingv1beta1.InferenceService) (string, bool) {
	cond := isvc.Status.GetCondition(servingv1beta1.PredictorReady)
	if cond == nil || cond.Status != corev1.ConditionFalse || cond.Reason != deploymentReasonFailedCreate {
		return "", false
	}
	return cond.Message, true
}

// podCreationRefusalDetail returns the refusal as a message suffix, or "" when
// the predictor Deployment can create pods.
func podCreationRefusalDetail(isvc *servingv1beta1.InferenceService) string {
	if refusal, refused := predictorPodCreationFailure(isvc); refused {
		return "; the predictor Deployment cannot create a pod: " + refusal
	}
	return ""
}

// activePodCount counts the pods that a ReplicaSet counts as replicas: not
// terminating and not in a terminal phase.
func activePodCount(pods *controllerutils.FetchResult[*corev1.PodList]) int {
	if pods == nil || !pods.OK() || pods.Value == nil {
		return 0
	}
	count := 0
	for i := range pods.Value.Items {
		pod := &pods.Value.Items[i]
		if pod.DeletionTimestamp == nil &&
			pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			count++
		}
	}
	return count
}

// pendingPodDetail names a Pending pod and, when the scheduler reported one,
// why it cannot be scheduled. The pod with the smallest name is chosen so the
// message is stable across reconciles of an unordered pod list.
func pendingPodDetail(pods []corev1.Pod) string {
	var pending *corev1.Pod
	for i := range pods {
		if pods[i].Status.Phase == corev1.PodPending && (pending == nil || pods[i].Name < pending.Name) {
			pending = &pods[i]
		}
	}
	if pending == nil {
		return ""
	}
	for _, cond := range pending.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			return fmt.Sprintf("; pod %s is Pending: %s: %s", pending.Name, cond.Reason, cond.Message)
		}
	}
	return fmt.Sprintf("; pod %s is Pending", pending.Name)
}

// rolloutStallDetail names what stops the predictor rollout: a crash-looping
// pod (the smallest name, for a stable message), or the Deployment's exceeded
// progress deadline together with the Pending pod behind it. It returns ""
// while the rollout can still progress. A stall on a Pending pod is the user's
// to fix (resources, scheduling, image).
func rolloutStallDetail(deployment *appsv1.Deployment, pods []corev1.Pod) (string, bool) {
	crashPod, crashDetail := "", ""
	for i := range pods {
		for _, status := range slices.Concat(pods[i].Status.InitContainerStatuses, pods[i].Status.ContainerStatuses) {
			if waiting := status.State.Waiting; waiting != nil && waiting.Reason == "CrashLoopBackOff" &&
				(crashPod == "" || pods[i].Name < crashPod) {
				crashPod, crashDetail = pods[i].Name, waiting.Message
			}
		}
	}
	if crashPod != "" {
		return fmt.Sprintf("pod %s is CrashLoopBackOff: %s", crashPod, crashDetail), false
	}
	for _, cond := range deployment.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse &&
			cond.Reason == "ProgressDeadlineExceeded" {
			pending := pendingPodDetail(pods)
			return cond.Reason + ": " + cond.Message + pending, pending != ""
		}
	}
	return "", false
}

// hpaComponentHealth is the HPA health verdict. It takes the observed
// predictor pod count and the InferenceService readiness as explicit inputs so
// callers that model their observations differently can reuse the identical
// scale-to-zero semantics. HPAComponentHealth (exports.go) is the exported
// wrapper.
func hpaComponentHealth(
	service *aimv1alpha2.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
	isvcReady bool,
) controllerutils.ComponentHealth {
	health := controllerutils.ComponentHealth{
		Component:      "HPA",
		DependencyType: controllerutils.DependencyTypeDownstream,
	}

	// Check if autoscaling is configured (HPA is expected)
	hasAutoscaling := service.Spec.AutoScaling != nil ||
		service.Spec.MinReplicas != nil ||
		service.Spec.MaxReplicas != nil

	// If autoscaling is not configured, no health check needed
	if !hasAutoscaling {
		return controllerutils.ComponentHealth{}
	}

	// Autoscaling is configured - check if HPA exists
	if hpa.Error != nil {
		if hpa.IsNotFound() {
			// HPA doesn't exist yet - KEDA may still be creating it
			// This is expected during initial deployment, don't fail
			health.State = constants.AIMStatusProgressing
			health.Reason = "HPANotFound"
			health.Message = "Waiting for KEDA to create HorizontalPodAutoscaler"
			return health
		}
		// Other fetch error
		health.State = constants.AIMStatusFailed
		health.Reason = "HPAFetchError"
		health.Message = hpa.Error.Error()
		health.Errors = []error{hpa.Error}
		return health
	}

	// HPA not found (no error but nil value)
	if hpa.Value == nil {
		health.State = constants.AIMStatusProgressing
		health.Reason = "HPANotFound"
		health.Message = "Waiting for KEDA to create HorizontalPodAutoscaler"
		return health
	}

	// HPA exists - check its conditions for operational status
	h := hpa.Value

	// Get HPA conditions
	ableToScale := getHPACondition(h, autoscalingv2.AbleToScale)
	scalingActive := getHPACondition(h, autoscalingv2.ScalingActive)

	// Check ScalingActive condition - indicates if HPA can get metrics and calculate replicas
	if scalingActive == nil || scalingActive.Status != corev1.ConditionTrue {
		// Under scale-to-zero the HPA's ScalingActive signal is about
		// *waking* a sleeping deployment, not about *certifying* a
		// serving one. The activation metric series may legitimately
		// be empty (no traffic yet, user metric not produced by the
		// workload, gateway scrape not run, etc.) for the entire
		// lifetime of a healthy idle service. Pod / ISVC health is the
		// source of truth for liveness here, so we never escalate the
		// HPA to Failed *or* gate readiness on ScalingActive under
		// scale-to-zero. Genuine HPA-side breakage still surfaces via
		// the AbleToScale branch below (ScaleTargetNotReady) and via
		// the HPA-missing branch above (HPANotFound).
		//
		// Three buckets under scale-to-zero, all of which are Ready
		// from the HPA component's perspective:
		//   1. KEDA's authoritative idle reason (ScalingDisabled) -> ScaledToZero.
		//   2. No replicas running, no authoritative signal yet     -> ScaledToZero.
		//   3. Replicas running                                     -> HPAOperational.
		if isScaleToZero(service) {
			// isScaleToZeroIdle is the shared idle predicate (also used by
			// scaledToZeroNow) so the HPA verdict and the InferenceServicePods
			// verdict never disagree under scale-to-zero.
			if isScaleToZeroIdle(scalingActive, podCount) {
				health.State = constants.AIMStatusReady
				health.Reason = aimv1alpha1.AIMServiceReasonScaledToZero
				switch {
				case scalingActive != nil && scalingActive.Reason == hpaReasonScalingDisabled:
					health.Message = "Service is idle: KEDA has scaled the deployment to zero replicas; will scale up when the configured trigger becomes active"
				case scalingActive == nil:
					health.Message = "Scale-to-zero: deployment is idle (no replicas running) and the HPA has not yet emitted a ScalingActive condition"
				default:
					health.Message = fmt.Sprintf("Scale-to-zero: deployment is idle (no replicas running); activation metric series not yet available (%s)", scalingActive.Reason)
				}
				return health
			}
			// Pods running: service is operational regardless of the
			// HPA's metric state. Reason stays HPAOperational so the
			// framework reads HPAReady=True.
			health.State = constants.AIMStatusReady
			health.Reason = "HPAOperational"
			if scalingActive == nil {
				health.Message = "Scale-to-zero: replicas running; HPA has not yet emitted a ScalingActive condition (does not gate readiness)"
			} else {
				health.Message = fmt.Sprintf("Scale-to-zero: replicas running; HPA ScalingActive=%s/%s (does not gate readiness)", scalingActive.Status, scalingActive.Reason)
			}
			return health
		}
		if !isvcReady {
			// Expected during startup - ISVC pods not ready yet, so metrics aren't available
			health.State = constants.AIMStatusProgressing
			health.Reason = "WaitingForMetrics"
			health.Message = "Waiting for InferenceService to be ready before metrics are available"
			return health
		}
		// ISVC is ready but metrics still failing - this indicates a problem
		health.State = constants.AIMStatusFailed
		health.Reason = "MetricsFailed"
		if scalingActive != nil {
			health.Message = fmt.Sprintf("HPA cannot get metrics: %s", scalingActive.Message)
		} else {
			health.Message = "HPA ScalingActive condition not found"
		}
		return health
	}

	// ScalingActive is True - check AbleToScale condition
	if ableToScale != nil && ableToScale.Status != corev1.ConditionTrue {
		// HPA can get metrics but cannot update the scale target
		health.State = constants.AIMStatusProgressing
		health.Reason = "ScaleTargetNotReady"
		health.Message = fmt.Sprintf("HPA cannot update scale target: %s", ableToScale.Message)
		return health
	}

	// Both conditions are healthy (or AbleToScale not present, which is fine)
	health.State = constants.AIMStatusReady
	health.Reason = "HPAOperational"
	health.Message = "HorizontalPodAutoscaler is actively scaling"
	return health
}

// inferenceServicePodsHealth reports the InferenceServicePods component health.
// Under scale-to-zero an empty pod list is the desired state, so it reports
// Ready (ScaledToZero) instead of the default "no pods is a failure" verdict;
// otherwise it defers to the standard pod health inspector. Returns ok=false
// when there is nothing to report (pods were never fetched).
func inferenceServicePodsHealth(
	ctx context.Context,
	clientset kubernetes.Interface,
	service *aimv1alpha2.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	pods *controllerutils.FetchResult[*corev1.PodList],
) (controllerutils.ComponentHealth, bool) {
	if pods == nil {
		return controllerutils.ComponentHealth{}, false
	}
	pods = withoutDeletedFailedPods(pods)

	if scaledToZeroNow(service, hpa, podItemCount(pods)) &&
		pods.OK() && pods.Value != nil && len(pods.Value.Items) == 0 {
		return controllerutils.ComponentHealth{
			Component:      "InferenceServicePods",
			State:          constants.AIMStatusReady,
			Reason:         aimv1alpha1.AIMServiceReasonScaledToZero,
			Message:        "Service is idle: KEDA has scaled the deployment to zero replicas; pods will be created on activity",
			DependencyType: controllerutils.DependencyTypeDownstream,
		}, true
	}

	// Predictor pods are a downstream resource the AIMService owns (controller →
	// InferenceService → Deployment → pod), not a user-referenced upstream. The
	// shared pod inspector classifies an unpullable image as a
	// MissingUpstreamDependency, which the state engine treats as a blocking
	// config error (ConfigValid=False, ShouldApply=false) — wrongly halting
	// unrelated convergence such as adapter subtree GC. Demote it to a downstream
	// dependency so a bad predictor image degrades readiness without blocking
	// apply.
	return controllerutils.DemoteUpstreamDependencyErrors(pods.ToComponentHealthWithContext(
		ctx, clientset, "InferenceServicePods", controllerutils.GetPodsHealth,
	)), true
}

// withoutDeletedFailedPods drops the predictor pods that failed while being
// deleted. A rollout or scale-down deletes the pods it replaces, and a
// container that exits non-zero on SIGTERM leaves its pod Failed until the pod
// is gone. Such a pod says nothing about the service's spec, but the shared pod
// inspector would report it as an invalid spec, which blocks apply and emits a
// Warning event on every reconcile.
func withoutDeletedFailedPods(
	pods *controllerutils.FetchResult[*corev1.PodList],
) *controllerutils.FetchResult[*corev1.PodList] {
	if !pods.OK() || pods.Value == nil {
		return pods
	}
	kept := make([]corev1.Pod, 0, len(pods.Value.Items))
	for i := range pods.Value.Items {
		pod := &pods.Value.Items[i]
		if pod.DeletionTimestamp == nil || pod.Status.Phase != corev1.PodFailed {
			kept = append(kept, *pod)
		}
	}
	if len(kept) == len(pods.Value.Items) {
		return pods
	}
	return &controllerutils.FetchResult[*corev1.PodList]{
		Value: &corev1.PodList{ListMeta: pods.Value.ListMeta, Items: kept},
	}
}

// inferenceServiceReady reports whether the fetched InferenceService has
// Ready=True.
func inferenceServiceReady(
	isvc controllerutils.FetchResult[*servingv1beta1.InferenceService],
) bool {
	if isvc.Error != nil || isvc.Value == nil {
		return false
	}
	if isvc.Value.Generation <= 0 ||
		isvc.Value.Status.ObservedGeneration != isvc.Value.Generation {
		return false
	}
	for _, cond := range isvc.Value.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == "True" {
			return true
		}
	}
	return false
}

// getHPACondition finds a condition by type in the HPA status.
func getHPACondition(hpa *autoscalingv2.HorizontalPodAutoscaler, condType autoscalingv2.HorizontalPodAutoscalerConditionType) *autoscalingv2.HorizontalPodAutoscalerCondition {
	for i := range hpa.Status.Conditions {
		if hpa.Status.Conditions[i].Type == condType {
			return &hpa.Status.Conditions[i]
		}
	}
	return nil
}

// hpaReasonScalingDisabled is the reason emitted by the K8s HPA on
// ScalingActive=False when the target has zero replicas (KEDA drives the
// Deployment directly under scale-to-zero). Not exported by client-go.
const hpaReasonScalingDisabled = "ScalingDisabled"

// isScaleToZero reports whether the user opted into scale-to-zero.
func isScaleToZero(service *aimv1alpha2.AIMService) bool {
	if service == nil || service.Spec.MinReplicas == nil {
		return false
	}
	return *service.Spec.MinReplicas == 0
}

// podItemCount returns the number of pods in a (possibly nil) pod-list fetch
// result, treating a missing or failed fetch as zero.
func podItemCount(pods *controllerutils.FetchResult[*corev1.PodList]) int {
	if pods == nil || !pods.OK() || pods.Value == nil {
		return 0
	}
	return len(pods.Value.Items)
}

// scaledToZeroNow reports whether the service is currently idled under
// scale-to-zero -- scale-to-zero is enabled, the HPA is observable, it is not
// actively scaling a running deployment (ScalingActive!=True), and the shared
// isScaleToZeroIdle predicate considers it idle. Used to treat empty pod lists
// as the healthy desired state.
func scaledToZeroNow(
	service *aimv1alpha2.AIMService,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
) bool {
	if !isScaleToZero(service) {
		return false
	}
	if hpa.Value == nil {
		return false
	}
	scalingActive := getHPACondition(hpa.Value, autoscalingv2.ScalingActive)
	// ScalingActive=True means the HPA is actively managing a running
	// deployment -- not idle.
	if scalingActive != nil && scalingActive.Status == corev1.ConditionTrue {
		return false
	}
	return isScaleToZeroIdle(scalingActive, podCount)
}

// isScaleToZeroIdle reports whether a scale-to-zero service is currently idle
// (scaled, or being scaled, to zero replicas). It is the single idle predicate
// shared by getHPAHealth and scaledToZeroNow so the HPA verdict and the
// InferenceServicePods verdict can never disagree.
//
// Callers must have already established that scale-to-zero is enabled and that
// ScalingActive is not True. scalingActive is the HPA's ScalingActive
// condition and may be nil if the HPA has not emitted it yet.
func isScaleToZeroIdle(scalingActive *autoscalingv2.HorizontalPodAutoscalerCondition, podCount int) bool {
	// KEDA's authoritative idle reason: idle regardless of the observed pods.
	if scalingActive != nil && scalingActive.Reason == hpaReasonScalingDisabled {
		return true
	}
	// ScalingActive absent (not emitted yet) or False with a non-authoritative
	// reason (e.g. the activation metric series is not available yet): idle iff
	// no predictor pods are running.
	return podCount == 0
}

const kedaZeroActivationMetricPrefix = "s0-"

// withOtherReportedExternalMetrics adds diagnostic context without making user
// metrics part of the zero-to-one availability decision.
func withOtherReportedExternalMetrics(
	detail string,
	hpa *autoscalingv2.HorizontalPodAutoscaler,
	excludedMetricName string,
) string {
	var metricNames []string
	for _, metric := range hpa.Status.CurrentMetrics {
		if metric.Type != autoscalingv2.ExternalMetricSourceType ||
			metric.External == nil ||
			metric.External.Metric.Name == excludedMetricName ||
			(metric.External.Current.Value == nil && metric.External.Current.AverageValue == nil) {
			continue
		}
		metricNames = append(metricNames, metric.External.Metric.Name)
	}
	if len(metricNames) == 0 {
		return detail
	}
	return fmt.Sprintf("%s; other reported external metrics: %q", detail, metricNames)
}

// zeroActivationMetricEvidence resolves KEDA's zero-to-one activation metric
// from the HPA spec, then requires exactly one matching, valued external metric
// in status.currentMetrics. Aggregate HPA conditions cannot identify which
// trigger succeeded, so they are deliberately not consulted here.
func zeroActivationMetricEvidence(
	hpa *autoscalingv2.HorizontalPodAutoscaler,
	expectedExternalMetricCount int,
) (string, bool, string) {
	if hpa.Status.ObservedGeneration != nil &&
		*hpa.Status.ObservedGeneration < hpa.Generation {
		return "", false, fmt.Sprintf(
			"HPA status is stale at generation %d; waiting for generation %d",
			*hpa.Status.ObservedGeneration,
			hpa.Generation,
		)
	}

	externalMetricCount := 0
	var metricNames []string
	for _, metric := range hpa.Spec.Metrics {
		if metric.Type != autoscalingv2.ExternalMetricSourceType || metric.External == nil {
			continue
		}
		externalMetricCount++
		if strings.HasPrefix(metric.External.Metric.Name, kedaZeroActivationMetricPrefix) {
			metricNames = append(metricNames, metric.External.Metric.Name)
		}
	}
	if externalMetricCount != expectedExternalMetricCount {
		return "", false, fmt.Sprintf(
			"HPA spec contains %d external metrics; waiting for the expected %d "+
				"(one zero-to-one activation metric and %d user scaling metrics)",
			externalMetricCount,
			expectedExternalMetricCount,
			expectedExternalMetricCount-1,
		)
	}

	switch len(metricNames) {
	case 0:
		return "", false, fmt.Sprintf(
			"HPA spec does not contain an external zero-to-one activation metric with prefix %q",
			kedaZeroActivationMetricPrefix,
		)
	case 1:
		// Continue below.
	default:
		return "", false, fmt.Sprintf(
			"HPA spec contains %d external zero-to-one activation metrics with prefix %q; expected exactly one",
			len(metricNames),
			kedaZeroActivationMetricPrefix,
		)
	}

	metricName := metricNames[0]
	if metricName == kedaZeroActivationMetricPrefix {
		return metricName, false, fmt.Sprintf(
			"HPA spec contains malformed zero-to-one activation metric name %q",
			metricName,
		)
	}

	matchingStatuses := 0
	hasCurrentValue := false
	for _, metric := range hpa.Status.CurrentMetrics {
		if metric.Type != autoscalingv2.ExternalMetricSourceType ||
			metric.External == nil ||
			metric.External.Metric.Name != metricName {
			continue
		}
		matchingStatuses++
		hasCurrentValue = metric.External.Current.Value != nil ||
			metric.External.Current.AverageValue != nil
	}

	switch matchingStatuses {
	case 0:
		return metricName, false, withOtherReportedExternalMetrics(
			fmt.Sprintf(
				"HPA currentMetrics does not contain zero-to-one activation metric %q",
				metricName,
			),
			hpa,
			metricName,
		)
	case 1:
		// Continue below.
	default:
		return metricName, false, fmt.Sprintf(
			"HPA currentMetrics contains %d entries for zero-to-one activation metric %q; expected exactly one",
			matchingStatuses,
			metricName,
		)
	}

	if !hasCurrentValue {
		return metricName, false, withOtherReportedExternalMetrics(
			fmt.Sprintf(
				"HPA currentMetrics entry for zero-to-one activation metric %q has no current value",
				metricName,
			),
			hpa,
			metricName,
		)
	}

	return metricName, true, fmt.Sprintf(
		"HPA reported zero-to-one activation metric %q in currentMetrics",
		metricName,
	)
}

// activationMetricGracePeriod is how long the scale-from-zero activation
// trigger may stay unreadable before ActivationMetricAvailable escalates from
// Unknown to False. It debounces two legitimate transients: KEDA needs a few
// seconds to create the HPA and report its first current metric, and a collector
// restart briefly interrupts the series. Deliberately longer than the
// keda-otel-add-on metric-store retention window (~120s) so a single scrape gap
// cannot trip the condition.
const activationMetricGracePeriod = 3 * time.Minute

// activationMetricCondition classifies the scale-from-zero activation signal
// for reporting only. The condition it drives never gates readiness.
//
// Scope is deliberately narrow: the trigger is only diagnosable while the
// predictor is holding replicas. KEDA cannot idle a Deployment whose trigger it
// cannot evaluate, so "replicas running + trigger unreadable" is the one state
// where absence is both actionable and unambiguous -- the service will never
// idle. At zero replicas absence is the design's normal resting state, because
// the selected gateway provider's activation counter (and therefore the series)
// may only come into existence once a request creates it; reporting there would
// alarm on every healthy idle service and cannot distinguish "resting" from
// "dark".
//
// Returns ok=false when the condition does not apply and should be removed.
func activationMetricCondition(
	service *aimv1alpha2.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
	existing *metav1.Condition,
	now time.Time,
) (metav1.ConditionStatus, string, string, bool) {
	if !isScaleToZero(service) || !hpa.OK() || hpa.Value == nil || podCount == 0 {
		return "", "", "", false
	}
	if !isRoutingEnabled(service, runtimeConfig) {
		return "", "", "", false
	}
	activationRequired, activationErr := validateScaleToZeroActivationMetric(service, runtimeConfig)
	if !activationRequired || activationErr != nil {
		// Invalid activation configuration is reported by ScaleToZeroConfig.
		// Do not inspect a potentially stale, user-only HPA and mislabel its s0
		// metric as zero-to-one activation evidence.
		return "", "", "", false
	}

	isvcName, err := GenerateInferenceServiceName(service.Name, service.Namespace)
	if err != nil {
		// Query resolution above already reports this as invalid configuration;
		// keep the defensive guard local in case those implementations diverge.
		return "", "", "", false
	}
	expectedExternalMetricCount := 1 + resolvedUserMetricTriggerCount(
		service,
		isvcName+constants.PredictorServiceSuffix,
	)
	metricName, readable, detail := zeroActivationMetricEvidence(
		hpa.Value,
		expectedExternalMetricCount,
	)
	if readable {
		return metav1.ConditionTrue,
			aimv1alpha1.AIMServiceReasonActivationMetricAvailable,
			fmt.Sprintf(
				"The HPA reported the zero-to-one activation metric %q in currentMetrics; "+
					"the activation query is readable",
				metricName,
			),
			true
	}

	if withinActivationGracePeriod(existing, now) {
		return metav1.ConditionUnknown,
			aimv1alpha1.AIMServiceReasonAwaitingActivationMetric,
			fmt.Sprintf("Waiting for the HPA to report the zero-to-one activation metric (%s)", detail),
			true
	}

	return metav1.ConditionFalse,
		aimv1alpha1.AIMServiceReasonActivationMetricUnavailable,
		fmt.Sprintf(
			"The HPA has not reported the zero-to-one activation metric for over %s (%s). "+
				"The service may not wake after scaling to zero. Verify the selected gateway provider's "+
				"activation counter and scale-from-zero collector path.",
			activationMetricGracePeriod, detail,
		),
		true
}

// withinActivationGracePeriod reports whether the activation signal has been
// unreadable for less than activationMetricGracePeriod. ConditionManager
// preserves LastTransitionTime while status and reason are unchanged, so the
// existing condition is the "unreadable since" clock -- no extra status field
// is needed.
func withinActivationGracePeriod(existing *metav1.Condition, now time.Time) bool {
	if existing == nil {
		return true
	}
	switch existing.Reason {
	case aimv1alpha1.AIMServiceReasonActivationMetricUnavailable:
		// Already escalated; don't silently de-escalate to Unknown.
		return false
	case aimv1alpha1.AIMServiceReasonAwaitingActivationMetric:
		return now.Sub(existing.LastTransitionTime.Time) < activationMetricGracePeriod
	default:
		// First observation, or recovering from a readable signal: restart the clock.
		return true
	}
}

// activationMetricRequeueAfter returns the remaining debounce window while the
// current scale-from-zero activation assessment is AwaitingActivationMetric.
// Reconciliation at this deadline lets the condition become False even when no
// Kubernetes object changes after the first unreadable observation.
func activationMetricRequeueAfter(
	service *aimv1alpha2.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
	now time.Time,
) time.Duration {
	var existing *metav1.Condition
	if service != nil {
		for i := range service.Status.Conditions {
			if service.Status.Conditions[i].Type == aimv1alpha1.AIMServiceConditionActivationMetricAvailable {
				existing = &service.Status.Conditions[i]
				break
			}
		}
	}

	status, reason, _, ok := activationMetricCondition(
		service,
		runtimeConfig,
		hpa,
		podCount,
		existing,
		now,
	)
	if !ok ||
		status != metav1.ConditionUnknown ||
		reason != aimv1alpha1.AIMServiceReasonAwaitingActivationMetric {
		return 0
	}

	if existing == nil ||
		existing.Status != metav1.ConditionUnknown ||
		existing.Reason != aimv1alpha1.AIMServiceReasonAwaitingActivationMetric ||
		existing.LastTransitionTime.IsZero() {
		return activationMetricGracePeriod
	}

	remaining := existing.LastTransitionTime.Add(activationMetricGracePeriod).Sub(now)
	switch {
	case remaining <= 0:
		return 0
	case remaining > activationMetricGracePeriod:
		return activationMetricGracePeriod
	default:
		return remaining
	}
}

// setActivationMetricCondition publishes (or removes) the informational
// ActivationMetricAvailable condition. Only the escalated False state emits an
// event; the healthy and awaiting states are silent so an ordinary deploy stays
// quiet.
func setActivationMetricCondition(
	cm *controllerutils.ConditionManager,
	service *aimv1alpha2.AIMService,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	hpa controllerutils.FetchResult[*autoscalingv2.HorizontalPodAutoscaler],
	podCount int,
) {
	if cm == nil {
		return
	}
	condType := aimv1alpha1.AIMServiceConditionActivationMetricAvailable
	status, reason, message, ok := activationMetricCondition(
		service, runtimeConfig, hpa, podCount, cm.Get(condType), time.Now(),
	)
	if !ok {
		cm.Delete(condType)
		return
	}
	opts := controllerutils.Silent()
	if status == metav1.ConditionFalse {
		opts = controllerutils.AsWarning()
	}
	cm.Set(condType, status, reason, message, opts)
}
