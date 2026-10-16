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

package templatemigration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
	v1alpha1aimartifact "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimartifact"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimruntimeconfig"
	v1alpha1aimservice "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/aimservice"
	v1alpha1utils "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha1/utils"
	v1alpha2aimprofilecache "github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/aimprofilecache"
	"github.com/amd-enterprise-ai/aim-engine/internal/v1alpha2/serving"
)

const (
	// ApplyResultVersion is the durable result format written by Apply.
	ApplyResultVersion = "aim-template-migration-apply-result/v2"

	applyResultFilename              = "apply-result.json"
	applyFieldOwner                  = "aim-template-migration"
	defaultWaitTimeout               = 10 * time.Minute
	defaultPollInterval              = 2 * time.Second
	resourceKindProfile              = "AIMProfile"
	resourceKindClusterProfile       = "AIMClusterProfile"
	resourceKindService              = "AIMService"
	resourceKindServiceTemplate      = "AIMServiceTemplate"
	resourceKindClusterTemplate      = "AIMClusterServiceTemplate"
	resourceKindRuntimeConfig        = "AIMRuntimeConfig"
	resourceKindClusterRuntimeConfig = "AIMClusterRuntimeConfig"
	resourceKindTemplateCache        = "AIMTemplateCache"
	resourceKindProfileCache         = "AIMProfileCache"
	resourceKindArtifact             = "AIMArtifact"
	resourceKindPVC                  = "PersistentVolumeClaim"
	resourceKindISVC                 = "InferenceService"
	roleSourceArtifactProtection     = "source-artifact-protection"
	roleSourceModel                  = "source-model"
	roleSourceTemplate               = "source-template"
	roleDedicatedSourceCache         = "dedicated-source-template-cache"
	roleDedicatedArtifact            = "dedicated-source-artifact"
	operationOutcomeDryRun           = "dry-run"
	operationOutcomeFailed           = "failed"
	operationOutcomeConflict         = "conflict"
	operationOutcomeReused           = "reused"
	operationOutcomeCreated          = "created"
	operationOutcomePatched          = "patched"
	operationOutcomeAttempting       = "attempting"
	reconciliationPausedValue        = "true"
	actionPauseSourceCache           = "pause-source-template-cache"
	actionCreatePausedCache          = "create-paused-profile-cache"
	actionStageArtifactOwner         = "stage-artifact-owner"
	actionUnpauseTargetCache         = "unpause-profile-cache"
	actionVerifyDedicatedCache       = "verify-dedicated-adoption"
	actionTransferArtifactOwner      = "transfer-artifact-controller"
	actionVerifyDedicatedHandoff     = "verify-dedicated-handoff"
)

// ApplyMode distinguishes admission-only execution from persistent execution.
type ApplyMode string

const (
	ApplyModeDryRun ApplyMode = operationOutcomeDryRun
	ApplyModeCommit ApplyMode = "commit"
)

// ApplyOptions controls one bounded execution of an editable plan.
type ApplyOptions struct {
	PlanPath     string
	ResultPath   string
	Commit       bool
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

// ApplySummary is the concise outcome returned to the CLI.
type ApplySummary struct {
	Mode          ApplyMode
	Automatic     int
	ManualSkipped int
	Succeeded     int
	Failed        int
	Manual        int
	Skipped       int
	Operations    int
	ResultPath    string
}

// ApplyOperationResult is one durable, secret-free operation outcome.
type ApplyOperationResult struct {
	UnitID          string              `json:"unitId"`
	Sequence        int                 `json:"sequence"`
	Action          string              `json:"action"`
	Resource        ResourceIdentity    `json:"resource"`
	Outcome         string              `json:"outcome"`
	Attempted       bool                `json:"attempted"`
	Completed       bool                `json:"completed"`
	CurrentIdentity *ResourceIdentity   `json:"currentIdentity,omitempty"`
	CurrentState    *ApplyResourceState `json:"currentState,omitempty"`
	Error           string              `json:"error,omitempty"`
}

// ApplyUnitOutcome is the terminal disposition of one reviewed migration unit.
type ApplyUnitOutcome string

const (
	ApplyUnitPending   ApplyUnitOutcome = "pending"
	ApplyUnitSucceeded ApplyUnitOutcome = "succeeded"
	ApplyUnitFailed    ApplyUnitOutcome = "failed"
	ApplyUnitManual    ApplyUnitOutcome = "manual"
	ApplyUnitSkipped   ApplyUnitOutcome = "skipped"
)

// ApplyUnitResult records independent progress and recovery handoff for a unit.
type ApplyUnitResult struct {
	UnitID            string           `json:"unitId"`
	PlannedOutcome    Outcome          `json:"plannedOutcome"`
	Outcome           ApplyUnitOutcome `json:"outcome"`
	LastAttemptedStep string           `json:"lastAttemptedStep,omitempty"`
	LastCompletedStep string           `json:"lastCompletedStep,omitempty"`
	Error             string           `json:"error,omitempty"`
}

// ApplyResultSummary counts terminal unit outcomes without treating manual
// review as execution failure.
type ApplyResultSummary struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Manual    int `json:"manual"`
	Skipped   int `json:"skipped"`
}

