{{/*
Chart name (truncated to 63 chars for label compliance).
*/}}
{{- define "longue-vue-ingest-gw.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. release-name + chart-name unless overridden.
*/}}
{{- define "longue-vue-ingest-gw.fullname" -}}
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

{{- define "longue-vue-ingest-gw.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels applied to every object in the release.
*/}}
{{- define "longue-vue-ingest-gw.labels" -}}
helm.sh/chart: {{ include "longue-vue-ingest-gw.chart" . }}
{{ include "longue-vue-ingest-gw.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: ingest-gateway
app.kubernetes.io/part-of: longue-vue
{{- end }}

{{- define "longue-vue-ingest-gw.selectorLabels" -}}
app.kubernetes.io/name: {{ include "longue-vue-ingest-gw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "longue-vue-ingest-gw.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "longue-vue-ingest-gw.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "longue-vue-ingest-gw.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
mtlsCertVolumeMount — always mounted at the same fixed path so the gateway
binary's --tls-cert-file / --tls-key-file env defaults are mode-agnostic.
*/}}
{{- define "longue-vue-ingest-gw.mtlsMountPath" -}}
/etc/longue-vue-ingest-gw/tls
{{- end }}

{{/*
vaultAgentAnnotations renders the Vault Agent injector annotations needed
to populate /etc/longue-vue-ingest-gw/tls/{tls.crt,tls.key} from a Vault PKI
mount. Empty when mtls.mode != "vault".

The TTL renewal trigger uses the Vault Agent template's `min_stale` style:
the cert is re-fetched at renewAt% of TTL, well before expiry.
*/}}
{{- define "longue-vue-ingest-gw.vaultAnnotations" -}}
{{- if eq .Values.mtls.mode "vault" }}
vault.hashicorp.com/agent-inject: "true"
vault.hashicorp.com/agent-init-first: "true"
vault.hashicorp.com/role: {{ .Values.mtls.vault.role | quote }}
vault.hashicorp.com/agent-pre-populate-only: "false"
vault.hashicorp.com/agent-inject-secret-tls.crt: "{{ .Values.mtls.vault.pkiMount }}/issue/{{ .Values.mtls.vault.pkiRole }}"
vault.hashicorp.com/agent-inject-template-tls.crt: |
  {{`{{ with secret "`}}{{ .Values.mtls.vault.pkiMount }}/issue/{{ .Values.mtls.vault.pkiRole }}{{`" "common_name=`}}{{ include "longue-vue-ingest-gw.fullname" . }}{{`" "ttl=`}}{{ .Values.mtls.vault.certTTL }}{{`" -}}
  {{ .Data.certificate }}
  {{- end }}`}}
vault.hashicorp.com/agent-inject-secret-tls.key: "{{ .Values.mtls.vault.pkiMount }}/issue/{{ .Values.mtls.vault.pkiRole }}"
vault.hashicorp.com/agent-inject-template-tls.key: |
  {{`{{ with secret "`}}{{ .Values.mtls.vault.pkiMount }}/issue/{{ .Values.mtls.vault.pkiRole }}{{`" "common_name=`}}{{ include "longue-vue-ingest-gw.fullname" . }}{{`" "ttl=`}}{{ .Values.mtls.vault.certTTL }}{{`" -}}
  {{ .Data.private_key }}
  {{- end }}`}}
vault.hashicorp.com/agent-inject-file-tls.crt: "tls.crt"
vault.hashicorp.com/agent-inject-file-tls.key: "tls.key"
vault.hashicorp.com/secret-volume-path: {{ include "longue-vue-ingest-gw.mtlsMountPath" . | quote }}
{{- end }}
{{- end }}
