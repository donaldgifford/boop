# boop — Helm chart recipes
#
# Imported by the root justfile via `import? 'helm.just'` — recipes may
# reference variables defined there (project_name, build_dir) and
# settings (shell, etc.) live there.

chart_root    := "charts"
chart_name    := "boopd"
chart_dir     := chart_root / chart_name
chart_pkg_dir := build_dir / "charts"
helm_oci      := "oci://ghcr.io/donaldgifford/charts"

# ─── Helm ───────────────────────────────────────────────────────────

# One-time setup: install the helm-unittest + helm-diff plugins (idempotent)
[group('helm')]
helm-plugins:
    @helm plugin list | grep -q '^unittest' || \
        helm plugin install https://github.com/helm-unittest/helm-unittest
    @helm plugin list | grep -q '^diff' || \
        helm plugin install https://github.com/databus23/helm-diff
    @echo "✓ helm plugins ready"

# Lint the chart: helm lint + chart-testing lint
[group('helm')]
helm-lint:
    @helm lint {{ chart_dir }}
    @ct lint --config ct.yaml --all

# Render templates with default values
[group('helm')]
helm-template:
    @helm template {{ chart_name }} {{ chart_dir }}

# Render templates with CI values (busybox stand-in image, probes off)
[group('helm')]
helm-template-ci:
    @helm template {{ chart_name }} {{ chart_dir }} \
        -f {{ chart_dir }}/ci/ci-values.yaml

# Run the helm-unittest suites in tests/ (needs `just helm-plugins` once)
[group('helm')]
helm-unittest:
    @helm unittest {{ chart_dir }}

# Render boopd.hcl from the CI, golden and ingest values and run `boopd config validate` on it
[group('helm')]
helm-config-validate:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{ build_dir }}
    for v in {{ chart_dir }}/ci/ci-values.yaml {{ chart_dir }}/tests/values/example.yaml {{ chart_dir }}/tests/values/ingest.yaml; do
        out="{{ build_dir }}/$(basename "$v" .yaml).hcl"
        helm template {{ chart_name }} {{ chart_dir }} -f "$v" --show-only templates/config.yaml \
            | yq '.data["boopd.hcl"]' > "$out"
        go run ./cmd/boopd config validate "$out"
    done

# Render twice with an existing redis Secret and fail on any difference:
# a GitOps tool renders with `helm template`, where lookup is empty, so
# anything generated per render would roll pods on every sync (issue #10)
[group('helm')]
helm-template-stable:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{ build_dir }}
    for i in 1 2; do
        helm template {{ chart_name }} {{ chart_dir }} \
            -f {{ chart_dir }}/ci/ci-values.yaml \
            -f {{ chart_dir }}/tests/values/existing-secret.yaml > "{{ build_dir }}/stable-$i.yaml"
    done
    diff -u "{{ build_dir }}/stable-1.yaml" "{{ build_dir }}/stable-2.yaml"
    echo "helm template is stable"
    yq 'select(.kind == "ConfigMap" and .metadata.name == "{{ chart_name }}-config") | .data["boopd.hcl"]' \
        "{{ build_dir }}/stable-1.yaml" > "{{ build_dir }}/existing-secret.hcl"
    go run ./cmd/boopd config validate "{{ build_dir }}/existing-secret.hcl"

# Full chart gate: lint + unit tests + rendered config validation + stable render
[group('helm')]
helm-test: helm-lint helm-unittest helm-config-validate helm-template-stable

# Regenerate the chart README from README.md.gotmpl
[group('helm')]
helm-docs:
    @helm-docs --chart-search-root {{ chart_root }}

# Package the chart into build/charts/
[group('helm')]
helm-package:
    @mkdir -p {{ chart_pkg_dir }}
    @helm package {{ chart_dir }} -d {{ chart_pkg_dir }}

# Push the newest packaged chart to GHCR (CI does this on release)
[group('helm')]
helm-push: helm-package
    @helm push "$(ls -t {{ chart_pkg_dir }}/{{ chart_name }}-*.tgz | head -1)" {{ helm_oci }}

# Diff an installed release (default: the k3d dev-loop release) against the local chart
[group('helm')]
helm-diff-check release=chart_name:
    @helm diff upgrade {{ release }} {{ chart_dir }}

# ─── k3d dev loop ───────────────────────────────────────────────────

# Create the local k3d cluster (no-op if it already exists)
[group('k3d')]
k3d-up:
    @k3d cluster list {{ project_name }} >/dev/null 2>&1 || \
        k3d cluster create {{ project_name }} --wait

# Delete the local k3d cluster
[group('k3d')]
k3d-down:
    @k3d cluster delete {{ project_name }}

# The developer loop against real Renovate (IMPL-0001 task 7.9):
#   just k3d-install dev/values.yaml dev/boop-bot-dev.pem
# (just takes recipe arguments by position)
# app_key becomes the `boop-bot-app` Secret (key private-key.pem) the
# values' privateKeySecretRef names; values sets temporal.address,
# boopd.apps and boopd.renovate.dryRun: full. dev/ is gitignored.
# Build the dev image, import it into k3d, install/upgrade the release
[group('k3d')]
k3d-install values="" app_key="": k3d-up docker-build
    #!/usr/bin/env bash
    set -euo pipefail
    k3d image import ghcr.io/donaldgifford/boopd:dev --cluster {{ project_name }}
    if [[ -n "{{ app_key }}" ]]; then
        kubectl --context k3d-{{ project_name }} create secret generic boop-bot-app \
            --from-file=private-key.pem="{{ app_key }}" --dry-run=client -o yaml \
            | kubectl --context k3d-{{ project_name }} apply -f -
    fi
    helm upgrade --install {{ chart_name }} {{ chart_dir }} --kube-context k3d-{{ project_name }} \
        --set image.tag=dev --set image.pullPolicy=Never \
        {{ if values != "" { "-f " + values } else { "" } }}

# Uninstall the release from the k3d cluster
[group('k3d')]
k3d-uninstall:
    @helm uninstall {{ chart_name }} --kube-context k3d-{{ project_name }}
