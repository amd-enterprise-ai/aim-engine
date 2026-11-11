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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Shared runtime configuration types for both namespace and cluster-scoped configs

// AIMDownloadFilter controls which files are included or excluded during artifact downloads.
// Patterns use fnmatch-style glob syntax applied against relative file paths in the repository.
// Both the size estimator and downloader apply the same filter, ensuring PVC sizing matches the actual download.
//
// Filter order (matching huggingface_hub behavior):
//  1. Include: if set, only files matching at least one include pattern are considered
//  2. Exclude: files matching any exclude pattern are then removed
//
// When no filter is configured (neither on the artifact nor in the runtime config),
// subdirectory files are excluded by default (equivalent to exclude: ["*/*"]).
// To download all files including subdirectories, set an empty filter: downloadFilter: {}.
type AIMDownloadFilter struct {
	// Include specifies glob patterns for files to download.
	// Only files matching at least one pattern are considered.
	// If empty, all files pass the include check.
	// Patterns use fnmatch syntax (e.g., ["*.safetensors", "config.json"]).
	// +optional
	Include []string `json:"include,omitempty"`

	// Exclude specifies glob patterns for files to skip.
	// Files matching any exclude pattern are removed after include filtering.
	// Patterns use fnmatch syntax (e.g., ["*/*", "*.bin"]).
	// Use ["*/*"] to exclude all files in subdirectories (the default when no filter is set).
	// +optional
	Exclude []string `json:"exclude,omitempty"`
}

// AIMStorageConfig configures storage defaults for artifacts and PVCs.
type AIMStorageConfig struct {
	// DefaultStorageClassName specifies the storage class to use for artifacts and PVCs
	// when the consuming resource (AIMArtifact, AIMTemplateCache, AIMServiceTemplate) does not
	// specify a storage class. If this field is empty, the cluster's default storage class is used.
	// +optional
	DefaultStorageClassName *string `json:"defaultStorageClassName,omitempty"`

	// PVCHeadroomPercent specifies the percentage of extra space to add to PVCs
	// for model storage. This accounts for filesystem overhead and temporary files
	// during model loading. The value represents a percentage (e.g., 10 means 10% extra space).
	// If not specified, defaults to 10%.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=0
	// +optional
	PVCHeadroomPercent *int32 `json:"pvcHeadroomPercent,omitempty"`

	// DownloadFilter controls which files are included or excluded during artifact downloads.
	// When set here, applies as the default for all artifacts using this runtime config.
	// Individual artifacts can override this with their own downloadFilter.
	// When no filter is configured at any level, subdirectory files are excluded by default.
	// Set to an empty object (downloadFilter: {}) to explicitly allow all files.
	// +optional
	DownloadFilter *AIMDownloadFilter `json:"downloadFilter,omitempty"`

	// AdapterDiskStorageClassName is the storage class for the shared
	// ReadWriteMany adapter disk. It must be RWX-capable (e.g. longhorn, NFS) and
	// is resolved before the (typically RWO) DefaultStorageClassName.
	// +optional
	AdapterDiskStorageClassName *string `json:"adapterDiskStorageClassName,omitempty"`

	// AdapterDiskSize is the cluster default size for the shared adapter disk PVC
	// (built-in default when unset). An artifact's adapterDisk.size wins over it.
	// +optional
	AdapterDiskSize *resource.Quantity `json:"adapterDiskSize,omitempty"`
}

// AIMScaleFromZeroConfig configures the gateway activation metric used to wake
// a service from zero replicas.
type AIMScaleFromZeroConfig struct {
	// ActivationMetricQueryTemplate overrides the provider-derived KEDA
	// OpenTelemetry metric query. The controller expands these placeholders:
	// ${namespace}, ${serviceName}, ${httpRouteName}, and
	// ${predictorDeployment}. Unknown placeholders make the service
	// configuration invalid.
	//
	// A value set directly on AIMService takes precedence over namespace and
	// cluster RuntimeConfig values.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	ActivationMetricQueryTemplate string `json:"activationMetricQueryTemplate,omitempty"`
}

