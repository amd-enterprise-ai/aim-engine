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
# VERSION must be supplied by the caller (CI pipeline, Tiltfile, etc.). We
# deliberately do not default to `latest` here because that tag's contents
# rotate on every release, and `imagePullPolicy: IfNotPresent` on the
# download Job would then resolve to an arbitrarily stale cached layer on
# the node. Building without --build-arg VERSION=... will fail the explicit
# guard in cmd/main.go at operator startup so the misconfiguration is loud.
ARG VERSION=""

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
    -ldflags "-X 'github.com/amd-enterprise-ai/aim-engine/api/v1alpha1.DefaultDownloadImage=ghcr.io/silogen/aim-artifact-downloader:${VERSION}'" \
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
