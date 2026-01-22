# Custom Models with Inline Sources - Implementation Plan

This plan implements [ADR-0002: Custom Models with Inline Sources](../EnterpriseAI-specs/AIM-Engine/adrs/0002-custom-models-with-inline-sources.md).

## Overview

The goal is to support external model sources (S3, HuggingFace) without requiring container image rebuilds. Two paths:
1. **Path 1**: Direct AIMModel creation with `spec.modelSources` and `spec.customTemplates`
2. **Path 2**: AIMService `spec.model.custom` for ad-hoc deployments (auto-creates AIMModel)

---

## Phase 1: API Type Changes

### Step 1.1: Extend AIMModelSource

**File**: `api/v1alpha1/discovery.go`

Update `AIMModelSource` struct:
- [x] Rename `Name` field to `ModelId` with validation pattern `^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$`
- [x] Add `Env []corev1.EnvVar` field for per-source credential overrides
- [x] Update JSON tag from `name` to `modelId`
- [x] Add kubebuilder validation markers

```go
type AIMModelSource struct {
    // ModelID is the canonical identifier in {org}/{name} format.
    // Determines the cache mount path: /workspace/model-cache/{modelId}
    // +required
    // +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+/[a-zA-Z0-9._-]+$`
    ModelID string `json:"modelId"`

    // SourceURI is the location from which the model should be downloaded.
    // +kubebuilder:validation:Pattern=`^(hf|s3)://[^ \t\r\n]+$`
    SourceURI string `json:"sourceUri"`

    // Size is the expected storage space required. Required for custom models.
    // +optional
    Size *resource.Quantity `json:"size,omitempty"`

    // Env specifies per-source credential overrides.
    // Takes precedence over base-level env for the same variable name.
    // +optional
    // +listType=map
    // +listMapKey=name
    Env []corev1.EnvVar `json:"env,omitempty"`
}
```

### Step 1.2: Add Hardware Requirements Types

**File**: `api/v1alpha1/runtime.go` (or new file `api/v1alpha1/hardware_types.go`)

Add new hardware types alongside existing `AIMGpuSelector`:

- [x] Add `AIMHardwareRequirements` struct
- [x] Add `AIMGpuRequirements` struct (similar to existing but with `models` array and `minVram`)
- [x] Add `AIMCpuRequirements` struct

```go
// AIMHardwareRequirements specifies compute resource requirements.
type AIMHardwareRequirements struct {
    // GPU specifies GPU requirements. If not set, no GPUs are requested.
    // +optional
    GPU *AIMGpuRequirements `json:"gpu,omitempty"`

    // CPU specifies CPU requirements.
    // +optional
    CPU *AIMCpuRequirements `json:"cpu,omitempty"`
}

// AIMGpuRequirements specifies GPU resource requirements.
type AIMGpuRequirements struct {
    // Requests is the number of GPUs to request. Required when GPU is specified.
    // +required
    // +kubebuilder:validation:Minimum=1
    Requests int32 `json:"requests"`

    // Models limits deployment to specific GPU models.
    // +optional
    Models []string `json:"models,omitempty"`

    // MinVRAM limits deployment to GPUs having at least this much VRAM.
    // +optional
    MinVRAM *resource.Quantity `json:"minVram,omitempty"`
}

// AIMCpuRequirements specifies CPU resource requirements.
type AIMCpuRequirements struct {
    // Requests is the number of CPU cores to request.
    // +optional
    Requests *resource.Quantity `json:"requests,omitempty"`
}
```

### Step 1.3: Add Template Type Enum

**File**: `api/v1alpha1/aimservicetemplate_shared.go`

- [x] Add `AIMTemplateType` enum (already exists as `AIMProfileType`, can reuse or alias)

```go
// AIMTemplateType indicates the optimization status of a template.
// +kubebuilder:validation:Enum=optimized;preview;unoptimized
type AIMTemplateType string

const (
    AIMTemplateTypeOptimized   AIMTemplateType = "optimized"
    AIMTemplateTypePreview     AIMTemplateType = "preview"
    AIMTemplateTypeUnoptimized AIMTemplateType = "unoptimized"
)
```

### Step 1.4: Add CustomTemplate Types

**File**: `api/v1alpha1/aimmodel_shared.go`

Add custom template and profile types:

- [x] Add `AIMCustomTemplate` struct
- [x] Add `AIMTemplateProfile` struct

```go
// AIMCustomTemplate defines a custom template configuration for a model.
type AIMCustomTemplate struct {
    // Name is the template name. Auto-generated if not provided.
    // +optional
    Name string `json:"name,omitempty"`

    // Type indicates the optimization status of this template.
    // +optional
    // +kubebuilder:validation:Enum=optimized;preview;unoptimized
    // +kubebuilder:default=unoptimized
    Type AIMTemplateType `json:"type,omitempty"`

    // Env specifies environment variable overrides when this template is selected.
    // +optional
    // +listType=map
    // +listMapKey=name
    Env []corev1.EnvVar `json:"env,omitempty"`

    // Hardware specifies GPU and CPU requirements for this template.
    // Optional when spec.hardware is set (inherits from spec).
    // +optional
    Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

    // Profile declares runtime profile variables for template selection.
    // +optional
    Profile *AIMTemplateProfile `json:"profile,omitempty"`
}

