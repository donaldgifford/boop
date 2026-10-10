{{/*
Expand the name of the chart.
*/}}
{{- define "boop.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this
(by the DNS naming spec). If release name contains chart name it will be used
as a full name.
*/}}
{{- define "boop.fullname" -}}
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
{{- define "boop.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "boop.labels" -}}
helm.sh/chart: {{ include "boop.chart" . }}
{{ include "boop.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "boop.selectorLabels" -}}
app.kubernetes.io/name: {{ include "boop.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "boop.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "boop.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the secret the deployment consumes via envFrom.
*/}}
{{- define "boop.secretName" -}}
{{- if .Values.secrets.create }}
{{- include "boop.fullname" . }}
{{- else }}
{{- required "secrets.existingSecret is required when secrets.create is false" .Values.secrets.existingSecret }}
{{- end }}
{{- end }}

{{/*
Create the name of the ConfigMap the deployment consumes via envFrom.
*/}}
{{- define "boop.configMapName" -}}
{{- .Values.configMap.existingConfigMap | default (include "boop.fullname" .) -}}
{{- end }}

{{/*
Reserved env-var names — keys that the chart already manages on the
Deployment container env list. `extraEnv`, `configMap.data`, and
`secrets.stringData` may not redeclare any of these: the chart-emitted
entry would shadow the operator's attempt (explicit `env` beats
`envFrom`) and produce confusing behavior at runtime.

Returns a space-separated string for has-element style checks.
*/}}
{{- define "boop.reservedEnvVars" -}}
LISTEN_ADDR METRICS_ADDR LOG_LEVEL POD_NAME
{{- end }}

{{/*
Validates that no operator-supplied env source collides with the
chart-managed env vars. Calls `fail` with a clear list of offenders so
the helm-render step exits with a useful error instead of silently
shadowing the chart's own env entries.

Renders empty on success; failure aborts the entire template render.
*/}}
{{- define "boop.validateEnvCollisions" -}}
{{- $reserved := splitList " " (trim (include "boop.reservedEnvVars" .)) -}}
{{- $offenders := list -}}
{{- range .Values.extraEnv -}}
{{- if has .name $reserved -}}
{{- $offenders = append $offenders (printf "extraEnv[%s]" .name) -}}
{{- end -}}
{{- end -}}
{{- range $k, $_ := .Values.configMap.data -}}
{{- if has $k $reserved -}}
{{- $offenders = append $offenders (printf "configMap.data[%s]" $k) -}}
{{- end -}}
{{- end -}}
{{- range $k, $_ := .Values.secrets.stringData -}}
{{- if has $k $reserved -}}
{{- $offenders = append $offenders (printf "secrets.stringData[%s]" $k) -}}
{{- end -}}
{{- end -}}
{{- if $offenders -}}
{{- fail (printf "chart-managed env vars are shadowed: %s (reserved: %s)" (join ", " $offenders) (join " " $reserved)) -}}
{{- end -}}
{{- end }}

{{/*
Fail render when a values file still sets a knob removed in a chart
upgrade. JSON Schema accepts unknown keys (there is no
additionalProperties: false on this chart), so without a guard a stale
values file renders happily and the operator silently loses the
behaviour they think they configured.

No knobs have been removed yet at chart 0.1.0 — this helper is the
documented pattern. When you remove a value in a future release, add a
`hasKey`/`fail` entry here (and a matching unit test), and delete the
entry once operators have had a release or two to notice:

  {{- if hasKey .Values.someSection "removedKnob" -}}
  {{- fail "someSection.removedKnob was removed in <ref>: <why>. Delete the value." -}}
  {{- end -}}

Renders empty on success; failure aborts the entire template render.
*/}}
{{- define "boop.validateRemovedValues" -}}
{{- end }}
