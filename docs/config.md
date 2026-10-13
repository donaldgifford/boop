# boopd.hcl reference

<!--toc:start-->
- [How the file is used](#how-the-file-is-used)
- [Syntax](#syntax)
- [Layout](#layout)
- [Top level](#top-level)
- [renovate](#renovate)
  - [global](#global)
  - [redis\_secret\_ref](#redis_secret_ref)
  - [redis\_url and redis\_password\_secret\_ref](#redis_url-and-redis_password_secret_ref)
  - [What a run's RENOVATE\_CONFIG holds](#what-a-runs-renovate_config-holds)
- [runs](#runs)
- [profile](#profile)
  - [pod](#pod)
  - [Inheritance](#inheritance)
- [Choosing a profile](#choosing-a-profile)
  - [profile\_rule](#profile_rule)
- [app](#app)
  - [private\_key\_secret\_ref](#private_key_secret_ref)
  - [discovery](#discovery)
  - [budget](#budget)
- [Secrets](#secrets)
- [Chart values](#chart-values)
- [Validation](#validation)
<!--toc:end-->

`boopd.hcl` is the worker's only configuration (ADR-0004): which Renovate
image runs, where its Jobs go, the ecosystem profiles and how a repository
is matched to one, and the GitHub Apps whose installations are discovered.
Limits are never configured. The installation budget is read from
`/rate_limit` (ADR-0008).

[examples/boopd.hcl](../examples/boopd.hcl) is a complete file. This page
describes every setting in it. `TestReferenceCoversSchema` in
`internal/config` fails if a setting is missing here.

## How the file is used

- The chart renders it from `.Values.boopd` into the `<release>-config`
  ConfigMap and mounts it at `/etc/boopd/boopd.hcl`. The worker starts with
  `boopd worker --config /etc/boopd/boopd.hcl`. See [Chart values](#chart-values)
  for how the keys map.
- `boopd config validate <file>` loads and validates a file without
  reading secrets or contacting anything, and exits 1 on any error. CI runs
  it on the chart's rendered files (`just helm-config-validate`).
- The file is read once, at worker start. A change takes a restart, which
  the chart does by rolling the Deployment when the ConfigMap changes.

## Syntax

- HCL native syntax. Unknown attributes and blocks are errors, not ignored.
- Every error is reported at once, each with file, line and column.
- Durations are strings in Go form: `"90s"`, `"10m"`, `"6h"`, `"24h"`. A
  set duration must be positive; omit the attribute to get its default.
- `env("NAME")` returns an environment variable of the worker, or `""`.
  Secrets do not go through it; see [Secrets](#secrets).
- Optional attributes may be omitted; the defaults below apply.

## Layout

```hcl
secrets_dir = "/etc/boopd/secrets"

renovate {
  image = "ghcr.io/renovatebot/renovate:44-full@sha256:…"
  # config_paths, shared_preset, log_level, dry_run, git_author
  global { … }
  redis_secret_ref { … } # or redis_url with redis_password_secret_ref { … }
}

runs {
  namespace = "boopd"
  # pending_timeout, max_concurrent
}

profile "baseline" { … } # one or more
profile "python" { inherit = "baseline" … }

order           = ["baseline", "python"]
default_profile = "baseline"
unknown_profile = "python"
profile_rule { … } # zero or more

app "boop-bot" { … } # one or more
```

## Top level

| Setting | Type | Default | Description |
| - | - | - | - |
| `secrets_dir` | string | `/etc/boopd/secrets` | Where the Secrets named by `*_secret_ref` blocks are mounted, as `<secrets_dir>/<name>/<key>`. |
| `renovate` | block | required | What every run shares. |
| `runs` | block | required | Where and how Jobs run. |
| `profile` | block, labelled | at least one | An ecosystem profile. |
| `order` | list of strings | required | Every profile, from least to most strict. |
| `default_profile` | string | required | The profile for a repository whose config extends presets no rule matches. |
| `unknown_profile` | string | required | The profile for a repository whose config has no `extends`. |
| `profile_rule` | block | none | Maps presets and managers to a profile. |
| `app` | block, labelled | at least one | A GitHub App whose installations are discovered. |

## renovate

| Setting | Type | Default | Description |
| - | - | - | - |
| `image` | string | required | The Renovate image every Job runs, pinned by digest (`name:tag@sha256:<64 hex>`). Renovate keeps the tag and digest current. |
| `config_paths` | list of strings | Renovate's GitHub names | The files a repository is onboarded by, in precedence order (ADR-0005, DESIGN-0001 OQ3). The default is `renovate.json`, `renovate.jsonc`, `renovate.json5`, `.github/renovate.json`, `.github/renovate.jsonc`, `.github/renovate.json5`, `.renovaterc`, `.renovaterc.json`, `.renovaterc.jsonc`, `.renovaterc.json5`: the order Renovate reads them in, so the first one present is the file Renovate uses and the one discovery parses for `extends`. At most 16 distinct, clean paths relative to the repository root. An empty list means the default. |
| `shared_preset` | string | none | Prepended to `extends` of every run. Its repository is added to each run token's scope (DESIGN-0001 OQ2), so it must be in every installation. A GitHub-hosted preset must name a `.json` file: Renovate only fetches `.json` from GitHub, and `.json5` is rejected here rather than failing at run time. |
| `log_level` | string | `info` | Renovate's `LOG_LEVEL` in the Job: `trace`, `debug`, `info`, `warn`, `error` or `fatal`. The worker's own level is the chart's `config.logLevel`. |
| `dry_run` | string | none | Renovate's `dryRun` for every run: `extract`, `lookup` or `full`. Overrides any `dryRun` in `global`. Unset, runs push branches and open PRs. |
| `git_author` | string | none | `RENOVATE_GIT_AUTHOR`, e.g. `boop-bot <boop-bot[bot]@users.noreply.github.com>`. |
| `global` | block | none | Renovate options for every run. |
| `redis_secret_ref` | block | none | The Secret holding Renovate's Redis URL. |
| `redis_url` | string | none | A Redis URL without a password (`redis://` or `rediss://`), the alternative to `redis_secret_ref`. |
| `redis_password_secret_ref` | block | none | The Secret holding `redis_url`'s password. |

### global

Any Renovate repository or global option, one attribute per option, with
nested config written as HCL objects and tuples:

```hcl
global {
  prHourlyLimit = 2
  automerge     = false # an explicit false is kept
  labels        = ["dependencies"]
  packageRules  = [{ matchManagers = ["gomod"], groupName = "go" }]
}
```

Values become JSON. `null` is an error. These options are rejected, because
boopd owns them through the Job's environment or they are Renovate's script
and environment controls (DESIGN-0001 § Process builder):

`exposeAllEnv`, `allowScripts`, `allowedCommands`, `logLevel`, `platform`,
`endpoint`, `token`, `autodiscover`, `repositories`, `requireConfig`,
`onboarding`, `baseDir`, `cacheDir`, `binarySource`, `redisUrl`,
`reportType`, `reportPath`, `exitCodeForErrors`, `gitAuthor`.

`customEnvVariables`, if set, must be a map of strings.

### redis_secret_ref

| Setting | Type | Default | Description |
| - | - | - | - |
| `name` | string | required | The Secret's name (a DNS subdomain). |
| `key` | string | required | The key holding the URL. Surrounding whitespace is trimmed. |

The URL reaches the Job as `RENOVATE_REDIS_URL`, Renovate's datasource
lookup cache. Without it every run starts with a cold cache.

### redis_url and redis_password_secret_ref

For a Secret that holds only the password, as the chart's
`redis.auth.existingSecret` does:

```hcl
redis_url = "redis://renovate@boopd-redis.boopd.svc:6379"

redis_password_secret_ref {
  name = "boopd-redis-password"
  key  = "password"
}
```

The worker reads the password at start and sets it in the URL, escaped,
so any password works. A user in `redis_url` is kept. `redis_url` must not
carry a password itself, cannot be combined with `redis_secret_ref`, and
can be set alone for a Redis without auth. The password ref has the same
`name` and `key` rules as `redis_secret_ref`.

### What a run's RENOVATE_CONFIG holds

Merged in this order, later entries winning:

1. `renovate.global`.
2. The run's profile `renovate` block, after inheritance.
3. `renovate.dry_run`, if set.
4. `renovate.shared_preset`, prepended to `extends`.

The repository's own config file then applies on top, as Renovate always
does.

## runs

| Setting | Type | Default | Description |
| - | - | - | - |
| `namespace` | string | required | Where Jobs and their token Secrets are created (a DNS label). The worker's Role must cover it; the chart's does for the namespace it renders. |
| `pending_timeout` | duration | `10m` | How long a run's pod may take to reach `Running`. Past it the Job is deleted and the attempt counts as pending (`boopd_run_pending_timeouts_total`); the repository tries again later. |
| `max_concurrent` | number | `0` (no cap) | The cluster-wide cap on concurrent runs across installations (DESIGN-0001 OQ12). A run takes a lease from the `CapacityWorkflow` after its installation's budget lease and before a token is minted or a Job created. When the cap is full it gives the installation lease back and waits, 1m doubling to 30m. The chart also renders a ResourceQuota on `pods` from it as a backstop. A pod past the quota is refused, and its Job waits `pending_timeout`. |

## profile

```hcl
profile "python" {
  inherit = "baseline"
  pod { … }
  renovate { … }
}
```

The label is the profile's name, unique across the file. Every profile
must appear in `order`.

| Setting | Type | Default | Description |
| - | - | - | - |
| `inherit` | string | none | A profile whose `pod` and `renovate` this one starts from. |
| `pod` | block | none | Overrides for the run's pod. |
| `renovate` | block | none | Renovate options for runs on this profile, with the same rules as [`global`](#global). |

### pod

| Setting | Type | Default | Description |
| - | - | - | - |
| `runtime_class_name` | string | none | The pod's RuntimeClass, e.g. a sandboxing runtime (gVisor, Kata) for the Python profile (DESIGN-0001 OQ9). |
| `labels` | map of strings | none | Pod labels, e.g. `boopd.dev/egress` to select one of the chart's NetworkPolicies. |
| `annotations` | map of strings | none | Pod annotations. |
| `node_selector` | map of strings | none | The pod's node selector. |
| `termination_grace_period_seconds` | number | Kubernetes' | Grace period on cancellation; not negative. |
| `work_size_limit` | quantity | none | Size limit of the run's work `emptyDir`, which holds Renovate's base and cache directories. |
| `resources` | block | none | `requests` and `limits`, each a map of quantities: `{ cpu = "1", memory = "2Gi" }`. |
| `toleration` | block, repeated | none | One toleration each: `key`, `operator`, `value`, `effect`, `toleration_seconds`, as in the pod spec. |

### Inheritance

A child's settings lay over its parent's, which may itself inherit:

- `labels`, `annotations`, `node_selector` and each of `resources.requests`
  and `resources.limits` merge key by key.
- `runtime_class_name`, `termination_grace_period_seconds` and
  `work_size_limit`: the child's value replaces the parent's.
- `toleration`: the child's list, if it has any, replaces the parent's.
- `renovate` options merge deeply. Nested objects merge; any other value in
  the child replaces the parent's.

A cycle is an error.

## Choosing a profile

The profile is chosen before each run's Job exists (DESIGN-0001 OQ1), from
two sources:

1. **`extends`** from the repository's config file, read at discovery. If
   `extends` is empty, the profile is `unknown_profile`. If no rule
   matches, it is `default_profile`. Otherwise it is the strictest
   matching rule's profile.
2. **Managers** from the repository's last Renovate report. The strictest
   rule naming one of them applies.

The run uses the stricter of the two, so managers only ever tighten a
profile. "Strictest" means latest in `order`.

| Setting | Type | Default | Description |
| - | - | - | - |
| `order` | list of strings | required | Every profile exactly once, least strict first. |
| `default_profile` | string | required | A declared profile. |
| `unknown_profile` | string | required | A declared profile; usually the strictest, since nothing is known yet. |

### profile_rule

```hcl
profile_rule {
  extends  = ["github>boop-bot/renovate-config:python"]
  managers = ["pip_requirements", "poetry", "pep621"]
  profile  = "python"
}
```

| Setting | Type | Default | Description |
| - | - | - | - |
| `extends` | list of strings | none | Matches when any entry is a substring of any of the repository's `extends`. |
| `managers` | list of strings | none | Matches when the last report used any of these Renovate managers, by exact name. |
| `profile` | string | required | The profile this rule selects. |

A rule needs `extends`, `managers` or both. Every matching rule counts.

## app

```hcl
app "boop-bot" {
  app_id = 123456
  private_key_secret_ref { … }
  discovery { … }
  budget { … }
}
```

The label names the App in logs, metrics and the discovery Schedule
(`discovery/<name>`). Names and App ids are unique across the file.

| Setting | Type | Default | Description |
| - | - | - | - |
| `endpoint` | string | `https://api.github.com` | The API base URL; must be https. |
| `app_id` | number | required | The GitHub App's id. |
| `installations` | list of numbers | every installation | An allowlist of installation ids. An empty list means every installation. |
| `cadence` | duration | `24h` | How often each repository runs. |
| `private_key_secret_ref` | block | required | The Secret holding the App's PEM private key. |
| `discovery` | block | defaults | How repositories are found. |
| `budget` | block | defaults | How runs are admitted against the installation's rate limits. |

### private_key_secret_ref

`name` (a DNS subdomain) and `key`, both required. Only the worker mounts
this Secret; Job pods never see it (ADR-0009).

### discovery

| Setting | Type | Default | Description |
| - | - | - | - |
| `every` | duration | `6h` | The discovery Schedule's interval. A repository unseen for 3 × `every` is checked and, if it is gone or has no config file, offboarded. |
| `probe` | string | `graphql` | How config files are found. `graphql` asks for every `config_paths` entry for 100 repositories in one query. `rest` lists the root and `.github` per repository, at most three calls each; it is the fallback. |
| `skip_forks` | bool | `true` | Skip forked repositories. |
| `skip_archived` | bool | `true` | Skip archived repositories. |

Each probed repository counts in
`boopd_discovery_repos_total{installation,config}` as `present`, `absent`
or `unparsed`. A config file that does not parse is still onboarded,
without its `extends`, and logged at warn.

### budget

| Setting | Type | Default | Description |
| - | - | - | - |
| `reserve_fraction` | number | `0.10` | Share of each rate-limit resource held back from runs, so discovery and other API use always have budget. 0 to 0.5. |
| `max_concurrent_runs` | number | `10` | Concurrent runs per installation, a guard against GitHub's secondary limits, which `/rate_limit` does not show (DESIGN-0001 OQ5). 0 means the budget alone decides. |
| `default_estimate` | map of numbers | `{ core = 300, graphql = 150 }` | Requests per run assumed for each resource until measured runs replace the estimate (DESIGN-0001 OQ6). Keys are `core` and `graphql`; values are positive. Keys you omit keep their defaults. |

## Secrets

Secrets are files, never values in this file and never environment
variables:

- Each `*_secret_ref` names a Kubernetes Secret and key. The chart mounts
  it read-only at `<secrets_dir>/<name>/<key>`, in the worker only.
- The worker reads them once at start. A missing or empty file stops it,
  naming the path.
- `boopd config validate` does not read them.
- Read values print and log as `[redacted]`.

## Chart values

`.Values.boopd` mirrors the file in camelCase. Blocks become maps, and
labelled blocks become maps keyed by label:

| boopd.hcl | Chart value |
| - | - |
| `secrets_dir` | `boopd.secretsDir` |
| `renovate.*` | `boopd.renovate.*`: `image`, `configPaths`, `sharedPreset`, `logLevel`, `dryRun`, `gitAuthor`, `global`, `redisSecretRef` |
| `runs.*` | `boopd.runs.*`: `namespace`, `pendingTimeout`, `maxConcurrent` |
| `profile "<name>"` | `boopd.profiles.<name>`: `inherit`, `pod`, `renovate`; pod keys in camelCase, `toleration` blocks as `pod.tolerations` |
| `order`, `default_profile`, `unknown_profile` | `boopd.order`, `boopd.defaultProfile`, `boopd.unknownProfile` |
| `profile_rule` | `boopd.profileRules`, a list |
| `app "<name>"` | `boopd.apps.<name>`: `endpoint`, `appId`, `installations`, `cadence`, `privateKeySecretRef`, `discovery`, `budget` |

What the chart adds:

- **Empty values:** a key that is absent, null, `""` or an empty list is
  left out of the file, so boopd's default applies.
- **`runs.namespace`:** empty means the release namespace.
- **The `redis` subchart (on by default):**
  - `redisSecretRef` defaults to the subchart's `<release>-redis-auth`
    Secret;
  - with `redis.auth.existingSecret`, the file gets `redis_url` for the
    subchart's Service and `redis_password_secret_ref` for that Secret
    instead (the chart README's "GitOps installs");
  - `global.redisPrefix` defaults to the prefix its ACL user is limited to.
- **Chart defaults that differ from the file's:** `runs.maxConcurrent` is
  20, and the chart ships the four example profiles (`baseline`, `node`,
  `python`, `strict`).
- **Removed values:** a removed value fails the render with what to use
  instead. `boopd.renovate.configPath` became `configPaths` after v0.1.0.
  `resourceQuota.enabled` went after v0.2.1: the runs quota renders
  whenever `runs.maxConcurrent > 0`.

## Validation

Load fails, listing every problem, when:

- **renovate:**
  - `image` is not pinned by digest;
  - a `config_paths` entry is empty, absolute, unclean, outside the
    repository or repeated, or the list is longer than 16;
  - `shared_preset` names a `.json5` file, or a GitHub-hosted file that is
    not `.json`;
  - an option in `global` or a profile's `renovate` block is forbidden or
    null, or `customEnvVariables` is not a map of strings.
  - `redis_url` is not a `redis://` or `rediss://` URL with a host,
    carries a password, or is set with `redis_secret_ref`;
  - `redis_password_secret_ref` is set without `redis_url`.
- **Enum values:** `log_level`, `dry_run` or `discovery.probe` is outside
  its set.
- **Durations:** a duration does not parse or is not positive.
- **Secret refs:** a name is not a DNS subdomain, or a key is not a valid
  Secret key.
- **runs:** `namespace` is not a DNS label, or `max_concurrent` is
  negative.
- **Profiles:**
  - a profile is declared twice or inherits in a cycle;
  - `order` misses a profile or repeats one;
  - any profile reference (`order`, `default_profile`, `unknown_profile`,
    `inherit`, `profile_rule.profile`) names a profile not declared;
  - a quantity does not parse, or `termination_grace_period_seconds` is
    negative;
  - a `profile_rule` has neither `extends` nor `managers`.
- **Apps:**
  - there is no `app`;
  - two apps share a name or an `app_id`;
  - an `app_id` or installation id is not a positive whole number;
  - `endpoint` is not https.
- **budget:** `reserve_fraction` is outside 0 to 0.5, `max_concurrent_runs`
  is negative, or `default_estimate` has an unknown resource or a value
  that is not positive.
