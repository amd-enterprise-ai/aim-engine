{{/*
Expand the name of the chart.
*/}}
{{- define "aim-engine.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "aim-engine.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else if and (eq .Release.Name "aim-engine") (eq $name "aim-engine") }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "aim-engine.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "aim-engine.labels" -}}
helm.sh/chart: {{ include "aim-engine.chart" . }}
{{ include "aim-engine.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "aim-engine.selectorLabels" -}}
app.kubernetes.io/name: "aim-engine"
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "aim-engine.serviceAccountName" -}}
{{- printf "%s-controller-manager" (include "aim-engine.fullname" .) }}
{{- end }}

{{/*
Image name
*/}}
{{- define "aim-engine.image" -}}
{{- $registry := .Values.image.registry | default "" -}}
{{- $repo := .Values.image.repository -}}
{{- if .Values.image.digest }}
  {{- if $registry }}
    {{- printf "%s/%s@%s" $registry $repo .Values.image.digest }}
  {{- else }}
    {{- printf "%s@%s" $repo .Values.image.digest }}
  {{- end }}
{{- else }}
  {{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
  {{- if $registry }}
    {{- printf "%s/%s:%s" $registry $repo $tag }}
  {{- else }}
    {{- printf "%s:%s" $repo $tag }}
  {{- end }}
{{- end }}
{{- end }}

