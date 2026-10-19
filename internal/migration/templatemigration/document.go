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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

const (
	// DocumentVersion is the plan format understood by this migration utility.
	DocumentVersion = "aim-template-migration/v2"

	// DedicatedHandoffEvidenceVersion is the non-executable snapshot format
	// emitted for each manual Dedicated migration.
	DedicatedHandoffEvidenceVersion = "aim-template-migration-dedicated-handoff-evidence/v1"
	// DedicatedHandoffRunbookReference identifies the manual procedure for
	// interpreting and comparing a Dedicated handoff snapshot.
	DedicatedHandoffRunbookReference = "docs/docs/admin/template-migration.md#manual-dedicated-handoff"

	backupFilename = "backup.yaml"
	planFilename   = "plan.json"

	dedicatedHandoffReasonCode = "DedicatedHandoffRequiresQuiescence"
	dedicatedHandoffReason     = "Dedicated migration requires administrator-managed writer quiescence and adoption synchronization"
)

// Outcome is the disposition of one migration unit.
type Outcome string

const (
	OutcomeAutomatic Outcome = "automatic"
	OutcomeManual    Outcome = "manual"
)

// OperationAction is an operation Apply may perform in a later stage.
type OperationAction string

const (
	OperationCreate OperationAction = "create"
	OperationReuse  OperationAction = "reuse"
	OperationPatch  OperationAction = "patch"
)

