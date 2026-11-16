SHELL := /bin/bash

BINARY := localmedia-unlock
GO ?= go
GOFLAGS ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: all build test vet fmt run build-linux build-linux-amd64 build-linux-arm64 smoke clean FORCE

all: build

build: FORCE
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

FORCE:

test:
	$(GO) test $(GOFLAGS) ./...

vet:
	$(GO) vet $(GOFLAGS) ./...

fmt:
	$(GO) fmt ./...

run:
	@mkdir -p .local
	$(GO) run . serve --config .local/config.json --listen 127.0.0.1:9090

build-linux: build-linux-amd64 build-linux-arm64

build-linux-amd64:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 .

build-linux-arm64:
	@mkdir -p dist
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 .

smoke: build
	bash scripts/smoke.sh bin/$(BINARY)

clean:
	rm -rf bin dist

