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

# Typecheck and lint the UI
[group('ui')]
ui-lint:
    cd ui && bun run typecheck && bun run lint

# Run the UI's unit tests (the BFF and the SPA) with bun test
[group('ui')]
ui-test:
    cd ui && bun test

# Build the UI into ui/dist
[group('ui')]
ui-build:
    cd ui && bun run build

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
