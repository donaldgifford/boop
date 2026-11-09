# boopd config (ADR-0004, DESIGN-0001 § API / Interface Changes).
# docs/config.md describes every setting, its default and its chart key.
# The chart renders this file from its values; `boopd config validate`
# checks it without starting anything.

# Where the chart mounts the Secrets named below: <dir>/<name>/<key>.
secrets_dir = "/etc/boopd/secrets"

renovate {
  # Renovate keeps the tag and digest current.
  image         = "ghcr.io/renovatebot/renovate:44-full@sha256:0f5b1a9e4c3d2b1a0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6c5b4a392"
  # config_paths lists the files a repository is onboarded by, in
  # precedence order. Omitted, it is Renovate's own GitHub list, so boopd
  # parses the file Renovate reads: renovate.json{,c,5},
  # .github/renovate.json{,c,5}, .renovaterc, .renovaterc.json{,c,5}.
  # Each path is one GraphQL lookup per repository during discovery:
  # config_paths = ["renovate.json5"] (what repo-guardian writes) probes
  # 100 repositories per query instead of 10 (INV-0002).
  shared_preset = "github>boop-bot/renovate-config:default.json"
  log_level     = "info"
  dry_run       = "full" # spike comparison runs

  # Merged into RENOVATE_CONFIG; an explicit false is preserved.
  global {
    prHourlyLimit = 0
  }

  redis_secret_ref {
    name = "boopd-redis"
    key  = "url"
  }
  # Or, for a Secret holding only the password:
  #   redis_url = "redis://renovate@boopd-redis:6379"
  #   redis_password_secret_ref {
  #     name = "boopd-redis-password"
  #     key  = "password"
  #   }
}

runs {
  namespace       = "boopd" # where Jobs are created; the worker's own namespace
  pending_timeout = "10m"
  max_concurrent  = 20 # cluster-capacity cap across installations (OQ12)
}

profile "baseline" {
  pod {
    labels = { "boopd.dev/egress" = "baseline" } # selects a NetworkPolicy, if the chart renders them
    resources {
      requests = { cpu = "1", memory = "2Gi" }
      limits   = { cpu = "2", memory = "4Gi" }
    }
  }
}

profile "node" {
  inherit = "baseline" # scripts are already ignored by default
}

# Sandboxed profiles run in the sandbox block's runtime class
# (DESIGN-0005): their builds execute repository code. The sandbox owns
# placement, so their pods set no runtime_class_name, node_selector or
# toleration.
profile "jvm" {
  inherit           = "baseline"
  sandboxed         = true
  unsafe_executions = ["gradleWrapper"] # Renovate's allowedUnsafeExecutions, sandbox only
  egress_allow      = ["repo1.maven.org", "services.gradle.org", "plugins.gradle.org"]
}

profile "python" {
  inherit      = "baseline"
  sandboxed    = true
  egress_allow = ["pypi.org", "files.pythonhosted.org"]
  pod {
    labels = { "boopd.dev/egress" = "python" }
    resources {
      limits = { memory = "6Gi" }
    }
  }
  # No PIP_ONLY_BINARY or UV_NO_BUILD: they do not stop a git+ or
  # direct-reference build, and pip-compile writes the flag into the lock
  # file (IMPL-0006 OQ12). The sandbox contains repository code.
}

profile "strict" {
  inherit = "python" # the posture for an ecosystem not yet classified
}

order           = ["baseline", "node", "jvm", "python", "strict"] # ascending strictness
default_profile = "baseline"
unknown_profile = "strict"

# Every matching rule contributes; the strictest wins.
profile_rule {
  extends  = ["github>boop-bot/renovate-config:python"]
  managers = ["pip_requirements", "pip_setup", "pipenv", "poetry", "pep621", "pip-compile", "pyenv"]
  profile  = "python"
}

profile_rule {
  extends  = ["github>boop-bot/renovate-config:jvm"]
  managers = ["gradle", "gradle-wrapper", "maven", "maven-wrapper"]
  profile  = "jvm"
}

profile_rule {
  extends  = ["github>boop-bot/renovate-config:node"]
  managers = ["npm", "bun"]
  profile  = "node"
}

# The sandboxed tier (DESIGN-0005). Without it, sandboxed profiles and
# repositories with a floor manager fail closed with NeedsSandbox; with
# it, they run under the RuntimeClass with the narrowed token and egress.
# sandbox {
#   runtime_class   = "kata"          # placement comes from the RuntimeClass
#   service_account = "boopd-sandbox" # no RBAC; the chart creates it
#
#   # Optional: dedicated nodes for sandboxed Jobs only.
#   # node_selector = { "boopd.dev/sandbox" = "true" }
#   # toleration {
#   #   key    = "boopd.dev/sandbox"
#   #   value  = "true"
#   #   effect = "NoSchedule"
#   # }
#
#   egress_allow = [
#     "github.com", "api.github.com", "codeload.github.com",
#     "objects.githubusercontent.com", "release-assets.githubusercontent.com",
#     "ghcr.io",
#   ]
#   floor_mode      = "report" # "enforce" sandboxes floor managers and forces them off elsewhere
#   floor_add       = []       # managers to sandbox in addition to the built-in floor
#   pending_timeout = "11m"    # VM boot included; runs.pending_timeout + 60s by default
# }

# Webhook ingest (DESIGN-0003): `boopd ingest` serves GitHub's deliveries
# for every app with a webhook block. Omit the block for the defaults.
ingest {
  path         = "/webhooks/github" # the only path the Ingress or HTTPRoute exposes
  settle       = "30s"              # gather a burst of events into one run
  min_interval = "5m"               # least time between run starts; checkbox clicks skip it
  audit_every  = "15m"              # delivery audit Schedule, per app with a webhook
}

app "boop-bot" {
  endpoint      = "https://api.github.com"
  app_id        = 123456
  installations = [] # optional allowlist; empty means every installation
  cadence       = "24h"

  private_key_secret_ref {
    name = "boop-bot-app"
    key  = "private-key.pem"
  }

  discovery {
    every         = "6h" # Schedule interval; also the RepoWorkflow absence base
    probe         = "graphql"
    skip_forks    = true
    skip_archived = true
  }

  # Limits are discovered, never configured.
  budget {
    reserve_fraction    = 0.10
    max_concurrent_runs = 10 # per-installation cap for secondary limits; 0 = budget only
    default_estimate    = { core = 300, graphql = 150 }
  }

  # Deliveries from this App reach ingest. Events boop-bot itself sends
  # are recorded but never routed.
  webhook {
    bot = "boop-bot[bot]" # default: "<app label>[bot]"
    secret_ref {
      name = "boop-bot-webhook"
      key  = "secret"
    }
  }
}
