# Base: Go env + cached deps, used by builder
FROM golang:1.25.4 AS base
WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Optional: non-root for dev/builder stages
RUN useradd -r -u 65532 -m nonroot && \
    chown -R nonroot:nonroot /workspace
USER 65532:65532

# Single builder stage: compile the binary
FROM base AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# Empty by default on purpose: the `if [ -z "${VERSION}" ]` guard below refuses
# to build without an explicit --build-arg VERSION=<release tag>, so a build can
# never silently bake a rolling `:latest` artifact-downloader ref. Mirrors the
# empty source default of DefaultDownloadImage in api/v1alpha1/aimartifact_types.go.
ARG VERSION=
# Compiled-in default for the artifact-downloader image the operator spawns.
# Defaults to the public docker.io/amdenterpriseai mirror so binaries produced
# by the public release flow work without further configuration. Silogen-private
# builds override this via --build-arg to point at docker.io/silogenai.
ARG ARTIFACT_DOWNLOADER_IMG=docker.io/amdenterpriseai/aim-artifact-downloader:${VERSION}

# Build as root to use cache mounts (final image is non-root)
USER root

# Copy all source (rely on .dockerignore to keep context small)
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    if [ -z "${VERSION}" ]; then \
        echo "ERROR: --build-arg VERSION=... must be supplied (e.g. v0.2.2)" >&2; \
        exit 1; \
    fi && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a \
    -ldflags "-X 'github.com/amd-enterprise-ai/aim-engine/api/v1alpha1.DefaultDownloadImage=${ARTIFACT_DOWNLOADER_IMG}'" \
    -o manager ./cmd/main.go

# Dev image for Tilt: full Go env + source + binary
FROM builder AS dev
USER 65532:65532
ENTRYPOINT ["/workspace/manager"]

# Production image: minimal distroless + binary only
FROM gcr.io/distroless/static:nonroot AS prod
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
