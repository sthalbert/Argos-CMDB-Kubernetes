{{/*
Chart name (truncated to 63 chars for label compliance).
*/}}
{{- define "argos-collector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. release-name + chart-name unless overridden.
*/}}
{{- define "argos-collector.fullname" -}}
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
{{- end }}

{{- define "argos-collector.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels applied to every object in the release.
*/}}
{{- define "argos-collector.labels" -}}
helm.sh/chart: {{ include "argos-collector.chart" . }}
{{ include "argos-collector.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: push-collector
app.kubernetes.io/part-of: argos
{{- end }}

{{- define "argos-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argos-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "argos-collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "argos-collector.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "argos-collector.clusterRoleName" -}}
{{- default (include "argos-collector.fullname" .) .Values.rbac.clusterRoleName }}
{{- end }}

{{- define "argos-collector.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}