// ResourceIdentity pins one observed Kubernetes object without copying status
// or secret-bearing desired state into the plan.
type ResourceIdentity struct {
	Role            string `json:"role"`
	APIVersion      string `json:"apiVersion"`
	Kind            string `json:"kind"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
	ContentDigest   string `json:"contentDigest,omitempty"`
}

// ProposedOperation describes one bounded operation for a later Apply command.
type ProposedOperation struct {
	Action   OperationAction  `json:"action"`
	Resource ResourceIdentity `json:"resource"`
	Object   map[string]any   `json:"object,omitempty"`
	Patch    map[string]any   `json:"patch,omitempty"`
}

// Finding is a stable, secret-free diagnostic.
type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Imputation records a deterministic default or field translation.
type Imputation struct {
	Field  string `json:"field"`
	Source string `json:"source"`
	Rule   string `json:"rule"`
}

// SecretReference records a Secret name/key reference without reading or
// copying the Secret object or its contents.
type SecretReference struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Key       string `json:"key,omitempty"`
	Use       string `json:"use"`
}

// MigrationUnit is one AIMService and its direct migration closure.
type MigrationUnit struct {
	ID                 string              `json:"id"`
	Namespace          string              `json:"namespace"`
	Service            ResourceIdentity    `json:"service"`
	Outcome            Outcome             `json:"outcome"`
	TargetProfile      *ResourceIdentity   `json:"targetProfile,omitempty"`
	ObservedResources  []ResourceIdentity  `json:"observedResources"`
	ProposedOperations []ProposedOperation `json:"proposedOperations"`
	Imputations        []Imputation        `json:"imputations"`
	Warnings           []Finding           `json:"warnings"`
	ManualOutcomes     []Finding           `json:"manualOutcomes"`
	SecretReferences   []SecretReference   `json:"secretReferences"`
}

// PlanDocument is the editable plan emitted by the read-only Plan command.
type PlanDocument struct {
	DocumentVersion       string          `json:"documentVersion"`
	RuntimeProjectionMode string          `json:"runtimeProjectionMode"`
	SelectedNamespaces    []string        `json:"selectedNamespaces"`
	Units                 []MigrationUnit `json:"units"`
	Warnings              []Finding       `json:"warnings"`
}

// Summary is the concise terminal result for a plan.
type Summary struct {
	Services  int
	Automatic int
	Manual    int
	Warnings  int
}

// Paths identifies the private files written by Plan.
type Paths struct {
	Backup                   string
	Plan                     string
	DedicatedHandoffEvidence []string
}

type backupResource struct {
	unitID     string
	role       string
	apiVersion string
	kind       string
	object     client.Object
}

// DedicatedHandoffEvidenceDocument is a private, non-executable snapshot for
// one separately approved manual Dedicated handoff.
type DedicatedHandoffEvidenceDocument struct {
	DocumentVersion       string             `json:"documentVersion"`
	PlanningTimestamp     time.Time          `json:"planningTimestamp"`
	UnitID                string             `json:"unitId"`
	ManualReason          Finding            `json:"manualReason"`
	RunbookReference      string             `json:"runbookReference"`
	Service               ApplyResourceState `json:"service"`
	SourceTemplateCache   ApplyResourceState `json:"sourceTemplateCache"`
	SourceArtifact        ApplyResourceState `json:"sourceArtifact"`
	PersistentVolumeClaim ApplyResourceState `json:"persistentVolumeClaim"`
}

type dedicatedHandoffEvidenceFile struct {
	name     string
	contents []byte
}

// PlanResult contains the editable document and the private in-memory backup
// inputs. Backup inputs are intentionally not exported to callers.
type PlanResult struct {
	Document          PlanDocument
	backupResources   []backupResource
	planningTimestamp time.Time
}

// Summary returns deterministic counts for terminal output.
func (r PlanResult) Summary() Summary {
	summary := Summary{Services: len(r.Document.Units), Warnings: len(r.Document.Warnings)}
	for _, unit := range r.Document.Units {
		switch unit.Outcome {
		case OutcomeAutomatic:
			summary.Automatic++
		default:
			summary.Manual++
		}
		summary.Warnings += len(unit.Warnings)
	}
	return summary
}

// WritePlanDirectory creates a new private output directory and writes the
// migration documents plus any required Dedicated evidence snapshots. It
// refuses to reuse an existing path.
func WritePlanDirectory(outputDirectory string, result *PlanResult) (Paths, error) {
	if result == nil {
		return Paths{}, fmt.Errorf("plan result is nil")
	}
	if strings.TrimSpace(outputDirectory) == "" {
		return Paths{}, fmt.Errorf("output directory is required")
	}

	backup, warnings := buildBackupYAML(result.backupResources)
	result.Document.Warnings = append(result.Document.Warnings, warnings...)
	normalizeDocument(&result.Document)

	plan, err := json.MarshalIndent(result.Document, "", "  ")
	if err != nil {
		return Paths{}, fmt.Errorf("marshal plan document: %w", err)
	}
	plan = append(plan, '\n')
	evidenceFiles, err := buildDedicatedHandoffEvidenceFiles(result)
	if err != nil {
		return Paths{}, err
	}

	cleanOutput := filepath.Clean(outputDirectory)
	outputName := filepath.Base(cleanOutput)
	if outputName == "." || outputName == string(os.PathSeparator) {
		return Paths{}, fmt.Errorf("output directory must name a new child directory")
	}
	parentRoot, parentDirectory, err := openNoFollowRoot(filepath.Dir(cleanOutput))
	if err != nil {
		return Paths{}, fmt.Errorf("open output parent without symlinks: %w", err)
	}
	defer func() {
		_ = parentRoot.Close()
	}()
	if err := parentRoot.Mkdir(outputName, 0o700); err != nil {
		return Paths{}, fmt.Errorf("create new output directory %q: %w", outputDirectory, err)
	}
	outputRoot, err := parentRoot.OpenRoot(outputName)
	if err != nil {
		return Paths{}, fmt.Errorf("open new output directory: %w", err)
	}
	defer func() {
		_ = outputRoot.Close()
	}()
	createdInfo, err := parentRoot.Lstat(outputName)
	if err != nil {
		return Paths{}, fmt.Errorf("inspect new output directory: %w", err)
	}
	openedInfo, err := outputRoot.Stat(".")
	if err != nil {
		return Paths{}, fmt.Errorf("inspect opened output directory: %w", err)
	}
	if createdInfo.Mode()&os.ModeSymlink != 0 ||
		!createdInfo.IsDir() ||
		!os.SameFile(createdInfo, openedInfo) {
		return Paths{}, fmt.Errorf("new output directory identity changed during creation")
	}
	if err := outputRoot.Chmod(".", 0o700); err != nil {
		return Paths{}, fmt.Errorf("set output directory permissions: %w", err)
	}
	outputDirectoryAbsolute := filepath.Join(parentDirectory, outputName)
	paths := Paths{
		Backup: filepath.Join(outputDirectory, backupFilename),
		Plan:   filepath.Join(outputDirectory, planFilename),
	}
	for _, evidence := range evidenceFiles {
		paths.DedicatedHandoffEvidence = append(
			paths.DedicatedHandoffEvidence,
			filepath.Join(outputDirectory, evidence.name),
		)
	}
	if err := writePrivateRootFile(outputRoot, backupFilename, backup); err != nil {
		return Paths{}, err
	}
	if err := syncAndVerifyRoot(outputDirectoryAbsolute, outputRoot); err != nil {
		return Paths{}, err
	}
	if err := writePrivateRootFile(outputRoot, planFilename, plan); err != nil {
		return Paths{}, err
	}
	if err := syncAndVerifyRoot(outputDirectoryAbsolute, outputRoot); err != nil {
		return Paths{}, err
	}
	for _, evidence := range evidenceFiles {
		if err := writePrivateRootFile(outputRoot, evidence.name, evidence.contents); err != nil {
			return Paths{}, err
		}
		if err := syncAndVerifyRoot(outputDirectoryAbsolute, outputRoot); err != nil {
			return Paths{}, err
		}
	}
	if err := verifyStableRoot(parentDirectory, parentRoot); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func buildDedicatedHandoffEvidenceFiles(
	result *PlanResult,
) ([]dedicatedHandoffEvidenceFile, error) {
	var files []dedicatedHandoffEvidenceFile
	for index := range result.Document.Units {
		unit := &result.Document.Units[index]
		if !hasManualCode(unit, dedicatedHandoffReasonCode) {
			continue
		}
		if result.planningTimestamp.IsZero() {
			return nil, fmt.Errorf("build Dedicated handoff evidence for unit %q: planning timestamp is unavailable", unit.ID)
		}
		service, err := dedicatedEvidenceResourceState(result, *unit, "service", resourceKindService)
		if err != nil {
			return nil, err
		}
		cache, err := dedicatedEvidenceResourceState(
			result,
			*unit,
			"source-template-cache",
			resourceKindTemplateCache,
		)
		if err != nil {
			return nil, err
		}
		artifact, err := dedicatedEvidenceResourceState(
			result,
			*unit,
			"source-artifact",
			resourceKindArtifact,
		)
		if err != nil {
			return nil, err
		}
		claim, err := dedicatedEvidenceResourceState(result, *unit, "source-pvc", resourceKindPVC)
		if err != nil {
			return nil, err
		}
		document := DedicatedHandoffEvidenceDocument{
			DocumentVersion:       DedicatedHandoffEvidenceVersion,
			PlanningTimestamp:     result.planningTimestamp.UTC(),
			UnitID:                unit.ID,
			ManualReason:          Finding{Code: dedicatedHandoffReasonCode, Message: dedicatedHandoffReason},
			RunbookReference:      DedicatedHandoffRunbookReference,
			Service:               service,
			SourceTemplateCache:   cache,
			SourceArtifact:        artifact,
			PersistentVolumeClaim: claim,
		}
		contents, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal Dedicated handoff evidence for unit %q: %w", unit.ID, err)
		}
		name := dedicatedHandoffEvidenceFilename(unit.Namespace, unit.Service.Name)
		files = append(files, dedicatedHandoffEvidenceFile{name: name, contents: append(contents, '\n')})
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return files, nil
}

func dedicatedEvidenceResourceState(
	result *PlanResult,
	unit MigrationUnit,
	role, kind string,
) (ApplyResourceState, error) {
	identity := findObservedIdentity(unit.ObservedResources, role)
	if identity == nil || identity.Kind != kind {
		return ApplyResourceState{}, fmt.Errorf(
			"build Dedicated handoff evidence for unit %q: required %s identity is unavailable",
			unit.ID,
			role,
		)
	}
	for _, resource := range result.backupResources {
		if resource.unitID != unit.ID || resource.role != role {
			continue
		}
		digest, secretLiteral, err := contentDigest(resource.object, identity.APIVersion, identity.Kind)
		if err != nil {
			return ApplyResourceState{}, fmt.Errorf(
				"build Dedicated handoff evidence for unit %q: serialize %s state: %w",
				unit.ID,
				role,
				err,
			)
		}
		if secretLiteral {
			return ApplyResourceState{}, fmt.Errorf(
				"build Dedicated handoff evidence for unit %q: %s contains a secret-looking literal",
				unit.ID,
				role,
			)
		}
		if identity.ContentDigest == "" || digest != identity.ContentDigest {
			return ApplyResourceState{}, fmt.Errorf(
				"build Dedicated handoff evidence for unit %q: %s identity is incomplete or changed",
				unit.ID,
				role,
			)
		}
		state := currentResourceState(resource.object, *identity)
		if state.Identity != *identity {
			return ApplyResourceState{}, fmt.Errorf(
				"build Dedicated handoff evidence for unit %q: %s identity changed",
				unit.ID,
				role,
			)
		}
		return state, nil
	}
	return ApplyResourceState{}, fmt.Errorf(
		"build Dedicated handoff evidence for unit %q: required %s state is unavailable",
		unit.ID,
		role,
	)
}

func dedicatedHandoffEvidenceFilename(namespace, service string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + service))
	namespace = safeEvidenceFilenamePart(namespace, 48)
	service = safeEvidenceFilenamePart(service, 120)
	return fmt.Sprintf(
		"dedicated-handoff-%s--%s-%s.json",
		namespace,
		service,
		hex.EncodeToString(sum[:6]),
	)
}

func safeEvidenceFilenamePart(value string, limit int) string {
	value = unsafeEvidenceFilenamePart.ReplaceAllString(value, "-")
	value = strings.Trim(value, ".-_")
	if value == "" {
		value = "unnamed"
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func writePrivateRootFile(root *os.Root, name string, contents []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private file %q: %w", name, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("set permissions on %q: %w", name, err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("verify permissions on %q: %w", name, err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("verify permissions on %q: got %04o, require 0600", name, info.Mode().Perm())
	}
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %q: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %q: %w", name, err)
	}
	return nil
}

func openNoFollowRoot(path string) (*os.Root, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve absolute directory path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	rootPath := volume + string(os.PathSeparator)
	current, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, "", fmt.Errorf("open filesystem root: %w", err)
	}
	relative := strings.TrimPrefix(absolute, rootPath)
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		info, err := current.Lstat(component)
		if err != nil {
			_ = current.Close()
			return nil, "", fmt.Errorf("inspect directory component %q: %w", component, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			_ = current.Close()
			return nil, "", fmt.Errorf("directory component %q is not a non-symlink directory", component)
		}
		next, err := current.OpenRoot(component)
		if err != nil {
			_ = current.Close()
			return nil, "", fmt.Errorf("open directory component %q: %w", component, err)
		}
		openedInfo, err := next.Stat(".")
		if err != nil || !os.SameFile(info, openedInfo) {
			_ = next.Close()
			_ = current.Close()
			return nil, "", fmt.Errorf("directory component %q changed while opening", component)
		}
		_ = current.Close()
		current = next
	}
	if err := verifyStableRoot(absolute, current); err != nil {
		_ = current.Close()
		return nil, "", err
	}
	return current, absolute, nil
}

func verifyStableRoot(path string, root *os.Root) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify publication directory %q: %w", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return fmt.Errorf("publication directory %q is not a non-symlink directory", path)
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("verify opened publication directory: %w", err)
	}
	if !os.SameFile(pathInfo, rootInfo) {
		return fmt.Errorf("publication directory %q was replaced", path)
	}
	return nil
}

func syncAndVerifyRoot(path string, root *os.Root) error {
	directoryHandle, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open publication directory for sync: %w", err)
	}
	if err := directoryHandle.Sync(); err != nil {
		_ = directoryHandle.Close()
		return fmt.Errorf("sync publication directory: %w", err)
	}
	if err := directoryHandle.Close(); err != nil {
		return fmt.Errorf("close publication directory: %w", err)
	}
	return verifyStableRoot(path, root)
}

func buildBackupYAML(resources []backupResource) ([]byte, []Finding) {
	sorted := append([]backupResource(nil), resources...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left := backupResourceKey(sorted[i])
		right := backupResourceKey(sorted[j])
		if left == right {
			return sorted[i].unitID < sorted[j].unitID
		}
		return left < right
	})

	seen := map[string]struct{}{}
	var documents [][]byte
	var warnings []Finding
	for _, resource := range sorted {
		key := backupResourceKey(resource)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}

		sanitized, _, err := sanitizeObject(resource.object, resource.apiVersion, resource.kind)
		if err != nil {
			warnings = append(warnings, Finding{
				Code:    "BackupSerializationFailed",
				Message: fmt.Sprintf("could not serialize %s for migration unit %s", displayResource(resource), resource.unitID),
			})
			continue
		}
		if sanitized == nil {
			continue
		}
		encoded, err := yaml.Marshal(sanitized)
		if err != nil {
			warnings = append(warnings, Finding{
				Code:    "BackupSerializationFailed",
				Message: fmt.Sprintf("could not serialize %s for migration unit %s", displayResource(resource), resource.unitID),
			})
			continue
		}
		documents = append(documents, bytes.TrimSpace(encoded))
	}
	sortFindings(warnings)

	if len(documents) == 0 {
		return []byte("# no replayable predecessor resources\n"), warnings
	}
	return append(bytes.Join(documents, []byte("\n---\n")), '\n'), warnings
}

func backupResourceKey(resource backupResource) string {
	namespace := ""
	name := ""
	if resource.object != nil {
		namespace = resource.object.GetNamespace()
		name = resource.object.GetName()
	}
	return strings.Join([]string{resource.apiVersion, resource.kind, namespace, name}, "\x00")
}

func displayResource(resource backupResource) string {
	if resource.object == nil {
		return resource.kind
	}
	if resource.object.GetNamespace() == "" {
		return fmt.Sprintf("%s/%s", resource.kind, resource.object.GetName())
	}
	return fmt.Sprintf("%s/%s/%s", resource.kind, resource.object.GetNamespace(), resource.object.GetName())
}

func sanitizeObject(object client.Object, apiVersion, kind string) (map[string]any, bool, error) {
	if object == nil {
		return nil, false, fmt.Errorf("object is nil")
	}
	if kind == "Secret" {
		return nil, false, nil
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, false, err
	}
	value["apiVersion"] = apiVersion
	value["kind"] = kind
	delete(value, "status")

	if metadata, ok := value["metadata"].(map[string]any); ok {
		for _, field := range []string{
			"creationTimestamp",
			"deletionGracePeriodSeconds",
			"deletionTimestamp",
			"finalizers",
			"generateName",
			"generation",
			"managedFields",
			"ownerReferences",
			"resourceVersion",
			"selfLink",
			"uid",
		} {
			delete(metadata, field)
		}
	}
	normalizeDigestRepresentation(value, apiVersion, kind)
	secretLiteral := sanitizeMap(value, false)
	return value, secretLiteral, nil
}

func normalizeDigestRepresentation(value map[string]any, apiVersion, kind string) {
	if value == nil {
		return
	}
	spec, ok := value["spec"].(map[string]any)
	if !ok {
		return
	}
	switch {
	case apiVersion == aimv1alpha2.GroupVersion.String() &&
		(kind == resourceKindProfile || kind == resourceKindClusterProfile):
		if manualOnly, found := spec["manualSelectionOnly"]; found && manualOnly == false {
			delete(spec, "manualSelectionOnly")
		}
	case apiVersion == aimv1alpha1.GroupVersion.String() &&
		(kind == resourceKindServiceTemplate || kind == resourceKindClusterTemplate):
		hardware, _ := spec["hardware"].(map[string]any)
		gpu, _ := hardware["gpu"].(map[string]any)
		if requests, found := gpu["requests"].(float64); found && requests == 0 {
			delete(gpu, "requests")
		}
	}
}

func contentDigest(object client.Object, apiVersion, kind string) (string, bool, error) {
	sanitized, secretLiteral, err := sanitizeObject(object, apiVersion, kind)
	if err != nil || sanitized == nil {
		return "", secretLiteral, err
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return "", secretLiteral, err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), secretLiteral, nil
}

func sanitizeMap(value map[string]any, referenceContext bool) bool {
	secretLiteral := false
	if name, ok := value["name"].(string); ok {
		if literal, exists := value["value"]; exists && hasLiteralValue(literal) && sensitiveName(name) {
			delete(value, "value")
			secretLiteral = true
		}
	}

	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := value[key]
		nextReferenceContext := referenceContext || isReferenceContainer(key)
		if key == "annotations" {
			if annotations, ok := item.(map[string]any); ok {
				if sanitizeAnnotations(annotations) {
					secretLiteral = true
				}
				continue
			}
		}
		if !nextReferenceContext &&
			!isSafeReferenceField(key) &&
			sensitiveName(key) &&
			hasLiteralValue(item) {
			delete(value, key)
			secretLiteral = true
			continue
		}
		switch typed := item.(type) {
		case map[string]any:
			if sanitizeMap(typed, nextReferenceContext) {
				secretLiteral = true
			}
		case []any:
			sanitized, found := sanitizeSlice(
				typed,
				nextReferenceContext || key == "imagePullSecrets",
			)
			value[key] = sanitized
			if found {
				secretLiteral = true
			}
		case string:
			if nextReferenceContext || isSafeReferenceField(key) {
				continue
			}
			if sensitiveName(key) || stringContainsSecretLiteral(typed) {
				delete(value, key)
				secretLiteral = true
			}
		}
	}
	return secretLiteral
}

func sanitizeSlice(value []any, referenceContext bool) ([]any, bool) {
	sanitized := make([]any, 0, len(value))
	secretLiteral := false
	for index := 0; index < len(value); index++ {
		item := value[index]
		switch typed := item.(type) {
		case map[string]any:
			if sanitizeMap(typed, referenceContext) {
				secretLiteral = true
			}
			sanitized = append(sanitized, typed)
		case []any:
			nested, found := sanitizeSlice(typed, referenceContext)
			if found {
				secretLiteral = true
			}
			sanitized = append(sanitized, nested)
		case string:
			if referenceContext {
				sanitized = append(sanitized, typed)
				continue
			}
			if credentialArgumentName(typed) {
				secretLiteral = true
				if index+1 < len(value) {
					index++
				}
				continue
			}
			if stringContainsSecretLiteral(typed) {
				secretLiteral = true
				continue
			}
			sanitized = append(sanitized, typed)
		default:
			sanitized = append(sanitized, typed)
		}
	}
	return sanitized, secretLiteral
}

func sanitizeAnnotations(annotations map[string]any) bool {
	secretLiteral := false
	for key, item := range annotations {
		text, ok := item.(string)
		if !ok {
			if isCredentialSnapshotAnnotation(key) {
				delete(annotations, key)
				secretLiteral = true
			}
			continue
		}

		structured, structuredSecret := inspectStructuredLiteral(text)
		if isCredentialSnapshotAnnotation(key) {
			delete(annotations, key)
			if structuredSecret || stringContainsSecretLiteral(text) {
				secretLiteral = true
			}
			continue
		}
		if stringContainsSecretLiteral(text) || (structured && structuredSecret) {
			delete(annotations, key)
			secretLiteral = true
		}
	}
	return secretLiteral
}

func inspectStructuredLiteral(value string) (bool, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false, false
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return true, true
	}
	switch typed := decoded.(type) {
	case map[string]any:
		return true, sanitizeMap(typed, false)
	case []any:
		_, secretLiteral := sanitizeSlice(typed, false)
		return true, secretLiteral
	default:
		return true, false
	}
}

func isCredentialSnapshotAnnotation(key string) bool {
	return key == constants.AimLabelDomain+"/legacy-auto-custom-spec" ||
		key == "kubectl.kubernetes.io/last-applied-configuration"
}

func hasLiteralValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func sensitiveName(value string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(value))
	for _, marker := range []string{
		"accesskey",
		"accesstoken",
		"apikey",
		"authorization",
		"bearertoken",
		"clientsecret",
		"credential",
		"password",
		"passwd",
		"privatekey",
		"secretkey",
		"sessiontoken",
		"signature",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "sig" ||
		normalized == "secret" ||
		normalized == "token" ||
		strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "token")
}

func credentialArgumentName(value string) bool {
	argument := strings.TrimSpace(value)
	if !strings.HasPrefix(argument, "-") {
		return false
	}
	trimmed := strings.TrimLeft(argument, "-")
	if trimmed == "" {
		return false
	}
	name, _, _ := strings.Cut(trimmed, "=")
	return sensitiveName(name)
}

func isReferenceContainer(key string) bool {
	normalized := strings.ToLower(key)
	return strings.Contains(normalized, "secretref") ||
		strings.Contains(normalized, "secretkeyref") ||
		normalized == "valuefrom" ||
		normalized == "imagepullsecrets"
}

func isSafeReferenceField(key string) bool {
	normalized := strings.ToLower(key)
	return normalized == "secretname" ||
		strings.HasSuffix(normalized, "keykey")
}

var (
	embeddedCredentialURI      = regexp.MustCompile(`(?i)://[^/\s:@]+:[^@\s/]+@`)
	embeddedSecretPair         = regexp.MustCompile(`(?i)(token|password|passwd|secret|credential|api[_-]?key|access[_-]?key|client[_-]?secret)["']?\s*[:=]\s*(?:"[^"]+"|'[^']+'|[^\s,;}\]]+)`)
	unsafeEvidenceFilenamePart = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

func stringContainsSecretLiteral(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if embeddedCredentialURI.MatchString(trimmed) || embeddedSecretPair.MatchString(trimmed) {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key, values := range parsed.Query() {
		if sensitiveName(key) && len(values) > 0 {
			return true
		}
	}
	return false
}

func normalizeDocument(document *PlanDocument) {
	sort.Strings(document.SelectedNamespaces)
	sort.SliceStable(document.Units, func(i, j int) bool {
		return document.Units[i].ID < document.Units[j].ID
	})
	sortFindings(document.Warnings)
	for index := range document.Units {
		normalizeUnit(&document.Units[index])
	}
}

func normalizeUnit(unit *MigrationUnit) {
	sort.SliceStable(unit.ObservedResources, func(i, j int) bool {
		return identityKey(unit.ObservedResources[i]) < identityKey(unit.ObservedResources[j])
	})
	sort.SliceStable(unit.Imputations, func(i, j int) bool {
		left := unit.Imputations[i].Field + "\x00" + unit.Imputations[i].Source
		right := unit.Imputations[j].Field + "\x00" + unit.Imputations[j].Source
		return left < right
	})
	sortFindings(unit.Warnings)
	sortFindings(unit.ManualOutcomes)
	sort.SliceStable(unit.SecretReferences, func(i, j int) bool {
		left := strings.Join([]string{
			unit.SecretReferences[i].Namespace,
			unit.SecretReferences[i].Name,
			unit.SecretReferences[i].Key,
			unit.SecretReferences[i].Use,
		}, "\x00")
		right := strings.Join([]string{
			unit.SecretReferences[j].Namespace,
			unit.SecretReferences[j].Name,
			unit.SecretReferences[j].Key,
			unit.SecretReferences[j].Use,
		}, "\x00")
		return left < right
	})
}

func identityKey(identity ResourceIdentity) string {
	return strings.Join([]string{
		identity.Role,
		identity.APIVersion,
		identity.Kind,
		identity.Namespace,
		identity.Name,
	}, "\x00")
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left := findings[i].Code + "\x00" + findings[i].Message
		right := findings[j].Code + "\x00" + findings[j].Message
		return left < right
	})
}
