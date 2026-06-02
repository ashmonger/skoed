{{/* Generate full release name. Truncated to 63 chars (K8s label limit). */}}
{{- define "dblock.fullname" -}}
{{- printf "%s" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels stamped on every resource. */}}
{{- define "dblock.labels" -}}
app.kubernetes.io/name: dblock
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* Selector labels stay stable across helm upgrade (no version-pinned labels). */}}
{{- define "dblock.selectorLabels" -}}
app.kubernetes.io/name: dblock
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Generate a bootstrap token: prefer the operator-supplied .Values.bootstrap.token,
fall back to a fresh randAlphaNum 64 at template time. Helm renders this once
per `helm install`; subsequent `helm upgrade` runs against the same release
reuse the existing Secret (Helm hashes the rendered manifest and only updates
when content changes).
*/}}
{{- define "dblock.bootstrapToken" -}}
{{- if .Values.bootstrap.token -}}
{{ .Values.bootstrap.token }}
{{- else -}}
{{ randAlphaNum 64 }}
{{- end -}}
{{- end -}}

{{- define "dblock.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "dblock.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
