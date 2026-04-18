BINARY  := argosd
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

IMAGE_NAME ?= argos
IMAGE_TAG  ?= dev

.PHONY: all build generate test test-one vet lint fmt tidy check clean docker-build

all: build

build:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

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
