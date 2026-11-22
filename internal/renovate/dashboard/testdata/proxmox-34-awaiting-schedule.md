This issue lists Renovate updates and detected dependencies. Read the [Dependency Dashboard](https://docs.renovatebot.com/key-concepts/dashboard/) docs to learn more.

## Awaiting Schedule

The following updates are awaiting their schedule. To get an update now, click on a checkbox below.

 - [ ] <!-- unschedule-branch=renovate/pin-dependencies -->chore(deps): pin dependencies (`actions/checkout`, `actions/labeler`, `actions/setup-go`, `actions/upload-artifact`, `aquasecurity/trivy-action`, `codecov/codecov-action`, `crazy-max/ghaction-import-gpg`, `docker/dockerfile`, `donaldgifford/govulncheck-action`, `gcr.io/distroless/static-debian12`, `github/codeql-action`, `golang`, `golangci/golangci-lint`, `golangci/golangci-lint-action`, `goreleaser/goreleaser-action`, `jdx/mise-action`, `jefflinse/pr-semver-bump`, `mheap/github-action-required-labels`, `trufflesecurity/trufflehog`)
 - [ ] <!-- unschedule-branch=renovate/github.com-pkg-sftp-1.x -->fix(deps): update module github.com/pkg/sftp to v1.13.11
 - [ ] <!-- unschedule-branch=renovate/docker-images-(non-major) -->chore(deps): update docker images (non-major) (`docker/dockerfile`, `golang`)
 - [ ] <!-- unschedule-branch=renovate/github-actions-(non-major) -->chore(deps): update github actions (non-major) (`actions/checkout`, `github/codeql-action`, `golangci/golangci-lint`, `goreleaser/goreleaser-action`, `step-security/harden-runner`, `trufflesecurity/trufflehog`)
 - [ ] <!-- unschedule-branch=renovate/mise-tools-(non-major) -->chore(deps): update mise tools (non-major) (`go`, `golangci-lint`, `markdownlint-cli2`, `prettier`, `yamllint`, `yq`)
 - [ ] <!-- unschedule-branch=renovate/golang.org-x-crypto-0.x -->fix(deps): update module golang.org/x/crypto to v0.58.0
 - [ ] <!-- unschedule-branch=renovate/actions-checkout-7.x -->chore(deps): update actions/checkout action to v7
 - [ ] <!-- unschedule-branch=renovate/actions-labeler-7.x -->chore(deps): update actions/labeler action to v7
 - [ ] <!-- unschedule-branch=renovate/actions-setup-go-7.x -->chore(deps): update actions/setup-go action to v7
 - [ ] <!-- unschedule-branch=renovate/codecov-codecov-action-7.x -->chore(deps): update codecov/codecov-action action to v7
 - [ ] <!-- unschedule-branch=renovate/crazy-max-ghaction-import-gpg-7.x -->chore(deps): update crazy-max/ghaction-import-gpg action to v7
 - [ ] <!-- unschedule-branch=renovate/jdx-mise-action-5.x -->chore(deps): update jdx/mise-action action to v5
 - [ ] <!-- create-all-awaiting-schedule-prs -->🔐 **Create all awaiting schedule PRs at once** 🔐

## Detected Dependencies

<details><summary>dockerfile (1)</summary>
<blockquote>

<details><summary>Dockerfile (3)</summary>

 - `docker/dockerfile 1.7` → [Updates: `1.28`, `1.7`]
 - `golang 1.26.4` → [Updates: `1.27.2`, `1.26.4`]
 - `gcr.io/distroless/static-debian12 nonroot` → [Updates: `nonroot`]

</details>

</blockquote>
</details>

<details><summary>github-actions (10)</summary>
<blockquote>

<details><summary>.github/workflows/changelog-regen.yml (3)</summary>

 - `step-security/harden-runner v2.19.3@ab7a9404c0f3da075243ca237b5fac12c98deaa5` → [Updates: `v2.22.1`]
 - `actions/checkout v6.0.2@de0fac2e4500dabe0009e67214ff5f5447ce83dd` → [Updates: `v6.1.0`, `v7.0.1`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]

</details>

<details><summary>.github/workflows/changelog.yml (2)</summary>

 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]

