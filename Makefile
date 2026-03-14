.PHONY: build test dev web clean lint hooks

BINARY := synapbus
MODULE := github.com/synapbus/synapbus
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
	rm -rf internal/web/dist
	cp -r web/build internal/web/dist

clean:
	rm -rf $(BUILD_DIR)
	rm -rf web/node_modules web/build
	rm -rf data

lint:
	golangci-lint run ./...

hooks:
	@echo "Installing git hooks..."
	@mkdir -p scripts/hooks
	@chmod +x scripts/hooks/pre-commit scripts/hooks/pre-push
	@ln -sf ../../scripts/hooks/pre-commit .git/hooks/pre-commit
	@ln -sf ../../scripts/hooks/pre-push .git/hooks/pre-push
	@echo "✅ Git hooks installed"

.DEFAULT_GOAL := build
