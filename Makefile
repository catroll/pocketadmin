# PocketAdmin — build & run helpers.
#
# The host's default Go module/build caches are not writable in every
# environment, so every target sources scripts/goenv.sh first. That script
# points GOMODCACHE/GOCACHE/GOPATH inside .cache/ and selects a reachable
# module proxy.
#
# Runtime settings come from pocketadmin.yaml. The variables below are empty by
# default so the YAML file stays authoritative; set one to override it on the
# command line.

SHELL := /bin/bash
BINARY := bin/pocketadmin

CONFIG ?=
DATA_DIR ?=
ADDR ?=

FLAGS := $(if $(CONFIG),-config $(CONFIG),) \
         $(if $(DATA_DIR),-dir $(DATA_DIR),) \
         $(if $(ADDR),-addr $(ADDR),)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: assets
assets: ## Vendor AdminLTE and its plugins into web/static/vendor
	bash scripts/vendor-assets.sh

.PHONY: build
build: ## Compile the server binary
	source scripts/goenv.sh && go build -o $(BINARY) .

.PHONY: run
run: build ## Build and run the panel
	./$(BINARY) $(FLAGS)

.PHONY: dev
dev: ## Run with PocketBase dev logging
	source scripts/goenv.sh && go run . $(FLAGS) -dev

.PHONY: test
test: ## Run the Go test suite
	source scripts/goenv.sh && go test ./...

.PHONY: vet
vet: ## Run go vet
	source scripts/goenv.sh && go vet ./...

.PHONY: fmt
fmt: ## Format all Go sources
	source scripts/goenv.sh && gofmt -l -w $$(find . -name '*.go' -not -path './.cache/*')

.PHONY: tidy
tidy: ## Sync go.mod / go.sum
	source scripts/goenv.sh && go mod tidy

.PHONY: superuser
superuser: build ## Create/update a superuser (EMAIL=… PASSWORD=…)
	@test -n "$(EMAIL)" || (echo "EMAIL is required, eg. make superuser EMAIL=me@example.com PASSWORD=secret123"; exit 1)
	@test -n "$(PASSWORD)" || (echo "PASSWORD is required"; exit 1)
	./$(BINARY) $(FLAGS) superuser upsert "$(EMAIL)" "$(PASSWORD)"

.PHONY: clean
clean: ## Remove build output and disposable caches
	rm -rf bin .cache/gobuild .cache/testrun
