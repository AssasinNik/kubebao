{{/*
Expand the name of the chart.
*/}}
{{- define "kubebao.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kubebao.fullname" -}}
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
{{- define "kubebao.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubebao.labels" -}}
helm.sh/chart: {{ include "kubebao.chart" . }}
{{ include "kubebao.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubebao.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubebao.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubebao.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubebao.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
KMS component labels
*/}}
{{- define "kubebao.kms.labels" -}}
{{ include "kubebao.labels" . }}
app.kubernetes.io/component: kms
{{- end }}

{{/*
KMS selector labels
*/}}
{{- define "kubebao.kms.selectorLabels" -}}
{{ include "kubebao.selectorLabels" . }}
app.kubernetes.io/component: kms
{{- end }}

{{/*
CSI component labels
*/}}
{{- define "kubebao.csi.labels" -}}
{{ include "kubebao.labels" . }}
app.kubernetes.io/component: csi
{{- end }}

{{/*
CSI selector labels
*/}}
{{- define "kubebao.csi.selectorLabels" -}}
{{ include "kubebao.selectorLabels" . }}
app.kubernetes.io/component: csi
{{- end }}

{{/*
Operator component labels
*/}}
{{- define "kubebao.operator.labels" -}}
{{ include "kubebao.labels" . }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Operator selector labels
*/}}
{{- define "kubebao.operator.selectorLabels" -}}
{{ include "kubebao.selectorLabels" . }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Get the image for a component
*/}}
{{- define "kubebao.image" -}}
{{- $registry := .root.Values.global.image.registry -}}
{{- $repository := .image.repository -}}
{{- $tag := .image.tag | default .root.Values.global.image.tag | default .root.Chart.AppVersion -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end }}

{{/*
OpenBao address
*/}}
{{- define "kubebao.openbaoAddress" -}}
{{- .Values.global.openbao.address -}}
{{- end }}
