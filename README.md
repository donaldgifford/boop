# boop

Give your repo a little boop

A Go service deployed to Kubernetes via a first-party Helm chart. The
container image and the chart are published together as OCI artifacts on
every release.

## Getting started

The scaffold ships the repo machinery — the application code is yours to
add:

```sh
mise install        # toolchain: go, helm, ct, helm-docs, k3d, ...
just helm-plugins   # one-time: install the helm-unittest + helm-diff plugins
just helm-docs      # generate charts/boop/README.md
just build          # build/bin/boopd
```

The service is expected to honor the environment contract the chart
manages (see [Configuration](#configuration)) and serve `/healthz` and
`/readyz` on the listen port for the default probes.

## Everyday commands

Run `just` (or `just --list`) for the full recipe list. The usual loops:

```sh
just check          # lint + test
just build          # binary into build/bin/boopd
just docker-build   # local image via docker buildx bake
just helm-test      # chart lint + helm-unittest suites
just k3d-install    # dev image → local k3d cluster → helm install
```

## Configuration

App configuration reaches the container as environment variables,
managed by the chart in `charts/boop`:

- `config.port` / `config.metricsPort` / `config.logLevel` values map to
  `LISTEN_ADDR`, `METRICS_ADDR`, and `LOG_LEVEL`; `POD_NAME` is injected
  from the pod spec.
- `configMap.data` and `secrets.stringData` are projected into a
  chart-managed ConfigMap/Secret and injected via `envFrom`.
- `extraEnv` appends raw env entries; collisions with the chart-managed
  names fail the render.

See the chart README for the full values reference.

## Releases

Merging to `main` runs the release workflow: the merged PR's semver
label (`major`/`minor`/`patch`, or `dont-release` to skip) bumps the
version and pushes the tag. Everything releases in lockstep from that
tag — goreleaser publishes the GitHub Release with binary archives,
the multi-arch image is built and signed with cosign (plus SLSA
provenance), and the Helm chart is packaged at the tag-derived version
(`Chart.yaml` keeps a `0.0.0-dev` placeholder by design) and
is pushed to `oci://ghcr.io/donaldgifford/charts/boop`.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/boopd/` | Service entrypoint (`boopd worker`) |
| `charts/boop/` | Helm chart + unittest suites |
| `docker-bake.hcl` | Image build targets (local / ci / release) |
| `justfile`, `docker.just`, `helm.just` | Task runner recipes |
| `.github/workflows/` | CI and release pipelines |
