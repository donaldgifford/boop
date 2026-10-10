{{/*
boopd.hcl, the worker's config file (ADR-0004, DESIGN-0001 § API /
Interface Changes), rendered from .Values.boopd. The values mirror the
blocks internal/config decodes; examples/boopd.hcl is the annotated
reference. `boopd config validate` checks the rendered file in CI.
*/}}

{{/*
An HCL literal for any value. JSON is valid HCL expression syntax; the
only difference that matters is template interpolation, so "${" and
"%{" are escaped.
*/}}
{{- define "boopd.hclValue" -}}
{{- toRawJson . | replace "${" "$${" | replace "%{" "%%{" -}}
{{- end }}

{{/*
Optional attributes: (list <map> (list "valueKey:hcl_name" ...) <indent>).
A key that is absent, null or the empty string is omitted, so the
config's own defaults apply; false and 0 are kept.
*/}}
{{- define "boopd.hclAttrs" -}}
{{- $m := index . 0 | default dict -}}
{{- $pad := repeat (int (index . 2)) " " -}}
{{- range $pair := index . 1 -}}
{{- $k := first (splitList ":" $pair) -}}
{{- if hasKey $m $k -}}
{{- $v := get $m $k -}}
{{- if not (or (kindIs "invalid" $v) (and (kindIs "string" $v) (eq $v ""))) }}
{{ $pad }}{{ last (splitList ":" $pair) }} = {{ include "boopd.hclValue" $v }}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
A free block of Renovate options: (list <map> <indent>), one attribute
per key.
*/}}
{{- define "boopd.hclFree" -}}
{{- $pad := repeat (int (index . 1)) " " -}}
{{- range $k, $v := index . 0 }}
{{ $pad }}{{ $k }} = {{ include "boopd.hclValue" $v }}
{{- end -}}
{{- end }}

{{/*
A secret_ref block: (list <block name> <map> <indent>).
*/}}
{{- define "boopd.hclSecretRef" -}}
{{- $pad := repeat (int (index . 2)) " " -}}
{{- $ref := index . 1 }}
{{ $pad }}{{ index . 0 }} {
{{ $pad }}  name = {{ include "boopd.hclValue" $ref.name }}
{{ $pad }}  key  = {{ include "boopd.hclValue" $ref.key }}
{{ $pad }}}
{{- end }}

{{/*
The Redis URL Secret the config names: the explicit
boopd.renovate.redisSecretRef, else the redis subchart's when enabled.
JSON; "{}" when there is none.
*/}}
{{- define "boopd.redisSecretRef" -}}
{{- $ref := .Values.boopd.renovate.redisSecretRef | default dict -}}
{{- if $ref.name -}}
{{- toJson $ref -}}
{{- else if .Values.redis.enabled -}}
{{- toJson (dict "name" (printf "%s-redis-auth" .Release.Name) "key" "url") -}}
{{- else -}}
{}
{{- end -}}
{{- end }}

{{/*
Renovate's global options: the configured ones plus, with the redis
subchart, the key prefix its ACL user is limited to.
*/}}
{{- define "boopd.renovateGlobal" -}}
{{- $g := deepCopy (.Values.boopd.renovate.global | default dict) -}}
{{- if and .Values.redis.enabled (not (hasKey $g "redisPrefix")) -}}
{{- $_ := set $g "redisPrefix" .Values.redis.keyPrefix -}}
{{- end -}}
{{- toJson $g -}}
{{- end }}

{{- define "boopd.configFile" -}}
{{- $c := .Values.boopd -}}
# Rendered by the boopd chart from .Values.boopd; see examples/boopd.hcl.
secrets_dir = {{ include "boopd.hclValue" $c.secretsDir }}

renovate {
{{- include "boopd.hclAttrs" (list $c.renovate (list "image:image" "configPath:config_path" "sharedPreset:shared_preset" "logLevel:log_level" "dryRun:dry_run" "gitAuthor:git_author") 2) }}
{{- with include "boopd.renovateGlobal" . | fromJson }}

  global {
{{- include "boopd.hclFree" (list . 4) }}
  }
{{- end }}
{{- with include "boopd.redisSecretRef" . | fromJson }}
{{ include "boopd.hclSecretRef" (list "redis_secret_ref" . 2) }}
{{- end }}
}

runs {
  namespace = {{ include "boopd.hclValue" ($c.runs.namespace | default .Release.Namespace) }}
{{- include "boopd.hclAttrs" (list $c.runs (list "pendingTimeout:pending_timeout" "maxConcurrent:max_concurrent") 2) }}
}
{{- range $name, $p := $c.profiles }}
{{- $p = $p | default dict }}

profile {{ include "boopd.hclValue" $name }} {
{{- include "boopd.hclAttrs" (list $p (list "inherit:inherit") 2) }}
{{- with $p.pod }}
  pod {
{{- include "boopd.hclAttrs" (list . (list "runtimeClassName:runtime_class_name" "labels:labels" "annotations:annotations" "nodeSelector:node_selector" "terminationGracePeriodSeconds:termination_grace_period_seconds" "workSizeLimit:work_size_limit") 4) }}
{{- with .resources }}
    resources {
{{- include "boopd.hclAttrs" (list . (list "requests:requests" "limits:limits") 6) }}
    }
{{- end }}
{{- range .tolerations }}
    toleration {
{{- include "boopd.hclAttrs" (list . (list "key:key" "operator:operator" "value:value" "effect:effect" "tolerationSeconds:toleration_seconds") 6) }}
    }
{{- end }}
  }
{{- end }}
{{- with $p.renovate }}
  renovate {
{{- include "boopd.hclFree" (list . 4) }}
  }
{{- end }}
}
{{- end }}

order           = {{ include "boopd.hclValue" $c.order }}
default_profile = {{ include "boopd.hclValue" $c.defaultProfile }}
unknown_profile = {{ include "boopd.hclValue" $c.unknownProfile }}
{{- range $c.profileRules }}

profile_rule {
{{- include "boopd.hclAttrs" (list . (list "extends:extends" "managers:managers" "profile:profile") 2) }}
}
{{- end }}
{{- range $name, $a := $c.apps }}

app {{ include "boopd.hclValue" $name }} {
{{- include "boopd.hclAttrs" (list $a (list "endpoint:endpoint" "appId:app_id" "installations:installations" "cadence:cadence") 2) }}
{{ include "boopd.hclSecretRef" (list "private_key_secret_ref" (required (printf "boopd.apps.%s.privateKeySecretRef is required" $name) $a.privateKeySecretRef) 2) }}
{{- with $a.discovery }}

  discovery {
{{- include "boopd.hclAttrs" (list . (list "every:every" "probe:probe" "skipForks:skip_forks" "skipArchived:skip_archived") 4) }}
  }
{{- end }}
{{- with $a.budget }}

  budget {
{{- include "boopd.hclAttrs" (list . (list "reserveFraction:reserve_fraction" "maxConcurrentRuns:max_concurrent_runs" "defaultEstimate:default_estimate") 4) }}
  }
{{- end }}
}
{{- end }}
{{ end }}
