{{/*
Expand the name of the chart.
*/}}
{{- define "longue-vue.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "longue-vue.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "longue-vue.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "longue-vue.labels" -}}
helm.sh/chart: {{ include "longue-vue.chart" . }}
{{ include "longue-vue.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "longue-vue.selectorLabels" -}}
app.kubernetes.io/name: {{ include "longue-vue.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "longue-vue.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "longue-vue.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "longue-vue.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Database URL. When bundled PostgreSQL is enabled, build the DSN from
the chart values. Otherwise require externalDatabase.url.
*/}}
{{- define "longue-vue.databaseUrl" -}}
{{- if .Values.postgresql.enabled -}}
postgres://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ include "longue-vue.fullname" . }}-postgresql:5432/{{ .Values.postgresql.auth.database }}?sslmode=disable
{{- else -}}
{{ required "externalDatabase.url is required when postgresql.enabled=false" .Values.externalDatabase.url }}
{{- end -}}
{{- end }}

{{/*
Secret name for longue-vue server credentials.
*/}}
{{- define "longue-vue.secretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else }}
{{- include "longue-vue.fullname" . }}
{{- end }}
{{- end }}
