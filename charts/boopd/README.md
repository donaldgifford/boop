# boopd

![Version: 0.0.0-dev](https://img.shields.io/badge/Version-0.0.0--dev-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.0-dev](https://img.shields.io/badge/AppVersion-0.0.0--dev-informational?style=flat-square)

boopd runs Renovate per repository with Temporal as the control plane

## Installing

The chart is published as an OCI artifact to GHCR alongside the
container image:

```sh
helm install boopd oci://ghcr.io/donaldgifford/charts/boopd
```

Render locally against the checked-out chart:

```sh
helm template boopd charts/boopd
```

## Configuration

App configuration flows to the container as environment variables:

- The chart manages `LISTEN_ADDR`, `METRICS_ADDR`, `LOG_LEVEL`, and
  `POD_NAME` from the `config` values.
- `configMap.data` and `secrets.stringData` are projected into a
  chart-managed ConfigMap/Secret and injected via `envFrom`.
- `extraEnv` appends raw env entries. Collisions with the
  chart-managed names fail the render.

Optional monitoring integrations (`serviceMonitor.enabled`,
`prometheusRule.enabled`) are off by default and require the
Prometheus Operator CRDs in the cluster.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules |
| args | list | `["worker","--config","/etc/boopd/boopd.hcl"]` | Container args. The default runs the worker role against the chart-rendered config file. |
| command | list | `[]` | Override the container command (default: image entrypoint) |
| config.logLevel | string | `"info"` | Log level (published as LOG_LEVEL) |
| config.metricsPort | int | `9090` | Container metrics port (published as METRICS_ADDR) |
| config.port | int | `8080` | Container HTTP port (published to the container as LISTEN_ADDR) |
| configMap.data | object | `{}` | Key/value pairs projected into a chart-managed ConfigMap and injected into the container via envFrom. Keys must not collide with the chart-managed env vars (LISTEN_ADDR, METRICS_ADDR, LOG_LEVEL, POD_NAME) — the render fails fast on collisions. |
| configMap.existingConfigMap | string | `""` | Use an existing ConfigMap for envFrom instead of rendering one |
| extraEnv | list | `[]` | Additional environment variables (must not collide with the chart-managed env vars — render fails fast) |
| extraVolumeMounts | list | `[]` | Additional volume mounts |
| extraVolumes | list | `[]` | Additional volumes |
| fullnameOverride | string | `""` | Override the full release name |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/donaldgifford/boopd"` | Container image repository |
| image.tag | string | `""` | Overrides the image tag (default: appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets |
| livenessProbe.httpGet.path | string | `"/healthz"` |  |
| livenessProbe.httpGet.port | string | `"http"` |  |
| livenessProbe.initialDelaySeconds | int | `5` |  |
| livenessProbe.periodSeconds | int | `15` |  |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector |
| podAnnotations | object | `{}` | Pod annotations |
| podLabels | object | `{}` | Pod labels |
| podSecurityContext | object | `{}` | Pod security context |
| prometheusRule.alerts | object | `{}` | Per-alert overrides: keys `replicasUnavailable` and `containerRestarting`, each accepting `enabled`, `for`, `severity`, and `threshold`. |
| prometheusRule.enabled | bool | `false` | Create PrometheusRule with the generic starter alerts (DeploymentReplicasUnavailable, ContainerRestarting). |
| prometheusRule.labels | object | `{}` | Additional labels (e.g., to match Prometheus operator `ruleSelector`). |
| readinessProbe.httpGet.path | string | `"/readyz"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| replicaCount | int | `2` | Worker replicas. A fixed, small count (ADR-0008 as amended by ADR-0009): runs are Jobs, so the worker never scales with load. |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Container resource requests and limits |
| revisionHistoryLimit | int | `3` | Number of old ReplicaSets retained for rollback. Defaults to 3 to keep the kubectl `get rs` view tidy; bump if you need more rollback headroom. Kubernetes default is 10. |
| secrets.create | bool | `true` | Create the Secret resource (false = use existingSecret) |
| secrets.existingSecret | string | `""` | Name of an existing Secret for envFrom (when create=false) |
| secrets.stringData | object | `{}` | Key/value pairs projected into the chart-managed Secret (stringData) and injected via envFrom. Same collision guard as configMap.data. |
| securityContext | object | `{"readOnlyRootFilesystem":true,"runAsNonRoot":true,"runAsUser":65532}` | Container security context (65532 = distroless nonroot uid) |
| service.httpPort | int | `80` | HTTP port |
| service.metricsPort | int | `9090` | Metrics port |
| service.type | string | `"ClusterIP"` | Service type |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount |
| serviceAccount.create | bool | `true` | Create a ServiceAccount |
| serviceAccount.name | string | `""` | Override the ServiceAccount name |
| serviceMonitor.enabled | bool | `false` | Create Prometheus ServiceMonitor |
| serviceMonitor.interval | string | `"30s"` | Scrape interval |
| serviceMonitor.labels | object | `{}` | Additional labels for ServiceMonitor |
| terminationGracePeriodSeconds | int | `3600` | Seconds Kubernetes waits after SIGTERM. Must exceed the RunRenovate StartToClose (58m) so a draining worker lets in-flight runs reach their soft deadlines (DESIGN-0001 § Worker shutdown). |
| tolerations | list | `[]` | Tolerations |

## Development

Chart linting, unit tests, and docs generation are wired into the
repo task runner:

```sh
just helm-lint       # helm lint + ct lint
just helm-unittest   # helm-unittest suite in tests/
just helm-docs       # regenerate this README from README.md.gotmpl
```

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