// AIMTemplateProfile declares profile variables for template selection.
type AIMTemplateProfile struct {
    // Metric specifies the optimization target (e.g., latency, throughput).
    // +optional
    Metric string `json:"metric,omitempty"`

    // Precision specifies the numerical precision (e.g., fp8, fp16, bf16).
    // +optional
    Precision string `json:"precision,omitempty"`
}
```

### Step 1.5: Extend AIMModelSpec

**File**: `api/v1alpha1/aimmodel_shared.go`

Add new fields to `AIMModelSpec`:

- [x] Add `Hardware *AIMHardwareRequirements` field (spec-level default)
- [x] Add `Type *AIMTemplateType` field (spec-level default type, pointer so nil = unoptimized)
- [x] Add `CustomTemplates []AIMCustomTemplate` field
- [ ] Add CEL validation: at least one of `spec.hardware` or template-level `hardware` must be set for custom models (deferred to Phase 5)

```go
type AIMModelSpec struct {
    // ... existing fields ...

    // Hardware specifies default hardware requirements for all custom templates.
    // Individual templates can override these defaults.
    // +optional
    Hardware *AIMHardwareRequirements `json:"hardware,omitempty"`

    // Type specifies default type for all custom templates.
    // Individual templates can override. When nil, defaults to "unoptimized".
    // +optional
    // +kubebuilder:validation:Enum=optimized;preview;unoptimized
    Type *AIMTemplateType `json:"type,omitempty"`

    // CustomTemplates defines explicit template configurations for this model.
    // When modelSources are specified, these are used instead of discovery.
    // +optional
    CustomTemplates []AIMCustomTemplate `json:"customTemplates,omitempty"`
}
```

### Step 1.6: Add SourceType to AIMModelStatus

**File**: `api/v1alpha1/aimmodel_shared.go`

- [x] Add `AIMModelSourceType` enum
- [x] Add `SourceType` field to `AIMModelStatus`
- [x] Add print column for `kubectl get aimmodel`

```go
// AIMModelSourceType indicates how a model's artifacts are sourced.
// +kubebuilder:validation:Enum=Image;Custom
type AIMModelSourceType string

const (
    AIMModelSourceTypeImage  AIMModelSourceType = "Image"
    AIMModelSourceTypeCustom AIMModelSourceType = "Custom"
)

type AIMModelStatus struct {
    // ... existing fields ...

    // SourceType indicates how this model's artifacts are sourced.
    // +optional
    SourceType AIMModelSourceType `json:"sourceType,omitempty"`
}
```

### Step 1.7: Update AIMServiceModelCustom

**File**: `api/v1alpha1/aimservice_types.go`

Restructure `AIMServiceModelCustom` to use `AIMHardwareRequirements`:

- [x] Replace `GpuSelector AIMGpuSelector` with `Hardware AIMHardwareRequirements`
- [x] Uncomment `Custom` field in `AIMServiceModel`
- [x] Update CEL validation to include `custom` option

```go
// AIMServiceModel specifies which model to deploy.
// +kubebuilder:validation:XValidation:rule="(has(self.name) ? 1 : 0) + (has(self.image) ? 1 : 0) + (has(self.custom) ? 1 : 0) == 1",message="exactly one of name, image, or custom must be specified"
type AIMServiceModel struct {
    Name   *string                  `json:"name,omitempty"`
    Image  *string                  `json:"image,omitempty"`
    Custom *AIMServiceModelCustom   `json:"custom,omitempty"`
}

