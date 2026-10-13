#!/usr/bin/env bash
# Check that release.yml's SLSA jobs name the image and chart the
# release pushes. Reusable-workflow inputs cannot read env, so the
# references are spelled out and can drift from IMAGE_REPO,
# CHART_NAMESPACE and CHART_NAME (issue #8).
set -euo pipefail

workflow=".github/workflows/release.yml"
chart="charts/boopd/Chart.yaml"
# shellcheck disable=SC2016 # the literal GitHub expression, not a shell one
owner='${{ github.repository_owner }}'

env_value() {
  yq -r ".env.$1" "${workflow}"
}

registry="$(env_value REGISTRY)"
image_repo="$(env_value IMAGE_REPO)"
chart_namespace="$(env_value CHART_NAMESPACE)"
chart_name="$(env_value CHART_NAME)"

check() {
  local job="$1" want="$2" got
  got="$(yq -r ".jobs.\"${job}\".with.image" "${workflow}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "${workflow}: jobs.${job}.with.image is ${got}, want ${want}" >&2
    return 1
  fi
}

status=0
check image-slsa "${registry}/${image_repo}" || status=1
check chart-slsa "${registry}/${chart_namespace}/${chart_name}" || status=1

if [[ "$(yq -r .name "${chart}")" != "${chart_name}" ]]; then
  echo "${chart}: name is not CHART_NAME (${chart_name})" >&2
  status=1
fi

# Both references must start with the owner expression, not a literal.
case "${image_repo}" in
  "${owner}"/*) ;;
  *) echo "${workflow}: IMAGE_REPO does not start with ${owner}" >&2; status=1 ;;
esac

exit "${status}"