// AIMAdapterRuntimeConfig configures the inference runtime's LoRA adapter
// capacity. It is shared by AIMService and namespace/cluster RuntimeConfigs so
// services can override administrator-provided defaults.
type AIMAdapterRuntimeConfig struct {
	// MaxRank is the startup-time LoRA rank ceiling for dynamic adapter mode.
	// A service-level value takes precedence over namespace and cluster
	// RuntimeConfig values. When unset, the controller uses a conservative
	// built-in default.
	//
	// Static adapter services infer the required maximum rank from their declared
	// AIMArtifact objects. A service-level value is rejected in static mode;
	// inherited RuntimeConfig values are ignored because one RuntimeConfig may
	// serve both static and dynamic services.
	//
	// Allowed values mirror vLLM's MaxLoRARanks:
	// 1, 8, 16, 32, 64, 128, 256, 320, and 512.
	//
	// Changing this value changes the inference container contract and rolls the
	// serving workload. Increases take effect immediately. A decrease is deferred
	// while any declared adapter artifact's rank is unresolved; the controller
	// retains the last resolved ceiling until every rank can be checked against
	// the lower value.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self in [1, 8, 16, 32, 64, 128, 256, 320, 512]",message="maxRank must be one of 1, 8, 16, 32, 64, 128, 256, 320, or 512"
	MaxRank *int32 `json:"maxRank,omitempty"`
}

