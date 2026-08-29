{{/*
Expand the name of the chart.
*/}}
{{- define "inari-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "inari-operator.fullname" -}}
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
Common labels.
*/}}
{{- define "inari-operator.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "inari-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "inari-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "inari-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/*
Name of the service account to use.
*/}}
{{- define "inari-operator.serviceAccountName" -}}
{{- include "inari-operator.fullname" . }}
{{- end }}

{{/*
Image reference: global.imageRegistry is prepended to the repository;
digest wins over tag; empty tag defaults to the chart appVersion.
Usage: {{ include "inari-operator.image" (dict "root" . "image" .Values.image) }}
*/}}
{{- define "inari-operator.image" -}}
{{- $repo := .image.repository -}}
{{- with .root.Values.global.imageRegistry -}}
{{- $repo = printf "%s/%s" (. | trimSuffix "/") $repo -}}
{{- end -}}
{{- if .image.digest -}}
{{- printf "%s@%s" $repo .image.digest -}}
{{- else -}}
{{- printf "%s:%s" $repo (.image.tag | default .root.Chart.AppVersion) -}}
{{- end -}}
{{- end -}}

{{/*
imagePullSecrets from global.imagePullSecrets (list of secret names), for
pod specs and the ServiceAccount. Include at the target indentation.
*/}}
{{- define "inari-operator.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
  {{- range . }}
  - name: {{ . | quote }}
  {{- end }}
{{- end }}
{{- end -}}
