# ymlib — common tasks. Run `make help` for the list.

BINARY  := ymlib
BIN_DIR := bin
PKG     := ./cmd/ymlib

GOLANGCI_VERSION := v2.14.0
GOLANGCI_LINT    := $(BIN_DIR)/golangci-lint

.DEFAULT_GOAL := help
.PHONY: help build test install-lint lint fmt check clean

help: ## Show available targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-13s %s\n", $$1, $$2}'

build: ## Build the binary into bin/ymlib
	go build -o $(BIN_DIR)/$(BINARY) $(PKG)

test: ## Run all tests
	go test ./...

# Each recipe line runs in its own shell, so the if/else is one logical
# line joined with backslashes.
install-lint: ## Install golangci-lint into bin/ (skipped if that version is there)
	@if $(GOLANGCI_LINT) version 2>/dev/null | grep -q "version $(GOLANGCI_VERSION:v%=%) "; then \
		echo "golangci-lint $(GOLANGCI_VERSION) already installed"; \
	else \
		echo "installing golangci-lint $(GOLANGCI_VERSION) into $(BIN_DIR)/"; \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(BIN_DIR) $(GOLANGCI_VERSION); \
	fi

lint: install-lint ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

fmt: ## Format all Go files
	gofmt -w cmd internal

check: lint test ## Lint and test (run before finishing a milestone)

clean: ## Remove build output
	rm -rf $(BIN_DIR)
