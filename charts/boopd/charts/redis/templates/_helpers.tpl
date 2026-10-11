{{/*
Names are fixed off the release so the parent chart can reference the
Secret and Service without sharing helpers.
*/}}
{{- define "redis.fullname" -}}
{{- printf "%s-redis" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "redis.labels" -}}
app.kubernetes.io/name: redis
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: renovate-cache
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "redis.selectorLabels" -}}
app.kubernetes.io/name: redis
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The renovate user's password: kept across upgrades through lookup,
generated on first install.
*/}}
{{- define "redis.password" -}}
{{- $existing := lookup "v1" "Secret" .Release.Namespace (printf "%s-auth" (include "redis.fullname" .)) -}}
{{- if and $existing $existing.data (hasKey $existing.data "password") -}}
{{- index $existing.data "password" | b64dec -}}
{{- else -}}
{{- randAlphaNum 40 -}}
{{- end -}}
{{- end }}