// ApplyOwnerReference records the exact, recovery-relevant owner flags without
// copying arbitrary object metadata into the durable result.
type ApplyOwnerReference struct {
	APIVersion         string `json:"apiVersion"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
	Controller         *bool  `json:"controller,omitempty"`
	BlockOwnerDeletion *bool  `json:"blockOwnerDeletion,omitempty"`
}

// ApplyReportedArtifact records the identity projected by a cache status.
type ApplyReportedArtifact struct {
	UID                   string              `json:"uid"`
	Name                  string              `json:"name"`
	Model                 string              `json:"model"`
	Status                constants.AIMStatus `json:"status"`
	PersistentVolumeClaim string              `json:"persistentVolumeClaim,omitempty"`
}

// ApplyResourceState is the bounded recovery state captured for a mutated or
// verified resource.
type ApplyResourceState struct {
	Identity              ResourceIdentity                   `json:"identity"`
	OwnerReferences       []ApplyOwnerReference              `json:"ownerReferences,omitempty"`
	ReconciliationPaused  *bool                              `json:"reconciliationPaused,omitempty"`
	Status                *constants.AIMStatus               `json:"status,omitempty"`
	ObservedGeneration    *int64                             `json:"observedGeneration,omitempty"`
	PersistentVolumeClaim string                             `json:"persistentVolumeClaim,omitempty"`
	PersistentVolumePhase *corev1.PersistentVolumeClaimPhase `json:"persistentVolumePhase,omitempty"`
	ReportedArtifacts     []ApplyReportedArtifact            `json:"reportedArtifacts,omitempty"`
}

// ApplyResultDocument is atomically replaced after each completed operation.
type ApplyResultDocument struct {
	DocumentVersion     string                 `json:"documentVersion"`
	PlanDocumentVersion string                 `json:"planDocumentVersion"`
	PlanDigest          string                 `json:"planDigest"`
	RunID               string                 `json:"runId"`
	Mode                ApplyMode              `json:"mode"`
	ManualUnitsSkipped  int                    `json:"manualUnitsSkipped"`
	LastOperation       string                 `json:"lastOperation,omitempty"`
	LastAttemptedStep   string                 `json:"lastAttemptedStep,omitempty"`
	LastCompletedStep   string                 `json:"lastCompletedStep,omitempty"`
	Summary             ApplyResultSummary     `json:"summary"`
	Units               []ApplyUnitResult      `json:"units"`
	ObservedIdentities  []ResourceIdentity     `json:"observedIdentities,omitempty"`
	ObservedStates      []ApplyResourceState   `json:"observedStates,omitempty"`
	Operations          []ApplyOperationResult `json:"operations"`
}

type preparedApply struct {
	unit                        MigrationUnit
	profileOperation            ProposedOperation
	cacheOperation              ProposedOperation
	serviceOperation            ProposedOperation
	profileObject               client.Object
	cacheObject                 *aimv1alpha2.AIMProfileCache
	service                     *aimv1alpha2.AIMService
	sourceTemplate              ResourceIdentity
	sourceModel                 ResourceIdentity
	inferenceService            typesafeInferenceService
	shared                      *preparedSharedApply
	dedicated                   *preparedDedicatedApply
	allowDedicatedInvestigation bool
}

type typesafeInferenceService struct {
	identity ResourceIdentity
	object   *servingv1beta1.InferenceService
}

type preparedSharedApply struct {
	sourceCache         ResourceIdentity
	sourceArtifact      ResourceIdentity
	sourcePVC           ResourceIdentity
	modelID             string
	protectionOperation ProposedOperation
}

type preparedDedicatedApply struct {
	sourceCache     ResourceIdentity
	sourceArtifact  ResourceIdentity
	sourcePVC       ResourceIdentity
	modelID         string
	initialCache    *aimv1alpha1.AIMTemplateCache
	initialArtifact *aimv1alpha1.AIMArtifact
	initialClaim    *corev1.PersistentVolumeClaim
	oldOwner        metav1.OwnerReference
}

type sharedSourceState struct {
	cache    *aimv1alpha1.AIMTemplateCache
	artifact *aimv1alpha1.AIMArtifact
	claim    *corev1.PersistentVolumeClaim
	resolved aimv1alpha1.AIMResolvedArtifact
}

type observedSourceSet struct {
	template    ResourceIdentity
	model       ResourceIdentity
	cache       ResourceIdentity
	artifact    *ResourceIdentity
	pvc         *ResourceIdentity
	targetCache *ResourceIdentity
}

type resultRecorder struct {
	path           string
	automaticUnits int
	document       ApplyResultDocument
	publication    *resultPublication
}

// Apply validates and independently executes every reviewed automatic unit.
// Kubernetes writes are server-side dry-run unless Commit is true.
func Apply(ctx context.Context, kubernetesClient client.Client, options ApplyOptions) (ApplySummary, error) {
	return apply(ctx, kubernetesClient, options, false)
}

// apply retains an internal test seam for the dormant Dedicated investigation
// executor. The exported Apply path never enables it.
//
//nolint:gocyclo // Batch validation, recording, cancellation, and per-unit continuation remain explicit.
func apply(
	ctx context.Context,
	kubernetesClient client.Client,
	options ApplyOptions,
	allowDedicatedInvestigation bool,
) (ApplySummary, error) {
	if kubernetesClient == nil {
		return ApplySummary{}, fmt.Errorf("kubernetes client is required")
	}
	if strings.TrimSpace(options.PlanPath) == "" {
		return ApplySummary{}, fmt.Errorf("plan path is required")
	}
	if options.ResultPath == "" {
		options.ResultPath = filepath.Join(filepath.Dir(options.PlanPath), applyResultFilename)
	}
	if options.WaitTimeout <= 0 {
		options.WaitTimeout = defaultWaitTimeout
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}

	document, planDigest, err := readPlanDocument(options.PlanPath)
	if err != nil {
		return ApplySummary{}, safeDiagnosticError(err)
	}
	namespaces, units, err := validateApplyDocument(document)
	if err != nil {
		return ApplySummary{}, safeDiagnosticError(err)
	}

	mode := ApplyModeDryRun
	if options.Commit {
		mode = ApplyModeCommit
	}
	runID, err := newRunID()
	if err != nil {
		return ApplySummary{}, safeDiagnosticError(err)
	}
	publication, err := acquireResultPublication(
		options.ResultPath,
		runID,
		planDigest,
	)
	if err != nil {
		return ApplySummary{
			Mode:       mode,
			ResultPath: options.ResultPath,
		}, safeDiagnosticError(err)
	}
	defer publication.close()
	automaticUnits := 0
	unitResults := make([]ApplyUnitResult, 0, len(units))
	for _, unit := range units {
		if unit.Outcome == OutcomeAutomatic {
			automaticUnits++
		}
		unitResults = append(unitResults, ApplyUnitResult{
			UnitID:         unit.ID,
			PlannedOutcome: unit.Outcome,
			Outcome:        ApplyUnitPending,
		})
	}
	recorder := &resultRecorder{
		path:           options.ResultPath,
		automaticUnits: automaticUnits,
		publication:    publication,
		document: ApplyResultDocument{
			DocumentVersion:     ApplyResultVersion,
			PlanDocumentVersion: document.DocumentVersion,
			PlanDigest:          planDigest,
			RunID:               runID,
			Mode:                mode,
			Units:               unitResults,
			Operations:          []ApplyOperationResult{},
		},
	}
	if err := recorder.write(); err != nil {
		return applySummary(mode, recorder), safeDiagnosticError(err)
	}

	var unitFailures []string
	for index, unit := range units {
		if ctx.Err() != nil {
			for _, remaining := range units[index:] {
				if err := recorder.recordUnitTerminal(
					remaining.ID,
					ApplyUnitSkipped,
					fmt.Errorf("apply context ended before this unit was attempted"),
				); err != nil {
					return applySummary(mode, recorder), safeDiagnosticError(err)
				}
			}
			unitFailures = append(unitFailures, "apply context ended before all units were attempted")
			break
		}
		if err := validateUnitEnvelope(unit, namespaces); err != nil {
			if recordErr := recorder.recordUnitTerminal(unit.ID, ApplyUnitSkipped, err); recordErr != nil {
				return applySummary(mode, recorder), safeDiagnosticError(recordErr)
			}
			unitFailures = append(unitFailures, fmt.Sprintf("unit %s: %s", unit.ID, sanitizeDiagnosticMessage(err.Error())))
			continue
		}
		if unit.Outcome == OutcomeManual {
			if err := recorder.recordUnitTerminal(unit.ID, ApplyUnitManual, nil); err != nil {
				return applySummary(mode, recorder), safeDiagnosticError(err)
			}
			continue
		}
		if unit.Outcome != OutcomeAutomatic {
			err := fmt.Errorf("unsupported planned outcome %q", unit.Outcome)
			if recordErr := recorder.recordUnitTerminal(unit.ID, ApplyUnitSkipped, err); recordErr != nil {
				return applySummary(mode, recorder), safeDiagnosticError(recordErr)
			}
			unitFailures = append(unitFailures, fmt.Sprintf("unit %s: %s", unit.ID, sanitizeDiagnosticMessage(err.Error())))
			continue
		}

		prepared, err := validateAutomaticUnitStructure(
			unit,
			namespaces,
			allowDedicatedInvestigation,
		)
		if err != nil {
			err = fmt.Errorf("validate automatic unit %q: %w", unit.ID, err)
			if recordErr := recorder.recordUnitTerminal(unit.ID, ApplyUnitFailed, err); recordErr != nil {
				return applySummary(mode, recorder), safeDiagnosticError(recordErr)
			}
			unitFailures = append(unitFailures, sanitizeDiagnosticMessage(err.Error()))
			continue
		}
		recorder.observeUnit(prepared.unit)
		if err := recorder.write(); err != nil {
			return applySummary(mode, recorder), safeDiagnosticError(err)
		}
		if err := executeApplyUnit(ctx, kubernetesClient, prepared, options, recorder); err != nil {
			if recordErr := recorder.recordUnitTerminal(unit.ID, ApplyUnitFailed, err); recordErr != nil {
				return applySummary(mode, recorder), safeDiagnosticError(recordErr)
			}
			unitFailures = append(
				unitFailures,
				fmt.Sprintf("unit %s: %s", unit.ID, sanitizeDiagnosticMessage(err.Error())),
			)
			continue
		}
		if err := recorder.recordUnitTerminal(unit.ID, ApplyUnitSucceeded, nil); err != nil {
			return applySummary(mode, recorder), safeDiagnosticError(err)
		}
	}
	summary := applySummary(mode, recorder)
	if len(unitFailures) != 0 {
		nonSucceeded := summary.Failed + summary.Skipped
		return summary, fmt.Errorf(
			"%d migration unit(s) did not succeed: %s",
			nonSucceeded,
			strings.Join(unitFailures, "; "),
		)
	}
	return summary, nil
}

func executeApplyUnit(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	options ApplyOptions,
	recorder *resultRecorder,
) error {
	if err := validatePinnedSources(ctx, kubernetesClient, prepared); err != nil {
		current := observeCurrentUnitIdentities(ctx, kubernetesClient, recorder, prepared.unit)
		return recorder.recordFailure(
			prepared.unit.ID,
			"validate-sources",
			prepared.unit.Service,
			current,
			err,
		)
	}
	if err := captureInferenceServiceIdentity(ctx, kubernetesClient, prepared); err != nil {
		return recorder.recordFailure(
			prepared.unit.ID,
			"capture-inference-service",
			prepared.unit.Service,
			nil,
			err,
		)
	}
	recorder.observe(prepared.inferenceService.identity)
	if err := recorder.write(); err != nil {
		return err
	}
	return executePreparedApply(ctx, kubernetesClient, prepared, options, recorder)
}

func observeCurrentUnitIdentities(
	ctx context.Context,
	kubernetesClient client.Client,
	recorder *resultRecorder,
	unit MigrationUnit,
) client.Object {
	var currentService client.Object
	for _, expected := range unit.ObservedResources {
		current, err := fetchPinnedObject(ctx, kubernetesClient, expected)
		if err != nil {
			continue
		}
		recorder.observeObject(current, expected)
		if expected.Role == "service" {
			currentService = current
		}
	}
	return currentService
}

func applySummary(mode ApplyMode, recorder *resultRecorder) ApplySummary {
	counts := recorder.document.Summary
	return ApplySummary{
		Mode:          mode,
		Automatic:     recorder.automaticUnits,
		ManualSkipped: counts.Manual,
		Succeeded:     counts.Succeeded,
		Failed:        counts.Failed,
		Manual:        counts.Manual,
		Skipped:       counts.Skipped,
		Operations:    len(recorder.document.Operations),
		ResultPath:    recorder.path,
	}
}

func readPlanDocument(path string) (PlanDocument, string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return PlanDocument{}, "", fmt.Errorf("read plan %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document PlanDocument
	if err := decoder.Decode(&document); err != nil {
		return PlanDocument{}, "", fmt.Errorf("decode editable plan: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return PlanDocument{}, "", err
	}
	digest := sha256.Sum256(contents)
	return document, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode editable plan: multiple JSON documents are not supported")
		}
		return fmt.Errorf("decode editable plan: %w", err)
	}
	return nil
}

func validateApplyDocument(
	document PlanDocument,
) (map[string]struct{}, []MigrationUnit, error) {
	if document.DocumentVersion != DocumentVersion {
		return nil, nil, fmt.Errorf(
			"unsupported documentVersion %q; require %q",
			document.DocumentVersion,
			DocumentVersion,
		)
	}
	projectionMode := aimv1alpha2.RuntimeProjectionMode(document.RuntimeProjectionMode)
	if projectionMode != aimv1alpha2.RuntimeProjectionModeExhaustive &&
		projectionMode != aimv1alpha2.RuntimeProjectionModeReduced &&
		projectionMode != aimv1alpha2.RuntimeProjectionModeBoth {
		return nil, nil, fmt.Errorf(
			"runtimeProjectionMode must be Exhaustive, Reduced, or Both",
		)
	}
	if !projectionMode.ProjectsPerProfile() {
		for index := range document.Units {
			if document.Units[index].Outcome == OutcomeAutomatic {
				return nil, nil, fmt.Errorf(
					"automatic unit %q is unsupported with Reduced runtime projection",
					document.Units[index].ID,
				)
			}
		}
	}
	namespaces := make(map[string]struct{}, len(document.SelectedNamespaces))
	for _, namespace := range document.SelectedNamespaces {
		if namespace == "" || strings.TrimSpace(namespace) != namespace {
			return nil, nil, fmt.Errorf("selectedNamespaces contains an empty or untrimmed namespace")
		}
		if _, duplicate := namespaces[namespace]; duplicate {
			return nil, nil, fmt.Errorf("selectedNamespaces contains duplicate namespace %q", namespace)
		}
		namespaces[namespace] = struct{}{}
	}
	if len(namespaces) == 0 {
		return nil, nil, fmt.Errorf("selectedNamespaces must contain at least one namespace")
	}

	unitIDs := map[string]struct{}{}
	for index := range document.Units {
		unit := &document.Units[index]
		if unit.ID == "" {
			return nil, nil, fmt.Errorf("units[%d].id is required", index)
		}
		if _, duplicate := unitIDs[unit.ID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate migration unit id %q", unit.ID)
		}
		unitIDs[unit.ID] = struct{}{}
	}
	units := append([]MigrationUnit(nil), document.Units...)
	sort.SliceStable(units, func(i, j int) bool {
		return units[i].ID < units[j].ID
	})
	return namespaces, units, nil
}

func validateUnitEnvelope(unit MigrationUnit, namespaces map[string]struct{}) error {
	if _, selected := namespaces[unit.Namespace]; !selected {
		return fmt.Errorf(
			"migration unit %q namespace %q is outside selectedNamespaces",
			unit.ID,
			unit.Namespace,
		)
	}
	if unit.Service.Namespace != unit.Namespace {
		return fmt.Errorf("migration unit %q service is outside its namespace", unit.ID)
	}
	return nil
}

//nolint:gocyclo // Strict editable-plan validation keeps the bounded operation ordering in one fail-closed gate.
func validateAutomaticUnitStructure(
	unit MigrationUnit,
	namespaces map[string]struct{},
	allowDedicatedInvestigation bool,
) (*preparedApply, error) {
	if len(unit.ManualOutcomes) != 0 {
		return nil, fmt.Errorf("automatic unit contains manual outcomes")
	}
	if unit.ID != unit.Namespace+"/"+unit.Service.Name {
		return nil, fmt.Errorf("automatic unit id must match namespace/service name")
	}
	if unit.TargetProfile == nil {
		return nil, fmt.Errorf("targetProfile is required")
	}
	if len(unit.ProposedOperations) < 3 {
		return nil, fmt.Errorf("profile preparation, profile-cache preparation, and service cutover are required")
	}

	profileOperation := unit.ProposedOperations[0]
	cacheOperation := unit.ProposedOperations[1]
	if profileOperation.Resource.Kind != resourceKindProfile &&
		profileOperation.Resource.Kind != resourceKindClusterProfile {
		return nil, fmt.Errorf("first operation must prepare AIMProfile or AIMClusterProfile")
	}
	if profileOperation.Action != OperationCreate && profileOperation.Action != OperationReuse {
		return nil, fmt.Errorf("first operation has unsupported action %q", profileOperation.Action)
	}
	if (cacheOperation.Action != OperationCreate && cacheOperation.Action != OperationReuse) ||
		cacheOperation.Resource.Kind != resourceKindProfileCache {
		return nil, fmt.Errorf("second operation must create or reuse an AIMProfileCache")
	}
	if err := validateOperationIdentityScope(profileOperation.Resource, unit.Namespace, namespaces); err != nil {
		return nil, err
	}
	if err := validateOperationIdentityScope(cacheOperation.Resource, unit.Namespace, namespaces); err != nil {
		return nil, err
	}
	if !sameTargetIdentity(*unit.TargetProfile, profileOperation.Resource) {
		return nil, fmt.Errorf("targetProfile does not match the first operation resource")
	}

	sources, err := validateObservedResourceSet(unit)
	if err != nil {
		return nil, err
	}
	profileObject, err := validateProfileOperation(profileOperation, sources.template)
	if err != nil {
		return nil, err
	}
	cacheObject, err := validateCacheOperation(cacheOperation, *unit.TargetProfile, unit.Namespace)
	if err != nil {
		return nil, err
	}
	if cacheObject != nil &&
		cacheObject.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated &&
		!allowDedicatedInvestigation {
		return nil, dedicatedApplyProhibitedError()
	}
	if err := validateTargetCacheObservation(cacheOperation, sources.targetCache); err != nil {
		return nil, err
	}

	serviceOperationIndex := 2
	var shared *preparedSharedApply
	var dedicated *preparedDedicatedApply
	if sources.artifact != nil {
		switch {
		case len(unit.ProposedOperations) == 4:
			protectionOperation := unit.ProposedOperations[2]
			if err := validateArtifactProtectionPatch(protectionOperation, *sources.artifact); err != nil {
				return nil, err
			}
			if cacheObject != nil && cacheObject.Spec.Mode != aimv1alpha2.ProfileCacheModeShared {
				return nil, fmt.Errorf("shared cached migration requires a Shared target AIMProfileCache")
			}
			shared = &preparedSharedApply{
				sourceCache:         sources.cache,
				sourceArtifact:      *sources.artifact,
				sourcePVC:           *sources.pvc,
				protectionOperation: protectionOperation,
			}
			serviceOperationIndex = 3
		case len(unit.ProposedOperations) == 3 &&
			cacheOperation.Action == OperationCreate &&
			cacheObject != nil &&
			cacheObject.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated:
			dedicated = &preparedDedicatedApply{
				sourceCache:    sources.cache,
				sourceArtifact: *sources.artifact,
				sourcePVC:      *sources.pvc,
			}
		default:
			return nil, fmt.Errorf(
				"cached migration must be either Shared protection or a fresh Dedicated handoff",
			)
		}
	} else if len(unit.ProposedOperations) != 3 {
		return nil, fmt.Errorf(
			"uncached migration requires profile, cache, and service operations",
		)
	}
	serviceOperation := unit.ProposedOperations[serviceOperationIndex]
	if serviceOperation.Action != OperationPatch ||
		serviceOperation.Resource.Kind != resourceKindService {
		return nil, fmt.Errorf("last operation must patch the existing AIMService")
	}
	if err := validateOperationIdentityScope(serviceOperation.Resource, unit.Namespace, namespaces); err != nil {
		return nil, err
	}
	if err := validateServiceOperationIdentity(unit.Service, serviceOperation.Resource); err != nil {
		return nil, err
	}
	if err := validateServicePatch(serviceOperation, *unit.TargetProfile); err != nil {
		return nil, err
	}

	return &preparedApply{
		unit:                        unit,
		profileOperation:            profileOperation,
		cacheOperation:              cacheOperation,
		serviceOperation:            serviceOperation,
		profileObject:               profileObject,
		cacheObject:                 cacheObject,
		sourceTemplate:              sources.template,
		sourceModel:                 sources.model,
		shared:                      shared,
		dedicated:                   dedicated,
		allowDedicatedInvestigation: allowDedicatedInvestigation,
	}, nil
}

func dedicatedApplyProhibitedError() error {
	return fmt.Errorf(
		"automatic Dedicated migration is prohibited; use the separately approved manual Dedicated handoff runbook at %s",
		DedicatedHandoffRunbookReference,
	)
}

func validateTargetCacheObservation(
	operation ProposedOperation,
	observed *ResourceIdentity,
) error {
	if operation.Action == OperationReuse {
		if observed == nil || !samePinnedResource(*observed, operation.Resource) {
			return fmt.Errorf("reused target AIMProfileCache must match its observed identity")
		}
		return nil
	}
	if observed != nil {
		return fmt.Errorf("created target AIMProfileCache must not have an observed identity")
	}
	return nil
}

func validateObservedResourceSet(unit MigrationUnit) (observedSourceSet, error) {
	allowed := map[string]map[string]string{
		"service": {
			resourceKindService: aimv1alpha1.GroupVersion.String(),
		},
		roleSourceTemplate: {
			resourceKindServiceTemplate: aimv1alpha1.GroupVersion.String(),
			resourceKindClusterTemplate: aimv1alpha1.GroupVersion.String(),
		},
		roleSourceModel: {
			"AIMModel":        aimv1alpha1.GroupVersion.String(),
			"AIMClusterModel": aimv1alpha1.GroupVersion.String(),
		},
		"source-template-cache": {
			"AIMTemplateCache": aimv1alpha1.GroupVersion.String(),
		},
		"source-artifact": {
			resourceKindArtifact: aimv1alpha1.GroupVersion.String(),
		},
		"source-pvc": {
			resourceKindPVC: corev1.SchemeGroupVersion.String(),
		},
		"runtime-config": {
			"AIMRuntimeConfig":        aimv1alpha1.GroupVersion.String(),
			"AIMClusterRuntimeConfig": aimv1alpha1.GroupVersion.String(),
		},
		"referenced-config": {
			"ConfigMap": corev1.SchemeGroupVersion.String(),
		},
		"target-profile": {
			resourceKindProfile:        aimv1alpha2.GroupVersion.String(),
			resourceKindClusterProfile: aimv1alpha2.GroupVersion.String(),
		},
		"target-profile-cache": {
			resourceKindProfileCache: aimv1alpha2.GroupVersion.String(),
		},
	}
	seen := map[string]struct{}{}
	var sources observedSourceSet
	var sourceModelCount, sourceTemplateCount, sourceCacheCount int
	var sourceArtifactCount, sourcePVCCount, serviceCount, targetProfileCount, targetCacheCount int
	for _, identity := range unit.ObservedResources {
		kinds := allowed[identity.Role]
		apiVersion, kindAllowed := kinds[identity.Kind]
		if !kindAllowed || identity.APIVersion != apiVersion {
			return observedSourceSet{}, fmt.Errorf(
				"observed resource role/kind/version %q/%q/%q is unsupported",
				identity.Role,
				identity.Kind,
				identity.APIVersion,
			)
		}
		if err := validatePinnedFields(identity); err != nil {
			return observedSourceSet{}, err
		}
		key := identityKey(identity)
		if _, duplicate := seen[key]; duplicate {
			return observedSourceSet{}, fmt.Errorf(
				"observed resource %s %s/%s is duplicated",
				identity.Kind,
				identity.Namespace,
				identity.Name,
			)
		}
		seen[key] = struct{}{}
		if err := validateIdentityNamespace(identity, unit.Namespace); err != nil {
			return observedSourceSet{}, err
		}
		switch identity.Role {
		case "service":
			serviceCount++
			if !samePinnedService(unit.Service, identity) {
				return observedSourceSet{}, fmt.Errorf(
					"service identity does not match observed service",
				)
			}
		case roleSourceTemplate:
			sourceTemplateCount++
			sources.template = identity
		case roleSourceModel:
			sourceModelCount++
			sources.model = identity
		case "source-template-cache":
			sourceCacheCount++
			sources.cache = identity
		case "source-artifact":
			sourceArtifactCount++
			copy := identity
			sources.artifact = &copy
		case "source-pvc":
			sourcePVCCount++
			copy := identity
			sources.pvc = &copy
		case "target-profile":
			targetProfileCount++
			if unit.TargetProfile == nil || !sameTargetIdentity(*unit.TargetProfile, identity) {
				return observedSourceSet{}, fmt.Errorf(
					"observed target profile does not match targetProfile",
				)
			}
		case "target-profile-cache":
			targetCacheCount++
			copy := identity
			sources.targetCache = &copy
		}
	}
	if serviceCount != 1 {
		return observedSourceSet{}, fmt.Errorf(
			"observedResources must contain exactly one service identity",
		)
	}
	if sourceTemplateCount != 1 {
		return observedSourceSet{}, fmt.Errorf(
			"observedResources must contain exactly one source template",
		)
	}
	if sourceModelCount != 1 {
		return observedSourceSet{}, fmt.Errorf(
			"observedResources must contain exactly one source model",
		)
	}
	if sourceCacheCount != 1 {
		return observedSourceSet{}, fmt.Errorf(
			"observedResources must contain exactly one source template cache",
		)
	}
	if sourceArtifactCount != sourcePVCCount || sourceArtifactCount > 1 {
		return observedSourceSet{}, fmt.Errorf(
			"cached predecessor must pin exactly one source artifact and its PVC",
		)
	}
	if unit.ProposedOperations[0].Action == OperationReuse && targetProfileCount != 1 {
		return observedSourceSet{}, fmt.Errorf(
			"profile reuse must pin exactly one observed target profile",
		)
	}
	if unit.ProposedOperations[0].Action == OperationCreate && targetProfileCount != 0 {
		return observedSourceSet{}, fmt.Errorf(
			"profile create must not contain an observed target profile",
		)
	}
	if targetCacheCount > 1 {
		return observedSourceSet{}, fmt.Errorf(
			"observedResources must not contain multiple target profile caches",
		)
	}
	return sources, nil
}

func validatePinnedFields(identity ResourceIdentity) error {
	if identity.Name == "" || identity.UID == "" ||
		identity.ResourceVersion == "" || identity.ContentDigest == "" {
		return fmt.Errorf(
			"observed %s %s/%s must pin name, uid, resourceVersion, and contentDigest",
			identity.Kind,
			identity.Namespace,
			identity.Name,
		)
	}
	if !strings.HasPrefix(identity.ContentDigest, "sha256:") {
		return fmt.Errorf("observed %s %s has an invalid contentDigest", identity.Kind, identity.Name)
	}
	return nil
}

func validateOperationIdentityScope(
	identity ResourceIdentity,
	unitNamespace string,
	namespaces map[string]struct{},
) error {
	switch identity.Kind {
	case resourceKindProfile:
		if identity.APIVersion != aimv1alpha2.GroupVersion.String() ||
			identity.Namespace != unitNamespace {
			return fmt.Errorf("AIMProfile operation is outside the selected unit namespace")
		}
	case resourceKindClusterProfile:
		if identity.APIVersion != aimv1alpha2.GroupVersion.String() ||
			identity.Namespace != "" {
			return fmt.Errorf("AIMClusterProfile operation must be cluster-scoped")
		}
	case resourceKindProfileCache, resourceKindService:
		if identity.APIVersion != aimv1alpha2.GroupVersion.String() ||
			identity.Namespace != unitNamespace {
			return fmt.Errorf("%s operation is outside the selected unit namespace", identity.Kind)
		}
	case resourceKindArtifact:
		if identity.APIVersion != aimv1alpha1.GroupVersion.String() ||
			identity.Namespace != unitNamespace {
			return fmt.Errorf("AIMArtifact operation is outside the selected unit namespace")
		}
	default:
		return fmt.Errorf("operation kind %q is unsupported", identity.Kind)
	}
	if identity.Namespace != "" {
		if _, selected := namespaces[identity.Namespace]; !selected {
			return fmt.Errorf("%s operation namespace %q is not selected", identity.Kind, identity.Namespace)
		}
	}
	return nil
}

func validateIdentityNamespace(identity ResourceIdentity, unitNamespace string) error {
	switch identity.Kind {
	case resourceKindClusterTemplate, "AIMClusterModel",
		"AIMClusterRuntimeConfig", resourceKindClusterProfile:
		if identity.Namespace != "" {
			return fmt.Errorf("%s %s must be cluster-scoped", identity.Kind, identity.Name)
		}
	default:
		if identity.Namespace != unitNamespace {
			return fmt.Errorf(
				"%s %s/%s is outside automatic unit namespace %q",
				identity.Kind,
				identity.Namespace,
				identity.Name,
				unitNamespace,
			)
		}
	}
	return nil
}

func validateProfileOperation(
	operation ProposedOperation,
	sourceTemplate ResourceIdentity,
) (client.Object, error) {
	if operation.Resource.Role != "target-profile" {
		return nil, fmt.Errorf("profile operation resource role must be target-profile")
	}
	switch operation.Action {
	case OperationCreate:
		if operation.Resource.UID != "" || operation.Resource.ResourceVersion != "" {
			return nil, fmt.Errorf("profile create operation must not pin an existing target identity")
		}
		if operation.Patch != nil || operation.Object == nil {
			return nil, fmt.Errorf("profile create operation must contain only object")
		}
		if mapContainsSecretLiteral(operation.Object) {
			return nil, fmt.Errorf("profile create object contains a secret-looking literal")
		}
		if err := validateObjectMap(operation.Object, operation.Resource, true); err != nil {
			return nil, err
		}
		var object client.Object
		switch operation.Resource.Kind {
		case resourceKindProfile:
			object = &aimv1alpha2.AIMProfile{}
		case resourceKindClusterProfile:
			object = &aimv1alpha2.AIMClusterProfile{}
		}
		if err := decodeStrictMap(operation.Object, object); err != nil {
			return nil, fmt.Errorf("decode target %s: %w", operation.Resource.Kind, err)
		}
		if err := validateProfileProvenance(object, sourceTemplate); err != nil {
			return nil, err
		}
		return object, nil
	case OperationReuse:
		if operation.Object != nil || operation.Patch != nil {
			return nil, fmt.Errorf("profile reuse operation must not contain object or patch")
		}
		if err := validatePinnedFields(operation.Resource); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported profile action %q", operation.Action)
	}
}

func validateCacheOperation(
	operation ProposedOperation,
	targetProfile ResourceIdentity,
	unitNamespace string,
) (*aimv1alpha2.AIMProfileCache, error) {
	if operation.Resource.Role != "target-profile-cache" {
		return nil, fmt.Errorf("profile-cache operation resource role must be target-profile-cache")
	}
	if operation.Action == OperationReuse {
		if operation.Object != nil || operation.Patch != nil {
			return nil, fmt.Errorf("profile-cache reuse operation must not contain object or patch")
		}
		if err := validatePinnedFields(operation.Resource); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if operation.Action != OperationCreate {
		return nil, fmt.Errorf("profile-cache operation has unsupported action %q", operation.Action)
	}
	if operation.Resource.UID != "" || operation.Resource.ResourceVersion != "" {
		return nil, fmt.Errorf("profile-cache create must not pin an existing target identity")
	}
	if operation.Object == nil || operation.Patch != nil {
		return nil, fmt.Errorf("profile-cache create operation must contain only object")
	}
	if mapContainsSecretLiteral(operation.Object) {
		return nil, fmt.Errorf("profile-cache create object contains a secret-looking literal")
	}
	if err := validateObjectMap(operation.Object, operation.Resource, true); err != nil {
		return nil, err
	}
	cache := &aimv1alpha2.AIMProfileCache{}
	if err := decodeStrictMap(operation.Object, cache); err != nil {
		return nil, fmt.Errorf("decode target AIMProfileCache: %w", err)
	}
	if cache.Namespace != unitNamespace ||
		cache.Spec.ProfileName != targetProfile.Name {
		return nil, fmt.Errorf(
			"ticket 0002 requires a same-namespace fresh cache for the target profile",
		)
	}
	if cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared &&
		cache.Spec.Mode != aimv1alpha2.ProfileCacheModeDedicated {
		return nil, fmt.Errorf("profile-cache mode %q is unsupported", cache.Spec.Mode)
	}
	expectedScope := aimv1alpha1.AIMResolutionScopeNamespace
	if targetProfile.Kind == resourceKindClusterProfile {
		expectedScope = aimv1alpha1.AIMResolutionScopeCluster
	}
	if cache.Spec.ProfileScope != expectedScope {
		return nil, fmt.Errorf("profile-cache scope does not match target profile scope")
	}
	return cache, nil
}

func validateArtifactProtectionPatch(
	operation ProposedOperation,
	sourceArtifact ResourceIdentity,
) error {
	if operation.Action != OperationPatch ||
		operation.Resource.Role != roleSourceArtifactProtection ||
		operation.Resource.Kind != resourceKindArtifact {
		return fmt.Errorf("third operation must protect the pinned Shared AIMArtifact")
	}
	if !samePinnedResource(operation.Resource, sourceArtifact) {
		return fmt.Errorf("artifact-protection operation does not match the pinned source artifact")
	}
	if operation.Object != nil || operation.Patch == nil {
		return fmt.Errorf("artifact-protection operation must contain only patch")
	}
	expected := map[string]any{
		"apiVersion": aimv1alpha1.GroupVersion.String(),
		"kind":       resourceKindArtifact,
		"metadata": map[string]any{
			"name":      sourceArtifact.Name,
			"namespace": sourceArtifact.Namespace,
			"annotations": map[string]any{
				aimv1alpha1.ArtifactEvictionProtectedAnnotation: aimv1alpha1.ArtifactEvictionProtectedValue,
			},
		},
	}
	if !equality.Semantic.DeepEqual(operation.Patch, expected) {
		return fmt.Errorf("artifact-protection patch may only set the existing eviction-protection annotation")
	}
	return nil
}

func validateServicePatch(operation ProposedOperation, targetProfile ResourceIdentity) error {
	if operation.Resource.Role != "service-cutover" {
		return fmt.Errorf("service patch operation resource role must be service-cutover")
	}
	if operation.Object != nil || operation.Patch == nil {
		return fmt.Errorf("service patch operation must contain only patch")
	}
	if mapContainsSecretLiteral(operation.Patch) {
		return fmt.Errorf("service patch contains a secret-looking literal")
	}
	expected := map[string]any{
		"apiVersion": aimv1alpha2.GroupVersion.String(),
		"kind":       resourceKindService,
		"metadata": map[string]any{
			"name":      operation.Resource.Name,
			"namespace": operation.Resource.Namespace,
			"annotations": map[string]any{
				constants.AnnotationReconcilerPipeline: constants.ReconcilerPipelineProfile,
				constants.AnnotationMigrationPreserveInferenceServiceGeneration: strconv.FormatInt(
					operation.Resource.Generation+1,
					10,
				),
			},
		},
		"spec": map[string]any{
			"profile":  map[string]any{"name": targetProfile.Name},
			"template": nil,
		},
	}
	if !equality.Semantic.DeepEqual(operation.Patch, expected) {
		return fmt.Errorf(
			"service patch may only set the exact profile, clear template, and select the generation-pinned profile pipeline",
		)
	}
	return nil
}

func validateObjectMap(
	object map[string]any,
	identity ResourceIdentity,
	rejectAssignedMetadata bool,
) error {
	if stringValue(object["apiVersion"]) != identity.APIVersion ||
		stringValue(object["kind"]) != identity.Kind {
		return fmt.Errorf("%s object apiVersion/kind does not match its operation", identity.Kind)
	}
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s object metadata is required", identity.Kind)
	}
	if stringValue(metadata["name"]) != identity.Name ||
		stringValue(metadata["namespace"]) != identity.Namespace {
		return fmt.Errorf("%s object path does not match its operation resource", identity.Kind)
	}
	if _, hasStatus := object["status"]; hasStatus {
		return fmt.Errorf("%s create object must not contain status", identity.Kind)
	}
	if rejectAssignedMetadata {
		for _, field := range []string{
			"creationTimestamp", "deletionTimestamp", "finalizers", "generation",
			"managedFields", "ownerReferences", "resourceVersion", "uid",
		} {
			if value, present := metadata[field]; present && value != nil {
				return fmt.Errorf("%s create object contains unsupported metadata.%s", identity.Kind, field)
			}
		}
	}
	return nil
}

func decodeStrictMap(value map[string]any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func mapContainsSecretLiteral(value map[string]any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return true
	}
	var copied map[string]any
	if json.Unmarshal(encoded, &copied) != nil {
		return true
	}
	return sanitizeMap(copied, false)
}

func validateServiceOperationIdentity(service, operation ResourceIdentity) error {
	if operation.APIVersion != aimv1alpha2.GroupVersion.String() ||
		operation.Kind != resourceKindService ||
		operation.Name != service.Name ||
		operation.Namespace != service.Namespace ||
		operation.UID != service.UID ||
		operation.ResourceVersion != service.ResourceVersion ||
		operation.Generation != service.Generation ||
		operation.ContentDigest != service.ContentDigest {
		return fmt.Errorf("service cutover identity does not match the pinned source service")
	}
	return nil
}

func samePinnedService(left, right ResourceIdentity) bool {
	return left.Kind == right.Kind &&
		left.Name == right.Name &&
		left.Namespace == right.Namespace &&
		left.UID == right.UID &&
		left.ResourceVersion == right.ResourceVersion &&
		left.Generation == right.Generation &&
		left.ContentDigest == right.ContentDigest
}

func samePinnedResource(left, right ResourceIdentity) bool {
	return left.APIVersion == right.APIVersion &&
		left.Kind == right.Kind &&
		left.Name == right.Name &&
		left.Namespace == right.Namespace &&
		left.UID == right.UID &&
		left.ResourceVersion == right.ResourceVersion &&
		left.Generation == right.Generation &&
		left.ContentDigest == right.ContentDigest
}

func sameTargetIdentity(left, right ResourceIdentity) bool {
	return left.Kind == right.Kind &&
		left.APIVersion == right.APIVersion &&
		left.Namespace == right.Namespace &&
		left.Name == right.Name &&
		left.UID == right.UID &&
		left.ResourceVersion == right.ResourceVersion &&
		left.Generation == right.Generation &&
		left.ContentDigest == right.ContentDigest
}

func validateProfileProvenance(profile client.Object, sourceTemplate ResourceIdentity) error {
	annotations := profile.GetAnnotations()
	if annotations[annotationMigrationSourceKind] != sourceTemplate.Kind ||
		annotations[annotationMigrationSourceNamespace] != sourceTemplate.Namespace ||
		annotations[annotationMigrationSourceName] != sourceTemplate.Name ||
		annotations[annotationMigrationSourceUID] != sourceTemplate.UID {
		return fmt.Errorf("target profile provenance does not exactly match the pinned source template")
	}
	if annotations[constants.AimLabelDomain+"/profile-yaml-contract"] == "" {
		return fmt.Errorf("target profile is missing its profile YAML contract annotation")
	}
	return nil
}

func validateReusableProfileProvenance(
	profile client.Object,
	sourceTemplate, sourceModel ResourceIdentity,
) error {
	if profile.GetAnnotations()[constants.AimLabelDomain+"/profile-yaml-contract"] == "" {
		return fmt.Errorf("target profile is missing its profile YAML contract annotation")
	}
	if validateProfileProvenance(profile, sourceTemplate) == nil {
		return nil
	}
	status, _ := profileStatus(profile)
	if status == nil || status.SourceModel == nil {
		return fmt.Errorf("target profile has no valid template or source-model provenance")
	}
	source := status.SourceModel
	if source.Name != sourceModel.Name ||
		string(source.Kind) != sourceModel.Kind ||
		source.Namespace != sourceModel.Namespace {
		return fmt.Errorf("target profile source-model status does not match the pinned source model")
	}
	for _, owner := range profile.GetOwnerReferences() {
		if (owner.APIVersion == aimv1alpha1.GroupVersion.String() ||
			owner.APIVersion == aimv1alpha2.GroupVersion.String()) &&
			owner.Kind == sourceModel.Kind &&
			owner.Name == sourceModel.Name &&
			owner.UID == types.UID(sourceModel.UID) {
			return nil
		}
	}
	return fmt.Errorf("target profile owner does not match the pinned source model")
}

func validatePinnedSources(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) error {
	current := make(map[string]*unstructured.Unstructured, len(prepared.unit.ObservedResources))
	for _, identity := range prepared.unit.ObservedResources {
		object, err := fetchPinnedObject(ctx, kubernetesClient, identity)
		if err != nil {
			return err
		}
		if err := comparePinnedIdentity(object, identity); err != nil {
			return err
		}
		current[identity.Role] = object
	}

	service := &aimv1alpha2.AIMService{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{Namespace: prepared.unit.Namespace, Name: prepared.unit.Service.Name},
		service,
	); err != nil {
		return kubernetesFailure("read current v1alpha2 AIMService", prepared.unit.Service, err)
	}
	if err := comparePinnedIdentity(service, prepared.unit.Service); err != nil {
		return err
	}
	if service.Spec.GetCachingMode() == aimv1alpha1.CachingModeDedicated &&
		!prepared.allowDedicatedInvestigation {
		return dedicatedApplyProhibitedError()
	}
	if err := validateCurrentSourceClosure(
		service,
		current,
		prepared.shared != nil || prepared.dedicated != nil,
	); err != nil {
		return err
	}
	var targetProfile client.Object
	if prepared.profileOperation.Action == OperationReuse {
		profile, err := fetchTargetProfile(ctx, kubernetesClient, prepared.profileOperation.Resource)
		if err != nil {
			return err
		}
		if err := validateReusableProfileProvenance(
			profile,
			prepared.sourceTemplate,
			prepared.sourceModel,
		); err != nil {
			return err
		}
		targetProfile = profile
	} else {
		targetProfile = prepared.profileObject
	}
	desiredCache, err := buildControllerCompatibleProfileCacheFromSpec(
		service.Namespace,
		service.Name,
		service.UID,
		&service.Spec,
		prepared.profileOperation.Resource,
		targetProfile,
	)
	if err != nil {
		return err
	}
	if err := validatePlannedProfileCache(
		ctx,
		kubernetesClient,
		prepared,
		desiredCache,
		service,
	); err != nil {
		return err
	}
	prepared.cacheObject = desiredCache
	prepared.service = service
	if err := validateProfileCacheCandidates(ctx, kubernetesClient, prepared); err != nil {
		return err
	}
	if prepared.shared != nil {
		if err := validateSharedMigrationState(
			ctx,
			kubernetesClient,
			prepared,
			targetProfile,
			true,
		); err != nil {
			return err
		}
	}
	if prepared.dedicated != nil {
		if err := validateInitialDedicatedMigrationState(
			ctx,
			kubernetesClient,
			prepared,
			targetProfile,
		); err != nil {
			return err
		}
	}
	return nil
}

func fetchPinnedObject(
	ctx context.Context,
	kubernetesClient client.Client,
	identity ResourceIdentity,
) (*unstructured.Unstructured, error) {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(schema.FromAPIVersionAndKind(identity.APIVersion, identity.Kind))
	key := client.ObjectKey{Namespace: identity.Namespace, Name: identity.Name}
	if err := kubernetesClient.Get(ctx, key, object); err != nil {
		return nil, kubernetesFailure("read pinned source", identity, err)
	}
	return object, nil
}

func comparePinnedIdentity(object client.Object, expected ResourceIdentity) error {
	if string(object.GetUID()) != expected.UID {
		return fmt.Errorf("%s %s/%s UID changed", expected.Kind, expected.Namespace, expected.Name)
	}
	if pinsExactResourceVersion(expected.Kind) &&
		object.GetResourceVersion() != expected.ResourceVersion {
		return fmt.Errorf(
			"%s %s/%s resourceVersion changed",
			expected.Kind,
			expected.Namespace,
			expected.Name,
		)
	}
	if object.GetGeneration() != expected.Generation {
		return fmt.Errorf("%s %s/%s generation changed", expected.Kind, expected.Namespace, expected.Name)
	}
	digest, secretLiteral, err := contentDigest(object, expected.APIVersion, expected.Kind)
	if err != nil {
		return fmt.Errorf("%s %s/%s relevant content could not be verified", expected.Kind, expected.Namespace, expected.Name)
	}
	if secretLiteral {
		return fmt.Errorf("%s %s/%s contains a secret-looking literal", expected.Kind, expected.Namespace, expected.Name)
	}
	if digest != expected.ContentDigest {
		return fmt.Errorf("%s %s/%s relevant content changed", expected.Kind, expected.Namespace, expected.Name)
	}
	return nil
}

func pinsExactResourceVersion(kind string) bool {
	switch kind {
	case resourceKindRuntimeConfig, resourceKindClusterRuntimeConfig:
		return true
	default:
		return false
	}
}

func validateCurrentSourceClosure(
	service *aimv1alpha2.AIMService,
	current map[string]*unstructured.Unstructured,
	allowCachedArtifact bool,
) error {
	if service.DeletionTimestamp != nil ||
		service.Generation != service.Status.ObservedGeneration ||
		service.Status.Status != constants.AIMStatusRunning {
		return fmt.Errorf("source AIMService is no longer current and Running")
	}
	if service.Spec.Profile != nil ||
		service.Status.ReconcilerPipeline == constants.ReconcilerPipelineProfile {
		return fmt.Errorf("source AIMService is no longer on the template pipeline")
	}
	template := current[roleSourceTemplate]
	model := current[roleSourceModel]
	if template == nil || model == nil {
		return fmt.Errorf("source template and model are required")
	}
	if err := validateReadyObservedObject(template); err != nil {
		return err
	}
	if err := validateReadyObservedObject(model); err != nil {
		return err
	}
	if !referenceMatches(service.Status.ResolvedTemplate, template) {
		return fmt.Errorf("AIMService resolved template no longer matches the pinned source template")
	}
	if !referenceMatches(service.Status.ResolvedModel, model) {
		return fmt.Errorf("AIMService resolved model no longer matches the pinned source model")
	}
	if modelName, _, _ := unstructured.NestedString(template.Object, "spec", "modelName"); modelName != "" &&
		modelName != model.GetName() {
		return fmt.Errorf("source template now points at a different model")
	}

	cache := current["source-template-cache"]
	var cacheReference *aimv1alpha1.AIMResolvedReference
	if service.Status.Cache != nil {
		cacheReference = service.Status.Cache.TemplateCacheRef
	}
	return validateSourceTemplateCacheClosure(cacheReference, cache, allowCachedArtifact)
}

func validateSourceTemplateCacheClosure(
	cacheReference *aimv1alpha1.AIMResolvedReference,
	cache *unstructured.Unstructured,
	allowCachedArtifact bool,
) error {
	switch {
	case cacheReference == nil && cache != nil:
		return fmt.Errorf("plan contains a source template cache not referenced by the AIMService")
	case cacheReference != nil && cache == nil:
		return fmt.Errorf("AIMService now references an unpinned source template cache")
	case cacheReference != nil && !referenceMatches(cacheReference, cache):
		return fmt.Errorf("AIMService source template cache identity changed")
	case cache != nil:
		if err := validateReadyObservedObject(cache); err != nil {
			return err
		}
		artifacts, found, err := unstructured.NestedMap(cache.Object, "status", "artifacts")
		if err != nil {
			return fmt.Errorf("source template cache artifacts could not be inspected")
		}
		if !allowCachedArtifact && found && len(artifacts) != 0 {
			return fmt.Errorf("source template cache has predecessor artifacts; cached migration is deferred")
		}
		if allowCachedArtifact && (!found || len(artifacts) != 1) {
			return fmt.Errorf("cached source template cache must report exactly one predecessor artifact")
		}
	}
	return nil
}

func validatePlannedProfileCache(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	desired *aimv1alpha2.AIMProfileCache,
	service *aimv1alpha2.AIMService,
) error {
	operation := prepared.cacheOperation
	if operation.Resource.Name != desired.Name ||
		operation.Resource.Namespace != desired.Namespace {
		return fmt.Errorf(
			"target AIMProfileCache name %q does not match the controller naming contract",
			operation.Resource.Name,
		)
	}
	switch operation.Action {
	case OperationCreate:
		planned := prepared.cacheObject
		if planned == nil ||
			!equality.Semantic.DeepEqual(planned.Spec, desired.Spec) ||
			!equality.Semantic.DeepEqual(planned.Labels, desired.Labels) ||
			len(planned.Annotations) != 0 ||
			len(planned.OwnerReferences) != 0 ||
			len(planned.Finalizers) != 0 {
			return fmt.Errorf("target AIMProfileCache does not match the controller-compatible complete shape")
		}
	case OperationReuse:
		existing := &aimv1alpha2.AIMProfileCache{}
		if err := kubernetesClient.Get(
			ctx,
			client.ObjectKey{Namespace: operation.Resource.Namespace, Name: operation.Resource.Name},
			existing,
		); err != nil {
			return kubernetesFailure("read pinned target profile cache", operation.Resource, err)
		}
		if err := comparePinnedIdentity(existing, operation.Resource); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported AIMProfileCache operation %q", operation.Action)
	}

	if desired.Spec.Mode == aimv1alpha2.ProfileCacheModeDedicated {
		desired.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: aimv1alpha2.GroupVersion.String(),
			Kind:       resourceKindService,
			Name:       service.Name,
			UID:        service.UID,
			Controller: ptr.To(true),
		}}
	}
	if operation.Action == OperationReuse {
		existing := &aimv1alpha2.AIMProfileCache{}
		if err := kubernetesClient.Get(
			ctx,
			client.ObjectKey{Namespace: operation.Resource.Namespace, Name: operation.Resource.Name},
			existing,
		); err != nil {
			return kubernetesFailure("read pinned target profile cache", operation.Resource, err)
		}
		if err := matchFreshProfileCache(existing, desired); err != nil {
			return err
		}
	}
	return nil
}

func validateProfileCacheCandidates(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) error {
	if prepared.shared == nil {
		return nil
	}
	var caches aimv1alpha2.AIMProfileCacheList
	if err := kubernetesClient.List(
		ctx,
		&caches,
		client.InNamespace(prepared.unit.Namespace),
	); err != nil {
		return fmt.Errorf("list Shared AIMProfileCache candidates: %w", err)
	}
	var matches []*aimv1alpha2.AIMProfileCache
	var deterministic *aimv1alpha2.AIMProfileCache
	for index := range caches.Items {
		cache := &caches.Items[index]
		if cache.Name == prepared.cacheObject.Name {
			deterministic = cache
		}
		if cache.Spec.ProfileName == prepared.profileOperation.Resource.Name &&
			normalizedProfileScope(cache.Spec.ProfileScope) ==
				normalizedProfileScope(prepared.cacheObject.Spec.ProfileScope) &&
			cache.Spec.Mode == aimv1alpha2.ProfileCacheModeShared {
			matches = append(matches, cache)
		}
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, cache := range matches {
			names = append(names, cache.Name)
		}
		sort.Strings(names)
		return fmt.Errorf(
			"multiple Shared AIMProfileCache candidates match the target profile: %s",
			strings.Join(names, ", "),
		)
	}
	if len(matches) == 1 && matches[0].Name != prepared.cacheObject.Name {
		return fmt.Errorf(
			"ambiguous Shared AIMProfileCache candidate %q conflicts with deterministic target %q",
			matches[0].Name,
			prepared.cacheObject.Name,
		)
	}
	if deterministic != nil {
		if err := matchFreshProfileCache(deterministic, prepared.cacheObject); err != nil {
			return err
		}
	}
	if prepared.cacheOperation.Action == OperationReuse && len(matches) != 1 {
		return fmt.Errorf("pinned target AIMProfileCache is no longer a unique Shared candidate")
	}
	return nil
}

func normalizedProfileScope(scope aimv1alpha1.AIMResolutionScope) aimv1alpha1.AIMResolutionScope {
	if scope == "" {
		return aimv1alpha1.AIMResolutionScopeNamespace
	}
	return scope
}

func validateSharedMigrationState(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	targetProfile client.Object,
	requirePinnedVersions bool,
) error {
	if prepared.shared == nil {
		return nil
	}
	source, err := readSharedSourceState(
		ctx,
		kubernetesClient,
		prepared.shared,
		requirePinnedVersions,
	)
	if err != nil {
		return err
	}
	profileSpec, profileScope, err := targetProfileSpec(targetProfile)
	if err != nil {
		return err
	}
	if profileScope != prepared.cacheObject.Spec.ProfileScope ||
		len(profileSpec.ModelSources) != 1 {
		return fmt.Errorf("target profile scope or model-source cardinality is incompatible with Shared adoption")
	}
	modelSource := profileSpec.ModelSources[0]
	prepared.shared.modelID = modelSource.ModelID
	if source.resolved.Model != modelSource.ModelID {
		return fmt.Errorf("shared source cache reports a different model identity")
	}
	if source.cache.Spec.TemplateName != prepared.sourceTemplate.Name ||
		aimv1alpha1.AIMResolutionScope(source.cache.Spec.TemplateScope) != profileScope ||
		(source.cache.Status.ResolvedTemplateKind != "" &&
			source.cache.Status.ResolvedTemplateKind != prepared.sourceTemplate.Kind) {
		return fmt.Errorf("shared source cache scope or template identity changed")
	}
	if len(source.cache.Spec.ModelSources) > 0 &&
		!equality.Semantic.DeepEqual(source.cache.Spec.ModelSources, profileSpec.ModelSources) {
		return fmt.Errorf("shared source cache model sources do not exactly match the target profile")
	}
	if source.cache.Spec.DownloadImage != "" ||
		len(source.cache.Spec.ImagePullSecrets) != 0 {
		return fmt.Errorf("shared source cache download image or pull-secret inputs are unsupported")
	}
	if err := validateSharedArtifactSemantics(
		ctx,
		kubernetesClient,
		prepared.cacheObject,
		source.cache,
		source.artifact,
		source.claim,
		modelSource,
	); err != nil {
		return err
	}
	return validateSharedArtifactCandidates(
		ctx,
		kubernetesClient,
		prepared.cacheObject,
		source.artifact,
		modelSource,
	)
}

func readSharedSourceState(
	ctx context.Context,
	kubernetesClient client.Client,
	shared *preparedSharedApply,
	requirePinnedArtifact bool,
) (*sharedSourceState, error) {
	sourceCache := &aimv1alpha1.AIMTemplateCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: shared.sourceCache.Namespace,
			Name:      shared.sourceCache.Name,
		},
		sourceCache,
	); err != nil {
		return nil, kubernetesFailure("read Shared source template cache", shared.sourceCache, err)
	}
	artifact := &aimv1alpha1.AIMArtifact{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: shared.sourceArtifact.Namespace,
			Name:      shared.sourceArtifact.Name,
		},
		artifact,
	); err != nil {
		return nil, kubernetesFailure("read Shared source artifact", shared.sourceArtifact, err)
	}
	claim := &corev1.PersistentVolumeClaim{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: shared.sourcePVC.Namespace,
			Name:      shared.sourcePVC.Name,
		},
		claim,
	); err != nil {
		return nil, kubernetesFailure("read Shared source PVC", shared.sourcePVC, err)
	}
	if requirePinnedArtifact {
		if err := comparePinnedIdentity(artifact, shared.sourceArtifact); err != nil {
			return nil, err
		}
	}
	for _, item := range []struct {
		object   client.Object
		identity ResourceIdentity
	}{
		{object: sourceCache, identity: shared.sourceCache},
		{object: claim, identity: shared.sourcePVC},
	} {
		if err := comparePinnedIdentity(item.object, item.identity); err != nil {
			return nil, err
		}
	}
	if string(sourceCache.UID) != shared.sourceCache.UID ||
		string(artifact.UID) != shared.sourceArtifact.UID ||
		string(claim.UID) != shared.sourcePVC.UID {
		return nil, fmt.Errorf("shared source cache, artifact, or PVC identity changed")
	}
	if sourceCache.DeletionTimestamp != nil ||
		sourceCache.Generation != sourceCache.Status.ObservedGeneration ||
		sourceCache.Status.Status != constants.AIMStatusReady {
		return nil, fmt.Errorf("shared source AIMTemplateCache is not current and Ready")
	}
	if sourceCache.Spec.Mode != aimv1alpha1.TemplateCacheModeShared ||
		len(sourceCache.OwnerReferences) != 0 {
		return nil, fmt.Errorf("shared source AIMTemplateCache mode or ownership changed")
	}
	if artifact.DeletionTimestamp != nil ||
		artifact.Generation != artifact.Status.ObservedGeneration ||
		artifact.Status.Status != constants.AIMStatusReady ||
		artifact.Status.Mode != aimv1alpha1.ArtifactModeShared ||
		len(artifact.OwnerReferences) != 0 {
		return nil, fmt.Errorf("shared source AIMArtifact is not current, Ready, and ownerless")
	}
	if claim.DeletionTimestamp != nil ||
		claim.Status.Phase != corev1.ClaimBound ||
		!hasOwnerUID(claim.OwnerReferences, artifact.UID) {
		return nil, fmt.Errorf("shared source PVC is not Bound and owned by the pinned artifact")
	}
	if artifact.Status.PersistentVolumeClaim != claim.Name {
		return nil, fmt.Errorf("shared source artifact reports a different PVC")
	}

	resolved, err := exactResolvedArtifact(sourceCache.Status.Artifacts)
	if err != nil {
		return nil, err
	}
	if resolved.Name != artifact.Name ||
		resolved.UID != string(artifact.UID) ||
		resolved.PersistentVolumeClaim != claim.Name ||
		resolved.Status != constants.AIMStatusReady {
		return nil, fmt.Errorf("shared source cache does not report the exact artifact UID and PVC")
	}
	return &sharedSourceState{
		cache:    sourceCache,
		artifact: artifact,
		claim:    claim,
		resolved: resolved,
	}, nil
}

//nolint:gocyclo // The Dedicated preflight intentionally checks the complete pinned closure in one fail-closed gate.
func validateInitialDedicatedMigrationState(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	targetProfile client.Object,
) error {
	dedicated := prepared.dedicated
	if dedicated == nil {
		return nil
	}
	sourceCache := &aimv1alpha1.AIMTemplateCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: dedicated.sourceCache.Namespace,
			Name:      dedicated.sourceCache.Name,
		},
		sourceCache,
	); err != nil {
		return kubernetesFailure("read Dedicated source template cache", dedicated.sourceCache, err)
	}
	artifact := &aimv1alpha1.AIMArtifact{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: dedicated.sourceArtifact.Namespace,
			Name:      dedicated.sourceArtifact.Name,
		},
		artifact,
	); err != nil {
		return kubernetesFailure("read Dedicated source artifact", dedicated.sourceArtifact, err)
	}
	claim := &corev1.PersistentVolumeClaim{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{
			Namespace: dedicated.sourcePVC.Namespace,
			Name:      dedicated.sourcePVC.Name,
		},
		claim,
	); err != nil {
		return kubernetesFailure("read Dedicated source PVC", dedicated.sourcePVC, err)
	}
	for _, item := range []struct {
		object   client.Object
		identity ResourceIdentity
	}{
		{object: sourceCache, identity: dedicated.sourceCache},
		{object: artifact, identity: dedicated.sourceArtifact},
		{object: claim, identity: dedicated.sourcePVC},
	} {
		if err := comparePinnedIdentity(item.object, item.identity); err != nil {
			return err
		}
	}
	if sourceCache.DeletionTimestamp != nil ||
		sourceCache.Generation != sourceCache.Status.ObservedGeneration ||
		sourceCache.Status.Status != constants.AIMStatusReady ||
		sourceCache.Spec.Mode != aimv1alpha1.TemplateCacheModeDedicated ||
		!dedicatedSourceCacheOwnerMatches(sourceCache, prepared.service) {
		return fmt.Errorf("dedicated source AIMTemplateCache is not current, Ready, and solely controlled by the pinned AIMService")
	}
	if sourceCache.Spec.TemplateName != prepared.sourceTemplate.Name ||
		aimv1alpha1.AIMResolutionScope(sourceCache.Spec.TemplateScope) !=
			prepared.cacheObject.Spec.ProfileScope ||
		(sourceCache.Status.ResolvedTemplateKind != "" &&
			sourceCache.Status.ResolvedTemplateKind != prepared.sourceTemplate.Kind) {
		return fmt.Errorf("dedicated source AIMTemplateCache scope or template identity changed")
	}
	if artifact.DeletionTimestamp != nil ||
		artifact.Generation != artifact.Status.ObservedGeneration ||
		artifact.Status.Status != constants.AIMStatusReady ||
		artifact.Status.Mode != aimv1alpha1.ArtifactModeDedicated {
		return fmt.Errorf("dedicated source AIMArtifact is not current and Ready")
	}
	oldOwner, ok := dedicatedArtifactControllerOwner(artifact, sourceCache)
	if !ok {
		return fmt.Errorf("dedicated source AIMArtifact does not have the source AIMTemplateCache as its exact sole controller owner")
	}
	if claim.DeletionTimestamp != nil ||
		claim.Status.Phase != corev1.ClaimBound ||
		!dedicatedPVCControllerOwnerMatches(claim, artifact) {
		return fmt.Errorf("dedicated source PVC is not Bound and solely controlled by the pinned artifact")
	}
	if artifact.Status.PersistentVolumeClaim != claim.Name {
		return fmt.Errorf("dedicated source artifact reports a different PVC")
	}
	resolved, err := exactResolvedArtifact(sourceCache.Status.Artifacts)
	if err != nil {
		return fmt.Errorf("dedicated source cache artifact is ambiguous: %w", err)
	}
	if resolved.Name != artifact.Name ||
		resolved.UID != string(artifact.UID) ||
		resolved.PersistentVolumeClaim != claim.Name ||
		resolved.Status != constants.AIMStatusReady {
		return fmt.Errorf("dedicated source cache does not report the exact artifact UID and PVC")
	}
	profileSpec, profileScope, err := targetProfileSpec(targetProfile)
	if err != nil {
		return err
	}
	if profileScope != prepared.cacheObject.Spec.ProfileScope ||
		len(profileSpec.ModelSources) != 1 {
		return fmt.Errorf("target profile scope or model-source cardinality is incompatible with Dedicated handoff")
	}
	modelSource := profileSpec.ModelSources[0]
	if resolved.Model != modelSource.ModelID {
		return fmt.Errorf("dedicated source cache reports a different model identity")
	}
	if len(sourceCache.Spec.ModelSources) > 0 &&
		!equality.Semantic.DeepEqual(sourceCache.Spec.ModelSources, profileSpec.ModelSources) {
		return fmt.Errorf("dedicated source cache model sources do not exactly match the target profile")
	}
	if sourceCache.Spec.DownloadImage != "" ||
		len(sourceCache.Spec.ImagePullSecrets) != 0 {
		return fmt.Errorf("dedicated source cache download image or pull-secret inputs are unsupported")
	}
	if err := validateSharedArtifactSemantics(
		ctx,
		kubernetesClient,
		prepared.cacheObject,
		sourceCache,
		artifact,
		claim,
		modelSource,
	); err != nil {
		return fmt.Errorf("dedicated artifact semantics are incompatible with target cache adoption: %w", err)
	}

	dedicated.modelID = modelSource.ModelID
	dedicated.initialCache = sourceCache.DeepCopy()
	dedicated.initialArtifact = artifact.DeepCopy()
	dedicated.initialClaim = claim.DeepCopy()
	dedicated.oldOwner = *oldOwner.DeepCopy()
	return nil
}

func dedicatedSourceCacheOwnerMatches(
	cache *aimv1alpha1.AIMTemplateCache,
	service *aimv1alpha2.AIMService,
) bool {
	if cache == nil || service == nil || len(cache.OwnerReferences) != 1 {
		return false
	}
	owner := metav1.GetControllerOf(cache)
	return owner != nil &&
		(owner.APIVersion == aimv1alpha1.GroupVersion.String() ||
			owner.APIVersion == aimv1alpha2.GroupVersion.String()) &&
		owner.Kind == resourceKindService &&
		owner.Name == service.Name &&
		owner.UID == service.UID &&
		owner.Controller != nil &&
		*owner.Controller
}

func dedicatedArtifactControllerOwner(
	artifact *aimv1alpha1.AIMArtifact,
	cache *aimv1alpha1.AIMTemplateCache,
) (*metav1.OwnerReference, bool) {
	if artifact == nil || cache == nil || len(artifact.OwnerReferences) != 1 {
		return nil, false
	}
	owner := metav1.GetControllerOf(artifact)
	return owner, owner != nil &&
		owner.APIVersion == aimv1alpha1.GroupVersion.String() &&
		owner.Kind == "AIMTemplateCache" &&
		owner.Name == cache.Name &&
		owner.UID == cache.UID &&
		owner.Controller != nil &&
		*owner.Controller
}

func dedicatedPVCControllerOwnerMatches(
	claim *corev1.PersistentVolumeClaim,
	artifact *aimv1alpha1.AIMArtifact,
) bool {
	if claim == nil || artifact == nil || len(claim.OwnerReferences) != 1 {
		return false
	}
	owner := metav1.GetControllerOf(claim)
	return owner != nil &&
		owner.APIVersion == aimv1alpha1.GroupVersion.String() &&
		owner.Kind == resourceKindArtifact &&
		owner.Name == artifact.Name &&
		owner.UID == artifact.UID &&
		owner.Controller != nil &&
		*owner.Controller
}

func targetProfileSpec(
	profile client.Object,
) (*aimv1alpha2.AIMProfileSpecCommon, aimv1alpha1.AIMResolutionScope, error) {
	switch typed := profile.(type) {
	case *aimv1alpha2.AIMProfile:
		return &typed.Spec.AIMProfileSpecCommon, aimv1alpha1.AIMResolutionScopeNamespace, nil
	case *aimv1alpha2.AIMClusterProfile:
		return &typed.Spec.AIMProfileSpecCommon, aimv1alpha1.AIMResolutionScopeCluster, nil
	default:
		return nil, "", fmt.Errorf("target profile has unsupported type %T", profile)
	}
}

func exactResolvedArtifact(
	artifacts map[string]aimv1alpha1.AIMResolvedArtifact,
) (aimv1alpha1.AIMResolvedArtifact, error) {
	if len(artifacts) != 1 {
		return aimv1alpha1.AIMResolvedArtifact{}, fmt.Errorf(
			"source cache must report exactly one artifact",
		)
	}
	for _, resolved := range artifacts {
		return resolved, nil
	}
	return aimv1alpha1.AIMResolvedArtifact{}, fmt.Errorf("source cache artifact is unavailable")
}

func validateSharedArtifactSemantics(
	ctx context.Context,
	kubernetesClient client.Client,
	targetCache *aimv1alpha2.AIMProfileCache,
	sourceCache *aimv1alpha1.AIMTemplateCache,
	artifact *aimv1alpha1.AIMArtifact,
	claim *corev1.PersistentVolumeClaim,
	modelSource aimv1alpha1.AIMModelSource,
) error {
	if artifact.Spec.Type != "" && artifact.Spec.Type != aimv1alpha1.ArtifactTypeModel {
		return fmt.Errorf("shared source artifact type is not model")
	}
	if artifact.Spec.SourceURI != modelSource.SourceURI {
		return fmt.Errorf("shared source artifact source URI does not exactly match the target profile")
	}
	if artifact.Spec.ModelID != modelSource.ModelID {
		return fmt.Errorf("shared source artifact model identity does not exactly match the target profile")
	}
	if modelSource.Size != nil && artifact.Spec.Size.Cmp(*modelSource.Size) != 0 {
		return fmt.Errorf("shared source artifact size does not exactly match the target profile")
	}
	if sourceCache.Spec.RuntimeConfigRef != targetCache.Spec.RuntimeConfigRef ||
		artifact.Spec.RuntimeConfigRef != targetCache.Spec.RuntimeConfigRef {
		return fmt.Errorf("shared source and target runtime configuration references do not exactly match")
	}
	sourceEnv := utils.MergeEnvVars(sourceCache.Spec.Env, modelSource.Env)
	targetEnv := utils.MergeEnvVars(targetCache.Spec.Env, modelSource.Env)
	if !semanticEnvEqual(sourceEnv, targetEnv) ||
		!semanticEnvEqual(artifact.Spec.Env, targetEnv) {
		return fmt.Errorf("shared source and target download environments do not exactly match")
	}
	requiresAdapterDisk := artifact.Spec.AdapterDisk != nil
	if targetCache.Spec.RequiresAdapterDisk != requiresAdapterDisk {
		return fmt.Errorf("shared source and target adapter-disk requirements do not exactly match")
	}
	if requiresAdapterDisk && artifact.Status.AdapterPersistentVolumeClaim == "" {
		return fmt.Errorf("shared source artifact adapter disk is not Ready")
	}
	if len(artifact.Spec.ImagePullSecrets) != 0 {
		return fmt.Errorf("shared source artifact image pull secrets do not match the target cache")
	}

	runtimeConfig := aimruntimeconfig.FetchMergedRuntimeConfig(
		ctx,
		kubernetesClient,
		targetCache.Spec.Name,
		targetCache.Namespace,
	)
	if runtimeConfig.Error != nil {
		return fmt.Errorf("resolve Shared artifact runtime configuration: %w", runtimeConfig.Error)
	}
	effectiveStorageClass := v1alpha1utils.ResolveStorageClass(
		targetCache.Spec.StorageClassName,
		runtimeConfig.Value,
	)
	if sourceCache.Spec.StorageClassName != effectiveStorageClass ||
		artifact.Spec.StorageClassName != effectiveStorageClass {
		return fmt.Errorf("shared source and target storage classes do not exactly match")
	}
	if effectiveStorageClass != "" &&
		(claim.Spec.StorageClassName == nil ||
			*claim.Spec.StorageClassName != effectiveStorageClass) {
		return fmt.Errorf("shared source PVC storage class does not exactly match the target cache")
	}
	hypothetical := &aimv1alpha1.AIMArtifact{
		Spec: aimv1alpha1.AIMArtifactSpec{
			Type:               aimv1alpha1.ArtifactTypeModel,
			SourceURI:          modelSource.SourceURI,
			ModelID:            modelSource.ModelID,
			StorageClassName:   targetCache.Spec.StorageClassName,
			Size:               artifact.Spec.Size.DeepCopy(),
			Env:                targetEnv,
			RuntimeConfigRef:   targetCache.Spec.RuntimeConfigRef,
			ImagePullSecrets:   nil,
			ModelDownloadImage: "",
			DownloadFilter:     modelSource.DownloadFilter,
		},
	}
	if requiresAdapterDisk {
		hypothetical.Spec.AdapterDisk = &aimv1alpha1.AIMAdapterDisk{}
	}
	if effectiveDownloadImage(artifact, runtimeConfig.Value) !=
		effectiveDownloadImage(hypothetical, runtimeConfig.Value) {
		return fmt.Errorf("shared source and target download images do not exactly match")
	}
	if !v1alpha2aimprofilecache.DownloadFilterMatches(
		effectiveDownloadFilter(artifact, runtimeConfig.Value),
		effectiveDownloadFilter(hypothetical, runtimeConfig.Value),
	) {
		return fmt.Errorf("shared source and target download filters do not exactly match")
	}
	if !v1alpha2aimprofilecache.DownloadFilterMatches(artifact.Spec.DownloadFilter, modelSource.DownloadFilter) {
		return fmt.Errorf("shared source artifact declared download filter does not exactly match the target profile")
	}
	return nil
}

func semanticEnvEqual(left, right []corev1.EnvVar) bool {
	leftCopy := copyEnv(left)
	rightCopy := copyEnv(right)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i].Name < leftCopy[j].Name })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i].Name < rightCopy[j].Name })
	return equality.Semantic.DeepEqual(leftCopy, rightCopy)
}

func effectiveDownloadImage(
	artifact *aimv1alpha1.AIMArtifact,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) string {
	if strings.HasPrefix(artifact.Spec.SourceURI, "s3://") &&
		v1alpha1aimartifact.DirectS3Connection(runtimeConfig) != nil {
		return aimv1alpha1.DefaultDownloadImage
	}
	return v1alpha1aimartifact.ResolveDownloadImage(artifact, runtimeConfig)
}

func effectiveDownloadFilter(
	artifact *aimv1alpha1.AIMArtifact,
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
) *aimv1alpha1.AIMDownloadFilter {
	if artifact.Spec.DownloadFilter != nil {
		return artifact.Spec.DownloadFilter
	}
	if runtimeConfig != nil &&
		runtimeConfig.Storage != nil &&
		runtimeConfig.Storage.DownloadFilter != nil {
		return runtimeConfig.Storage.DownloadFilter
	}
	if strings.HasPrefix(artifact.Spec.SourceURI, "hf://") {
		return &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}}
	}
	return nil
}

func validateSharedArtifactCandidates(
	ctx context.Context,
	kubernetesClient client.Client,
	targetCache *aimv1alpha2.AIMProfileCache,
	sourceArtifact *aimv1alpha1.AIMArtifact,
	modelSource aimv1alpha1.AIMModelSource,
) error {
	var artifacts aimv1alpha1.AIMArtifactList
	if err := kubernetesClient.List(
		ctx,
		&artifacts,
		client.InNamespace(targetCache.Namespace),
	); err != nil {
		return fmt.Errorf("list Shared AIMArtifact candidates: %w", err)
	}
	var matches []aimv1alpha1.AIMArtifact
	for index := range artifacts.Items {
		candidate := artifacts.Items[index]
		if !v1alpha2aimprofilecache.ArtifactAdoptableBy(targetCache, &candidate) ||
			candidate.Spec.SourceURI != modelSource.SourceURI ||
			!runtimeConfigAdoptionMatches(
				modelSource.SourceURI,
				candidate.Spec.RuntimeConfigRef,
				targetCache.Spec.RuntimeConfigRef,
			) ||
			!v1alpha2aimprofilecache.DownloadFilterMatches(candidate.Spec.DownloadFilter, modelSource.DownloadFilter) ||
			candidate.DeletionTimestamp != nil ||
			candidate.Status.Status == "" {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 {
		names := make([]string, 0, len(matches))
		for index := range matches {
			names = append(names, matches[index].Name)
		}
		sort.Strings(names)
		return fmt.Errorf(
			"shared artifact adoption is ambiguous: found %d controller-compatible candidates (%s)",
			len(matches),
			strings.Join(names, ", "),
		)
	}
	if matches[0].UID != sourceArtifact.UID {
		return fmt.Errorf("shared artifact adoption selected a different artifact UID")
	}
	return nil
}

func runtimeConfigAdoptionMatches(
	sourceURI string,
	left, right aimv1alpha1.RuntimeConfigRef,
) bool {
	if !strings.HasPrefix(sourceURI, "s3://") {
		return true
	}
	return normalizedRuntimeConfigName(left.Name) == normalizedRuntimeConfigName(right.Name)
}

func normalizedRuntimeConfigName(name string) string {
	if name == "" {
		return constants.DefaultRuntimeConfigName
	}
	return name
}

func hasOwnerUID(references []metav1.OwnerReference, uid types.UID) bool {
	for _, reference := range references {
		if reference.UID == uid {
			return true
		}
	}
	return false
}

func validateReadyObservedObject(object *unstructured.Unstructured) error {
	if object.GetDeletionTimestamp() != nil {
		return fmt.Errorf("%s %s/%s is terminating", object.GetKind(), object.GetNamespace(), object.GetName())
	}
	observed, found, err := unstructured.NestedInt64(object.Object, "status", "observedGeneration")
	if err != nil || !found || observed != object.GetGeneration() {
		return fmt.Errorf("%s %s/%s status is stale", object.GetKind(), object.GetNamespace(), object.GetName())
	}
	status, found, err := unstructured.NestedString(object.Object, "status", "status")
	if err != nil || !found || status != string(constants.AIMStatusReady) {
		return fmt.Errorf("%s %s/%s is not Ready", object.GetKind(), object.GetNamespace(), object.GetName())
	}
	return nil
}

func referenceMatches(
	reference *aimv1alpha1.AIMResolvedReference,
	object *unstructured.Unstructured,
) bool {
	if reference == nil || object == nil ||
		reference.Name != object.GetName() ||
		reference.UID != object.GetUID() {
		return false
	}
	if reference.Namespace != "" && reference.Namespace != object.GetNamespace() {
		return false
	}
	return reference.Kind == "" || reference.Kind == object.GetKind()
}

func captureInferenceServiceIdentity(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) error {
	name, err := v1alpha1aimservice.GenerateInferenceServiceName(
		prepared.service.Name,
		prepared.service.Namespace,
	)
	if err != nil {
		return fmt.Errorf("derive InferenceService name from the pinned AIMService")
	}
	inferenceService := &servingv1beta1.InferenceService{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{Namespace: prepared.service.Namespace, Name: name},
		inferenceService,
	); err != nil {
		return kubernetesFailure("capture pre-cutover InferenceService", ResourceIdentity{
			Kind:      "InferenceService",
			Namespace: prepared.service.Namespace,
			Name:      name,
		}, err)
	}
	if inferenceService.UID == "" {
		return fmt.Errorf("pre-cutover InferenceService %s/%s has no UID", prepared.service.Namespace, name)
	}
	if !inferenceServiceReady(inferenceService) {
		return fmt.Errorf(
			"pre-cutover InferenceService %s/%s is not current and Ready",
			prepared.service.Namespace,
			name,
		)
	}
	if inferenceService.Labels[constants.LabelTemplate] == "" {
		return fmt.Errorf(
			"pre-cutover InferenceService %s/%s has no legacy template label",
			prepared.service.Namespace,
			name,
		)
	}
	if inferenceService.Labels[constants.LabelProfile] != "" {
		return fmt.Errorf(
			"pre-cutover InferenceService %s/%s has a profile label",
			prepared.service.Namespace,
			name,
		)
	}
	prepared.inferenceService = typesafeInferenceService{
		identity: currentIdentity(inferenceService, ResourceIdentity{
			Role:       "inference-service",
			APIVersion: servingv1beta1.SchemeGroupVersion.String(),
			Kind:       resourceKindISVC,
			Namespace:  prepared.service.Namespace,
			Name:       name,
		}),
		object: inferenceService.DeepCopy(),
	}
	return nil
}

//nolint:gocyclo // Common, Shared, and Dedicated paths remain explicit instead of introducing a phase framework.
func executePreparedApply(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	options ApplyOptions,
	recorder *resultRecorder,
) error {
	profileAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		prepared.profileOperation,
	)
	if err != nil {
		return err
	}
	profile, profileOutcome, err := prepareTargetProfile(
		ctx,
		kubernetesClient,
		prepared,
		options.Commit,
	)
	if recordErr := recorder.completeAttempt(
		profileAttempt,
		profileOutcome,
		profile,
		err,
	); recordErr != nil {
		return recordErr
	}
	if err != nil {
		return err
	}

	if options.Commit {
		profile, err = waitForProfileAndRuntime(
			ctx,
			kubernetesClient,
			prepared.profileOperation.Resource,
			prepared.sourceTemplate,
			prepared.sourceModel,
			options.WaitTimeout,
			options.PollInterval,
		)
		if err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"wait-profile-runtime",
				prepared.profileOperation.Resource,
				profile,
				err,
			)
		}
	}

	var cache *aimv1alpha2.AIMProfileCache
	if prepared.dedicated != nil {
		cache, err = executeDedicatedHandoff(
			ctx,
			kubernetesClient,
			prepared,
			profile,
			options,
			recorder,
		)
		if err != nil {
			return err
		}
	} else {
		cacheOutcome := ""
		cacheAttempt, recordErr := recorder.recordAttempt(
			prepared.unit.ID,
			prepared.cacheOperation,
		)
		if recordErr != nil {
			return recordErr
		}
		cache, cacheOutcome, err = prepareFreshProfileCache(
			ctx,
			kubernetesClient,
			prepared.cacheObject,
			prepared.cacheOperation.Resource,
			options.Commit,
		)
		if recordErr := recorder.completeAttempt(
			cacheAttempt,
			cacheOutcome,
			cache,
			err,
		); recordErr != nil {
			return recordErr
		}
		if err != nil {
			return err
		}
		if options.Commit {
			cache, err = waitForProfileCache(
				ctx,
				kubernetesClient,
				prepared.cacheObject,
				options.WaitTimeout,
				options.PollInterval,
			)
			if err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"wait-profile-cache",
					prepared.cacheOperation.Resource,
					cache,
					err,
				)
			}
		}
	}

	if prepared.shared != nil {
		if options.Commit {
			if err := validateProfileCacheCandidates(ctx, kubernetesClient, prepared); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-cache-authority",
					prepared.cacheOperation.Resource,
					cache,
					err,
				)
			}
			if err := validateSharedCacheAdoption(cache, prepared.shared); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-adoption",
					prepared.cacheOperation.Resource,
					cache,
					err,
				)
			}
			if err := validateSharedMigrationState(
				ctx,
				kubernetesClient,
				prepared,
				profile,
				true,
			); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-source",
					prepared.shared.sourceArtifact,
					nil,
					err,
				)
			}
			cache, err = readAndValidateSharedCacheAdoption(
				ctx,
				kubernetesClient,
				prepared,
			)
			if err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-adoption",
					prepared.cacheOperation.Resource,
					cache,
					err,
				)
			}
			if err := recorder.recordVerification(
				prepared.unit.ID,
				"verify-shared-adoption",
				prepared.cacheOperation.Resource,
				cache,
			); err != nil {
				return err
			}
		}
		protectionAttempt, recordErr := recorder.recordAttempt(
			prepared.unit.ID,
			prepared.shared.protectionOperation,
		)
		if recordErr != nil {
			return recordErr
		}
		artifact, protectionOutcome, protectionErr := protectSharedArtifact(
			ctx,
			kubernetesClient,
			prepared,
			options.Commit,
		)
		if recordErr := recorder.completeAttempt(
			protectionAttempt,
			protectionOutcome,
			artifact,
			protectionErr,
		); recordErr != nil {
			return recordErr
		}
		if protectionErr != nil {
			return protectionErr
		}
		if options.Commit {
			if err := validateSharedMigrationState(
				ctx,
				kubernetesClient,
				prepared,
				profile,
				false,
			); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-protection",
					prepared.shared.sourceArtifact,
					artifact,
					err,
				)
			}
			if err := validateFinalSourceDependencies(ctx, kubernetesClient, prepared); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-dependencies",
					prepared.shared.sourceArtifact,
					artifact,
					err,
				)
			}
			if err := validateCurrentTargetProfile(
				ctx,
				kubernetesClient,
				prepared,
				profile,
			); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-profile",
					prepared.profileOperation.Resource,
					profile,
					err,
				)
			}
			if err := validateProfileCacheCandidates(ctx, kubernetesClient, prepared); err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-cache-authority",
					prepared.cacheOperation.Resource,
					cache,
					err,
				)
			}
			currentCache, err := readAndValidateSharedCacheAdoption(
				ctx,
				kubernetesClient,
				prepared,
			)
			if err != nil {
				return recorder.recordFailure(
					prepared.unit.ID,
					"verify-shared-before-cutover",
					prepared.cacheOperation.Resource,
					currentCache,
					err,
				)
			}
		}
	}

	if options.Commit && prepared.shared == nil && prepared.dedicated == nil {
		if err := validateFinalSourceDependencies(ctx, kubernetesClient, prepared); err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"verify-uncached-dependencies",
				prepared.profileOperation.Resource,
				profile,
				err,
			)
		}
		if err := validateCurrentTargetProfile(
			ctx,
			kubernetesClient,
			prepared,
			profile,
		); err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"verify-uncached-profile",
				prepared.profileOperation.Resource,
				profile,
				err,
			)
		}
	}

	if options.Commit {
		if err := validateInferenceServiceBeforeCutover(
			ctx,
			kubernetesClient,
			prepared,
		); err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"verify-inference-service-before-cutover",
				prepared.inferenceService.identity,
				nil,
				err,
			)
		}
	}
	serviceAttempt, recordErr := recorder.recordAttempt(
		prepared.unit.ID,
		prepared.serviceOperation,
	)
	if recordErr != nil {
		return recordErr
	}
	service, err := patchServiceInPlace(
		ctx,
		kubernetesClient,
		prepared,
		options.Commit,
	)
	serviceOutcome := operationOutcomePatched
	if !options.Commit {
		serviceOutcome = operationOutcomeDryRun
	}
	if err != nil {
		serviceOutcome = operationOutcomeFailed
	}
	if recordErr := recorder.completeAttempt(
		serviceAttempt,
		serviceOutcome,
		service,
		err,
	); recordErr != nil {
		return recordErr
	}
	if err != nil {
		return err
	}
	if !options.Commit {
		return nil
	}

	service, err = waitForCutover(
		ctx,
		kubernetesClient,
		prepared,
		profile,
		options.WaitTimeout,
		options.PollInterval,
	)
	if err != nil {
		return recorder.recordFailure(
			prepared.unit.ID,
			"verify-cutover",
			prepared.serviceOperation.Resource,
			service,
			err,
		)
	}
	if prepared.shared != nil {
		currentCache, err := readAndValidateSharedCacheAdoption(
			ctx,
			kubernetesClient,
			prepared,
		)
		if err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"verify-shared-after-cutover",
				prepared.cacheOperation.Resource,
				currentCache,
				err,
			)
		}
		if err := validateSharedMigrationState(
			ctx,
			kubernetesClient,
			prepared,
			profile,
			false,
		); err != nil {
			return recorder.recordFailure(
				prepared.unit.ID,
				"verify-shared-after-cutover",
				prepared.shared.sourceArtifact,
				nil,
				err,
			)
		}
	}
	if prepared.dedicated != nil {
		_, currentArtifact, err := validateDedicatedHandoffBeforeCutover(
			ctx,
			kubernetesClient,
			prepared,
			profile,
		)
		if err != nil {
			return recordDedicatedFailure(
				ctx,
				kubernetesClient,
				prepared,
				recorder,
				"verify-dedicated-after-cutover",
				dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
				currentArtifact,
				err,
			)
		}
	}
	return recorder.recordVerification(
		prepared.unit.ID,
		"verify-cutover",
		prepared.serviceOperation.Resource,
		service,
	)
}

func prepareTargetProfile(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	commit bool,
) (client.Object, string, error) {
	identity := prepared.profileOperation.Resource
	existing, err := fetchTargetProfile(ctx, kubernetesClient, identity)
	switch {
	case err == nil:
		if prepared.profileOperation.Action == OperationReuse {
			if err := comparePinnedIdentity(existing, identity); err != nil {
				return existing, operationOutcomeFailed, err
			}
			if err := validateReusableProfileProvenance(
				existing,
				prepared.sourceTemplate,
				prepared.sourceModel,
			); err != nil {
				return existing, operationOutcomeConflict, err
			}
		} else if err := matchDesiredProfile(existing, prepared.profileObject, prepared.sourceTemplate); err != nil {
			return existing, operationOutcomeConflict, err
		}
		return existing, operationOutcomeReused, nil
	case !apierrors.IsNotFound(err):
		return nil, operationOutcomeFailed, err
	case prepared.profileOperation.Action == OperationReuse:
		return nil, operationOutcomeFailed, fmt.Errorf(
			"pinned target %s %s/%s is missing",
			identity.Kind,
			identity.Namespace,
			identity.Name,
		)
	}

	createOptions := []client.CreateOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		createOptions = append(createOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "create target AIMProfile"); err != nil {
		return prepared.profileObject, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Create(ctx, prepared.profileObject, createOptions...); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing, fetchErr := fetchTargetProfile(ctx, kubernetesClient, identity)
			if fetchErr == nil {
				if matchErr := matchDesiredProfile(existing, prepared.profileObject, prepared.sourceTemplate); matchErr == nil {
					return existing, operationOutcomeReused, nil
				}
			}
			return existing, operationOutcomeConflict, fmt.Errorf(
				"target %s %s/%s appeared with conflicting spec, scope, or provenance",
				identity.Kind,
				identity.Namespace,
				identity.Name,
			)
		}
		return prepared.profileObject, operationOutcomeFailed, kubernetesFailure(
			"create target profile",
			identity,
			err,
		)
	}
	if !commit {
		return prepared.profileObject, operationOutcomeDryRun, nil
	}
	return prepared.profileObject, operationOutcomeCreated, nil
}

func fetchTargetProfile(
	ctx context.Context,
	kubernetesClient client.Client,
	identity ResourceIdentity,
) (client.Object, error) {
	var object client.Object
	switch identity.Kind {
	case resourceKindProfile:
		object = &aimv1alpha2.AIMProfile{}
	case resourceKindClusterProfile:
		object = &aimv1alpha2.AIMClusterProfile{}
	default:
		return nil, fmt.Errorf("unsupported target profile kind %q", identity.Kind)
	}
	err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{Namespace: identity.Namespace, Name: identity.Name},
		object,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, kubernetesFailure("read target profile", identity, err)
	}
	return object, nil
}

func matchDesiredProfile(
	existing client.Object,
	desired client.Object,
	sourceTemplate ResourceIdentity,
) error {
	if desired == nil {
		return fmt.Errorf("desired target profile is unavailable")
	}
	if existing.GetNamespace() != desired.GetNamespace() ||
		existing.GetName() != desired.GetName() {
		return fmt.Errorf("target profile scope does not match")
	}
	switch typed := existing.(type) {
	case *aimv1alpha2.AIMProfile:
		candidate, ok := desired.(*aimv1alpha2.AIMProfile)
		if !ok || !equality.Semantic.DeepEqual(typed.Spec, candidate.Spec) {
			return fmt.Errorf("existing AIMProfile has a conflicting complete spec")
		}
	case *aimv1alpha2.AIMClusterProfile:
		candidate, ok := desired.(*aimv1alpha2.AIMClusterProfile)
		if !ok || !equality.Semantic.DeepEqual(typed.Spec, candidate.Spec) {
			return fmt.Errorf("existing AIMClusterProfile has a conflicting complete spec")
		}
	default:
		return fmt.Errorf("existing target profile has unsupported kind")
	}
	return validateProfileProvenance(existing, sourceTemplate)
}

func waitForProfileAndRuntime(
	ctx context.Context,
	kubernetesClient client.Client,
	identity ResourceIdentity,
	sourceTemplate ResourceIdentity,
	sourceModel ResourceIdentity,
	timeout, pollInterval time.Duration,
) (client.Object, error) {
	var latest client.Object
	err := waitBoundedly(ctx, timeout, pollInterval, func() (bool, error) {
		profile, err := fetchTargetProfile(ctx, kubernetesClient, identity)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		latest = profile
		if err := validateReusableProfileProvenance(profile, sourceTemplate, sourceModel); err != nil {
			return false, err
		}
		status, generation := profileStatus(profile)
		if status == nil ||
			profile.GetDeletionTimestamp() != nil ||
			status.ObservedGeneration != generation ||
			status.Status != constants.AIMStatusReady ||
			!status.Deployable {
			return false, nil
		}
		runtimeName := serving.RuntimeName(profile.GetName())
		if status.ProjectedRuntimeName != runtimeName ||
			!meta.IsStatusConditionTrue(status.Conditions, aimv1alpha2.AIMProfileConditionRuntimeProjected) {
			return false, nil
		}
		return projectedRuntimeExists(ctx, kubernetesClient, profile, runtimeName)
	})
	if err != nil {
		return latest, fmt.Errorf(
			"target %s %s/%s and its projected runtime did not become Ready: %s",
			identity.Kind,
			identity.Namespace,
			identity.Name,
			safeWaitCause(err),
		)
	}
	return latest, nil
}

func profileStatus(profile client.Object) (*aimv1alpha2.AIMProfileStatus, int64) {
	switch typed := profile.(type) {
	case *aimv1alpha2.AIMProfile:
		return &typed.Status, typed.Generation
	case *aimv1alpha2.AIMClusterProfile:
		return &typed.Status, typed.Generation
	default:
		return nil, 0
	}
}

func projectedRuntimeExists(
	ctx context.Context,
	kubernetesClient client.Client,
	profile client.Object,
	runtimeName string,
) (bool, error) {
	if profile.GetNamespace() == "" {
		runtime := &kservev1alpha1.ClusterServingRuntime{}
		err := kubernetesClient.Get(ctx, client.ObjectKey{Name: runtimeName}, runtime)
		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case err != nil:
			return false, kubernetesFailure("read projected ClusterServingRuntime", ResourceIdentity{
				Kind: "ClusterServingRuntime",
				Name: runtimeName,
			}, err)
		default:
			return true, nil
		}
	}
	runtime := &kservev1alpha1.ServingRuntime{}
	key := client.ObjectKey{Namespace: profile.GetNamespace(), Name: runtimeName}
	if err := kubernetesClient.Get(ctx, key, runtime); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, kubernetesFailure("read projected ServingRuntime", ResourceIdentity{
			Kind:      "ServingRuntime",
			Namespace: key.Namespace,
			Name:      key.Name,
		}, err)
	}
	configMap := &corev1.ConfigMap{}
	if err := kubernetesClient.Get(ctx, key, configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, kubernetesFailure("read projected runtime ConfigMap", ResourceIdentity{
			Kind:      "ConfigMap",
			Namespace: key.Namespace,
			Name:      key.Name,
		}, err)
	}
	return true, nil
}

func executeDedicatedHandoff(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	profile client.Object,
	options ApplyOptions,
	recorder *resultRecorder,
) (*aimv1alpha2.AIMProfileCache, error) {
	if options.Commit {
		if err := validateInitialDedicatedMigrationState(
			ctx,
			kubernetesClient,
			prepared,
			profile,
		); err != nil {
			return nil, recordDedicatedFailure(
				ctx,
				kubernetesClient,
				prepared,
				recorder,
				"verify-dedicated-before-pause",
				prepared.dedicated.sourceArtifact,
				nil,
				err,
			)
		}
	}

	pauseAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		ProposedOperation{
			Action:   OperationAction(actionPauseSourceCache),
			Resource: dedicatedResource(prepared.dedicated.sourceCache, roleDedicatedSourceCache),
		},
	)
	if err != nil {
		return nil, err
	}
	sourceCache, outcome, err := pauseDedicatedSourceCache(
		ctx,
		kubernetesClient,
		prepared,
		options.Commit,
	)
	if recordErr := recordDedicatedOperation(
		ctx,
		kubernetesClient,
		prepared,
		recorder,
		pauseAttempt,
		outcome,
		sourceCache,
		err,
	); recordErr != nil {
		return nil, recordErr
	}

	createCacheAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		ProposedOperation{
			Action:   OperationAction(actionCreatePausedCache),
			Resource: prepared.cacheOperation.Resource,
		},
	)
	if err != nil {
		return nil, err
	}
	cache, cacheExisted, outcome, err := createPausedDedicatedProfileCache(
		ctx,
		kubernetesClient,
		prepared,
		options.Commit,
	)
	if recordErr := recordDedicatedOperation(
		ctx,
		kubernetesClient,
		prepared,
		recorder,
		createCacheAttempt,
		outcome,
		cache,
		err,
	); recordErr != nil {
		return cache, recordErr
	}

	stageOwnerAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		ProposedOperation{
			Action:   OperationAction(actionStageArtifactOwner),
			Resource: dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
		},
	)
	if err != nil {
		return cache, err
	}
	artifact, outcome, err := stageDedicatedArtifactOwner(
		ctx,
		kubernetesClient,
		prepared,
		cache,
		options.Commit,
	)
	if recordErr := recordDedicatedOperation(
		ctx,
		kubernetesClient,
		prepared,
		recorder,
		stageOwnerAttempt,
		outcome,
		artifact,
		err,
	); recordErr != nil {
		return cache, recordErr
	}

	unpauseAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		ProposedOperation{
			Action:   OperationAction(actionUnpauseTargetCache),
			Resource: prepared.cacheOperation.Resource,
		},
	)
	if err != nil {
		return cache, err
	}
	cache, outcome, err = unpauseDedicatedProfileCache(
		ctx,
		kubernetesClient,
		prepared,
		cache,
		cacheExisted,
		options.Commit,
	)
	if recordErr := recordDedicatedOperation(
		ctx,
		kubernetesClient,
		prepared,
		recorder,
		unpauseAttempt,
		outcome,
		cache,
		err,
	); recordErr != nil {
		return cache, recordErr
	}
	if !options.Commit {
		transferAttempt, recordErr := recorder.recordAttempt(
			prepared.unit.ID,
			ProposedOperation{
				Action:   OperationAction(actionTransferArtifactOwner),
				Resource: dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
			},
		)
		if recordErr != nil {
			return cache, recordErr
		}
		artifact, outcome, err = transferDedicatedArtifactOwner(
			ctx,
			kubernetesClient,
			prepared,
			cache,
			false,
		)
		if recordErr := recordDedicatedOperation(
			ctx,
			kubernetesClient,
			prepared,
			recorder,
			transferAttempt,
			outcome,
			artifact,
			err,
		); recordErr != nil {
			return cache, recordErr
		}
		return cache, nil
	}

	cache, err = waitForDedicatedCacheAdoption(
		ctx,
		kubernetesClient,
		prepared,
		options.WaitTimeout,
		options.PollInterval,
	)
	if err != nil {
		return cache, recordDedicatedFailure(
			ctx,
			kubernetesClient,
			prepared,
			recorder,
			actionVerifyDedicatedCache,
			prepared.cacheOperation.Resource,
			cache,
			err,
		)
	}
	if err := recorder.recordVerification(
		prepared.unit.ID,
		actionVerifyDedicatedCache,
		prepared.cacheOperation.Resource,
		cache,
	); err != nil {
		return cache, err
	}

	transferAttempt, err := recorder.recordAttempt(
		prepared.unit.ID,
		ProposedOperation{
			Action:   OperationAction(actionTransferArtifactOwner),
			Resource: dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
		},
	)
	if err != nil {
		return cache, err
	}
	artifact, outcome, err = transferDedicatedArtifactOwner(
		ctx,
		kubernetesClient,
		prepared,
		cache,
		true,
	)
	if recordErr := recordDedicatedOperation(
		ctx,
		kubernetesClient,
		prepared,
		recorder,
		transferAttempt,
		outcome,
		artifact,
		err,
	); recordErr != nil {
		return cache, recordErr
	}
	cache, artifact, err = validateDedicatedHandoffBeforeCutover(
		ctx,
		kubernetesClient,
		prepared,
		profile,
	)
	if err != nil {
		return cache, recordDedicatedFailure(
			ctx,
			kubernetesClient,
			prepared,
			recorder,
			actionVerifyDedicatedHandoff,
			dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
			artifact,
			err,
		)
	}
	if err := recorder.recordVerification(
		prepared.unit.ID,
		actionVerifyDedicatedHandoff,
		dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
		artifact,
	); err != nil {
		return cache, err
	}
	return cache, nil
}

func recordDedicatedOperation(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	recorder *resultRecorder,
	attempt int,
	outcome string,
	current client.Object,
	operationErr error,
) error {
	if operationErr != nil {
		observeDedicatedRecoveryState(ctx, kubernetesClient, prepared, recorder)
		if outcome == operationOutcomeFailed {
			current = nil
		}
	}
	if err := recorder.completeAttempt(
		attempt,
		outcome,
		current,
		operationErr,
	); err != nil {
		return err
	}
	return operationErr
}

func recordDedicatedFailure(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	recorder *resultRecorder,
	action string,
	resource ResourceIdentity,
	current client.Object,
	operationErr error,
) error {
	observeDedicatedRecoveryState(ctx, kubernetesClient, prepared, recorder)
	return recorder.recordFailure(
		prepared.unit.ID,
		action,
		resource,
		current,
		operationErr,
	)
}

func dedicatedResource(identity ResourceIdentity, role string) ResourceIdentity {
	identity.Role = role
	return identity
}

func pauseDedicatedSourceCache(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	commit bool,
) (*aimv1alpha1.AIMTemplateCache, string, error) {
	current := &aimv1alpha1.AIMTemplateCache{}
	key := client.ObjectKey{
		Namespace: prepared.dedicated.sourceCache.Namespace,
		Name:      prepared.dedicated.sourceCache.Name,
	}
	if err := kubernetesClient.Get(ctx, key, current); err != nil {
		return nil, operationOutcomeFailed, kubernetesFailure(
			"read Dedicated source cache before pause",
			prepared.dedicated.sourceCache,
			err,
		)
	}
	if err := validateUnmutatedDedicatedSourceCache(prepared, current); err != nil {
		return current, operationOutcomeFailed, err
	}
	if current.Annotations[constants.AnnotationReconciliationPaused] == reconciliationPausedValue {
		return current, operationOutcomeReused, nil
	}
	before := current.DeepCopy()
	annotations := copyStringMap(current.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[constants.AnnotationReconciliationPaused] = reconciliationPausedValue
	current.Annotations = annotations
	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "pause Dedicated source AIMTemplateCache"); err != nil {
		return current, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, operationOutcomeFailed, kubernetesFailure(
			"pause Dedicated source AIMTemplateCache",
			prepared.dedicated.sourceCache,
			err,
		)
	}
	if err := validatePausedDedicatedSourceCache(prepared, current); err != nil {
		return current, operationOutcomeFailed, err
	}
	if !commit {
		return current, operationOutcomeDryRun, nil
	}
	return current, operationOutcomePatched, nil
}

func validateUnmutatedDedicatedSourceCache(
	prepared *preparedApply,
	current *aimv1alpha1.AIMTemplateCache,
) error {
	initial := prepared.dedicated.initialCache
	if initial == nil || current == nil {
		return fmt.Errorf("dedicated source AIMTemplateCache state is unavailable")
	}
	if err := comparePinnedIdentity(current, prepared.dedicated.sourceCache); err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(current.Spec, initial.Spec) ||
		!equality.Semantic.DeepEqual(current.Labels, initial.Labels) ||
		!equality.Semantic.DeepEqual(current.Annotations, initial.Annotations) ||
		!equality.Semantic.DeepEqual(current.Finalizers, initial.Finalizers) ||
		!equality.Semantic.DeepEqual(current.OwnerReferences, initial.OwnerReferences) ||
		current.DeletionTimestamp != nil ||
		current.Generation != current.Status.ObservedGeneration ||
		current.Status.Status != constants.AIMStatusReady ||
		!dedicatedSourceCacheOwnerMatches(current, prepared.service) {
		return fmt.Errorf("dedicated source AIMTemplateCache drifted before pause")
	}
	return nil
}

func validatePausedDedicatedSourceCache(
	prepared *preparedApply,
	current *aimv1alpha1.AIMTemplateCache,
) error {
	initial := prepared.dedicated.initialCache
	if initial == nil || current == nil {
		return fmt.Errorf("paused Dedicated source AIMTemplateCache state is unavailable")
	}
	expectedAnnotations := copyStringMap(initial.Annotations)
	if expectedAnnotations == nil {
		expectedAnnotations = map[string]string{}
	}
	expectedAnnotations[constants.AnnotationReconciliationPaused] = reconciliationPausedValue
	if current.UID != initial.UID ||
		current.Generation != initial.Generation ||
		current.DeletionTimestamp != nil ||
		!equality.Semantic.DeepEqual(current.Spec, initial.Spec) ||
		!equality.Semantic.DeepEqual(current.Labels, initial.Labels) ||
		!equality.Semantic.DeepEqual(current.Annotations, expectedAnnotations) ||
		!equality.Semantic.DeepEqual(current.Finalizers, initial.Finalizers) ||
		!equality.Semantic.DeepEqual(current.OwnerReferences, initial.OwnerReferences) ||
		!dedicatedSourceCacheOwnerMatches(current, prepared.service) {
		return fmt.Errorf("dedicated source AIMTemplateCache pause did not preserve its identity and unrelated metadata")
	}
	return nil
}

func createPausedDedicatedProfileCache(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	commit bool,
) (*aimv1alpha2.AIMProfileCache, bool, string, error) {
	desired := prepared.cacheObject.DeepCopy()
	desired.Annotations = map[string]string{
		constants.AnnotationReconciliationPaused: reconciliationPausedValue,
	}
	createOptions := []client.CreateOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		createOptions = append(createOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "create paused Dedicated target profile cache"); err != nil {
		return desired, false, operationOutcomeFailed, err
	}
	err := kubernetesClient.Create(ctx, desired, createOptions...)
	if apierrors.IsAlreadyExists(err) {
		existing := &aimv1alpha2.AIMProfileCache{}
		key := client.ObjectKeyFromObject(desired)
		if fetchErr := kubernetesClient.Get(ctx, key, existing); fetchErr != nil {
			return existing, true, operationOutcomeConflict, kubernetesFailure(
				"read concurrently created Dedicated target cache",
				prepared.cacheOperation.Resource,
				fetchErr,
			)
		}
		if matchErr := matchDedicatedProfileCache(existing, desired, true); matchErr != nil {
			return existing, true, operationOutcomeConflict, fmt.Errorf(
				"target AIMProfileCache %s/%s already exists without the exact paused Dedicated spec, scope, labels, and sole owner: %w",
				desired.Namespace,
				desired.Name,
				matchErr,
			)
		}
		prepared.cacheObject.UID = existing.UID
		return existing, true, operationOutcomeReused, nil
	}
	if err != nil {
		return desired, false, operationOutcomeFailed, kubernetesFailure(
			"create paused Dedicated target profile cache",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	if !commit {
		if desired.UID == "" {
			desired.UID = types.UID("00000000-0000-0000-0000-000000000000")
		}
		return desired, false, operationOutcomeDryRun, nil
	}
	current := &aimv1alpha2.AIMProfileCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(desired),
		current,
	); err != nil {
		return desired, false, operationOutcomeFailed, kubernetesFailure(
			"re-read created Dedicated target profile cache",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	if current.UID == "" {
		return current, false, operationOutcomeFailed, fmt.Errorf(
			"created Dedicated target AIMProfileCache has no UID",
		)
	}
	if err := matchDedicatedProfileCache(current, desired, true); err != nil {
		return current, false, operationOutcomeConflict, err
	}
	prepared.cacheObject.UID = current.UID
	return current, false, operationOutcomeCreated, nil
}

func matchDedicatedProfileCache(
	current, desired *aimv1alpha2.AIMProfileCache,
	paused bool,
) error {
	if current == nil || desired == nil ||
		current.Namespace != desired.Namespace ||
		current.Name != desired.Name ||
		current.DeletionTimestamp != nil ||
		current.Spec.Mode != aimv1alpha2.ProfileCacheModeDedicated ||
		!equality.Semantic.DeepEqual(current.Spec, desired.Spec) ||
		!equality.Semantic.DeepEqual(current.Labels, desired.Labels) ||
		!equality.Semantic.DeepEqual(current.OwnerReferences, desired.OwnerReferences) {
		return fmt.Errorf("dedicated target AIMProfileCache complete shape changed")
	}
	expectedAnnotations := map[string]string{}
	if paused {
		expectedAnnotations[constants.AnnotationReconciliationPaused] = reconciliationPausedValue
	}
	if !equality.Semantic.DeepEqual(current.Annotations, expectedAnnotations) {
		return fmt.Errorf("dedicated target AIMProfileCache pause state or annotations changed")
	}
	if desired.UID != "" && current.UID != "" && current.UID != desired.UID {
		return fmt.Errorf("dedicated target AIMProfileCache UID changed")
	}
	return nil
}

func stageDedicatedArtifactOwner(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	targetCache *aimv1alpha2.AIMProfileCache,
	commit bool,
) (*aimv1alpha1.AIMArtifact, string, error) {
	current := &aimv1alpha1.AIMArtifact{}
	key := client.ObjectKey{
		Namespace: prepared.dedicated.sourceArtifact.Namespace,
		Name:      prepared.dedicated.sourceArtifact.Name,
	}
	if err := kubernetesClient.Get(ctx, key, current); err != nil {
		return nil, operationOutcomeFailed, kubernetesFailure(
			"read Dedicated artifact before owner staging",
			prepared.dedicated.sourceArtifact,
			err,
		)
	}
	if err := validateUnmutatedDedicatedArtifact(prepared, current); err != nil {
		return current, operationOutcomeFailed, err
	}
	if commit {
		oldCache := &aimv1alpha1.AIMTemplateCache{}
		if err := kubernetesClient.Get(
			ctx,
			client.ObjectKeyFromObject(prepared.dedicated.initialCache),
			oldCache,
		); err != nil {
			return current, operationOutcomeFailed, kubernetesFailure(
				"re-read paused Dedicated source cache",
				prepared.dedicated.sourceCache,
				err,
			)
		}
		if err := validatePausedDedicatedSourceCache(prepared, oldCache); err != nil {
			return current, operationOutcomeFailed, err
		}
		if err := matchDedicatedProfileCache(targetCache, pausedDedicatedCacheShape(prepared), true); err != nil {
			return current, operationOutcomeFailed, err
		}
	}
	if targetCache == nil || targetCache.UID == "" {
		return current, operationOutcomeFailed, fmt.Errorf("dedicated target AIMProfileCache UID is unavailable for owner staging")
	}
	before := current.DeepCopy()
	current.OwnerReferences = dedicatedDualOwnerReferences(prepared, targetCache)
	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "stage Dedicated artifact owner"); err != nil {
		return current, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, operationOutcomeFailed, kubernetesFailure(
			"stage Dedicated artifact owner",
			prepared.dedicated.sourceArtifact,
			err,
		)
	}
	if err := validateDualOwnedDedicatedArtifact(prepared, current, targetCache); err != nil {
		return current, operationOutcomeFailed, err
	}
	if !commit {
		return current, operationOutcomeDryRun, nil
	}
	return current, operationOutcomePatched, nil
}

func validateUnmutatedDedicatedArtifact(
	prepared *preparedApply,
	current *aimv1alpha1.AIMArtifact,
) error {
	initial := prepared.dedicated.initialArtifact
	if initial == nil || current == nil {
		return fmt.Errorf("dedicated artifact source state is unavailable")
	}
	if err := comparePinnedIdentity(current, prepared.dedicated.sourceArtifact); err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(current.OwnerReferences, initial.OwnerReferences) {
		return fmt.Errorf("dedicated artifact owner references changed before staging")
	}
	if _, ok := dedicatedArtifactControllerOwner(current, prepared.dedicated.initialCache); !ok {
		return fmt.Errorf("dedicated artifact does not retain the old cache as its exact sole controller owner")
	}
	return validateDedicatedArtifactAndPVCStatus(prepared, current, prepared.dedicated.initialClaim)
}

func pausedDedicatedCacheShape(prepared *preparedApply) *aimv1alpha2.AIMProfileCache {
	desired := prepared.cacheObject.DeepCopy()
	desired.Annotations = map[string]string{
		constants.AnnotationReconciliationPaused: reconciliationPausedValue,
	}
	return desired
}

func dedicatedDualOwnerReferences(
	prepared *preparedApply,
	targetCache *aimv1alpha2.AIMProfileCache,
) []metav1.OwnerReference {
	oldOwner := *prepared.dedicated.oldOwner.DeepCopy()
	return []metav1.OwnerReference{
		oldOwner,
		{
			APIVersion:         aimv1alpha2.GroupVersion.String(),
			Kind:               resourceKindProfileCache,
			Name:               targetCache.Name,
			UID:                targetCache.UID,
			Controller:         ptr.To(false),
			BlockOwnerDeletion: copyBoolPointer(oldOwner.BlockOwnerDeletion),
		},
	}
}

func dedicatedTransferredOwnerReference(
	prepared *preparedApply,
	targetCache *aimv1alpha2.AIMProfileCache,
) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         aimv1alpha2.GroupVersion.String(),
		Kind:               resourceKindProfileCache,
		Name:               targetCache.Name,
		UID:                targetCache.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: copyBoolPointer(prepared.dedicated.oldOwner.BlockOwnerDeletion),
	}
}

func copyBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	return ptr.To(*value)
}

func validateDualOwnedDedicatedArtifact(
	prepared *preparedApply,
	current *aimv1alpha1.AIMArtifact,
	targetCache *aimv1alpha2.AIMProfileCache,
) error {
	if current == nil || len(current.OwnerReferences) != 2 {
		return fmt.Errorf("dedicated artifact does not have exactly the old and new cache owners")
	}
	expected := dedicatedDualOwnerReferences(prepared, targetCache)
	if !ownerReferenceSetEqual(current.OwnerReferences, expected) {
		return fmt.Errorf("dedicated artifact dual-owner identities or flags changed")
	}
	return validateDedicatedArtifactAndPVCStatus(prepared, current, prepared.dedicated.initialClaim)
}

func validateTransferredDedicatedArtifact(
	prepared *preparedApply,
	current *aimv1alpha1.AIMArtifact,
	targetCache *aimv1alpha2.AIMProfileCache,
) error {
	if current == nil ||
		len(current.OwnerReferences) != 1 ||
		!equality.Semantic.DeepEqual(
			current.OwnerReferences[0],
			dedicatedTransferredOwnerReference(prepared, targetCache),
		) {
		return fmt.Errorf("dedicated artifact does not have the target AIMProfileCache as its exact sole controller owner")
	}
	return validateDedicatedArtifactAndPVCStatus(prepared, current, prepared.dedicated.initialClaim)
}

func ownerReferenceSetEqual(left, right []metav1.OwnerReference) bool {
	if len(left) != len(right) {
		return false
	}
	for _, expected := range right {
		found := false
		for _, actual := range left {
			if equality.Semantic.DeepEqual(actual, expected) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validateDedicatedArtifactAndPVCStatus(
	prepared *preparedApply,
	artifact *aimv1alpha1.AIMArtifact,
	claim *corev1.PersistentVolumeClaim,
) error {
	if artifact == nil ||
		artifact.UID != prepared.dedicated.initialArtifact.UID ||
		artifact.Generation != prepared.dedicated.initialArtifact.Generation ||
		artifact.DeletionTimestamp != nil ||
		artifact.Generation != artifact.Status.ObservedGeneration ||
		artifact.Status.Status != constants.AIMStatusReady ||
		artifact.Status.Mode != aimv1alpha1.ArtifactModeDedicated ||
		artifact.Status.PersistentVolumeClaim != prepared.dedicated.sourcePVC.Name {
		return fmt.Errorf("dedicated artifact identity, readiness, mode, or reported PVC changed")
	}
	digest, secretLiteral, err := contentDigest(
		artifact,
		prepared.dedicated.sourceArtifact.APIVersion,
		prepared.dedicated.sourceArtifact.Kind,
	)
	if err != nil ||
		secretLiteral ||
		digest != prepared.dedicated.sourceArtifact.ContentDigest {
		return fmt.Errorf("dedicated artifact relevant content changed")
	}
	if claim != nil {
		return validateDedicatedLivePVC(prepared, artifact, claim)
	}
	return nil
}

func validateDedicatedLivePVC(
	prepared *preparedApply,
	artifact *aimv1alpha1.AIMArtifact,
	claim *corev1.PersistentVolumeClaim,
) error {
	if claim == nil ||
		claim.Namespace != prepared.dedicated.sourcePVC.Namespace ||
		claim.Name != prepared.dedicated.sourcePVC.Name ||
		string(claim.UID) != prepared.dedicated.sourcePVC.UID ||
		claim.DeletionTimestamp != nil ||
		claim.Status.Phase != corev1.ClaimBound ||
		!dedicatedPVCControllerOwnerMatches(claim, artifact) {
		return fmt.Errorf("dedicated live PVC name, UID, binding, or artifact owner changed")
	}
	digest, secretLiteral, err := contentDigest(
		claim,
		prepared.dedicated.sourcePVC.APIVersion,
		prepared.dedicated.sourcePVC.Kind,
	)
	if err != nil ||
		secretLiteral ||
		digest != prepared.dedicated.sourcePVC.ContentDigest {
		return fmt.Errorf("dedicated live PVC relevant content changed")
	}
	return nil
}

func unpauseDedicatedProfileCache(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	targetCache *aimv1alpha2.AIMProfileCache,
	cacheExisted bool,
	commit bool,
) (*aimv1alpha2.AIMProfileCache, string, error) {
	if !commit && !cacheExisted {
		desired := prepared.cacheObject.DeepCopy()
		desired.UID = ""
		desired.ResourceVersion = ""
		desired.Finalizers = nil
		desired.Status = aimv1alpha2.AIMProfileCacheStatus{}
		createOptions := []client.CreateOption{
			client.FieldOwner(applyFieldOwner),
			client.DryRunAll,
		}
		if err := mutationContextError(ctx, "validate unpaused Dedicated target profile cache"); err != nil {
			return desired, operationOutcomeFailed, err
		}
		if err := kubernetesClient.Create(ctx, desired, createOptions...); err != nil {
			return desired, operationOutcomeFailed, kubernetesFailure(
				"validate unpaused Dedicated target profile cache",
				prepared.cacheOperation.Resource,
				err,
			)
		}
		desired.UID = targetCache.UID
		return desired, operationOutcomeDryRun, nil
	}

	current := &aimv1alpha2.AIMProfileCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(targetCache),
		current,
	); err != nil {
		return nil, operationOutcomeFailed, kubernetesFailure(
			"read Dedicated target cache before unpause",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	if err := matchDedicatedProfileCache(current, pausedDedicatedCacheShape(prepared), true); err != nil {
		return current, operationOutcomeConflict, err
	}
	if commit {
		artifact := &aimv1alpha1.AIMArtifact{}
		if err := kubernetesClient.Get(
			ctx,
			client.ObjectKeyFromObject(prepared.dedicated.initialArtifact),
			artifact,
		); err != nil {
			return current, operationOutcomeFailed, kubernetesFailure(
				"re-read dual-owned Dedicated artifact before unpause",
				prepared.dedicated.sourceArtifact,
				err,
			)
		}
		if err := validateDualOwnedDedicatedArtifact(prepared, artifact, current); err != nil {
			return current, operationOutcomeFailed, err
		}
	}
	before := current.DeepCopy()
	annotations := copyStringMap(current.Annotations)
	delete(annotations, constants.AnnotationReconciliationPaused)
	current.Annotations = annotations
	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "unpause Dedicated target profile cache"); err != nil {
		return current, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, operationOutcomeFailed, kubernetesFailure(
			"unpause Dedicated target profile cache",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	if err := matchDedicatedProfileCache(current, prepared.cacheObject, false); err != nil {
		return current, operationOutcomeFailed, err
	}
	if !commit {
		return current, operationOutcomeDryRun, nil
	}
	return current, operationOutcomePatched, nil
}

func waitForDedicatedCacheAdoption(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	timeout, pollInterval time.Duration,
) (*aimv1alpha2.AIMProfileCache, error) {
	var latest *aimv1alpha2.AIMProfileCache
	err := waitBoundedly(ctx, timeout, pollInterval, func() (bool, error) {
		cache, artifact, claim, sourceCache, err := readDedicatedHandoffObjects(
			ctx,
			kubernetesClient,
			prepared,
		)
		if err != nil {
			return false, err
		}
		latest = cache
		if err := matchDedicatedProfileCache(cache, prepared.cacheObject, false); err != nil {
			return false, err
		}
		if err := validatePausedDedicatedSourceCache(prepared, sourceCache); err != nil {
			return false, err
		}
		if err := validateDualOwnedDedicatedArtifact(prepared, artifact, cache); err != nil {
			return false, err
		}
		if err := validateDedicatedLivePVC(prepared, artifact, claim); err != nil {
			return false, err
		}
		if cache.Generation != cache.Status.ObservedGeneration ||
			cache.Status.Status != constants.AIMStatusReady {
			return false, nil
		}
		if err := validateDedicatedCacheReport(cache, prepared); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return latest, fmt.Errorf(
			"target Dedicated AIMProfileCache %s/%s did not report the exact current-generation predecessor artifact and live PVC: %s",
			prepared.cacheObject.Namespace,
			prepared.cacheObject.Name,
			safeWaitCause(err),
		)
	}
	return latest, nil
}

func readDedicatedHandoffObjects(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) (
	*aimv1alpha2.AIMProfileCache,
	*aimv1alpha1.AIMArtifact,
	*corev1.PersistentVolumeClaim,
	*aimv1alpha1.AIMTemplateCache,
	error,
) {
	cache := &aimv1alpha2.AIMProfileCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.cacheObject),
		cache,
	); err != nil {
		return nil, nil, nil, nil, kubernetesFailure(
			"read Dedicated target profile cache",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	artifact := &aimv1alpha1.AIMArtifact{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.dedicated.initialArtifact),
		artifact,
	); err != nil {
		return cache, nil, nil, nil, kubernetesFailure(
			"read Dedicated source artifact",
			prepared.dedicated.sourceArtifact,
			err,
		)
	}
	claim := &corev1.PersistentVolumeClaim{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.dedicated.initialClaim),
		claim,
	); err != nil {
		return cache, artifact, nil, nil, kubernetesFailure(
			"read Dedicated live PVC",
			prepared.dedicated.sourcePVC,
			err,
		)
	}
	sourceCache := &aimv1alpha1.AIMTemplateCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.dedicated.initialCache),
		sourceCache,
	); err != nil {
		return cache, artifact, claim, nil, kubernetesFailure(
			"read paused Dedicated source cache",
			prepared.dedicated.sourceCache,
			err,
		)
	}
	return cache, artifact, claim, sourceCache, nil
}

func validateDedicatedCacheReport(
	cache *aimv1alpha2.AIMProfileCache,
	prepared *preparedApply,
) error {
	resolved, err := exactResolvedArtifact(cache.Status.Artifacts)
	if err != nil {
		return fmt.Errorf("dedicated target cache artifact report is ambiguous: %w", err)
	}
	if resolved.UID != prepared.dedicated.sourceArtifact.UID ||
		resolved.Name != prepared.dedicated.sourceArtifact.Name ||
		resolved.Model != prepared.dedicated.modelID ||
		resolved.PersistentVolumeClaim != prepared.dedicated.sourcePVC.Name ||
		resolved.Status != constants.AIMStatusReady {
		return fmt.Errorf("dedicated target cache does not report the exact predecessor artifact UID and PVC")
	}
	return nil
}

func transferDedicatedArtifactOwner(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	targetCache *aimv1alpha2.AIMProfileCache,
	commit bool,
) (*aimv1alpha1.AIMArtifact, string, error) {
	current := &aimv1alpha1.AIMArtifact{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.dedicated.initialArtifact),
		current,
	); err != nil {
		return nil, operationOutcomeFailed, kubernetesFailure(
			"read Dedicated artifact before controller transfer",
			prepared.dedicated.sourceArtifact,
			err,
		)
	}
	if commit {
		cache, artifact, claim, sourceCache, err := readDedicatedHandoffObjects(
			ctx,
			kubernetesClient,
			prepared,
		)
		if err != nil {
			return current, operationOutcomeFailed, err
		}
		current = artifact
		if err := matchDedicatedProfileCache(cache, prepared.cacheObject, false); err != nil {
			return current, operationOutcomeFailed, err
		}
		if cache.Generation != cache.Status.ObservedGeneration ||
			cache.Status.Status != constants.AIMStatusReady {
			return current, operationOutcomeFailed, fmt.Errorf("dedicated target cache is no longer current and Ready before ownership transfer")
		}
		if err := validateDedicatedCacheReport(cache, prepared); err != nil {
			return current, operationOutcomeFailed, err
		}
		if err := validateDualOwnedDedicatedArtifact(prepared, artifact, cache); err != nil {
			return current, operationOutcomeFailed, err
		}
		if err := validateDedicatedLivePVC(prepared, artifact, claim); err != nil {
			return current, operationOutcomeFailed, err
		}
		if err := validatePausedDedicatedSourceCache(prepared, sourceCache); err != nil {
			return current, operationOutcomeFailed, err
		}
		targetCache = cache
	} else if err := validateUnmutatedDedicatedArtifact(prepared, current); err != nil {
		return current, operationOutcomeFailed, err
	}
	if targetCache == nil || targetCache.UID == "" {
		return current, operationOutcomeFailed, fmt.Errorf("dedicated target cache UID is unavailable for controller transfer")
	}
	before := current.DeepCopy()
	current.OwnerReferences = []metav1.OwnerReference{
		dedicatedTransferredOwnerReference(prepared, targetCache),
	}
	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "transfer Dedicated artifact controller owner"); err != nil {
		return current, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, operationOutcomeFailed, kubernetesFailure(
			"transfer Dedicated artifact controller owner",
			prepared.dedicated.sourceArtifact,
			err,
		)
	}
	if err := validateTransferredDedicatedArtifact(prepared, current, targetCache); err != nil {
		return current, operationOutcomeFailed, err
	}
	if !commit {
		return current, operationOutcomeDryRun, nil
	}
	return current, operationOutcomePatched, nil
}

func validateDedicatedHandoffBeforeCutover(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	profile client.Object,
) (*aimv1alpha2.AIMProfileCache, *aimv1alpha1.AIMArtifact, error) {
	cache, artifact, claim, sourceCache, err := readDedicatedHandoffObjects(
		ctx,
		kubernetesClient,
		prepared,
	)
	if err != nil {
		return cache, artifact, err
	}
	if err := matchDedicatedProfileCache(cache, prepared.cacheObject, false); err != nil {
		return cache, artifact, err
	}
	if cache.Generation != cache.Status.ObservedGeneration ||
		cache.Status.Status != constants.AIMStatusReady {
		return cache, artifact, fmt.Errorf("dedicated target cache is not current and Ready before service cutover")
	}
	if err := validateDedicatedCacheReport(cache, prepared); err != nil {
		return cache, artifact, err
	}
	if err := validateTransferredDedicatedArtifact(prepared, artifact, cache); err != nil {
		return cache, artifact, err
	}
	if err := validateDedicatedLivePVC(prepared, artifact, claim); err != nil {
		return cache, artifact, err
	}
	if err := validatePausedDedicatedSourceCache(prepared, sourceCache); err != nil {
		return cache, artifact, err
	}
	if err := validateFinalSourceDependencies(ctx, kubernetesClient, prepared); err != nil {
		return cache, artifact, err
	}
	if err := validateCurrentTargetProfile(ctx, kubernetesClient, prepared, profile); err != nil {
		return cache, artifact, err
	}
	return cache, artifact, nil
}

func validateFinalSourceDependencies(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) error {
	for _, expected := range prepared.unit.ObservedResources {
		switch expected.Role {
		case roleSourceTemplate, roleSourceModel, "runtime-config", "referenced-config":
		default:
			continue
		}
		current, err := fetchPinnedObject(ctx, kubernetesClient, expected)
		if err != nil {
			return err
		}
		if string(current.GetUID()) != expected.UID ||
			current.GetGeneration() != expected.Generation {
			return fmt.Errorf(
				"%s %s/%s identity changed before service cutover",
				expected.Kind,
				expected.Namespace,
				expected.Name,
			)
		}
		digest, secretLiteral, err := contentDigest(
			current,
			expected.APIVersion,
			expected.Kind,
		)
		if err != nil || secretLiteral || digest != expected.ContentDigest {
			return fmt.Errorf(
				"%s %s/%s relevant content changed before service cutover",
				expected.Kind,
				expected.Namespace,
				expected.Name,
			)
		}
		if expected.Role == roleSourceTemplate || expected.Role == roleSourceModel {
			if err := validateReadyObservedObject(current); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCurrentTargetProfile(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	expected client.Object,
) error {
	current, err := fetchTargetProfile(ctx, kubernetesClient, prepared.profileOperation.Resource)
	if err != nil {
		return err
	}
	if current.GetUID() == "" || current.GetUID() != expected.GetUID() {
		return fmt.Errorf("target profile UID changed before service cutover")
	}
	if prepared.profileOperation.Action == OperationCreate {
		if err := matchDesiredProfile(current, prepared.profileObject, prepared.sourceTemplate); err != nil {
			return err
		}
	} else {
		digest, secretLiteral, digestErr := contentDigest(
			current,
			prepared.profileOperation.Resource.APIVersion,
			prepared.profileOperation.Resource.Kind,
		)
		if digestErr != nil ||
			secretLiteral ||
			digest != prepared.profileOperation.Resource.ContentDigest ||
			string(current.GetUID()) != prepared.profileOperation.Resource.UID ||
			current.GetGeneration() != prepared.profileOperation.Resource.Generation {
			return fmt.Errorf("reused target profile relevant content or identity changed")
		}
		if err := validateReusableProfileProvenance(
			current,
			prepared.sourceTemplate,
			prepared.sourceModel,
		); err != nil {
			return err
		}
	}
	status, generation := profileStatus(current)
	if status == nil ||
		current.GetDeletionTimestamp() != nil ||
		status.ObservedGeneration != generation ||
		status.Status != constants.AIMStatusReady ||
		!status.Deployable ||
		status.ProjectedRuntimeName != serving.RuntimeName(current.GetName()) ||
		!meta.IsStatusConditionTrue(status.Conditions, aimv1alpha2.AIMProfileConditionRuntimeProjected) {
		return fmt.Errorf("target profile is no longer current, Ready, and runtime-projected")
	}
	exists, err := projectedRuntimeExists(
		ctx,
		kubernetesClient,
		current,
		status.ProjectedRuntimeName,
	)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("target profile projected runtime disappeared before service cutover")
	}
	return nil
}

func observeDedicatedRecoveryState(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	recorder *resultRecorder,
) {
	if prepared == nil || prepared.dedicated == nil {
		return
	}
	observations := []struct {
		object   client.Object
		identity ResourceIdentity
	}{
		{
			object:   &aimv1alpha1.AIMTemplateCache{},
			identity: dedicatedResource(prepared.dedicated.sourceCache, roleDedicatedSourceCache),
		},
		{
			object:   &aimv1alpha1.AIMArtifact{},
			identity: dedicatedResource(prepared.dedicated.sourceArtifact, roleDedicatedArtifact),
		},
		{
			object:   &corev1.PersistentVolumeClaim{},
			identity: prepared.dedicated.sourcePVC,
		},
		{
			object:   &aimv1alpha2.AIMProfileCache{},
			identity: prepared.cacheOperation.Resource,
		},
		{
			object:   &aimv1alpha2.AIMService{},
			identity: prepared.serviceOperation.Resource,
		},
	}
	for _, observation := range observations {
		err := kubernetesClient.Get(
			ctx,
			client.ObjectKey{
				Namespace: observation.identity.Namespace,
				Name:      observation.identity.Name,
			},
			observation.object,
		)
		if err == nil {
			recorder.observeObject(observation.object, observation.identity)
		}
	}
}

func prepareFreshProfileCache(
	ctx context.Context,
	kubernetesClient client.Client,
	desired *aimv1alpha2.AIMProfileCache,
	identity ResourceIdentity,
	commit bool,
) (*aimv1alpha2.AIMProfileCache, string, error) {
	existing := &aimv1alpha2.AIMProfileCache{}
	key := client.ObjectKey{Namespace: identity.Namespace, Name: identity.Name}
	err := kubernetesClient.Get(ctx, key, existing)
	switch {
	case err == nil:
		if err := matchFreshProfileCache(existing, desired); err != nil {
			return existing, operationOutcomeConflict, err
		}
		desired.UID = existing.UID
		return existing, operationOutcomeReused, nil
	case !apierrors.IsNotFound(err):
		return nil, operationOutcomeFailed, kubernetesFailure("read target profile cache", identity, err)
	}

	createOptions := []client.CreateOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		createOptions = append(createOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "create target profile cache"); err != nil {
		return desired, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Create(ctx, desired, createOptions...); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if fetchErr := kubernetesClient.Get(ctx, key, existing); fetchErr == nil {
				if matchErr := matchFreshProfileCache(existing, desired); matchErr == nil {
					desired.UID = existing.UID
					return existing, operationOutcomeReused, nil
				}
			}
			return existing, operationOutcomeConflict, fmt.Errorf(
				"target AIMProfileCache %s/%s appeared with conflicting spec or scope",
				identity.Namespace,
				identity.Name,
			)
		}
		return desired, operationOutcomeFailed, kubernetesFailure("create fresh profile cache", identity, err)
	}
	if !commit {
		return desired, operationOutcomeDryRun, nil
	}
	return desired, operationOutcomeCreated, nil
}

func matchFreshProfileCache(existing, desired *aimv1alpha2.AIMProfileCache) error {
	if existing.Namespace != desired.Namespace ||
		existing.Name != desired.Name ||
		!equality.Semantic.DeepEqual(existing.Spec, desired.Spec) ||
		!freshCacheOwnershipMatches(existing, desired) {
		return fmt.Errorf(
			"existing AIMProfileCache %s/%s conflicts with the complete fresh-cache spec or scope",
			desired.Namespace,
			desired.Name,
		)
	}
	return nil
}

func freshCacheOwnershipMatches(
	existing, desired *aimv1alpha2.AIMProfileCache,
) bool {
	if desired.Spec.Mode == aimv1alpha2.ProfileCacheModeShared {
		return len(existing.OwnerReferences) == 0
	}
	expected := metav1.GetControllerOf(desired)
	actual := metav1.GetControllerOf(existing)
	return expected != nil &&
		actual != nil &&
		actual.APIVersion == expected.APIVersion &&
		actual.Kind == expected.Kind &&
		actual.Name == expected.Name &&
		actual.UID == expected.UID
}

func waitForProfileCache(
	ctx context.Context,
	kubernetesClient client.Client,
	desired *aimv1alpha2.AIMProfileCache,
	timeout, pollInterval time.Duration,
) (*aimv1alpha2.AIMProfileCache, error) {
	var latest *aimv1alpha2.AIMProfileCache
	err := waitBoundedly(ctx, timeout, pollInterval, func() (bool, error) {
		current := &aimv1alpha2.AIMProfileCache{}
		err := kubernetesClient.Get(ctx, client.ObjectKeyFromObject(desired), current)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, kubernetesFailure("read target profile cache", ResourceIdentity{
				Kind:      resourceKindProfileCache,
				Namespace: desired.Namespace,
				Name:      desired.Name,
			}, err)
		}
		latest = current
		if current.UID == "" {
			return false, nil
		}
		if desired.UID == "" {
			desired.UID = current.UID
		}
		if desired.UID != "" && current.UID != desired.UID {
			return false, fmt.Errorf("target AIMProfileCache UID changed while waiting for readiness")
		}
		if err := matchFreshProfileCache(current, desired); err != nil {
			return false, err
		}
		return current.DeletionTimestamp == nil &&
			current.Generation == current.Status.ObservedGeneration &&
			current.Status.Status == constants.AIMStatusReady, nil
	})
	if err != nil {
		return latest, fmt.Errorf(
			"target AIMProfileCache %s/%s did not become Ready: %s",
			desired.Namespace,
			desired.Name,
			safeWaitCause(err),
		)
	}
	return latest, nil
}

func validateSharedCacheAdoption(
	cache *aimv1alpha2.AIMProfileCache,
	shared *preparedSharedApply,
) error {
	if cache == nil ||
		cache.DeletionTimestamp != nil ||
		cache.Generation != cache.Status.ObservedGeneration ||
		cache.Status.Status != constants.AIMStatusReady ||
		cache.Spec.Mode != aimv1alpha2.ProfileCacheModeShared ||
		len(cache.OwnerReferences) != 0 {
		return fmt.Errorf("target Shared AIMProfileCache is not current, Ready, and ownerless")
	}
	resolved, err := exactResolvedArtifact(cache.Status.Artifacts)
	if err != nil {
		return fmt.Errorf("target Shared AIMProfileCache adoption is incomplete: %w", err)
	}
	if resolved.UID != shared.sourceArtifact.UID ||
		resolved.Name != shared.sourceArtifact.Name ||
		resolved.Model != shared.modelID ||
		resolved.PersistentVolumeClaim != shared.sourcePVC.Name ||
		resolved.Status != constants.AIMStatusReady {
		return fmt.Errorf(
			"target Shared AIMProfileCache does not report the exact predecessor artifact UID and PVC",
		)
	}
	return nil
}

func readAndValidateSharedCacheAdoption(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) (*aimv1alpha2.AIMProfileCache, error) {
	current := &aimv1alpha2.AIMProfileCache{}
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKeyFromObject(prepared.cacheObject),
		current,
	); err != nil {
		return nil, kubernetesFailure(
			"read target Shared profile cache",
			prepared.cacheOperation.Resource,
			err,
		)
	}
	if prepared.cacheObject.UID == "" ||
		current.UID != prepared.cacheObject.UID {
		return current, fmt.Errorf("target Shared AIMProfileCache UID changed")
	}
	if err := matchFreshProfileCache(current, prepared.cacheObject); err != nil {
		return current, err
	}
	if err := validateSharedCacheAdoption(current, prepared.shared); err != nil {
		return current, err
	}
	return current, nil
}

func protectSharedArtifact(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	commit bool,
) (*aimv1alpha1.AIMArtifact, string, error) {
	current := &aimv1alpha1.AIMArtifact{}
	key := client.ObjectKey{
		Namespace: prepared.shared.sourceArtifact.Namespace,
		Name:      prepared.shared.sourceArtifact.Name,
	}
	if err := kubernetesClient.Get(ctx, key, current); err != nil {
		return nil, operationOutcomeFailed, kubernetesFailure(
			"read Shared artifact before eviction protection",
			prepared.shared.sourceArtifact,
			err,
		)
	}
	if err := comparePinnedIdentity(current, prepared.shared.sourceArtifact); err != nil {
		return current, operationOutcomeFailed, err
	}
	if current.DeletionTimestamp != nil ||
		current.Generation != current.Status.ObservedGeneration ||
		current.Status.Status != constants.AIMStatusReady ||
		current.Status.Mode != aimv1alpha1.ArtifactModeShared ||
		len(current.OwnerReferences) != 0 ||
		current.Status.PersistentVolumeClaim != prepared.shared.sourcePVC.Name {
		return current, operationOutcomeFailed, fmt.Errorf(
			"shared artifact changed before eviction protection",
		)
	}

	before := current.DeepCopy()
	annotations := make(map[string]string, len(current.Annotations)+1)
	for name, value := range current.Annotations {
		annotations[name] = value
	}
	annotations[aimv1alpha1.ArtifactEvictionProtectedAnnotation] = aimv1alpha1.ArtifactEvictionProtectedValue
	current.Annotations = annotations
	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "protect Shared artifact from eviction"); err != nil {
		return current, operationOutcomeFailed, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, operationOutcomeFailed, kubernetesFailure(
			"protect Shared artifact from eviction",
			prepared.shared.sourceArtifact,
			err,
		)
	}
	if string(current.UID) != prepared.shared.sourceArtifact.UID ||
		len(current.OwnerReferences) != 0 {
		return current, operationOutcomeFailed, fmt.Errorf(
			"shared artifact identity or ownerlessness changed during eviction protection",
		)
	}
	if !commit {
		return current, operationOutcomeDryRun, nil
	}
	return current, operationOutcomePatched, nil
}

func validateInferenceServiceBeforeCutover(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
) error {
	current := &servingv1beta1.InferenceService{}
	identity := prepared.inferenceService.identity
	if err := kubernetesClient.Get(
		ctx,
		client.ObjectKey{Namespace: identity.Namespace, Name: identity.Name},
		current,
	); err != nil {
		return kubernetesFailure("read InferenceService before cutover", identity, err)
	}
	if !inferenceServiceDesiredStateUnchanged(
		prepared.inferenceService.object,
		current,
	) {
		return fmt.Errorf(
			"InferenceService changed before cutover; automatic migration requires identical no-rollout serving state",
		)
	}
	if !inferenceServiceReady(current) {
		return fmt.Errorf("InferenceService is not current and Ready immediately before cutover")
	}
	return nil
}

func patchServiceInPlace(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	commit bool,
) (*aimv1alpha2.AIMService, error) {
	current := &aimv1alpha2.AIMService{}
	key := client.ObjectKey{Namespace: prepared.unit.Namespace, Name: prepared.unit.Service.Name}
	if err := kubernetesClient.Get(ctx, key, current); err != nil {
		return nil, kubernetesFailure("read AIMService before cutover", prepared.unit.Service, err)
	}
	if string(current.UID) != prepared.unit.Service.UID {
		return current, fmt.Errorf("AIMService %s/%s UID changed before cutover", key.Namespace, key.Name)
	}
	digest, secretLiteral, err := contentDigest(
		current,
		prepared.unit.Service.APIVersion,
		prepared.unit.Service.Kind,
	)
	if err != nil || secretLiteral || digest != prepared.unit.Service.ContentDigest ||
		current.Generation != prepared.unit.Service.Generation {
		return current, fmt.Errorf("AIMService %s/%s relevant content changed before cutover", key.Namespace, key.Name)
	}

	before := current.DeepCopy()
	current.Spec.Profile = &aimv1alpha1.AIMServiceProfileConfig{
		Name: prepared.profileOperation.Resource.Name,
	}
	current.Spec.Template = nil
	annotations := make(map[string]string, len(current.Annotations)+1)
	for name, value := range current.Annotations {
		annotations[name] = value
	}
	annotations[constants.AnnotationReconcilerPipeline] = constants.ReconcilerPipelineProfile
	annotations[constants.AnnotationMigrationPreserveInferenceServiceGeneration] = strconv.FormatInt(
		current.Generation+1,
		10,
	)
	annotations[constants.AnnotationMigrationPreserveInferenceServiceUID] = prepared.inferenceService.identity.UID
	current.Annotations = annotations

	patch := client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})
	patchOptions := []client.PatchOption{client.FieldOwner(applyFieldOwner)}
	if !commit {
		patchOptions = append(patchOptions, client.DryRunAll)
	}
	if err := mutationContextError(ctx, "patch AIMService in place"); err != nil {
		return current, err
	}
	if err := kubernetesClient.Patch(ctx, current, patch, patchOptions...); err != nil {
		return current, kubernetesFailure("patch AIMService in place", prepared.serviceOperation.Resource, err)
	}
	if string(current.UID) != prepared.unit.Service.UID {
		return current, fmt.Errorf("AIMService UID changed during cutover")
	}
	return current, nil
}

func waitForCutover(
	ctx context.Context,
	kubernetesClient client.Client,
	prepared *preparedApply,
	profile client.Object,
	timeout, pollInterval time.Duration,
) (*aimv1alpha2.AIMService, error) {
	var latest *aimv1alpha2.AIMService
	err := waitBoundedly(ctx, timeout, pollInterval, func() (bool, error) {
		service := &aimv1alpha2.AIMService{}
		key := client.ObjectKey{Namespace: prepared.unit.Namespace, Name: prepared.unit.Service.Name}
		if err := kubernetesClient.Get(ctx, key, service); err != nil {
			return false, kubernetesFailure("read cutover AIMService", prepared.serviceOperation.Resource, err)
		}
		latest = service
		if string(service.UID) != prepared.unit.Service.UID {
			return false, fmt.Errorf("AIMService UID changed after cutover")
		}
		expectedGeneration := prepared.unit.Service.Generation + 1
		if service.Generation != expectedGeneration ||
			service.Annotations[constants.AnnotationMigrationPreserveInferenceServiceGeneration] !=
				strconv.FormatInt(expectedGeneration, 10) ||
			service.Annotations[constants.AnnotationMigrationPreserveInferenceServiceUID] !=
				prepared.inferenceService.identity.UID {
			return false, fmt.Errorf(
				"AIMService migration preservation generation or InferenceService UID pin changed after cutover",
			)
		}
		if !serviceCutoverReady(
			service,
			prepared.profileOperation.Resource,
			profile.GetUID(),
			prepared.cacheObject,
		) {
			return false, nil
		}
		inferenceService := &servingv1beta1.InferenceService{}
		if err := kubernetesClient.Get(ctx, client.ObjectKey{
			Namespace: prepared.unit.Namespace,
			Name:      prepared.inferenceService.identity.Name,
		}, inferenceService); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, kubernetesFailure("read post-cutover InferenceService", ResourceIdentity{
				Kind:      "InferenceService",
				Namespace: prepared.unit.Namespace,
				Name:      prepared.inferenceService.identity.Name,
			}, err)
		}
		if string(inferenceService.UID) != prepared.inferenceService.identity.UID {
			return false, fmt.Errorf("InferenceService UID changed after cutover")
		}
		if !inferenceServiceDesiredStateUnchanged(
			prepared.inferenceService.object,
			inferenceService,
		) {
			return false, fmt.Errorf(
				"InferenceService changed during cutover; automatic migration requires identical no-rollout serving state",
			)
		}
		return inferenceServiceReady(inferenceService), nil
	})
	if err != nil {
		return latest, fmt.Errorf(
			"AIMService %s/%s cutover did not preserve a Ready service and InferenceService identity: %s",
			prepared.unit.Namespace,
			prepared.unit.Service.Name,
			safeWaitCause(err),
		)
	}
	return latest, nil
}

func serviceCutoverReady(
	service *aimv1alpha2.AIMService,
	target ResourceIdentity,
	profileUID types.UID,
	targetCache *aimv1alpha2.AIMProfileCache,
) bool {
	if service.DeletionTimestamp != nil ||
		service.Generation != service.Status.ObservedGeneration ||
		service.Status.Status != constants.AIMStatusRunning ||
		service.Spec.Template != nil ||
		service.Spec.Profile == nil ||
		service.Spec.Profile.Name != target.Name ||
		service.Annotations[constants.AnnotationReconcilerPipeline] != constants.ReconcilerPipelineProfile ||
		service.Status.ReconcilerPipeline != constants.ReconcilerPipelineProfile ||
		service.Status.ResolvedProfile == nil {
		return false
	}
	resolved := service.Status.ResolvedProfile
	if resolved.Name != target.Name || resolved.UID != profileUID {
		return false
	}
	if targetCache != nil {
		if service.Status.Cache == nil ||
			service.Status.Cache.ProfileCacheRef == nil ||
			service.Status.Cache.ProfileCacheRef.Name != targetCache.Name ||
			service.Status.Cache.ProfileCacheRef.Namespace != targetCache.Namespace ||
			service.Status.Cache.ProfileCacheRef.UID != targetCache.UID {
			return false
		}
	}
	expectedScope := aimv1alpha1.AIMResolutionScopeNamespace
	if target.Kind == resourceKindClusterProfile {
		expectedScope = aimv1alpha1.AIMResolutionScopeCluster
	}
	return resolved.Scope == expectedScope
}

func inferenceServiceReady(inferenceService *servingv1beta1.InferenceService) bool {
	if inferenceService == nil ||
		inferenceService.DeletionTimestamp != nil ||
		inferenceService.Generation <= 0 ||
		inferenceService.Status.ObservedGeneration != inferenceService.Generation {
		return false
	}
	for _, condition := range inferenceService.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func inferenceServiceDesiredStateUnchanged(
	before, after *servingv1beta1.InferenceService,
) bool {
	return before != nil &&
		after != nil &&
		before.UID == after.UID &&
		before.Generation == after.Generation &&
		equality.Semantic.DeepEqual(before.Spec, after.Spec) &&
		equality.Semantic.DeepEqual(before.Labels, after.Labels) &&
		equality.Semantic.DeepEqual(before.Annotations, after.Annotations) &&
		equality.Semantic.DeepEqual(before.OwnerReferences, after.OwnerReferences) &&
		equality.Semantic.DeepEqual(before.Finalizers, after.Finalizers)
}

func waitBoundedly(
	ctx context.Context,
	timeout, pollInterval time.Duration,
	check func() (bool, error),
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-deadline.C:
			return fmt.Errorf("bounded wait timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}

func safeWaitCause(err error) string {
	if err == nil {
		return "unknown wait failure"
	}
	if apierrors.IsNotFound(err) {
		return "required object was not found"
	}
	return sanitizeDiagnosticMessage(err.Error())
}

func kubernetesFailure(action string, identity ResourceIdentity, err error) error {
	reason := apierrors.ReasonForError(err)
	if reason == metav1.StatusReasonUnknown {
		reason = "RequestFailed"
	}
	path := identity.Name
	if identity.Namespace != "" {
		path = identity.Namespace + "/" + identity.Name
	}
	return fmt.Errorf("%s for %s %s failed (%s)", action, identity.Kind, path, reason)
}

func mutationContextError(ctx context.Context, action string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s cancelled before Kubernetes mutation: %w", action, err)
	}
	return nil
}

func (recorder *resultRecorder) recordAttempt(
	unitID string,
	operation ProposedOperation,
) (int, error) {
	result := ApplyOperationResult{
		UnitID:    unitID,
		Sequence:  len(recorder.document.Operations) + 1,
		Action:    string(operation.Action),
		Resource:  operation.Resource,
		Outcome:   operationOutcomeAttempting,
		Attempted: true,
		Completed: false,
	}
	recorder.document.LastOperation = string(operation.Action)
	recorder.updateUnitProgress(unitID, string(operation.Action), false)
	recorder.document.Operations = append(recorder.document.Operations, result)
	index := len(recorder.document.Operations) - 1
	if err := recorder.write(); err != nil {
		recorder.document.Operations = recorder.document.Operations[:index]
		return -1, err
	}
	return index, nil
}

func (recorder *resultRecorder) completeAttempt(
	index int,
	outcome string,
	current client.Object,
	operationErr error,
) error {
	if index < 0 || index >= len(recorder.document.Operations) {
		return fmt.Errorf("complete unknown apply operation attempt %d", index)
	}
	result := &recorder.document.Operations[index]
	result.Outcome = outcome
	result.Completed = operationErr == nil
	if current != nil {
		state := currentResourceState(current, result.Resource)
		result.CurrentIdentity = &state.Identity
		result.CurrentState = &state
		recorder.observeObject(current, result.Resource)
	}
	if operationErr != nil {
		result.Error = sanitizeDiagnosticMessage(operationErr.Error())
	} else {
		result.Error = ""
	}
	recorder.document.LastOperation = result.Action
	recorder.updateUnitProgress(result.UnitID, result.Action, result.Completed)
	return recorder.write()
}

func (recorder *resultRecorder) recordFailure(
	unitID, action string,
	resource ResourceIdentity,
	current client.Object,
	operationErr error,
) error {
	result := ApplyOperationResult{
		UnitID:    unitID,
		Sequence:  len(recorder.document.Operations) + 1,
		Action:    action,
		Resource:  resource,
		Outcome:   operationOutcomeFailed,
		Attempted: true,
		Completed: false,
		Error:     sanitizeDiagnosticMessage(errorMessage(operationErr)),
	}
	if current != nil {
		state := currentResourceState(current, resource)
		result.CurrentIdentity = &state.Identity
		result.CurrentState = &state
		recorder.observeState(state)
	}
	recorder.document.LastOperation = action
	recorder.updateUnitProgress(unitID, action, false)
	recorder.document.Operations = append(recorder.document.Operations, result)
	if err := recorder.write(); err != nil {
		return err
	}
	return operationErr
}

func (recorder *resultRecorder) recordVerification(
	unitID, action string,
	resource ResourceIdentity,
	current client.Object,
) error {
	result := ApplyOperationResult{
		UnitID:    unitID,
		Sequence:  len(recorder.document.Operations) + 1,
		Action:    action,
		Resource:  resource,
		Outcome:   "verified",
		Attempted: true,
		Completed: true,
	}
	if current != nil {
		state := currentResourceState(current, resource)
		result.CurrentIdentity = &state.Identity
		result.CurrentState = &state
		recorder.observeState(state)
	}
	recorder.document.LastOperation = action
	recorder.updateUnitProgress(unitID, action, true)
	recorder.document.Operations = append(recorder.document.Operations, result)
	return recorder.write()
}

func (recorder *resultRecorder) updateUnitProgress(unitID, action string, completed bool) {
	recorder.document.LastAttemptedStep = action
	for index := range recorder.document.Units {
		unit := &recorder.document.Units[index]
		if unit.UnitID != unitID {
			continue
		}
		unit.LastAttemptedStep = action
		if completed {
			unit.LastCompletedStep = action
			recorder.document.LastCompletedStep = action
		}
		return
	}
}

func (recorder *resultRecorder) recordUnitTerminal(
	unitID string,
	outcome ApplyUnitOutcome,
	unitErr error,
) error {
	found := false
	for index := range recorder.document.Units {
		unit := &recorder.document.Units[index]
		if unit.UnitID != unitID {
			continue
		}
		unit.Outcome = outcome
		if unitErr != nil {
			unit.Error = sanitizeDiagnosticMessage(unitErr.Error())
		} else {
			unit.Error = ""
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("record terminal result for unknown migration unit %q", unitID)
	}
	recorder.recountUnitOutcomes()
	return recorder.write()
}

func (recorder *resultRecorder) recountUnitOutcomes() {
	recorder.document.Summary = ApplyResultSummary{}
	for _, unit := range recorder.document.Units {
		switch unit.Outcome {
		case ApplyUnitSucceeded:
			recorder.document.Summary.Succeeded++
		case ApplyUnitFailed:
			recorder.document.Summary.Failed++
		case ApplyUnitManual:
			recorder.document.Summary.Manual++
		case ApplyUnitSkipped:
			recorder.document.Summary.Skipped++
		}
	}
	recorder.document.ManualUnitsSkipped = recorder.document.Summary.Manual
}

func (recorder *resultRecorder) observeUnit(unit MigrationUnit) {
	for _, identity := range unit.ObservedResources {
		recorder.observe(identity)
	}
	recorder.observe(unit.Service)
}

func (recorder *resultRecorder) observe(identity ResourceIdentity) {
	for index := range recorder.document.ObservedIdentities {
		observed := recorder.document.ObservedIdentities[index]
		if observed.APIVersion == identity.APIVersion &&
			observed.Kind == identity.Kind &&
			observed.Namespace == identity.Namespace &&
			observed.Name == identity.Name {
			recorder.document.ObservedIdentities[index] = identity
			return
		}
	}
	recorder.document.ObservedIdentities = append(recorder.document.ObservedIdentities, identity)
}

func (recorder *resultRecorder) observeObject(
	object client.Object,
	planned ResourceIdentity,
) {
	recorder.observeState(currentResourceState(object, planned))
}

func (recorder *resultRecorder) observeState(state ApplyResourceState) {
	recorder.observe(state.Identity)
	for index := range recorder.document.ObservedStates {
		observed := recorder.document.ObservedStates[index].Identity
		if observed.APIVersion == state.Identity.APIVersion &&
			observed.Kind == state.Identity.Kind &&
			observed.Namespace == state.Identity.Namespace &&
			observed.Name == state.Identity.Name {
			recorder.document.ObservedStates[index] = state
			return
		}
	}
	recorder.document.ObservedStates = append(recorder.document.ObservedStates, state)
}

func currentResourceState(
	object client.Object,
	planned ResourceIdentity,
) ApplyResourceState {
	state := ApplyResourceState{
		Identity: currentIdentity(object, planned),
	}
	for _, owner := range object.GetOwnerReferences() {
		state.OwnerReferences = append(state.OwnerReferences, ApplyOwnerReference{
			APIVersion:         owner.APIVersion,
			Kind:               owner.Kind,
			Name:               owner.Name,
			UID:                string(owner.UID),
			Controller:         copyBoolPointer(owner.Controller),
			BlockOwnerDeletion: copyBoolPointer(owner.BlockOwnerDeletion),
		})
	}
	sort.SliceStable(state.OwnerReferences, func(i, j int) bool {
		left := state.OwnerReferences[i]
		right := state.OwnerReferences[j]
		return strings.Join(
			[]string{left.APIVersion, left.Kind, left.Name, left.UID},
			"\x00",
		) < strings.Join(
			[]string{right.APIVersion, right.Kind, right.Name, right.UID},
			"\x00",
		)
	})
	switch typed := object.(type) {
	case *aimv1alpha1.AIMService:
		state.Status = ptr.To(typed.Status.Status)
		state.ObservedGeneration = ptr.To(typed.Status.ObservedGeneration)
	case *aimv1alpha1.AIMTemplateCache:
		paused := typed.Annotations[constants.AnnotationReconciliationPaused] == reconciliationPausedValue
		state.ReconciliationPaused = &paused
		state.Status = ptr.To(typed.Status.Status)
		state.ObservedGeneration = ptr.To(typed.Status.ObservedGeneration)
		state.ReportedArtifacts = reportedArtifactStates(typed.Status.Artifacts)
	case *aimv1alpha2.AIMProfileCache:
		paused := typed.Annotations[constants.AnnotationReconciliationPaused] == reconciliationPausedValue
		state.ReconciliationPaused = &paused
		state.Status = ptr.To(typed.Status.Status)
		state.ObservedGeneration = ptr.To(typed.Status.ObservedGeneration)
		state.ReportedArtifacts = reportedArtifactStates(typed.Status.Artifacts)
	case *aimv1alpha1.AIMArtifact:
		state.Status = ptr.To(typed.Status.Status)
		state.ObservedGeneration = ptr.To(typed.Status.ObservedGeneration)
		state.PersistentVolumeClaim = typed.Status.PersistentVolumeClaim
	case *corev1.PersistentVolumeClaim:
		state.PersistentVolumePhase = ptr.To(typed.Status.Phase)
	}
	return state
}

func reportedArtifactStates(
	artifacts map[string]aimv1alpha1.AIMResolvedArtifact,
) []ApplyReportedArtifact {
	reported := make([]ApplyReportedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		reported = append(reported, ApplyReportedArtifact{
			UID:                   artifact.UID,
			Name:                  artifact.Name,
			Model:                 artifact.Model,
			Status:                artifact.Status,
			PersistentVolumeClaim: artifact.PersistentVolumeClaim,
		})
	}
	sort.SliceStable(reported, func(i, j int) bool {
		left := reported[i]
		right := reported[j]
		return strings.Join(
			[]string{left.UID, left.Name, left.Model, left.PersistentVolumeClaim},
			"\x00",
		) < strings.Join(
			[]string{right.UID, right.Name, right.Model, right.PersistentVolumeClaim},
			"\x00",
		)
	})
	return reported
}

func currentIdentity(object client.Object, planned ResourceIdentity) ResourceIdentity {
	digest, _, _ := contentDigest(object, planned.APIVersion, planned.Kind)
	return ResourceIdentity{
		Role:            planned.Role,
		APIVersion:      planned.APIVersion,
		Kind:            planned.Kind,
		Namespace:       object.GetNamespace(),
		Name:            object.GetName(),
		UID:             string(object.GetUID()),
		ResourceVersion: object.GetResourceVersion(),
		Generation:      object.GetGeneration(),
		ContentDigest:   digest,
	}
}

func (recorder *resultRecorder) write() error {
	contents, err := json.MarshalIndent(recorder.document, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal apply result: %w", err)
	}
	contents = append(contents, '\n')
	if recorder.publication == nil {
		return fmt.Errorf("apply result publication is not initialized")
	}
	if err := recorder.publication.write(contents); err != nil {
		return fmt.Errorf("update apply result: %w", err)
	}
	return nil
}

type resultPublication struct {
	root        *os.Root
	directory   string
	resultName  string
	lockName    string
	lock        *os.File
	initialized bool
}

func newRunID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate apply run identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func acquireResultPublication(
	path, runID, planDigest string,
) (*resultPublication, error) {
	cleanPath := filepath.Clean(path)
	resultName := filepath.Base(cleanPath)
	if resultName == "." || resultName == string(os.PathSeparator) {
		return nil, fmt.Errorf("apply result path must name a file")
	}
	root, directory, err := openNoFollowRoot(filepath.Dir(cleanPath))
	if err != nil {
		return nil, fmt.Errorf("open apply result directory without symlinks: %w", err)
	}
	publication := &resultPublication{
		root:       root,
		directory:  directory,
		resultName: resultName,
		lockName:   "." + resultName + ".lock",
	}
	lock, err := root.OpenFile(
		publication.lockName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf(
			"acquire exclusive apply lock %q: %w; another run may be active or require manual stale-lock removal",
			filepath.Join(directory, publication.lockName),
			err,
		)
	}
	publication.lock = lock
	releaseOnError := func() {
		_ = lock.Close()
		_ = root.Remove(publication.lockName)
		_ = root.Close()
	}
	if info, statErr := root.Lstat(resultName); statErr == nil {
		releaseOnError()
		return nil, fmt.Errorf(
			"apply result %q already exists (%s); archive it before starting a new run",
			path,
			info.Mode().Type(),
		)
	} else if !os.IsNotExist(statErr) {
		releaseOnError()
		return nil, fmt.Errorf("inspect existing apply result %q: %w", path, statErr)
	}
	lockContents := []byte(
		"runId=" + runID + "\n" +
			"planDigest=" + planDigest + "\n",
	)
	if _, err := lock.Write(lockContents); err != nil {
		releaseOnError()
		return nil, fmt.Errorf("write apply lock identity: %w", err)
	}
	if err := lock.Sync(); err != nil {
		releaseOnError()
		return nil, fmt.Errorf("sync apply lock identity: %w", err)
	}
	if err := verifyStableRoot(directory, root); err != nil {
		releaseOnError()
		return nil, err
	}
	return publication, nil
}

func (publication *resultPublication) close() {
	if publication == nil {
		return
	}
	if publication.lock != nil {
		_ = publication.lock.Close()
	}
	if publication.root != nil {
		_ = publication.root.Remove(publication.lockName)
		if directoryHandle, err := publication.root.Open("."); err == nil {
			_ = directoryHandle.Sync()
			_ = directoryHandle.Close()
		}
		_ = publication.root.Close()
	}
}

func (publication *resultPublication) write(contents []byte) error {
	if err := verifyStableRoot(publication.directory, publication.root); err != nil {
		return err
	}
	if !publication.initialized {
		if err := writePrivateRootFile(
			publication.root,
			publication.resultName,
			contents,
		); err != nil {
			return err
		}
		publication.initialized = true
		return syncAndVerifyRoot(publication.directory, publication.root)
	}
	if info, err := publication.root.Lstat(publication.resultName); err != nil {
		return fmt.Errorf("inspect current apply result: %w", err)
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("current apply result is not a regular file")
	}
	randomSuffix, err := newRunID()
	if err != nil {
		return err
	}
	temporaryName := "." + publication.resultName + "-" + randomSuffix + ".tmp"
	file, err := publication.root.OpenFile(
		temporaryName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("create temporary result file: %w", err)
	}
	defer func() {
		_ = publication.root.Remove(temporaryName)
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary result permissions: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary result: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary result: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary result: %w", err)
	}
	if err := publication.root.Rename(
		temporaryName,
		publication.resultName,
	); err != nil {
		return fmt.Errorf("replace result file atomically: %w", err)
	}
	return syncAndVerifyRoot(publication.directory, publication.root)
}

var diagnosticBearer = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)

func sanitizeDiagnosticMessage(message string) string {
	sanitized := embeddedCredentialURI.ReplaceAllString(message, "://<redacted>@")
	sanitized = embeddedSecretPair.ReplaceAllString(sanitized, "$1=<redacted>")
	sanitized = diagnosticBearer.ReplaceAllString(sanitized, "Bearer <redacted>")
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return "operation failed; inspect current object identities"
	}
	return sanitized
}

func safeDiagnosticError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", sanitizeDiagnosticMessage(err.Error()))
}

func errorMessage(err error) string {
	if err == nil {
		return "operation failed; inspect current object identities"
	}
	return err.Error()
}
