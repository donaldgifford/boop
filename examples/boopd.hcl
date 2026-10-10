# boopd config (ADR-0004, DESIGN-0001 § API / Interface Changes).
# The chart renders this file from its values; `boopd config validate`
# checks it without starting anything.

# Where the chart mounts the Secrets named below: <dir>/<name>/<key>.
secrets_dir = "/etc/boopd/secrets"

renovate {
  # Renovate keeps the tag and digest current.
  image         = "ghcr.io/renovatebot/renovate:44-full@sha256:0f5b1a9e4c3d2b1a0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6c5b4a392"
  config_path   = "renovate.json" # the one path discovery probes; what repo-guardian writes
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

profile "python" {
  inherit = "baseline"
  pod {
    runtime_class_name = "gvisor" # omit if the cluster has no sandboxing runtime
    labels             = { "boopd.dev/egress" = "python" }
    resources {
      limits = { memory = "6Gi" }
    }
  }
  renovate {
    # Passed to child processes by Renovate.
    customEnvVariables = {
      PIP_ONLY_BINARY = ":all:" # pip, pip-compile, pipenv: never build an sdist
      UV_NO_BUILD     = "1"     # uv: never build an sdist
    }
  }
}

profile "strict" {
  inherit = "python" # the posture for an ecosystem not yet classified
}

order           = ["baseline", "node", "python", "strict"] # ascending strictness
default_profile = "baseline"
unknown_profile = "strict"

# Every matching rule contributes; the strictest wins.
profile_rule {
  extends  = ["github>boop-bot/renovate-config:python"]
  managers = ["pip_requirements", "pip_setup", "pipenv", "poetry", "pep621", "pip-compile", "pyenv"]
  profile  = "python"
}

profile_rule {
  extends  = ["github>boop-bot/renovate-config:node"]
  managers = ["npm", "bun"]
  profile  = "node"
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
}
