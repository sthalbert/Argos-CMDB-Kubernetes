# syntax=docker/dockerfile:1.7
#
# longue-vue container image.
# Multi-stage: node builder (UI) -> golang builder (binary embedding the UI)
# -> distroless static runtime. Binary is static (CGO_ENABLED=0) and runs
# as the non-root UID baked into the distroless :nonroot tag (65532).
#
# Build args:
#   GO_VERSION       - Go toolchain tag (default 1.26).
#   NODE_VERSION     - Node toolchain tag (default 22-alpine).
#   VERSION          - value baked into the binary's `main.version` via
#                      -ldflags. Override in CI/releases.

ARG GO_VERSION=1.26
ARG NODE_VERSION=22-alpine

# ---- UI stage ------------------------------------------------------------
# Produces ui/dist so the Go builder can //go:embed it. Split from the Go
# stage so a backend-only edit doesn't reinstall npm deps (and vice versa).
FROM node:${NODE_VERSION} AS ui-build

WORKDIR /src/ui

COPY ui/package.json ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY ui/ ./
RUN npm run build

# ---- Go stage ------------------------------------------------------------
FROM golang:${GO_VERSION} AS build

WORKDIR /src

# Prime the module cache before copying the rest of the source so unrelated
# edits don't invalidate the module download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .
# Drop the UI source we just consumed in the ui-build stage, then drop in
# the produced bundle. //go:embed all:dist (in ui/embed.go) picks it up.
RUN rm -rf ui/dist
COPY --from=ui-build /src/ui/dist ./ui/dist

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
        -o /out/longue-vue \
        ./cmd/longue-vue

# ---- runtime stage -------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary to /. Distroless has no shell; the container runs longue-vue directly.
COPY --from=build /out/longue-vue /longue-vue

# Default HTTP port (overridable via LONGUE_VUE_ADDR).
EXPOSE 8080

# distroless:nonroot provides UID/GID 65532.
USER nonroot:nonroot

ENTRYPOINT ["/longue-vue"]