</details>

<details><summary>.github/workflows/ci.yml (21)</summary>

 - `actions/labeler v6` → [Updates: `v7`, `v6`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `golangci/golangci-lint-action v9` → [Updates: `v9`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]
 - `codecov/codecov-action v6` → [Updates: `v7`, `v6`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `donaldgifford/govulncheck-action v1` → [Updates: `v1`]
 - `aquasecurity/trivy-action v0.36.0` → [Updates: `v0.36.0`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]
 - `goreleaser/goreleaser-action v7.1.0` → [Updates: `v7.3.0`, `v7.1.0`]
 - `golangci/golangci-lint v2.11.4` → [Updates: `v2.15.0`, `v2.11.4`]
 - `goreleaser/goreleaser ~> v2`

</details>

<details><summary>.github/workflows/codeql.yml (5)</summary>

 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `github/codeql-action v4.35.5` → [Updates: `v4.39.0`, `v4.35.5`]
 - `github/codeql-action v4.35.5` → [Updates: `v4.39.0`, `v4.35.5`]
 - `github/codeql-action v4.35.5` → [Updates: `v4.39.0`, `v4.35.5`]

</details>

<details><summary>.github/workflows/dependabot-severity-label.yml</summary>


</details>

<details><summary>.github/workflows/license-check.yml (3)</summary>

 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `actions/upload-artifact v7` → [Updates: `v7`]

</details>

<details><summary>.github/workflows/pr-labels.yml (1)</summary>

 - `mheap/github-action-required-labels v5` → [Updates: `v5`]

</details>

<details><summary>.github/workflows/release.yml (8)</summary>

 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `jefflinse/pr-semver-bump v1.7.4` → [Updates: `v1.7.4`]
 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `actions/setup-go v6` → [Updates: `v7`, `v6`]
 - `jdx/mise-action v4` → [Updates: `v5`, `v4`]
 - `crazy-max/ghaction-import-gpg v6` → [Updates: `v7`, `v6`]
 - `goreleaser/goreleaser-action v7` → [Updates: `v7`]
 - `goreleaser/goreleaser ~> v2`

</details>

<details><summary>.github/workflows/security.yml (2)</summary>

 - `donaldgifford/govulncheck-action v1` → [Updates: `v1`]
 - `github/codeql-action v4.35.5` → [Updates: `v4.39.0`, `v4.35.5`]

</details>

<details><summary>.github/workflows/trufflehog.yml (2)</summary>

 - `actions/checkout v6` → [Updates: `v7`, `v6`]
 - `trufflesecurity/trufflehog v3.95.8` → [Updates: `v3.99.2`, `v3.95.8`]

</details>

</blockquote>
</details>

<details><summary>gomod (1)</summary>
<blockquote>

<details><summary>go.mod (6)</summary>

 - `go 1.26.4`
 - `github.com/joho/godotenv v1.5.1`
 - `github.com/pkg/sftp v1.13.10` → [Updates: `v1.13.11`]
 - `go.yaml.in/yaml/v4 v4.0.0-rc.6`
 - `golang.org/x/crypto v0.52.0` → [Updates: `v0.58.0`]
 - `gopkg.in/dnaeon/go-vcr.v4 v4.0.7`

</details>

</blockquote>
</details>

<details><summary>mise (1)</summary>
<blockquote>

<details><summary>mise.toml (7)</summary>

 - `go 1.26.4` → [Updates: `1.27.2`]
 - `markdownlint-cli2 0.18.1` → [Updates: `0.23.3`]
 - `yamlfmt 0.21.0`
 - `yamllint 1.37.1` → [Updates: `1.38.0`]
 - `prettier 3.7.4` → [Updates: `3.9.10`]
 - `yq 4.48.1` → [Updates: `4.54.1`]
 - `golangci-lint 2.11.4` → [Updates: `2.15.0`]

</details>

</blockquote>
</details>

<details><summary>renovate-config (1)</summary>
<blockquote>

<details><summary>renovate.json5</summary>


</details>

</blockquote>
</details>