type AIMServiceModelCustom struct {
    BaseImage    string                   `json:"baseImage"`
    ModelSources []AIMModelSource         `json:"modelSources"`
    Hardware     AIMHardwareRequirements  `json:"hardware"`
}
```

### Step 1.8: Run Code Generation

- [x] Run `mise exec -- make generate` to update DeepCopy methods
- [x] Run `mise exec -- make manifests` to regenerate CRDs
- [x] Fix any linter errors (updated references from `source.Name` to `source.ModelID`)

---

## Phase 2: AIMModel Controller Changes

### Step 2.1: Detect Custom vs Image-Based Models

**File**: `internal/aimmodel/reconcile.go`, `internal/aimmodel/hardware.go`

- [x] In `FetchRemoteState` or `ComposeState`, determine if model has `spec.modelSources`
- [x] Set `status.sourceType` to `Custom` or `Image` based on detection
- [x] Add helper function `IsCustomModel(spec) bool`

### Step 2.2: Skip Image Discovery for Custom Models

**File**: `internal/aimmodel/reconcile.go`

- [x] When `spec.modelSources` is populated, skip container image metadata extraction
- [x] Use `spec.modelSources` directly for template creation
- [ ] Validate that `size` is provided (required for custom models, no discovery to populate it) - deferred to Phase 5

### Step 2.3: Create Templates from CustomTemplates

**Files**: `internal/aimmodel/template.go`, `internal/aimmodel/reconcile.go`

Implement template creation from `spec.customTemplates`:

- [x] Add function `buildCustomServiceTemplates(model *AIMModel) []*AIMServiceTemplate`
- [x] Add function `buildCustomClusterServiceTemplates(model *AIMClusterModel) []*AIMClusterServiceTemplate`
- [x] Implement hardware inheritance (merge spec.hardware with template.hardware)
- [x] Implement type inheritance (use spec.type if template.type is nil)
- [x] Auto-generate template names if not provided: `{modelName}-custom-{precision}-{gpu.models[0]}`
- [x] If no `customTemplates` provided but `modelSources` exists, create single template from hardware

### Step 2.4: Hardware Merging Logic

**File**: `internal/aimmodel/hardware.go` (new file)

- [x] Create `MergeHardware(specDefault, templateOverride) AIMHardwareRequirements`
- [x] Field-by-field merge: template values take precedence
- [x] GPU.models: template replaces spec (not merged)
- [x] GPU.requests: template overrides spec
- [x] CPU: template overrides spec
- [x] Add `GetEffectiveType()` for type inheritance
- [x] Unit tests in `internal/aimmodel/hardware_test.go`

### Step 2.5: Update GetComponentHealth

**File**: `internal/aimmodel/reconcile.go`

- [x] Skip image metadata health for custom models (returns Ready with reason "CustomModelSkipped")
- [x] Track template status for custom templates (uses existing template health logic)

---

## Phase 3: AIMService Controller - Custom Model Support

### Step 3.1: Handle spec.model.custom

**File**: `internal/aimservice/model.go`

- [x] In `fetchModel`, detect if `spec.model.custom` is set
- [x] Add `CustomSpec` field to `ModelFetchResult`
- [x] Call `resolveCustomModel` to search for matching model

### Step 3.2: Model Matching Logic

**File**: `internal/aimservice/model_matching.go` (new file)

Implement spec-based model matching:

- [x] `FindMatchingCustomModel(ctx, client, namespace, custom AIMServiceModelCustom) *AIMModel`
- [x] Match by: `spec.image == custom.baseImage` AND `spec.modelSources` match
- [x] For S3 sources, include AWS_ENDPOINT_URL in comparison (from env)
- [x] Return first match or nil
- [x] `GenerateCustomModelName` for unique model naming
- [x] Unit tests in `model_matching_test.go`

### Step 3.3: Auto-Create AIMModel

**Files**: `internal/aimservice/model.go`, `internal/aimservice/reconcile.go`

- [x] In `ComposeState`, detect custom model needs creation (CustomSpec set, no matching model)
- [x] In `PlanResources`, if no matching model found, plan model creation with owner reference
- [x] `buildModelForCustom` creates namespace-scoped AIMModel with:
  - [x] Owner reference to AIMService (deleted with service)
  - [x] Labels: `aim.silogen.ai/origin: auto-generated`, `aim.silogen.ai/custom-model: true`
  - [x] `spec.hardware` set from custom.hardware
  - [x] `spec.modelSources` set from custom.modelSources
- [x] Name generated from hash of modelId + baseImage + endpoint

### Step 3.4: Single Template Creation for Custom

**Note**: Template creation is handled by AIMModel controller (Phase 2)

- [x] When AIMModel has modelSources but no customTemplates, a single template is auto-generated
- [x] Template inherits hardware from AIMModel.spec.hardware
- [x] Template type defaults to "unoptimized"

---

## Phase 4: Unified Download Architecture

### Step 4.1: Refactor AIMModelCache for Dedicated Mode

**Files**: `api/v1alpha1/aimmodelcache_types.go`, `internal/aimservice/caching.go`

- [x] Add `ModelID` field to `AIMModelCacheSpec` for consistent mount paths
- [x] Add `LabelValueCacheTypeDedicated` constant for dedicated caches
- [x] Add `planDedicatedModelCaches()` to create AIMModelCache resources owned by service
- [x] Add `fetchDedicatedModelCaches()` to fetch service-owned model caches
- [x] Add `areDedicatedCachesReady()` and `getDedicatedCacheForModel()` helpers

### Step 4.2: Update AIMService to Always Use Engine Downloads

**Files**: `internal/aimservice/reconcile.go`, `internal/aimservice/kserve.go`

- [x] Add `dedicatedModelCaches` field to `ServiceFetchResult`
- [x] Replace PVC-based storage with dedicated model caches for non-cached modes
- [x] Update `isReadyForInferenceService()` to check dedicated cache readiness
- [x] Update `addStorageVolumes()` to mount from dedicated model caches
- [x] Update `getCacheHealth()` to report dedicated cache status

### Step 4.3: Download Job Uses ModelId for Path

**Files**: `internal/aimmodelcache/download.go`, `internal/aimmodelcache/download.sh`

- [x] Add `resolveModelID()` function to derive modelId from sourceURI or spec
- [x] Pass `MODEL_ID` environment variable to download job
- [x] Update download.sh to download to `${MOUNT_PATH}/${MODEL_ID}` when MODEL_ID is set
- [x] Update `aimtemplatecache/reconcile.go` to set ModelID on created caches

---

## Phase 5: Validation & Error Handling

### Step 5.1: CEL Validation Rules

**File**: `api/v1alpha1/aimmodel_types.go`

Add CEL validations:

- [ ] If `modelSources` is set, `modelSources[].size` is required
- [ ] At least one of `spec.hardware` or each `customTemplates[].hardware` must be set
- [ ] `customTemplates` only valid when `modelSources` is set

### Step 5.2: Secret Existence Validation

**File**: `internal/aimmodel/reconcile.go`

- [ ] Validate referenced secrets exist before starting download
- [ ] Return `AuthValid=False` condition if secrets missing
- [ ] Clear error message indicating which secret is missing

### Step 5.3: Endpoint URL Handling

**File**: `internal/aimmodel/reconcile.go`

- [ ] Extract AWS_ENDPOINT_URL from env for S3 sources
- [ ] Include in model matching to distinguish different MinIO instances

---

## Phase 6: Testing

### Step 6.1: Unit Tests

- [ ] Test `MergeHardware` function
- [ ] Test `FindMatchingModel` logic
- [ ] Test `IsCustomModel` detection
- [ ] Test template name generation
- [ ] Test hardware inheritance

### Step 6.2: E2E Tests - Direct AIMModel

**File**: `tests/e2e/aimmodel/custom/`

- [ ] Create AIMModel with S3 source and customTemplates
- [ ] Verify templates created with correct hardware
- [ ] Verify status.sourceType = Custom
- [ ] Verify download job runs successfully

### Step 6.3: E2E Tests - AIMService Custom

**File**: `tests/e2e/aimservice/custom/`

- [ ] Create AIMService with spec.model.custom
- [ ] Verify auto-created AIMModel
- [ ] Verify auto-created template
- [ ] Verify service becomes Running
- [ ] Test model reuse (second service finds existing model)

### Step 6.4: E2E Tests - HuggingFace Source

**File**: `tests/e2e/aimmodel/custom-hf/`

- [ ] Create AIMModel with HuggingFace source
- [ ] Test with HF_TOKEN authentication
- [ ] Verify gated model access error handling

---

## Phase 7: Documentation

### Step 7.1: User Documentation

- [ ] Document Path 1: Creating custom AIMModels
- [ ] Document Path 2: Using AIMService spec.model.custom
- [ ] Examples for S3 and HuggingFace sources
- [ ] Credential configuration guide

### Step 7.2: Update ADR Status

- [ ] Update implementation status table in ADR
- [ ] Mark features as implemented

---

## Implementation Order

Recommended order to minimize conflicts:

1. **Phase 1.1-1.6**: API types (low risk, no behavioral changes)
2. **Phase 1.7-1.8**: AIMService types + code generation
3. **Phase 2**: AIMModel controller (Path 1 implementation)
4. **Phase 5.1-5.3**: Validation rules
5. **Phase 6.1**: Unit tests for new logic
6. **Phase 3**: AIMService controller (Path 2 implementation)
7. **Phase 4**: Unified download (can be done in parallel with Phase 3)
8. **Phase 6.2-6.4**: E2E tests
9. **Phase 7**: Documentation

---

## Breaking Changes

1. **AIMModelSource.name → modelId**: Rename with different validation
   - Migration: Update existing manifests to use `modelId` instead of `name`
   - Add validation pattern `{org}/{name}`

2. **AIMServiceModelCustom.gpuSelector → hardware**: Structure change
   - Migration: Update to nested `hardware.gpu` structure

---

## Notes

- `AIMGpuSelector` can be deprecated but kept for backward compatibility
- Custom models always create namespace-scoped resources
- Discovery job still runs for custom models to validate sources (but doesn't discover them)
- Consider adding conversion webhooks if breaking changes need gradual migration

