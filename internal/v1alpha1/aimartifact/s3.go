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
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/trust"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

const (
	s3CredentialsVolumeName = "aim-s3-credentials"

	s3CredentialsMountPath = "/var/run/secrets/aim-s3"
)

var typedS3ArtifactEnvNames = map[string]struct{}{
	"AIM_S3_LOG_LEVEL":              {},
	"AIM_S3_MAX_CONCURRENCY":        {},
	"AIM_S3_MAX_WORKERS":            {},
	"AIM_S3_MULTIPART_CHUNKSIZE_MB": {},
}

// S3PodSettings is the S3-specific part of a transfer Pod contract.
type S3PodSettings struct {
	Env          []corev1.EnvVar
	Volumes      []corev1.Volume
	VolumeMounts []corev1.VolumeMount
}

// DirectS3Connection returns the typed connection used for direct s3://
// artifacts.
func DirectS3Connection(runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.S3ConnectionConfig {
	if runtimeConfig == nil || runtimeConfig.Artifact == nil {
		return nil
	}
	return runtimeConfig.Artifact.S3
}

// ResolveS3PodSettings merges the legacy environment contract and then applies
// the typed connection as the final authority. A nil connection preserves the
// existing environment-only behavior exactly.
func ResolveS3PodSettings(
	connection *aimv1alpha1.S3ConnectionConfig,
	defaults []corev1.EnvVar,
	runtimeEnv []corev1.EnvVar,
	artifactEnv []corev1.EnvVar,
) S3PodSettings {
	if connection != nil {
		artifactEnv = filterTypedS3ArtifactEnv(artifactEnv)
	}
	env := utils.MergeEnvVars(defaults, runtimeEnv)
	env = utils.MergeEnvVars(env, artifactEnv)

	settings := S3PodSettings{Env: env}
	if connection == nil {
		return settings
	}

	authMode := aimv1alpha1.S3AuthModeChain
	if connection.Auth != nil && connection.Auth.Mode != "" {
		authMode = connection.Auth.Mode
	}

	signatureVersion := ""
	if connection.SignatureVersion != "" &&
		connection.SignatureVersion != aimv1alpha1.S3SignatureVersionAuto {
		signatureVersion = string(connection.SignatureVersion)
	}

	typedEnv := []corev1.EnvVar{
		{Name: "AWS_ENDPOINT_URL", Value: connection.Endpoint},
		{Name: "AWS_ENDPOINT_URL_FILE", Value: ""},
		{Name: "AWS_ENDPOINT_URL_S3", Value: ""},
		{Name: "AWS_ENDPOINT_URL_S3_FILE", Value: ""},
		{Name: "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS", Value: "true"},
		{Name: "AWS_REGION", Value: connection.Region},
		{Name: "AWS_REGION_FILE", Value: ""},
		{Name: "AWS_DEFAULT_REGION", Value: connection.Region},
		{Name: "AWS_DEFAULT_REGION_FILE", Value: ""},
		{Name: "S3_NO_SSL", Value: ""},
		{Name: "S3_USE_HTTPS", Value: ""},
		{Name: "AIM_S3_ADDRESSING_STYLE", Value: string(connection.AddressingStyle)},
		{Name: "AIM_S3_ADDRESSING_STYLE_FILE", Value: ""},
		{Name: "AIM_S3_SIGNATURE_VERSION", Value: signatureVersion},
		{Name: "AIM_S3_SIGNATURE_VERSION_FILE", Value: ""},
		{Name: "AIM_S3_AUTH_MODE", Value: string(authMode)},
		{Name: "AIM_S3_ANONYMOUS", Value: ""},
		{Name: "AWS_ACCESS_KEY_ID", Value: ""},
		{Name: "AWS_ACCESS_KEY_ID_FILE", Value: ""},
		{Name: "AWS_SECRET_ACCESS_KEY", Value: ""},
		{Name: "AWS_SECRET_ACCESS_KEY_FILE", Value: ""},
		{Name: "AWS_SESSION_TOKEN", Value: ""},
		{Name: "AWS_SESSION_TOKEN_FILE", Value: ""},
		{Name: "AIM_S3_INSECURE_SKIP_VERIFY", Value: "false"},
		{Name: "AWS_CA_BUNDLE", Value: ""},
		{Name: "AWS_CA_BUNDLE_FILE", Value: ""},
		{Name: "AIM_S3_CA_BUNDLE", Value: ""},
		{Name: "AIM_S3_CA_BUNDLE_FILE", Value: ""},
	}
	setTypedEnv := func(name, value string) {
		for i := range typedEnv {
			if typedEnv[i].Name == name {
				typedEnv[i].Value = value
				return
			}
		}
		typedEnv = append(typedEnv, corev1.EnvVar{Name: name, Value: value})
	}

	if authMode == aimv1alpha1.S3AuthModeStatic &&
		connection.Auth != nil &&
		connection.Auth.CredentialsSecretRef != nil {
		ref := connection.Auth.CredentialsSecretRef
		accessKey := ref.AccessKeyIDKey
		if accessKey == "" {
			accessKey = "accessKeyId"
		}
		secretKey := ref.SecretAccessKeyKey
		if secretKey == "" {
			secretKey = "secretAccessKey"
		}

		items := []corev1.KeyToPath{
			{Key: accessKey, Path: "access-key-id"},
			{Key: secretKey, Path: "secret-access-key"},
		}
		setTypedEnv("AWS_ACCESS_KEY_ID_FILE", s3CredentialsMountPath+"/access-key-id")
		setTypedEnv("AWS_SECRET_ACCESS_KEY_FILE", s3CredentialsMountPath+"/secret-access-key")
		if ref.SessionTokenKey != "" {
			items = append(items, corev1.KeyToPath{
				Key:  ref.SessionTokenKey,
				Path: "session-token",
			})
			setTypedEnv("AWS_SESSION_TOKEN_FILE", s3CredentialsMountPath+"/session-token")
		}

		settings.Volumes = append(settings.Volumes, corev1.Volume{
			Name: s3CredentialsVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ref.Name,
					Items:      items,
				},
			},
		})
		settings.VolumeMounts = append(settings.VolumeMounts, corev1.VolumeMount{
			Name:      s3CredentialsVolumeName,
			MountPath: s3CredentialsMountPath,
			ReadOnly:  true,
		})
	}

	if connection.TLS != nil {
		setTypedEnv(
			"AIM_S3_INSECURE_SKIP_VERIFY",
			strconv.FormatBool(connection.TLS.InsecureSkipVerify),
		)
	}

	settings.Env = utils.MergeEnvVars(settings.Env, typedEnv)
	return settings
}

