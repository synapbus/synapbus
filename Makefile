.PHONY: build test dev web clean lint

BINARY := synapbus
MODULE := github.com/smart-mcp-proxy/synapbus
BUILD_DIR := bin
LDFLAGS := -s -w

CGO_ENABLED := 0

build:
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/synapbus

test:
	CGO_ENABLED=$(CGO_ENABLED) go test ./... -v -count=1

dev:
	CGO_ENABLED=$(CGO_ENABLED) go run ./cmd/synapbus serve

web:
	cd web && npm install && npm run build
	@echo "Svelte SPA built to internal/web/dist/"

clean:
	rm -rf $(BUILD_DIR)
	rm -rf web/node_modules web/build
	rm -rf data

lint:
	golangci-lint run ./...

.DEFAULT_GOAL := build
