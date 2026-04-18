# syntax=docker/dockerfile:1.7
#
# argosd container image.
# Multi-stage: golang builder -> distroless static runtime. Binary is static
# (CGO_ENABLED=0) and runs as the non-root UID baked into the distroless
# :nonroot tag (65532).
#
# Build args:
#   GO_VERSION       - Go toolchain tag (default 1.26).
#   VERSION          - value baked into the binary's `main.version` via
#                      -ldflags. Override in CI/releases.

ARG GO_VERSION=1.26

# ---- build stage ---------------------------------------------------------
FROM golang:${GO_VERSION} AS build

WORKDIR /src

# Prime the module cache before copying the rest of the source so unrelated
# edits don't invalidate the module download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG VERSION=docker
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/argosd \
        ./cmd/argosd

# ---- runtime stage -------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary to /. Distroless has no shell; the container runs argosd directly.
COPY --from=build /out/argosd /argosd

# Default HTTP port (overridable via ARGOS_ADDR).
EXPOSE 8080

# distroless:nonroot provides UID/GID 65532.
USER nonroot:nonroot

ENTRYPOINT ["/argosd"]
