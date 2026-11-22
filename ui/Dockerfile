# syntax=docker/dockerfile:1.7
# boopd-ui: the Bun BFF serving the React SPA (DESIGN-0004 § The UI). One
# process: it serves the build, proxies /api to boopd's api role and holds
# the session.

# The SPA build. Vite's output is static files, so this stage runs on the
# build platform only.
FROM --platform=$BUILDPLATFORM oven/bun:1.3.14 AS build
WORKDIR /src
# @donaldgifford/design-system installs from GitHub Packages, which wants
# a token. It arrives as a build secret (bake: id=NPM_TOKEN) and exists in
# this RUN's environment only: never in a layer, an ARG or .npmrc, which
# holds the ${NPM_TOKEN} reference and nothing else.
COPY package.json bun.lock .npmrc ./
RUN --mount=type=secret,id=NPM_TOKEN,required=true \
    NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)" bun install --frozen-lockfile
COPY . .
RUN bun run build

# The BFF's runtime dependencies, for the target platform. None comes from
# GitHub Packages (the design system is a devDependency), so no token.
FROM oven/bun:1.3.14 AS deps
WORKDIR /src
COPY package.json bun.lock ./
RUN printf '' > .npmrc && bun install --frozen-lockfile --production

FROM oven/bun:1.3.14-distroless
WORKDIR /app
COPY --from=deps /src/node_modules ./node_modules
COPY --from=build /src/dist ./dist
COPY package.json tsconfig.json ./
COPY server ./server

# The chart mounts the root filesystem read-only, so Bun keeps no
# transpiler cache.
ENV NODE_ENV=production \
    BUN_RUNTIME_TRANSPILER_CACHE_PATH=0

EXPOSE 3000
USER 65532:65532

# No shell for an in-image probe: Kubernetes probes /healthz and /readyz.
HEALTHCHECK NONE

ENTRYPOINT ["/usr/local/bin/bun", "server/main.ts"]
