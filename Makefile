BINARY  := longue-vue
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

COLLECTOR_BINARY    := longue-vue-collector
VM_COLLECTOR_BINARY := longue-vue-vm-collector

IMAGE_NAME ?= longue-vue
IMAGE_TAG  ?= dev

.PHONY: all build build-noui build-collector build-vm-collector generate test test-one vet lint fmt tidy check clean docker-build docker-build-collector docker-build-vm-collector docker-build-ingest-gw ui-install ui-build ui-dev ui-check ui-test

all: build

# Default build embeds ui/dist — run `make ui-build` first (or once) so the
# Vite bundle exists. For backend-only workflows use `make build-noui`.
build:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

# Compile longue-vue without the embedded UI — /ui/ replies 404. No Node/npm
# required. CI and release builds do not use this target.
build-noui:
	go build -tags noui $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

# Compile the push-mode collector binary (ADR-0009). No UI, no DB dependency.
build-collector:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(COLLECTOR_BINARY) ./cmd/$(COLLECTOR_BINARY)

# Compile the VM collector binary (ADR-0015). Pulls cloud-provider VMs and
# pushes to longue-vue over HTTPS. No UI, no DB dependency.
build-vm-collector:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(VM_COLLECTOR_BINARY) ./cmd/$(VM_COLLECTOR_BINARY)

ui-install:
	cd ui && npm ci

ui-build:
	cd ui && npm run build

ui-dev:
	cd ui && npm run dev

ui-check:
	cd ui && npm run typecheck

ui-test:
	cd ui && npm test

generate:
	go generate ./...

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

docker-build-collector:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-f Dockerfile.collector \
		-t $(IMAGE_NAME)-collector:$(IMAGE_TAG) \
		.

docker-build-vm-collector:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-f Dockerfile.vm-collector \
		-t $(IMAGE_NAME)-vm-collector:$(IMAGE_TAG) \
		.

docker-build-ingest-gw:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-f Dockerfile.ingest-gw \
		-t $(IMAGE_NAME)-ingest-gw:$(IMAGE_TAG) \
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

check: fmt vet lint test ui-test

clean:
	rm -rf $(BIN_DIR)
