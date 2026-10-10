# boop — docker recipes
#
# Docker image build/push pipeline via docker buildx bake. Reads
# docker-bake.hcl for target definitions. Imported by the root justfile
# via `import? 'docker.just'` — settings (shell, etc.) live there.

# ─── Docker ─────────────────────────────────────────────────────────

# Local single-arch build via docker buildx bake
[group('docker')]
docker-build:
    @docker buildx bake

# Multi-arch build and push via docker buildx bake
[group('docker')]
docker-buildx:
    @docker buildx bake release

# Push the image (requires prior build)
[group('docker')]
docker-push:
    @docker buildx bake --push

# CI build (linux/amd64 only, for fast PR validation)
[group('docker')]
docker-ci:
    @docker buildx bake ci
