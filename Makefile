BINARY  := argosd
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

IMAGE_NAME ?= argos
IMAGE_TAG  ?= dev

.PHONY: all build build-noui generate test test-one vet lint fmt tidy check clean docker-build ui-install ui-build ui-dev ui-check

all: build

# Default build embeds ui/dist — run `make ui-build` first (or once) so the
# Vite bundle exists. For backend-only workflows use `make build-noui`.
build:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

# Compile argosd without the embedded UI — /ui/ replies 404. No Node/npm
# required. CI and release builds do not use this target.
build-noui:
	go build -tags noui $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

ui-install:
	cd ui && npm ci

ui-build:
	cd ui && npm run build

ui-dev:
	cd ui && npm run dev

ui-check:
	cd ui && npm run typecheck

generate:
	go generate ./...

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

test:
	go test -race -cover ./...

test-one:
	@if [ -z "$(TEST)" ]; then echo "usage: make test-one TEST=TestName"; exit 1; fi
	go test -race -run '^$(TEST)$$' ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

tidy:
	go mod tidy

check: fmt vet lint test

clean:
	rm -rf $(BIN_DIR)
