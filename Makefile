GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/shyper/autodownloadarr/internal/buildinfo.Version=$(VERSION)

.PHONY: all web build test test-go test-web lint run dev-web docker clean

all: web build

## web: build the web UI into internal/webui/dist
web:
	cd web && npm ci && npm run build

## build: build the server binary (embeds the last web build)
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/autodownloadarr ./cmd/autodownloadarr

test: test-go test-web

test-go:
	$(GO) test -race ./...

test-web:
	cd web && npm test

lint:
	$(GO) vet ./...
	golangci-lint run
	cd web && npm run lint && npm run typecheck

## run: start the server with a local ./config directory
run:
	AUTODOWNLOADARR_CONFIG_DIR=./config AUTODOWNLOADARR_LOG_LEVEL=debug $(GO) run ./cmd/autodownloadarr

## dev-web: Vite dev server on :5173, proxies /api to `make run`
dev-web:
	cd web && npm run dev

docker:
	docker build --build-arg VERSION=$(VERSION) -t autodownloadarr:$(VERSION) .

clean:
	rm -rf bin
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
