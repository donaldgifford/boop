#!/usr/bin/env bash
# Creates or refreshes the IMPL-0001 Phase 8 fixture repositories (task
# 8.1) under $OWNER, each prefixed boop-fixture-. Re-running pushes the
# current contents again, so the fixtures can always be rebuilt from
# this file.
#
#   OWNER=donaldgifford scripts/phase8-fixtures.sh
#
# Environment:
#   OWNER            account or org (default donaldgifford)
#   PRESET           shared preset (default github>$OWNER/renovate-config)
#   VISIBILITY       private or public (default private); the sdist
#                    package is always public, since pip clones it with
#                    no credentials
#
# An installation on selected repositories needs the four onboarded
# fixtures added by hand (the App's installation settings): gh's OAuth
# token cannot call the installation repositories API.
#
# Needs gh (logged in, repo scope), git and npm.
# The probes and JSON5 are literal text for the fixture repositories.
# shellcheck disable=SC2016
set -euo pipefail

OWNER="${OWNER:-donaldgifford}"
PRESET="${PRESET:-github>${OWNER}/renovate-config}"
VISIBILITY="${VISIBILITY:-private}"
PREFIX="boop-fixture-"
SDIST_REPO="${PREFIX}sdist-package"

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# checkout clones $1 into $work/$1, creating it first if it is missing.
checkout() {
  local name="$1" visibility="$2" desc="$3"
  if ! gh repo view "${OWNER}/${name}" >/dev/null 2>&1; then
    gh repo create "${OWNER}/${name}" "--${visibility}" --description "${desc}" >/dev/null
    echo "created ${OWNER}/${name}"
  fi
  rm -rf "${work:?}/${name}"
  git init -q -b main "${work}/${name}"
  local url="https://github.com/${OWNER}/${name}.git"
  if [[ "$(gh config get git_protocol 2>/dev/null)" == ssh ]]; then
    url="git@github.com:${OWNER}/${name}.git"
  fi
  git -C "${work}/${name}" remote add origin "${url}"
  if git -C "${work}/${name}" fetch -q origin main 2>/dev/null; then
    git -C "${work}/${name}" reset -q --hard origin/main
  fi
  # Start from an empty tree so removed files go away.
  git -C "${work}/${name}" rm -rq --ignore-unmatch . >/dev/null
}

# publish commits and pushes $work/$1 when anything changed.
publish() {
  local dir="${work}/$1"
  git -C "${dir}" add -A
  if git -C "${dir}" diff --cached --quiet; then
    echo "unchanged ${OWNER}/$1"
  else
    git -C "${dir}" commit -qm "chore: boop fixture contents (IMPL-0001 8.1)"
    git -C "${dir}" push -q origin main
    echo "pushed ${OWNER}/$1"
  fi
}

# renovate_config writes $1/renovate.json5: the preset, the preset's
# files named in $3..., and $2 as extra top-level JSON5 lines.
renovate_config() {
  local dir="$1" extra="$2"
  shift 2
  {
    echo '{'
    echo '  $schema: "https://docs.renovatebot.com/renovate-schema.json",'
    echo '  extends: ['
    echo "    \"${PRESET}\","
    local p
    for p in "$@"; do echo "    \"${PRESET}:${p}\","; done
    echo '  ],'
    echo '  // Fixtures run whenever boopd schedules them.'
    echo '  schedule: ["at any time"],'
    [[ -n "${extra}" ]] && printf '%s\n' "${extra}"
    echo '}'
  } >"${dir}/renovate.json5"
}

# gradle_wrapper writes an outdated Gradle wrapper whose gradlew is the
# probe in $2. Renovate's gradle-wrapper artifact update runs ./gradlew,
# so the probe runs as repository code inside the Job; it needs no JVM
# or Gradle download to report.
gradle_wrapper() {
  local dir="$1" probe="$2"
  mkdir -p "${dir}/gradle/wrapper"
  cat >"${dir}/gradle/wrapper/gradle-wrapper.properties" <<'EOF'
distributionBase=GRADLE_USER_HOME
distributionPath=wrapper/dists
distributionUrl=https\://services.gradle.org/distributions/gradle-7.6-bin.zip
zipStoreBase=GRADLE_USER_HOME
zipStorePath=wrapper/dists
EOF
  echo "rootProject.name = '$(basename "${dir}")'" >"${dir}/settings.gradle"
  printf '%s\n' "${probe}" >"${dir}/gradlew"
  chmod +x "${dir}/gradlew"
}

