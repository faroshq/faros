{{/*
Shared helpers. fullName follows the standard
{{ release-name }}-{{ chart-name }} pattern unless the user overrides
.Values.fullnameOverride.
*/}}

{{- define "infrastructure.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "infrastructure.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "infrastructure.labels" -}}
app.kubernetes.io/name: {{ include "infrastructure.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "infrastructure.selectorLabels" -}}
app.kubernetes.io/name: {{ include "infrastructure.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "infrastructure.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "infrastructure.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
centralKroSecretName resolves to either the user-supplied existing
Secret (centralKro.kubeconfigSecretRef.name) or the chart-rendered
"<release>-kro-kubeconfig" Secret when centralKro.kubeconfig is set
inline. Returns empty string when neither is configured. A seeded non-operator
install rejects that state; bootstrap.seedTemplates=false keeps the explicit
externally-managed/stub escape hatch.
*/}}
{{- define "infrastructure.centralKroSecretName" -}}
{{- if .Values.centralKro.kubeconfigSecretRef.name -}}
{{- .Values.centralKro.kubeconfigSecretRef.name -}}
{{- else if .Values.centralKro.kubeconfig -}}
{{- printf "%s-kro-kubeconfig" (include "infrastructure.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "infrastructure.centralKroSecretKey" -}}
{{- default "kubeconfig" .Values.centralKro.kubeconfigSecretRef.key -}}
{{- end -}}

{{/*
kcpKubeconfigSecretName resolves the Secret the bootstrap init container reads
the kcp admin kubeconfig from: either the user-supplied existing Secret
(bootstrap.kcpKubeconfigSecretRef.name) or the chart-rendered
"<release>-kcp-kubeconfig" Secret when bootstrap.kcpKubeconfig is set inline.
Empty when bootstrap is disabled / no kubeconfig configured.
*/}}
{{- define "infrastructure.kcpKubeconfigSecretName" -}}
{{- if .Values.bootstrap.kcpKubeconfigSecretRef.name -}}
{{- .Values.bootstrap.kcpKubeconfigSecretRef.name -}}
{{- else if .Values.bootstrap.kcpKubeconfig -}}
{{- printf "%s-kcp-kubeconfig" (include "infrastructure.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "infrastructure.kcpKubeconfigSecretKey" -}}
{{- default "kubeconfig" .Values.bootstrap.kcpKubeconfigSecretRef.key -}}
{{- end -}}

{{/*
bootstrapSecretName / bootstrapSecretKey resolve the admin/supplied kubeconfig
Secret mounted by the init container when bootstrap.enabled=true. Serve never
mounts this credential; init writes a minted least-privilege kubeconfig to a
shared emptyDir. Two bootstrap input sources:
  - kubeconfigSource=hubMinted (default): the hub-delivered runtime kubeconfig
    Secret (providerKubeconfig.secretName, key "kubeconfig"). The platform
    admin's CatalogEntry triggers the hub to mint it (cluster-admin in the
    provider workspace) and write it via HostSecretWriter.
  - kubeconfigSource=supplied: a kcp kubeconfig you provide
    (bootstrap.kcpKubeconfig inline or kcpKubeconfigSecretRef).
*/}}
{{- define "infrastructure.bootstrapSecretName" -}}
{{- if eq .Values.bootstrap.kubeconfigSource "supplied" -}}
{{- include "infrastructure.kcpKubeconfigSecretName" . -}}
{{- else -}}
{{- .Values.providerKubeconfig.secretName -}}
{{- end -}}
{{- end -}}

{{- define "infrastructure.bootstrapSecretKey" -}}
{{- if eq .Values.bootstrap.kubeconfigSource "supplied" -}}
{{- include "infrastructure.kcpKubeconfigSecretKey" . -}}
{{- else -}}
kubeconfig
{{- end -}}
{{- end -}}
