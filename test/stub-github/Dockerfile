# syntax=docker/dockerfile:1.7
# Stub GitHub image for the chart e2e only (IMPL-0001 task 7.8). Never published.
FROM golang:1.27.2 AS build
WORKDIR /src
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY test/fakegithub/ test/fakegithub/
COPY test/stub-github/ test/stub-github/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stub-github ./test/stub-github

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stub-github /usr/local/bin/stub-github
ENTRYPOINT ["/usr/local/bin/stub-github"]