# Shared prologue for both probes: where Renovate's cacheDir and baseDir
# are, from the env boopd sets or the inherited RENOVATE_CONFIG.
read -r -d '' PROBE_PATHS <<'EOF' || true
cache="${RENOVATE_CACHE_DIR:-}"
if [ -z "$cache" ] && [ -n "${RENOVATE_CONFIG:-}" ]; then
  cache=$(printf '%s' "$RENOVATE_CONFIG" | sed -n 's/.*"cacheDir" *: *"\([^"]*\)".*/\1/p')
fi
base="${RENOVATE_BASE_DIR:-$(dirname "$(pwd)")}"
out=$(mktemp)
say() { printf '%s\n' "$*" >>"$out"; }
say "== boop fixture $(basename "$(pwd)") at $(date -u +%FT%TZ)"
say "cacheDir=$cache baseDir=$base pwd=$(pwd)"
EOF

ISOLATION_PROBE="#!/bin/sh
# boop fixture probe (IMPL-0001 8.1 isolation). Not a Gradle wrapper.
${PROBE_PATHS}"'
for d in "$cache" "$base" /tmp; do
  [ -n "$d" ] || continue
  if printf "written by isolation\n" >"$d/boopd-marker" 2>/dev/null; then
    say "marker written: $d/boopd-marker"
  else
    say "marker refused: $d"
  fi
done
say "== inherited environment"
env | sort >>"$out"
say "== other processes (environ, cmdline)"
for p in /proc/[0-9]*; do
  [ "$p" = "/proc/$$" ] && continue
  cmd=$(tr "\0" " " <"$p/cmdline" 2>/dev/null) || continue
  say "$p cmdline: $cmd"
  if tr "\0" "\n" <"$p/environ" 2>/dev/null | grep -E "TOKEN|KEY|SECRET|PASSWORD|redis://|ghs_" >/dev/null; then
    say "$p environ readable, secret-looking names:"
    tr "\0" "\n" <"$p/environ" | grep -oE "^[A-Z0-9_]*(TOKEN|KEY|SECRET|PASSWORD)[A-Z0-9_]*=|redis://|ghs_" >>"$out"
  fi
done
say "== files with key material (5 min cap)"
timeout 300 grep -rIlsE "BEGIN .*PRIVATE KEY|ghs_|redis://" / \
  --exclude-dir=proc --exclude-dir=sys --exclude-dir=dev 2>/dev/null | head -100 >>"$out"
say "== background process"
nohup sleep 3600 >/dev/null 2>&1 &
say "started sleep 3600 as pid $!"
cat "$out" >&2
exit 1'

ISOLATION_PROBE_PROBE="#!/bin/sh
# boop fixture probe (IMPL-0001 8.1 isolation-probe), run after
# boop-fixture-isolation. Not a Gradle wrapper.
${PROBE_PATHS}"'
say "== markers left by boop-fixture-isolation"
for d in "$cache" "$base" /tmp; do
  [ -n "$d" ] || continue
  find "$d" -maxdepth 6 -name "boopd-marker*" 2>/dev/null >>"$out"
done
say "== sleep processes visible"
for p in /proc/[0-9]*; do
  cmd=$(tr "\0" " " <"$p/cmdline" 2>/dev/null) || continue
  case "$cmd" in *"sleep 3600"*) say "$p: $cmd" ;; esac
done
say "== end"
cat "$out" >&2
exit 1'

# --- isolation -------------------------------------------------------
name="${PREFIX}isolation"
checkout "${name}" "${VISIBILITY}" "boop Phase 8 fixture: isolation probe (writes markers, reads env)"
renovate_config "${work}/${name}" '  enabledManagers: ["gradle-wrapper"],'
gradle_wrapper "${work}/${name}" "${ISOLATION_PROBE}"
publish "${name}"

# --- isolation-probe -------------------------------------------------
name="${PREFIX}isolation-probe"
checkout "${name}" "${VISIBILITY}" "boop Phase 8 fixture: looks for isolation's markers"
renovate_config "${work}/${name}" '  enabledManagers: ["gradle-wrapper"],'
gradle_wrapper "${work}/${name}" "${ISOLATION_PROBE_PROBE}"
publish "${name}"

# --- sdist package (dependency of python-sdist; not onboarded) -------
name="${SDIST_REPO}"
checkout "${name}" public "boop Phase 8 fixture: sdist-only package whose setup.py writes a marker"
cat >"${work}/${name}/setup.py" <<'EOF'
# boop fixture (IMPL-0001 8.1): building this sdist runs repository code.
import sys

with open("/tmp/boopd-sdist-marker", "w") as f:
    f.write("setup.py ran\n")
print("BOOPD-SDIST-MARKER: setup.py ran and wrote /tmp/boopd-sdist-marker", file=sys.stderr)

from setuptools import setup

