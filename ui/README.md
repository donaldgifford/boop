# boop UI

The management UI (DESIGN-0004), built on
[`@donaldgifford/design-system`](https://github.com/donaldgifford/design-system).
repo-guardian's `ui/` is the example for how the package is used and for
the toolchain; nothing is copied from it (INV-0005).

**Today this is a mockup.** The pages render fixture data
(`web/src/fixtures.ts`). There is no BFF, no API client and no sign-in
yet; IMPL-0005 Phase 11 adds them.

```text
ui/
  web/                  React + Vite SPA
    src/shell/          the frame, composed from the package's shell parts
    src/views/          pages: Estate, Pull requests, one repository, Status
    src/components/     boop's own: state badges, page controls
    src/fixtures.ts     fixture data in the API's shapes
```

## Commands

| Recipe | Does |
| --- | --- |
| `just ui-install` | `bun install --frozen-lockfile`, with the registry token |
| `just ui-dev` | the mockup on `http://localhost:5173` |
| `just ui-lint` | typecheck |
| `just ui-build` | build into `ui/dist` |

Add `?apps=off` to any URL to see the sidebar header with no app list
configured.

## Registry token

The design system is published to GitHub Packages, which asks for a
token even to read a public package. `.npmrc` maps the `@donaldgifford`
scope there and holds only the reference `${NPM_TOKEN}`. `just
ui-install` uses `NPM_TOKEN` when it is set, else `gh auth token`, which
needs the `read:packages` scope (`gh auth refresh -s read:packages`).

## Components from the mockup

The components the first mockup drafted here (segmented control, option
rows, filter bar, inline states, stacked bars over time, the app
switcher) and the status page parts are in the package since 0.4.0. boop
keeps no local copy. A generic component the package lacks is drafted
here, seen working, and moved there (design-system RFC-0001).

## Gaps found while building the mockup

Each is an issue for the design system, not a patch here:

- **`Meter`** treats a high percentage as good. For budget spend, high is
  bad, so the mockup passes `tone` itself (`views/estate.tsx`).
- **`Timeline`** takes `when` as a string, so a timeline cannot use the
  package's `Time` and does not follow the Local/UTC toggle
  (`lib/when.ts`).
- **Dev server and CommonJS.** The package ships source, so Vite's dev
  server does not pre-bundle what it imports, and the CommonJS packages
  underneath fail to load (a blank page). `web/vite.config.ts` names
  them in `optimizeDeps.include`. The package's README should say this
  for every consumer.
- **`Button` as a link** needs `nativeButton={false}` beside `render`,
  or Base UI warns.
