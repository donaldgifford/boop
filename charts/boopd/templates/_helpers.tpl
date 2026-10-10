{{/*
Expand the name of the chart.
*/}}
{{- define "boopd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this
(by the DNS naming spec). If release name contains chart name it will be used
as a full name.
*/}}
{{- define "boopd.fullname" -}}
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
{{- define "boopd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "boopd.labels" -}}
helm.sh/chart: {{ include "boopd.chart" . }}
{{ include "boopd.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "boopd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "boopd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "boopd.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "boopd.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the secret the deployment consumes via envFrom.
*/}}
{{- define "boopd.secretName" -}}
{{- if .Values.secrets.create }}
{{- include "boopd.fullname" . }}
{{- else }}
{{- required "secrets.existingSecret is required when secrets.create is false" .Values.secrets.existingSecret }}
{{- end }}
{{- end }}

{{/*
Create the name of the ConfigMap the deployment consumes via envFrom.
*/}}
{{- define "boopd.configMapName" -}}
{{- .Values.configMap.existingConfigMap | default (include "boopd.fullname" .) -}}
{{- end }}

{{/*
Reserved env-var names — keys that the chart already manages on the
Deployment container env list. `extraEnv`, `configMap.data`, and
`secrets.stringData` may not redeclare any of these: the chart-emitted
entry would shadow the operator's attempt (explicit `env` beats
`envFrom`) and produce confusing behavior at runtime.

Returns a space-separated string for has-element style checks.
*/}}
{{- define "boopd.reservedEnvVars" -}}
LISTEN_ADDR METRICS_ADDR LOG_LEVEL POD_NAME TEMPORAL_ADDRESS TEMPORAL_NAMESPACE TEMPORAL_TASK_QUEUE TEMPORAL_BUILD_ID TEMPORAL_TLS_CERT_PATH TEMPORAL_TLS_KEY_PATH TEMPORAL_TLS_CA_PATH TEMPORAL_TLS_SERVER_NAME TEMPORAL_TLS_DISABLED TEMPORAL_OIDC_TOKEN_URL TEMPORAL_OIDC_CLIENT_ID TEMPORAL_OIDC_CLIENT_SECRET_PATH TEMPORAL_OIDC_SCOPES TEMPORAL_OIDC_AUDIENCE
{{- end }}

{{/*
Validates that no operator-supplied env source collides with the
chart-managed env vars. Calls `fail` with a clear list of offenders so
the helm-render step exits with a useful error instead of silently
shadowing the chart's own env entries.

Renders empty on success; failure aborts the entire template render.
*/}}
{{- define "boopd.validateEnvCollisions" -}}
{{- $reserved := splitList " " (trim (include "boopd.reservedEnvVars" .)) -}}
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
{{- define "boopd.validateRemovedValues" -}}
{{- end }}

{{/*
Temporal connection env (TEMPORAL_*) and the mounted credential paths.
Copied from repo-guardian's chart with /etc/boopd paths.
*/}}
{{- define "boopd.temporalEnv" -}}
- name: TEMPORAL_ADDRESS
  value: {{ .Values.temporal.address | quote }}
- name: TEMPORAL_NAMESPACE
  value: {{ .Values.temporal.namespace | quote }}
- name: TEMPORAL_TASK_QUEUE
  value: {{ .Values.temporal.taskQueue | quote }}
- name: TEMPORAL_BUILD_ID
  value: {{ .Values.worker.buildId | default .Values.image.tag | default .Chart.AppVersion | quote }}
{{- $tls := .Values.temporal.tls }}
{{- $oidc := .Values.temporal.auth.oidc }}
{{- if $tls.existingSecret }}
- name: TEMPORAL_TLS_CERT_PATH
  value: /etc/boopd/temporal-tls/tls.crt
- name: TEMPORAL_TLS_KEY_PATH
  value: /etc/boopd/temporal-tls/tls.key
- name: TEMPORAL_TLS_CA_PATH
  value: /etc/boopd/temporal-tls/ca.crt
{{- else if $tls.caSecret }}
- name: TEMPORAL_TLS_CA_PATH
  value: /etc/boopd/temporal-ca/ca.crt
{{- end }}
{{- if and $tls.serverName (or $tls.existingSecret $oidc.tokenUrl) }}
- name: TEMPORAL_TLS_SERVER_NAME
  value: {{ $tls.serverName | quote }}
{{- end }}
{{- if $tls.disabled }}
- name: TEMPORAL_TLS_DISABLED
  value: "true"
{{- end }}
{{- if $oidc.tokenUrl }}
- name: TEMPORAL_OIDC_TOKEN_URL
  value: {{ $oidc.tokenUrl | quote }}
- name: TEMPORAL_OIDC_CLIENT_ID
  value: {{ $oidc.clientId | quote }}
- name: TEMPORAL_OIDC_CLIENT_SECRET_PATH
  value: /etc/boopd/temporal-oidc/client-secret
{{- with $oidc.scopes }}
- name: TEMPORAL_OIDC_SCOPES
  value: {{ join " " . | quote }}
{{- end }}
{{- with $oidc.audience }}
- name: TEMPORAL_OIDC_AUDIENCE
  value: {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Render guards for Temporal auth and TLS: each combination the binary
would refuse at startup (or silently misapply) fails here instead.
*/}}
{{- define "boopd.validateTemporalAuth" -}}
{{- $tls := .Values.temporal.tls -}}
{{- $oidc := .Values.temporal.auth.oidc -}}
{{- if and (not $oidc.tokenUrl) (or $oidc.clientId $oidc.existingSecret $oidc.scopes $oidc.audience) -}}
{{- fail "temporal.auth.oidc.tokenUrl is required when any other temporal.auth.oidc value is set" -}}
{{- end -}}
{{- if and $oidc.tokenUrl (not (and $oidc.clientId $oidc.existingSecret)) -}}
{{- fail "temporal.auth.oidc needs clientId and existingSecret (with key client-secret) alongside tokenUrl" -}}
{{- end -}}
{{- if and $tls.disabled (or $tls.existingSecret $tls.caSecret $tls.serverName) -}}
{{- fail "temporal.tls.disabled contradicts temporal.tls.existingSecret, caSecret and serverName" -}}
{{- end -}}
{{- if and $tls.caSecret $tls.existingSecret -}}
{{- fail "temporal.tls.caSecret and temporal.tls.existingSecret are exclusive: put ca.crt in existingSecret for mTLS" -}}
{{- end -}}
{{- if and $tls.caSecret (not $oidc.tokenUrl) -}}
{{- fail "temporal.tls.caSecret is for temporal.auth.oidc (server-verified TLS without a client certificate); for mTLS use temporal.tls.existingSecret" -}}
{{- end -}}
{{- end }}

{{/*
The Secrets boopd.hcl names (each App's private key, the Redis URL),
as a dict of Secret name -> list of keys. Each is mounted at
<secretsDir>/<name>/<key>, the path internal/config reads.
*/}}
{{- define "boopd.configSecrets" -}}
{{- $out := dict -}}
{{- range $name, $a := .Values.boopd.apps -}}
{{- with $a.privateKeySecretRef -}}
{{- $_ := set $out .name (append (get $out .name | default list) .key | uniq) -}}
{{- end -}}
{{- end -}}
{{- with include "boopd.redisSecretRef" . | fromJson -}}
{{- $_ := set $out .name (append (get $out .name | default list) .key | uniq) -}}
{{- end -}}
{{- toJson $out -}}
{{- end }}

{{/*
A volume name for a Secret: Secret names are DNS subdomains (dots
allowed), volume names are DNS labels.
*/}}
{{- define "boopd.secretVolumeName" -}}
{{- printf "secret-%s" (sha256sum . | trunc 10) -}}
{{- end }}

{{/*
Credential volumes and mounts: the config's Secrets plus Temporal's.
Mounted into the worker only; files are 0440 under the pod's fsGroup.
*/}}
{{- define "boopd.credentialVolumes" -}}
{{- range $name, $keys := include "boopd.configSecrets" . | fromJson }}
- name: {{ include "boopd.secretVolumeName" $name }}
  secret:
    secretName: {{ $name }}
    defaultMode: 0440
    items:
      {{- range $keys }}
      - key: {{ . }}
        path: {{ . }}
      {{- end }}
{{- end }}
{{- with .Values.temporal.tls.existingSecret }}
- name: temporal-tls
  secret:
    secretName: {{ . }}
    defaultMode: 0440
{{- end }}
{{- with .Values.temporal.tls.caSecret }}
- name: temporal-ca
  secret:
    secretName: {{ . }}
    defaultMode: 0440
{{- end }}
{{- with .Values.temporal.auth.oidc.existingSecret }}
- name: temporal-oidc
  secret:
    secretName: {{ . }}
    defaultMode: 0440
    items:
      - key: client-secret
        path: client-secret
{{- end }}
{{- end }}

{{- define "boopd.credentialMounts" -}}
{{- $dir := .Values.boopd.secretsDir -}}
{{- range $name, $_ := include "boopd.configSecrets" . | fromJson }}
- name: {{ include "boopd.secretVolumeName" $name }}
  mountPath: {{ printf "%s/%s" $dir $name }}
  readOnly: true
{{- end }}
{{- if .Values.temporal.tls.existingSecret }}
- name: temporal-tls
  mountPath: /etc/boopd/temporal-tls
  readOnly: true
{{- end }}
{{- if .Values.temporal.tls.caSecret }}
- name: temporal-ca
  mountPath: /etc/boopd/temporal-ca
  readOnly: true
{{- end }}
{{- if .Values.temporal.auth.oidc.existingSecret }}
- name: temporal-oidc
  mountPath: /etc/boopd/temporal-oidc
  readOnly: true
{{- end }}
{{- end }}

{{/*
PodSecurity admission labels for the runs namespace.
*/}}
{{- define "boopd.podSecurityLabels" -}}
pod-security.kubernetes.io/enforce: restricted
pod-security.kubernetes.io/enforce-version: latest
pod-security.kubernetes.io/audit: restricted
pod-security.kubernetes.io/warn: restricted
{{- end }}
