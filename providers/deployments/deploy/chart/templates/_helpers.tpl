{{- define "deployments.fullname" -}}
{{- printf "%s-deployments" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "deployments.labels" -}}
app.kubernetes.io/name: faros-deployments-provider
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
