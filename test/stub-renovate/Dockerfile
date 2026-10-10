# syntax=docker/dockerfile:1.7
# Stub Renovate image for e2e tests only (IMPL-0001 task 4.6). Never published.
FROM golang:1.27.2 AS build
WORKDIR /src
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY test/stub-renovate/ test/stub-renovate/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/renovate ./test/stub-renovate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/renovate /usr/local/bin/renovate
ENTRYPOINT ["/usr/local/bin/renovate"]
