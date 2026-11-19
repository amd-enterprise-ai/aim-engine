{{/*
Helpers for repository-maintained templates.

Keep these independent of Kubebuilder's generated helpers. Generated helper
names and availability can change across helm plugin versions, while the custom
templates copied from config/helm/templates must render consistently.
*/}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "chart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end -}}

{{- define "chart.labels" -}}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- if .Chart.Version }}
helm.sh/chart: {{ .Chart.Version | quote }}
{{- end }}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Translate the user-facing gateway provider into the controller's internal
metric scope. Keeping this mapping in one helper prevents the controller and
collector templates from accepting mismatched configuration.
*/}}
{{- define "chart.gatewayActivationScope" -}}
{{- range $entry := .Values.manager.env -}}
{{- if has $entry.name (list "AIM_KEDA_OTEL_SCALER_ADDRESS" "AIM_COOLDOWN_SECONDS_PER_GI_MEMORY" "AIM_GATEWAY_ACTIVATION_SCOPE" "AIM_ARTIFACT_DOWNLOADER_IMAGE") -}}
{{- fail (printf "manager.env must not override chart-managed environment variable %q; use its dedicated Helm value" $entry.name) -}}
{{- end -}}
{{- end -}}
{{- if hasKey .Values.scaleFromZero "gatewayActivationScope" -}}
{{- fail "scaleFromZero.gatewayActivationScope was replaced by scaleFromZero.gatewayProvider; use none, envoyGateway, kgateway, or custom" -}}
{{- end -}}
{{- $provider := required "scaleFromZero.gatewayProvider is required" .Values.scaleFromZero.gatewayProvider -}}
{{- if eq $provider "none" -}}
none
{{- else if eq $provider "envoyGateway" -}}
httproute
{{- else if eq $provider "kgateway" -}}
deployment
{{- else if eq $provider "custom" -}}
custom
{{- else -}}
{{- fail (printf "scaleFromZero.gatewayProvider must be one of none, envoyGateway, kgateway, or custom, got %q" $provider) -}}
{{- end -}}
{{- end -}}
