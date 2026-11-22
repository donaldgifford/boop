# boop UI

The management UI (DESIGN-0004): a Bun + Hono BFF serving a React SPA
built on
[`@donaldgifford/design-system`](https://github.com/donaldgifford/design-system).
The BFF, the shell composition, the pages and the test harness are
written here; the look and the components come from the package
(INV-0005).

Every page reads the API through the BFF with TanStack Query
(`web/src/api/queries.ts`). Lists poll every 30 s, the event stream
every 15 s, and a repository with a run in progress every 5 s. Filters
live in the URL. The status page (`/status`) is public and sits outside
the shell.

```text
ui/
  server/               the BFF: OIDC code + PKCE, sealed session, /api proxy, static, health
    testing/            mock OIDC issuer, a cookie-keeping Browser, the local dev stack
  web/                  React + Vite SPA
    src/api/            generated types (schema.gen.ts), the openapi-fetch client, queries
    src/shell/          the frame from the package's parts, nav, breadcrumbs, palette, errors
    src/views/          pages
    src/components/     boop's own: badges, page controls, ListCard, update rows, waiting sections, run columns, events
    testing/            per-file DOM, the shell and app harnesses, sample payloads (data.ts)
  scripts/              gates: check-api, check-dist, check-tokens
  e2e/                  Playwright flows, axe and screenshots against a deployed stack (BOOP_UI_URL, BOOP_ISSUER_URL)
```

## Commands

| Recipe | Does |
| --- | --- |
| `just ui-install` | `bun install --frozen-lockfile`, with the registry token |
| `just ui-dev` | Vite on `http://localhost:5173`, proxying `/api`, `/auth`, `/ui` to the BFF on 3000 |
| `just ui-dev-stack` | the mock issuer (9400) and a stub API (9401) for the BFF to use locally |
| `just ui-lint` | typecheck, ESLint, `check-api`, `check-tokens` |
| `just ui-test` | `bun test` over the BFF, the SPA and the gates |
| `just ui-build` | build into `ui/dist` and `check-dist` it |
| `just generate-ui-api` | regenerate `web/src/api/schema.gen.ts` from `api/openapi.yaml` |
| `just ui-docker-build` | the `boopd-ui` image (bake `ui-dev`) |

To run the BFF locally against the dev stack:

```sh
just ui-dev-stack &
cd ui && OIDC_ISSUER=http://127.0.0.1:9400 OIDC_CLIENT_ID=boopd-ui \
  OIDC_CLIENT_SECRET=client-secret API_UPSTREAM=http://127.0.0.1:9401 \
  PUBLIC_URL=http://localhost:5173 UI_SESSION_KEYS=$(openssl rand -hex 32) \
  bun run dev:server
```

## The BFF's environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `OIDC_ISSUER` | required | the issuer; https, or http on loopback |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | required | the confidential client `boopd-ui` |
| `OIDC_SCOPES` | `openid profile email offline_access` | must include `openid` |
| `OIDC_TOKEN_AUTH_METHOD` | `client_secret_post` | or `client_secret_basic` |
| `OIDC_AUTHORIZATION_PARAMS` | none | a JSON object of extra authorize parameters (an `audience`); the flow's own cannot be set |
| `API_UPSTREAM` | required | the boopd api Service; the proxy forwards nowhere else |
| `PUBLIC_URL` | required | the UI's origin; https, or http on localhost |
| `UI_SESSION_KEYS` | required | comma-separated secrets of 32 bytes or more, newest first: the first seals, any opens |
| `UI_SESSION_TTL` | `8h` | a session's absolute end; refreshes never extend it |
| `UI_LINKS` | none | a JSON array of `{"label", "url"}` (https) for the sidebar's Links group |
| `PORT` | `3000` | |

The proxy forwards GET and HEAD, plus `POST /api/v1/repositories/{id}/recheck`
from the UI's own origin. `/api/v1/status` and `/api/v1/openapi.yaml`
go without a token; everything else needs the session (or the caller's
own Bearer). The CSP is `'self'` only.

## Registry token

The design system is published to GitHub Packages, which asks for a
token even to read a public package. `.npmrc` maps the `@donaldgifford`
scope there and holds only the reference `${NPM_TOKEN}`. `just
ui-install` and `just ui-docker-build` use `NPM_TOKEN` when it is set,
else `gh auth token`, which needs the `read:packages` scope (`gh auth
refresh -s read:packages`). The image build takes it as a build secret,
so no layer holds it; Renovate reads it from the runner's `NPM_TOKEN`
secret (`renovate.json5`).

## Components from the mockup

The components the first mockup drafted here (segmented control, option
rows, filter bar, inline states, stacked bars over time, the app
switcher) and the status page parts are in the package since 0.4.0. boop
keeps no local copy. A generic component the package lacks is drafted
here, seen working, and moved there (design-system RFC-0001).

## Gaps in the design system

The per-page list is in IMPL-0005's Phase 12 findings (component gaps).
Each is an issue for the design system, not a patch here. Two are about
consuming the package at all:

- **Dev server and CommonJS.** The package ships source, so Vite's dev
  server does not pre-bundle what it imports, and the CommonJS packages
  underneath fail to load (a blank page). `web/vite.config.ts` names
  them in `optimizeDeps.include`. The package's README should say this
  for every consumer.
- **`Button` as a link** needs `nativeButton={false}` beside `render`,
  or Base UI warns.
