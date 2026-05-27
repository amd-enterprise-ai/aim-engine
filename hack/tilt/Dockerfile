# syntax=docker/dockerfile:1
# Dockerfile for Tilt live_update workflow
#
# This image includes the full Go toolchain for in-container rebuilds.
# Tilt syncs source files and triggers rebuilds inside the running container.
#
# Build targets:
#   dev   - Fast rebuilds, no debugger
#   debug - Includes Delve for remote debugging
#
# Caching strategy:
#   - BuildKit cache mounts for /go/pkg/mod and /root/.cache/go-build
#   - These persist across Docker builds on the same machine
#   - PVCs in k8s provide caching for live_update rebuilds

FROM golang:1.25.4-bookworm AS base

WORKDIR /workspace

# DOWNLOAD_IMAGE is the full image ref baked into the operator binary's
# DefaultDownloadImage via LDFLAGS. The Tiltfile passes a content-addressed
# tag (sha256 of images/artifact-downloader/**) so download Jobs reference
# the exact downloader image Tilt just pushed. The Dockerfile fails fast
# if this build-arg is missing so an LDFLAGS-less binary never reaches a
# cluster (cmd/main.go also has a runtime guard).
ARG DOWNLOAD_IMAGE=""

# Install Delve for debug builds (cached in layer)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/go-delve/delve/cmd/dlv@latest

# Copy go.mod/sum first for layer caching
COPY go.mod go.sum ./

# Download modules with cache mount
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source (will be overwritten by live_update)
COPY . .

# Pre-build to warm the cache (makes first live_update faster)
# Uses cache mounts so rebuilds are fast even if layers are invalidated
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    if [ -z "${DOWNLOAD_IMAGE}" ]; then \
        echo "ERROR: --build-arg DOWNLOAD_IMAGE=... must be supplied by the Tiltfile" >&2; \
        exit 1; \
    fi && \
    CGO_ENABLED=0 go build \
    -ldflags "-X 'github.com/amd-enterprise-ai/aim-engine/api/v1alpha1.DefaultDownloadImage=${DOWNLOAD_IMAGE}'" \
    -o /manager ./cmd/main.go

# ============================================================================
# Dev target: Fast rebuilds without debugger
# ============================================================================
FROM base AS dev

EXPOSE 8081 8080

ENTRYPOINT ["/manager"]

# ============================================================================
# Debug target: Includes Delve for remote debugging
# ============================================================================
FROM base AS debug

# Rebuild with debug symbols (disable optimizations and inlining) — same
# LDFLAGS as the dev target so DefaultDownloadImage matches what the
# Tiltfile pushed, even in delve-attached builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build \
    -gcflags="all=-N -l" \
    -ldflags "-X 'github.com/amd-enterprise-ai/aim-engine/api/v1alpha1.DefaultDownloadImage=${DOWNLOAD_IMAGE}'" \
    -o /manager ./cmd/main.go

EXPOSE 8081 8080 2345

# Delve headless server - accepts connections on port 2345
ENTRYPOINT ["/go/bin/dlv", "exec", "/manager", "--headless", "--listen=:2345", "--api-version=2", "--accept-multiclient", "--continue", "--"]