// ResolveTrustProjection builds the additional trust contract for a transfer
// workload. S3-specific trust is additive to the RuntimeConfig-wide bundle.
func ResolveTrustProjection(
	runtimeConfig *aimv1alpha1.AIMRuntimeConfigCommon,
	sourceURI string,
	includeAdditionalCABundle bool,
	image string,
) *trust.Projection {
	options := trust.Options{Image: image}
	if runtimeConfig != nil && includeAdditionalCABundle {
		options.AdditionalCABundleRef = runtimeConfig.AdditionalCABundleRef
	}
	if strings.HasPrefix(sourceURI, "s3://") {
		options.IncludeS3Env = true
		connection := DirectS3Connection(runtimeConfig)
		if connection != nil && connection.TLS != nil {
			options.S3CABundleRef = connection.TLS.CABundleRef
		}
	}
	return trust.BuildProjection(options)
}

// ValidateTypedS3ArtifactEnv rejects process, transport, trust, and credential
// controls from an artifact when an administrator selected a typed S3
// connection. RuntimeConfig env remains the administrator-owned place for
// infrastructure settings such as proxies and SDK credential providers.
func ValidateTypedS3ArtifactEnv(artifactEnv []corev1.EnvVar) error {
	var problems []string
	for _, item := range artifactEnv {
		if err := validateTypedS3ArtifactEnvVar(item); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf(
		"typed S3 connections only allow bounded downloader tuning in "+
			"AIMArtifact.spec.env: %s; configure transport, proxy, trust, and "+
			"credential-provider settings through RuntimeConfig",
		strings.Join(problems, "; "),
	)
}

func validateTypedS3ArtifactEnvVar(item corev1.EnvVar) error {
	if _, allowed := typedS3ArtifactEnvNames[item.Name]; !allowed {
		return fmt.Errorf("%s is not permitted", item.Name)
	}
	if item.ValueFrom != nil {
		return fmt.Errorf("%s must use a literal value", item.Name)
	}

	value := strings.TrimSpace(item.Value)
	switch item.Name {
	case "AIM_S3_LOG_LEVEL":
		switch strings.ToUpper(value) {
		case "DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL":
			return nil
		default:
			return fmt.Errorf(
				"%s must be one of DEBUG, INFO, WARNING, ERROR, CRITICAL",
				item.Name,
			)
		}
	case "AIM_S3_MAX_WORKERS", "AIM_S3_MAX_CONCURRENCY":
		return validateTypedS3Integer(item.Name, value, 1, 64)
	case "AIM_S3_MULTIPART_CHUNKSIZE_MB":
		return validateTypedS3Integer(item.Name, value, 5, 5120)
	default:
		return fmt.Errorf("%s is not permitted", item.Name)
	}
}

func validateTypedS3Integer(name, value string, minimum, maximum int) error {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return fmt.Errorf(
			"%s must be an integer from %d through %d",
			name,
			minimum,
			maximum,
		)
	}
	return nil
}

func filterTypedS3ArtifactEnv(artifactEnv []corev1.EnvVar) []corev1.EnvVar {
	filtered := make([]corev1.EnvVar, 0, len(artifactEnv))
	for _, item := range artifactEnv {
		if validateTypedS3ArtifactEnvVar(item) == nil {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
