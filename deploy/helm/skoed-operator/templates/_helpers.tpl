{{- define "skoed-operator.name" -}}
{{- .Chart.Name }}
{{- end }}

{{- define "skoed-operator.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "skoed-operator.labels" -}}
app.kubernetes.io/name: {{ include "skoed-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "skoed-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- include "skoed-operator.fullname" . }}
{{- end }}
{{- end }}
