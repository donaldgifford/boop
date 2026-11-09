#!/usr/bin/env bash
# IMPL-0006 task 1.4: does Renovate work with the sandbox's narrowed
# token (OQ5)? Mints an installation token with exactly that set for one
# fixture repository, runs the chart's pinned Renovate image against it
# locally, revokes the token and prints what Renovate managed to do.
#
#   scripts/sandbox-token-check.sh setup
#   APP_ID=5285106 APP_KEY=~/boop-bot.pem scripts/sandbox-token-check.sh run
#
# setup creates (or refreshes) $OWNER/boop-fixture-sandbox-token with an
# outdated npm dependency and an outdated action in
# .github/workflows/ci.yml. It has no Renovate config file on purpose, so
# boopd never onboards it and its full-permission token never touches
# the test; the run passes the config through RENOVATE_CONFIG.
#
# Environment:
#   OWNER            account or org (default donaldgifford)
#   APP_ID           the GitHub App's ID (run)
#   APP_KEY          path to the App's private key PEM (run); never copied
#   INSTALLATION_ID  default 170572643
#
# Needs gh (logged in), git, openssl, curl, jq and docker.
set -euo pipefail

OWNER="${OWNER:-donaldgifford}"
INSTALLATION_ID="${INSTALLATION_ID:-170572643}"
REPO="boop-fixture-sandbox-token"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PERMISSIONS='{"contents":"write","pull_requests":"write","issues":"write","checks":"write","statuses":"write","metadata":"read","vulnerability_alerts":"read"}'

die() { echo "error: $*" >&2; exit 1; }

setup() {
  local work url
  work="$(mktemp -d)"
  trap 'rm -rf "${work}"' RETURN
  if ! gh repo view "${OWNER}/${REPO}" >/dev/null 2>&1; then
    gh repo create "${OWNER}/${REPO}" --private \
      --description "boop IMPL-0006 1.4 fixture: narrowed sandbox token (no Renovate config on purpose)" >/dev/null
    echo "created ${OWNER}/${REPO}"
  fi
  git init -q -b main "${work}"
  url="https://github.com/${OWNER}/${REPO}.git"
  [[ "$(gh config get git_protocol 2>/dev/null)" == ssh ]] && url="git@github.com:${OWNER}/${REPO}.git"
  git -C "${work}" remote add origin "${url}"
  mkdir -p "${work}/.github/workflows"
  cat >"${work}/package.json" <<'EOF'
{
  "name": "boop-fixture-sandbox-token",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "lodash": "4.17.20"
  }
}
EOF
  cat >"${work}/.github/workflows/ci.yml" <<'EOF'
# boop fixture (IMPL-0006 1.4): Renovate's update of this action must be
# refused, because the sandbox token has no workflows permission.
name: ci
on: workflow_dispatch
jobs:
  noop:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3.0.0
EOF
  echo "# ${REPO}" >"${work}/README.md"
  git -C "${work}" add -A
  git -C "${work}" -c commit.gpgSign=false commit -qm "chore: boop fixture contents (IMPL-0006 1.4)"
  git -C "${work}" push -qf origin main
  echo "pushed ${OWNER}/${REPO}"
}

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# app_jwt prints a 9-minute App JWT signed with $APP_KEY.
app_jwt() {
  local now header payload sig
  now="$(date +%s)"
  header="$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)"
  payload="$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' "$((now - 60))" "$((now + 540))" "${APP_ID}" | b64url)"
  sig="$(printf '%s.%s' "${header}" "${payload}" | openssl dgst -sha256 -sign "${APP_KEY}" | b64url)"
  printf '%s.%s.%s' "${header}" "${payload}" "${sig}"
}

run() {
  [[ -n "${APP_ID:-}" && -r "${APP_KEY:-}" ]] || die "set APP_ID and APP_KEY (a readable PEM)"
  local repo_id image jwt resp token log
  repo_id="$(gh api "repos/${OWNER}/${REPO}" --jq .id)"
  image="$(sed -n 's/^    image: \(ghcr.io\/renovatebot\/renovate:.*\)$/\1/p' "${ROOT}/charts/boopd/values.yaml")"
  [[ -n "${image}" ]] || die "pinned Renovate image not found in charts/boopd/values.yaml"

  jwt="$(app_jwt)"
  resp="$(curl -fsS -X POST \
    -H "Authorization: Bearer ${jwt}" -H "Accept: application/vnd.github+json" \
    "https://api.github.com/app/installations/${INSTALLATION_ID}/access_tokens" \
    -d "{\"repository_ids\":[${repo_id}],\"permissions\":${PERMISSIONS}}")" \
    || die "mint refused: the App lacks part of the set, or APP_ID/APP_KEY are wrong"
  token="$(jq -r .token <<<"${resp}")"
  echo "minted; GitHub granted: $(jq -c .permissions <<<"${resp}")"
  revoke() {
    curl -fsS -o /dev/null -X DELETE -H "Authorization: Bearer ${token}" \
      https://api.github.com/installation/token && echo "token revoked"
  }
  trap revoke EXIT

  log="$(mktemp -t sandbox-token-check.XXXXXX.log)"
  echo "running ${image} against ${OWNER}/${REPO}; log: ${log}"
  docker run --rm \
    -e RENOVATE_PLATFORM=github \
    -e RENOVATE_TOKEN="${token}" \
    -e RENOVATE_REPOSITORIES="${OWNER}/${REPO}" \
    -e RENOVATE_ONBOARDING=false \
    -e RENOVATE_REQUIRE_CONFIG=optional \
    -e RENOVATE_BINARY_SOURCE=global \
    -e RENOVATE_GIT_AUTHOR="boop-bot <boop-bot@users.noreply.github.com>" \
    -e RENOVATE_CONFIG='{"dependencyDashboard":true,"minimumReleaseAge":"3 days","internalChecksFilter":"none","prHourlyLimit":0,"prConcurrentLimit":0,"schedule":["at any time"]}' \
    -e LOG_LEVEL=debug -e LOG_FORMAT=json \
    "${image}" >"${log}" 2>&1 || echo "renovate exited non-zero (expected if the workflow push failed)"

  echo
  echo "== Renovate warnings and errors"
  jq -r 'select(.level >= 40) | "\(.level) \(.msg) \(.err.message // .error // "" | tostring | .[0:200])"' "${log}" | sort | uniq -c
  echo "== Status writes (renovate/stability-days)"
  jq -r 'select(.msg | test("[Ss]tatus")) | select(.context // .branchName // "" | tostring | length > 0) | "\(.msg) \(.context // "") \(.state // "")"' "${log}" | sort -u | head
  echo "== Branches"
  gh api "repos/${OWNER}/${REPO}/branches" --jq '.[].name'
  echo "== Pull requests"
  gh api "repos/${OWNER}/${REPO}/pulls?state=all" --jq '.[] | "#\(.number) \(.head.ref) \(.title)"'
  echo "== Issues (dependency dashboard)"
  gh api "repos/${OWNER}/${REPO}/issues?state=all" --jq '.[] | select(.pull_request | not) | "#\(.number) \(.title) updated \(.updated_at)"'
  echo "== Commit statuses on each renovate/ branch"
  gh api "repos/${OWNER}/${REPO}/branches" --jq '.[].name | select(startswith("renovate/"))' |
    while read -r b; do
      echo "${b}: $(gh api "repos/${OWNER}/${REPO}/commits/${b}/statuses" --jq '[.[] | "\(.context)=\(.state)"] | unique | join(", ")')"
    done
}

case "${1:-}" in
  setup) setup ;;
  run) run ;;
  *) die "usage: $0 setup|run" ;;
esac