// AIMServiceRuntimeConfig contains runtime configuration fields that apply to services.
// This struct is shared between AIMService.spec (inlined) and AIMRuntimeConfigCommon,
// allowing services to override these specific runtime settings while inheriting defaults
// from namespace/cluster RuntimeConfigs.
type AIMServiceRuntimeConfig struct {
	// AdapterRuntime configures LoRA adapter capacity for inference containers.
	// Service values override namespace and cluster RuntimeConfig defaults.
	// +optional
	AdapterRuntime *AIMAdapterRuntimeConfig `json:"adapterRuntime,omitempty"`

	// Storage configures storage defaults for this service's PVCs and caches.
	// When set, these values override namespace/cluster runtime config defaults.
	// +optional
	Storage *AIMStorageConfig `json:"storage,omitempty"`

	// Routing controls HTTP routing configuration for this service.
	// When set, these values override namespace/cluster runtime config defaults.
	// +optional
	Routing *AIMRuntimeRoutingConfig `json:"routing,omitempty"`

	// ScaleFromZero configures the activation metric query for this service.
	// When set, these values override namespace/cluster runtime config defaults.
	// +optional
	ScaleFromZero *AIMScaleFromZeroConfig `json:"scaleFromZero,omitempty"`

	// Env specifies environment variables for inference containers.
	// When set on AIMService, these take highest precedence in the merge hierarchy.
	// When set on RuntimeConfig, these provide namespace/cluster-level defaults.
	// Merge order (highest to lowest): Service.Env > Template.Env > RuntimeConfig.Env > Profile.Env
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

type AIMModelConfig struct {
	// AutoDiscovery controls whether models run discovery by default.
	// When true, models run discovery jobs to extract metadata and auto-create templates.
	// When false, discovery is skipped. Discovery failures are non-fatal and reported via conditions.
	// +optional
	AutoDiscovery *bool `json:"autoDiscovery,omitempty"`

	// ProfileGeneration configures generic runtime fallbacks used to generate
	// profiles for v1alpha2 modelId-backed models.
	// +optional
	ProfileGeneration *AIMProfileGenerationConfig `json:"profileGeneration,omitempty"`
}

// AIMProfileGenerationConfig controls profile generation for modelId-backed
// AIMModel and AIMClusterModel resources.
type AIMProfileGenerationConfig struct {
	// Fallbacks are runtime templates selected by observed cluster hardware.
	// Namespace entries replace cluster entries with the same name.
	// +optional
	// +listType=map
	// +listMapKey=name
	Fallbacks []AIMProfileGenerationFallback `json:"fallbacks,omitempty"`
}

// AIMProfileGenerationFallback describes one hardware match and runtime
// template used to generate a self-contained AIMProfile.
type AIMProfileGenerationFallback struct {
	// Name is the stable fallback identity and merge key.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Priority selects the primary profile when multiple fallbacks match.
	// Higher values win.
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Type is the optimization tier stamped on the generated profile.
	// Generated profiles default to "unoptimized" because a generic runtime
	// template is not tuned for any particular model. AutoSelectionPolicy
	// controls whether a lower-tier generated profile can serve as an automatic
	// fallback.
	// +optional
	// +kubebuilder:default=unoptimized
	Type AIMProfileType `json:"type,omitempty"`

	// AutoSelectionPolicy is stamped on the generated profile.
	//
	// "optimized" keeps the profile subject to the implicit optimized
	// minimumType floor. "any" permits it to participate in a second
	// automatic-selection pass when no optimized candidate matched and the
	// service did not explicitly set minimumType.
	// +optional
	// +kubebuilder:default=optimized
	AutoSelectionPolicy AIMProfileAutoSelectionPolicy `json:"autoSelectionPolicy,omitempty"`

	// Match constrains the hardware on which this fallback is applicable.
	Match AIMProfileGenerationMatch `json:"match"`

	// Runtime is copied into the generated profile.
	Runtime AIMProfileGenerationRuntime `json:"runtime"`
}

// AIMProfileGenerationMatch identifies compatible accelerator hardware.
type AIMProfileGenerationMatch struct {
	// AcceleratorVendor selects the hardware vendor.
	AcceleratorVendor AcceleratorVendor `json:"acceleratorVendor"`

	// AcceleratorType selects GPU or CPU hardware.
	AcceleratorType AcceleratorType `json:"acceleratorType"`

	// AcceleratorModel optionally requires an exact detector model label.
	// +optional
	AcceleratorModel string `json:"acceleratorModel,omitempty"`

	// AcceleratorPartitioningMode optionally requires a partitioning mode.
	// +optional
	AcceleratorPartitioningMode string `json:"acceleratorPartitioningMode,omitempty"`
}

// AIMProfileGenerationRuntime contains the profile runtime fields controlled by
// cluster or namespace policy.
type AIMProfileGenerationRuntime struct {
	// Image is the serving container image.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Engine identifies the inference engine, for example vllm.
	// +kubebuilder:validation:MinLength=1
	Engine string `json:"engine"`

	// EngineArgs overrides or extends generated engine CLI arguments.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +optional
	EngineArgs *apiextensionsv1.JSON `json:"engineArgs,omitempty"`

	// EngineEnv contains inference-engine subprocess environment variables.
	// +optional
	EngineEnv map[string]string `json:"engineEnv,omitempty"`

	// ContainerEnv contains pod-level environment variables.
	// +optional
	// +listType=map
	// +listMapKey=name
	ContainerEnv []corev1.EnvVar `json:"containerEnv,omitempty"`

	// Resources overrides generated Kubernetes resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ImagePullSecrets contains credentials for pulling the serving image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// ServiceAccountName selects the serving workload service account.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// AIMArtifactConfig controls artifact-level defaults that are not appropriate for
// individual services. These settings apply at namespace/cluster scope only.
type AIMArtifactConfig struct {
	// DefaultRetentionPriority sets the default retention priority for AIMArtifacts
	// that do not specify one in their spec. When set, artifacts without an explicit
	// retentionPriority become eligible for automatic eviction at this priority level.
	// Lower values are evicted first. If not set, artifacts without an explicit
	// retentionPriority are never automatically evicted.
	// +optional
	// +kubebuilder:validation:Minimum=0
	DefaultRetentionPriority *int32 `json:"defaultRetentionPriority,omitempty"`

	// ModelDownloadImage specifies the default container image for artifact
	// download and size-check jobs. Applies when an AIMArtifact does not set
	// spec.modelDownloadImage. When neither is set, the operator falls back
	// to its build-time default (matching the release version). Direct S3
	// artifacts using typed S3 configuration always use the build-time image so
	// administrator-managed credentials are not exposed to arbitrary images.
	// +optional
	ModelDownloadImage string `json:"modelDownloadImage,omitempty"`

	// S3 configures typed connection settings for AIMArtifacts whose sourceUri
	// uses the s3:// scheme. RuntimeConfig env remains available for
	// administrator-owned infrastructure settings, while artifact-level env is
	// restricted to bounded downloader tuning. Typed fields take final
	// precedence.
	// +optional
	S3 *S3ConnectionConfig `json:"s3,omitempty"`
}

// AdditionalCABundleReference identifies additional PEM CA certificates in a
// namespace-local ConfigMap.
type AdditionalCABundleReference struct {
	// Name is the ConfigMap name in the workload namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`

	// Key is the ConfigMap data key containing PEM CA certificates.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[-._a-zA-Z0-9]+$`
	Key string `json:"key"`
}

// AIMImageRegistryConfig controls explicit repository-prefix remapping for
// image references resolved by AIM Engine.
type AIMImageRegistryConfig struct {
	// Mappings rewrite matching image repository prefixes. The longest matching
	// From prefix wins. Namespace entries replace cluster entries with the same
	// From value. Tags and digests are preserved exactly.
	// +optional
	// +listType=map
	// +listMapKey=from
	Mappings []AIMImageRegistryMapping `json:"mappings,omitempty"`
}

// AIMImageRegistryMapping rewrites one image repository prefix to another.
// Prefixes do not include a tag or digest.
type AIMImageRegistryMapping struct {
	// From is the source registry or repository prefix to match.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^[:space:]@/:]+(?::[0-9]+)?(?:/[^[:space:]@/:]+)*$`
	From string `json:"from"`

	// To is the registry or repository prefix that replaces From.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^[:space:]@/:]+(?::[0-9]+)?(?:/[^[:space:]@/:]+)*$`
	To string `json:"to"`
}

// AIMRuntimeConfigCommon captures configuration fields shared across cluster and namespace scopes.
// These settings apply to both AIMRuntimeConfig (namespace-scoped) and AIMClusterRuntimeConfig (cluster-scoped).
// It embeds AIMServiceRuntimeConfig which contains fields that can also be overridden at the service level.
type AIMRuntimeConfigCommon struct {
	AIMServiceRuntimeConfig `json:",inline"`

	// AdditionalCABundleRef selects PEM CA certificates that AIM Engine adds to
	// its standard public trust roots for outbound TLS from inference,
	// artifact-download, size-check, and adapter-staging containers.
	//
	// The ConfigMap is resolved in each workload namespace. When configured on
	// an AIMClusterRuntimeConfig, each consuming namespace needs a ConfigMap
	// with the same name and key to receive the additional trust.
	//
	// AIM Engine checks the reference directly during reconciliation and omits
	// the projection when the ConfigMap or key is absent. Certificate parsing
	// occurs only in a workload init container. CA changes are consumed when
	// workloads are recreated; the ConfigMap is not watched for rotation.
	// +optional
	AdditionalCABundleRef *AdditionalCABundleReference `json:"additionalCABundleRef,omitempty"`

	// ImageRegistry controls explicit image repository remapping. Image
	// references remain unchanged when no mapping matches.
	// +optional
	ImageRegistry *AIMImageRegistryConfig `json:"imageRegistry,omitempty"`

	// Model controls model creation and discovery defaults.
	// This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services.
	// +optional
	Model *AIMModelConfig `json:"model,omitempty"`

	// Artifact controls artifact-level defaults such as eviction policy.
	// This field only applies to RuntimeConfig/ClusterRuntimeConfig and is not available for services.
	// +optional
	Artifact *AIMArtifactConfig `json:"artifact,omitempty"`

	// DEPRECATED: The embedded Hugging Face-to-S3 artifact cache has been
	// removed. This field is retained temporarily for API compatibility and is
	// no longer honored by the controller. Use direct s3:// model sources with
	// Artifact.S3 connection settings instead.
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="The embedded artifact cache has been removed. Use direct s3:// sources with spec.artifact.s3 instead."
	ArtifactCache *ArtifactCacheConfig `json:"artifactCache,omitempty"`

	// LabelPropagation controls how labels from parent AIM resources are propagated to child resources.
	// When enabled, labels matching the specified patterns are automatically copied from parent resources
	// (e.g., AIMService, AIMTemplateCache) to their child resources (e.g., Deployments, Services, PVCs).
	// This is useful for propagating organizational metadata like cost centers, team identifiers,
	// or compliance labels through the resource hierarchy.
	// +optional
	LabelPropagation *AIMRuntimeConfigLabelPropagationSpec `json:"labelPropagation,omitempty"`

	// DEPRECATED: Use Storage.DefaultStorageClassName instead. This field will be removed in a future version.
	// For backward compatibility, if this field is set and Storage.DefaultStorageClassName is not set,
	// the value will be automatically migrated.
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="Use Storage.DefaultStorageClassName instead. This field will be removed in a future version."
	DefaultStorageClassName string `json:"defaultStorageClassName,omitempty"`

	// DEPRECATED: Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version.
	// For backward compatibility, if this field is set and Storage.PVCHeadroomPercent is not set,
	// the value will be automatically migrated.
	// +optional
	// +kubebuilder:validation:Deprecated
	// +kubebuilder:validation:DeprecatedMessage="Use Storage.PVCHeadroomPercent instead. This field will be removed in a future version."
	PVCHeadroomPercent *int32 `json:"pvcHeadroomPercent,omitempty"`
}

type AIMRuntimeConfigLabelPropagationSpec struct {
	// Enabled, if true, allows propagating parent labels to all child resources it creates directly
	// Only label keys that match the ones in Match are propagated.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Match is a list of label keys that will be propagated to any child resources created.
	// Wildcards are supported, so for example `org.my/my-key-*` would match any label with that prefix.
	// +optional
	Match []string `json:"match,omitempty"`
}

// AIMArtifactStorageQuota configures storage limits for AIMArtifacts.
// These settings are only available on AIMClusterRuntimeConfig (cluster-scoped)
// because they enforce cluster-wide and cross-namespace policies.
type AIMArtifactStorageQuota struct {
	// ClusterLimit is the maximum total allocated storage for all AIMArtifacts cluster-wide.
	// When the sum of all artifact PVC sizes across all namespaces would exceed this limit,
	// new artifact PVCs are blocked until evictable artifacts are cleaned up or the limit is raised.
	// +optional
	ClusterLimit *resource.Quantity `json:"clusterLimit,omitempty"`

	// DefaultNamespaceLimit is the default maximum allocated storage for AIMArtifacts per namespace.
	// Can be overridden for individual namespaces via the aim.eai.amd.com/artifact-storage-quota annotation.
	// +optional
	DefaultNamespaceLimit *resource.Quantity `json:"defaultNamespaceLimit,omitempty"`
}

// ArtifactCacheConfig is the deprecated configuration for the removed embedded
// Hugging Face-to-S3 artifact cache. It is retained only so existing manifests
// remain valid while migrating to direct s3:// sources.
//
// Deprecated: this configuration is ignored by the controller.
type ArtifactCacheConfig struct {
	// Enabled formerly controlled whether the embedded S3 artifact cache was
	// active. It is retained for API compatibility and has no effect.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// S3URI formerly selected the base S3 path for cached artifacts. It is
	// retained for API compatibility and has no effect.
	// +optional
	S3URI string `json:"s3Uri,omitempty"`

	// Env formerly provided S3 endpoint and credential configuration for the
	// embedded cache. It is retained for API compatibility and has no effect.
	// +optional
	// +listType=map
	// +listMapKey=name
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// S3AddressingStyle controls how the bucket is encoded in S3 HTTP requests.
// +kubebuilder:validation:Enum=auto;path;virtual
type S3AddressingStyle string

const (
	S3AddressingStyleAuto    S3AddressingStyle = "auto"
	S3AddressingStylePath    S3AddressingStyle = "path"
	S3AddressingStyleVirtual S3AddressingStyle = "virtual"
)

// S3SignatureVersion controls request signing for authenticated S3 requests.
// +kubebuilder:validation:Enum=auto;s3v4
type S3SignatureVersion string

const (
	S3SignatureVersionAuto S3SignatureVersion = "auto"
	S3SignatureVersionV4   S3SignatureVersion = "s3v4"
)

// S3AuthMode selects how the S3 client obtains credentials.
// +kubebuilder:validation:Enum=chain;static;anonymous
type S3AuthMode string

const (
	// S3AuthModeChain uses the standard boto3 credential provider chain.
	S3AuthModeChain S3AuthMode = "chain"
	// S3AuthModeStatic loads credentials from a namespace-local Secret.
	S3AuthModeStatic S3AuthMode = "static"
	// S3AuthModeAnonymous sends unsigned requests to a public bucket.
	S3AuthModeAnonymous S3AuthMode = "anonymous"
)

// S3CredentialsSecretReference identifies credential keys in a
// namespace-local Secret.
type S3CredentialsSecretReference struct {
	// Name is the Secret name in the AIMArtifact namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// AccessKeyIDKey is the Secret data key containing the access key ID.
	// +kubebuilder:default=accessKeyId
	// +optional
	AccessKeyIDKey string `json:"accessKeyIdKey,omitempty"`

	// SecretAccessKeyKey is the Secret data key containing the secret access key.
	// +kubebuilder:default=secretAccessKey
	// +optional
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`

	// SessionTokenKey is an optional Secret data key containing a temporary
	// credential session token.
	// +optional
	SessionTokenKey string `json:"sessionTokenKey,omitempty"`
}

// S3AuthConfig configures S3 authentication.
// +kubebuilder:validation:XValidation:rule="!has(self.mode) || self.mode != 'static' || has(self.credentialsSecretRef)",message="credentialsSecretRef is required when auth mode is static"
// +kubebuilder:validation:XValidation:rule="!has(self.credentialsSecretRef) || (has(self.mode) && self.mode == 'static')",message="credentialsSecretRef may only be set when auth mode is static"
type S3AuthConfig struct {
	// Mode selects the authentication strategy. Omitted mode defaults to chain.
	// +kubebuilder:default=chain
	// +optional
	Mode S3AuthMode `json:"mode,omitempty"`

	// CredentialsSecretRef is required for static authentication.
	// +optional
	CredentialsSecretRef *S3CredentialsSecretReference `json:"credentialsSecretRef,omitempty"`
}

// S3CABundleReference identifies a PEM CA bundle in a namespace-local
// ConfigMap or Secret.
type S3CABundleReference struct {
	// Kind is the object kind containing the CA bundle.
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind"`

	// Name is the ConfigMap or Secret name in the AIMArtifact namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the data key containing the PEM CA bundle.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// S3TLSConfig controls TLS certificate and hostname verification.
// +kubebuilder:validation:XValidation:rule="!has(self.insecureSkipVerify) || !self.insecureSkipVerify || !has(self.caBundleRef)",message="caBundleRef and insecureSkipVerify cannot be set together"
type S3TLSConfig struct {
	// CABundleRef selects a namespace-local PEM CA bundle for S3 TLS
	// verification.
	// +optional
	CABundleRef *S3CABundleReference `json:"caBundleRef,omitempty"`

	// InsecureSkipVerify disables certificate-chain and hostname verification.
	// This is unsafe and intended only as a temporary diagnostic escape hatch.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// S3ConnectionConfig provides typed S3 endpoint, authentication, addressing,
// signing, and TLS settings.
// +structType=atomic
type S3ConnectionConfig struct {
	// Endpoint is an optional S3-compatible API endpoint. Leave empty for AWS
	// S3. Custom endpoints must include an explicit http:// or https:// scheme.
	// +optional
	// +kubebuilder:validation:Pattern=`^https?://[^ \t\r\n]+$`
	Endpoint string `json:"endpoint,omitempty"`

	// Region is the signing region. Custom endpoints default to us-east-1 when
	// omitted; AWS S3 uses normal SDK region resolution.
	// +optional
	Region string `json:"region,omitempty"`

	// AddressingStyle controls path-style versus virtual-hosted bucket routing.
	// Custom endpoints default to path when omitted.
	// +optional
	AddressingStyle S3AddressingStyle `json:"addressingStyle,omitempty"`

	// SignatureVersion controls authenticated request signing.
	// +optional
	SignatureVersion S3SignatureVersion `json:"signatureVersion,omitempty"`

	// Auth configures credential-chain, static, or anonymous access. Omitted
	// auth defaults to the standard SDK credential provider chain.
	// +optional
	Auth *S3AuthConfig `json:"auth,omitempty"`

	// TLS configures custom CA trust or the unsafe verification bypass.
	// +optional
	TLS *S3TLSConfig `json:"tls,omitempty"`
}

// AIMClusterRuntimeConfigSpec defines cluster-wide defaults for AIM resources.
type AIMClusterRuntimeConfigSpec struct {
	AIMRuntimeConfigCommon `json:",inline"`

	// ArtifactStorageQuota configures storage limits for AIMArtifacts.
	// These limits control how much total PVC storage artifacts may consume,
	// both cluster-wide and per-namespace.
	// +optional
	ArtifactStorageQuota *AIMArtifactStorageQuota `json:"artifactStorageQuota,omitempty"`
}

// AIMRuntimeConfigSpec defines namespace-scoped overrides for AIM resources.
type AIMRuntimeConfigSpec struct {
	AIMRuntimeConfigCommon `json:",inline"`
}

// AIMRuntimeRoutingConfig configures HTTP routing defaults for inference services.
// These settings control how Gateway API HTTPRoutes are created and configured.
type AIMRuntimeRoutingConfig struct {
	// Enabled controls whether HTTP routing is managed for inference services using this config.
	// When true, the operator creates HTTPRoute resources for services that reference this config.
	// When false or unset, routing must be explicitly enabled on each service.
	// This provides a namespace or cluster-wide default that individual services can override.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// GatewayRef specifies the Gateway API Gateway resource that should receive HTTPRoutes.
	// This identifies the parent gateway for routing traffic to inference services.
	// The gateway can be in any namespace (cross-namespace references are supported).
	// If routing is enabled but GatewayRef is not specified, service reconciliation will fail
	// with a validation error.
	// +optional
	GatewayRef *gatewayapiv1.ParentReference `json:"gatewayRef,omitempty"`

	// Hostnames pins generated HTTPRoutes to these hostnames so a route only
	// attaches to the matching Gateway listener instead of every listener on
	// the parent gateway. Without a hostname, an HTTPRoute matches all of the
	// parent gateway's listener hostnames, which can expose a service on
	// listeners that do not enforce the intended authentication.
	//
	// This field is required when the parent gateway exposes more than one
	// listener: in that case a service with routing enabled but no hostnames
	// configured will not get an HTTPRoute and reports ConfigValid=False with
	// reason RouteHostnameRequired. When the parent gateway has a single
	// listener, leaving this empty preserves the existing behavior (the route
	// inherits that listener's hostnames).
	//
	// Individual services can override this list via spec.routing.hostnames.
	// +optional
	Hostnames []gatewayapiv1.Hostname `json:"hostnames,omitempty"`

	// PathTemplate defines the HTTP path template for routes, evaluated using JSONPath expressions.
	// The template is rendered against the AIMService object to generate unique paths.
	//
	// Example templates:
	// - `/{.metadata.namespace}/{.metadata.name}` - namespace and service name
	// - `/{.metadata.namespace}/{.metadata.labels['team']}/inference` - with label
	// - `/models/{.metadata.name}` - based on service name
	//
	// The template must:
	// - Use valid JSONPath expressions wrapped in {...}
	// - Reference fields that exist on the service
	// - Produce a path ≤ 200 characters after rendering
	// - Result in valid URL path segments (lowercase, RFC 1123 compliant)
	//
	// If evaluation fails, the service enters Degraded state with PathTemplateInvalid reason.
	// Individual services can override this template via spec.routing.pathTemplate.
	// +optional
	PathTemplate *string `json:"pathTemplate,omitempty"`

	// RequestTimeout defines the HTTP request timeout for routes.
	// This sets the maximum duration for a request to complete before timing out.
	// The timeout applies to the entire request/response cycle.
	// If not specified, no timeout is set on the route.
	// Individual services can override this value via spec.routing.requestTimeout.
	// +optional
	RequestTimeout *metav1.Duration `json:"requestTimeout,omitempty"`

	// Annotations defines default annotations to add to all HTTPRoute resources.
	// Services can add additional annotations or override these via spec.routing.annotations.
	// When both are specified, service annotations take precedence for conflicting keys.
	// Common use cases include ingress controller settings, rate limiting, monitoring labels,
	// and security policies that should apply to all services using this config.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// AIMRuntimeConfigStatus records the resolved config reference surfaced to consumers.
type AIMRuntimeConfigStatus struct {
	// ObservedGeneration is the last reconciled generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions communicate reconciliation progress.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
