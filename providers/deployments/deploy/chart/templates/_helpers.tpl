{{- define "deployments.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-deployments" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- define "deployments.labels" -}}
app.kubernetes.io/name: faros-deployments-provider
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "deployments.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "deployments.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
