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

# Typecheck the UI
[group('ui')]
ui-lint:
    cd ui && bun run typecheck

# Build the UI into ui/dist
[group('ui')]
ui-build:
    cd ui && bun run build

# Run the UI mockup over fixture data (http://localhost:5173)
[group('ui')]
ui-dev:
    cd ui && bun run dev

# Open the static sketch of the component candidates
[group('ui')]
ui-sketch:
    open ui/design/candidates.html