setup(name="boop-fixture-sdist", version="0.1.0", py_modules=[])
EOF
echo "No wheel is ever published for this package, on purpose." >"${work}/${name}/README.md"
publish "${name}"
# A lightweight tag: pip only needs the ref, and tag.gpgSign would open
# an editor for a message.
git -C "${work}/${name}" -c tag.gpgSign=false tag -f v0.1.0 >/dev/null
git -C "${work}/${name}" push -qf origin v0.1.0

# --- python-sdist ----------------------------------------------------
# pip-compile resolves every requirement, so updating six makes it build
# the git dependency from source, so setup.py runs. PIP_ONLY_BINARY does
# not stop it (IMPL-0001 8.8); only the sandbox contains it.
name="${PREFIX}python-sdist"
checkout "${name}" "${VISIBILITY}" "boop Phase 8 fixture: sdist-only dependency under the python profile"
renovate_config "${work}/${name}" '  "pip-compile": {
    managerFilePatterns: ["/(^|/)requirements\\.txt$/"],
  },' python
sdist_url="git+https://github.com/${OWNER}/${SDIST_REPO}@v0.1.0"
cat >"${work}/${name}/requirements.in" <<EOF
six==1.15.0
boop-fixture-sdist @ ${sdist_url}
EOF
cat >"${work}/${name}/requirements.txt" <<EOF
#
# This file is autogenerated by pip-compile with Python 3.12
# by the following command:
#
#    pip-compile requirements.in
#
boop-fixture-sdist @ ${sdist_url}
    # via -r requirements.in
six==1.15.0
    # via -r requirements.in
EOF
publish "${name}"

# --- many-updates ----------------------------------------------------
# About fifty dependencies at old majors and no PR limits, so one run
# has far more work than a two-minute soft deadline allows.
name="${PREFIX}many-updates"
checkout "${name}" "${VISIBILITY}" "boop Phase 8 fixture: many updates for the convergence run"
renovate_config "${work}/${name}" '  prConcurrentLimit: 0,
  prHourlyLimit: 0,
  branchConcurrentLimit: 0,' node
cat >"${work}/${name}/package.json" <<'EOF'
{
  "name": "boop-fixture-many-updates",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "ajv": "6.12.6",
    "axios": "0.18.1",
    "body-parser": "1.19.0",
    "bootstrap": "3.4.1",
    "chalk": "1.1.3",
    "cheerio": "0.22.0",
    "commander": "2.20.3",
    "date-fns": "1.30.1",
    "debug": "2.6.9",
    "dotenv": "6.2.0",
    "execa": "1.0.0",
    "express": "3.21.2",
    "glob": "6.0.4",
    "got": "9.6.0",
    "graphql": "14.7.0",
    "helmet": "3.23.3",
    "immutable": "3.8.2",
    "inquirer": "6.5.2",
    "joi": "14.3.1",
    "jquery": "2.2.4",
    "jsonwebtoken": "8.5.1",
    "lodash": "3.10.1",
    "marked": "0.8.2",
    "mkdirp": "0.5.6",
    "mongoose": "4.13.21",
    "mysql2": "1.7.0",
    "node-fetch": "1.7.3",
    "ora": "3.4.0",
    "p-limit": "2.3.0",
    "pg": "6.4.2",
    "pug": "2.0.4",
    "react": "15.6.2",
    "react-dom": "15.6.2",
    "react-redux": "5.1.2",
    "react-router": "3.2.6",
    "redis": "2.8.0",
    "redux": "3.7.2",
    "rimraf": "2.7.1",
    "rxjs": "5.5.12",
    "semver": "5.7.2",
    "socket.io": "2.5.0",
    "uuid": "3.4.0",
    "validator": "10.11.0",
    "vue": "2.6.14",
    "ws": "5.2.3",
    "yargs": "12.0.5"
  },
  "devDependencies": {
    "chai": "3.5.0",
    "concurrently": "4.1.2",
    "cross-env": "5.2.1",
    "eslint": "5.16.0",
    "husky": "1.3.1",
    "jest": "24.9.0",
    "lint-staged": "8.2.1",
    "mocha": "5.2.0",
    "nodemon": "1.19.4",
    "prettier": "1.19.1",
    "sinon": "7.5.0",
    "typescript": "3.9.10",
    "webpack": "3.12.0"
  }
}
EOF
# Old peers conflict once single packages move; Renovate reads .npmrc.
echo "legacy-peer-deps=true" >"${work}/${name}/.npmrc"
(cd "${work}/${name}" && npm install --package-lock-only --ignore-scripts --no-audit --no-fund >/dev/null)
publish "${name}"

echo "done: run isolation before isolation-probe; ${SDIST_REPO} is not onboarded (no renovate.json5)"
