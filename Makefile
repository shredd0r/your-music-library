# ymlib — common tasks. Run `make help` for the list.

BINARY  := ymlib
BIN_DIR := bin
PKG     := ./cmd/ymlib

.DEFAULT_GOAL := help
.PHONY: help build test lint fmt check clean

help: ## Show available targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

build: ## Build the binary into bin/ymlib
	go build -o $(BIN_DIR)/$(BINARY) $(PKG)

test: ## Run all tests
	go test ./...

lint: ## Check formatting, go vet and staticcheck
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: these files need formatting (run 'make fmt'):"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	go tool staticcheck ./...

fmt: ## Format all Go files
	gofmt -w cmd internal

check: lint test ## Lint and test (run before finishing a milestone)

clean: ## Remove build output
	rm -rf $(BIN_DIR)
