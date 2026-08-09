{{/*
Expand the chart name.
*/}}
{{- define "namespace-cost-governor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a release-specific fully qualified app name.
*/}}
{{- define "namespace-cost-governor.fullname" -}}
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
Chart label.
*/}}
{{- define "namespace-cost-governor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "namespace-cost-governor.labels" -}}
helm.sh/chart: {{ include "namespace-cost-governor.chart" . }}
app.kubernetes.io/name: {{ include "namespace-cost-governor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "namespace-cost-governor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "namespace-cost-governor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "namespace-cost-governor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "namespace-cost-governor.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Slack secret name.
*/}}
{{- define "namespace-cost-governor.slackSecretName" -}}
{{- default (include "namespace-cost-governor.fullname" .) .Values.slack.existingSecret }}
{{- end }}

{{/*
Slack secret data key.
*/}}
{{- define "namespace-cost-governor.slackSecretKey" -}}
{{- if .Values.slack.existingSecret }}
{{- .Values.slack.existingSecretKey }}
{{- else }}
webhookURL
{{- end }}
{{- end }}
