{{/*
Chart name (truncated to 63 chars for label compliance).
*/}}
{{- define "longue-vue-collector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. release-name + chart-name unless overridden.
*/}}
{{- define "longue-vue-collector.fullname" -}}
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

{{- define "longue-vue-collector.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels applied to every object in the release.
*/}}
{{- define "longue-vue-collector.labels" -}}
helm.sh/chart: {{ include "longue-vue-collector.chart" . }}
{{ include "longue-vue-collector.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: push-collector
app.kubernetes.io/part-of: longue-vue
{{- end }}

{{- define "longue-vue-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "longue-vue-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "longue-vue-collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "longue-vue-collector.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "longue-vue-collector.clusterRoleName" -}}
{{- default (include "longue-vue-collector.fullname" .) .Values.rbac.clusterRoleName }}
{{- end }}

{{- define "longue-vue-collector.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}
