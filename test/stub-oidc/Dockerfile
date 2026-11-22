# The mock OIDC issuer (ui/server/testing) for the e2e cluster: the API
# verifies its tokens, the BFF signs in through it (IMPL-0005 14.1).
# Built locally and in CI, never pushed.
FROM oven/bun:1.3.14 AS deps
WORKDIR /src
COPY ui/package.json ui/bun.lock ./
RUN printf '' > .npmrc && bun install --frozen-lockfile --production

FROM oven/bun:1.3.14-distroless
WORKDIR /app
COPY --from=deps /src/node_modules ./node_modules
COPY ui/server/testing/mock-issuer.ts ui/server/testing/issuer-main.ts ./
ENV BUN_RUNTIME_TRANSPILER_CACHE_PATH=0
USER 65532:65532
EXPOSE 9443
ENTRYPOINT ["/usr/local/bin/bun", "issuer-main.ts"]
