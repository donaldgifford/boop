# UI recipes (boopd-ui). Imported by the root justfile.
#
# @donaldgifford/design-system installs from GitHub Packages, which wants a
# token even for a public package. NPM_TOKEN is used when set, else the gh
# CLI's token, which needs the read:packages scope
# (`gh auth refresh -s read:packages`).

# Install the UI's dependencies. The token is read in the shell, so just
# never prints it.
[group('ui')]
ui-install:
    @cd ui && NPM_TOKEN="${NPM_TOKEN:-$(gh auth token)}" bun install --frozen-lockfile

# Typecheck and lint the UI, and run the API-drift and colour-token gates
[group('ui')]
ui-lint:
    cd ui && bun run typecheck && bun run lint && bun run check:api && bun run check:tokens

# Run the UI's unit tests (the BFF and the SPA) with bun test
[group('ui')]
ui-test:
    cd ui && bun run test

# Build the UI into ui/dist and check it is CSP-clean (no data: URL, no
# Google Fonts URL)
[group('ui')]
ui-build:
    cd ui && bun run check:dist

# Run the UI mockup over fixture data (http://localhost:5173)
[group('ui')]
ui-dev:
    cd ui && bun run dev

# Run the UI's Playwright flows (ui/e2e) against a running stack
[group('ui')]
ui-e2e:
    cd ui && bun run e2e

# Run the UI's visual comparisons; pass --update-snapshots to refresh
[group('ui')]
ui-visual *args:
    cd ui && bun run visual {{args}}

# Generate the UI's API types (ui/web/src/api/schema.gen.ts) from api/openapi.yaml
[group('generate')]
generate-ui-api:
    cd ui && bun run gen:api

# Fail when the UI's API types drift from api/openapi.yaml
[group('ui')]
ui-check-api:
    cd ui && bun run check:api

# Build the boopd-ui image into the local Docker daemon (bake ui-dev). The
# registry token is passed as a build secret and never lands in a layer.
[group('ui')]
ui-docker-build:
    @NPM_TOKEN="${NPM_TOKEN:-$(gh auth token)}" docker buildx bake ui-dev
