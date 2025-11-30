# Base: Go env + cached deps, used by builder
FROM golang:1.25.4 AS base
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

# Optional: non-root for dev/builder stages
RUN useradd -r -u 65532 -m nonroot && \
    chown -R nonroot:nonroot /workspace
USER 65532:65532

# Single builder stage: compile the binary
FROM base AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# Copy all source (rely on .dockerignore to keep context small)
COPY --chown=nonroot:nonroot . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a -o manager ./cmd/main.go

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
