# burrow development tasks. Run `make` or `make help` for the target list.

GO      ?= go
BIN     ?= bin/burrow
PKG     ?= ./...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -X github.com/brandtg/burrow/internal/journal.Version=$(VERSION)
FUZZTIME ?= 30s

# Static binary: never link libc.
export CGO_ENABLED := 0

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets.
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the static CLI to bin/burrow.
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/burrow

.PHONY: build-all
build-all: ## Compile every package (no output binary).
	$(GO) build $(PKG)

.PHONY: run
run: ## Run the CLI from source; pass ARGS, e.g. make run ARGS="version".
	$(GO) run ./cmd/burrow $(ARGS)

.PHONY: install
install: ## Install the CLI into GOBIN/GOPATH with the version stamp.
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/burrow

.PHONY: test
test: ## Run unit and safety tests.
	$(GO) test $(PKG)

.PHONY: test-race
test-race: ## Run tests with the race detector.
	$(GO) test -race $(PKG)

.PHONY: fuzz
fuzz: ## Fuzz the path validator for FUZZTIME (default 30s).
	$(GO) test ./internal/safe -run '^$$' -fuzz FuzzValidate -fuzztime $(FUZZTIME)

.PHONY: fmt
fmt: ## Format all Go source in place.
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs formatting.
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint (skips if not installed).
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed; skipping"; exit 0; }
	golangci-lint run $(PKG)

.PHONY: vuln
vuln: ## Run govulncheck (skips if not installed).
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck not installed; skipping"; exit 0; }
	govulncheck $(PKG)

.PHONY: doctor
doctor: ## Check that the external gate tools are installed.
	scripts/dev-tools.sh

.PHONY: setup-tools
setup-tools: ## Install the external gate tools at the CI-pinned versions.
	scripts/dev-tools.sh --install

.PHONY: safety-gate
safety-gate: ## Run the static destructive-sink gate.
	$(GO) run ./scripts/safety-gate .

.PHONY: docs
docs: ## Generate the man pages into docs/man (review the diff).
	$(GO) run ./cmd/burrow docs --dir docs/man

.PHONY: verify-static
verify-static: build ## Verify the built binary has no dynamic loader deps.
	@if command -v ldd >/dev/null 2>&1; then \
	  out="$$(ldd $(BIN) 2>&1 || true)"; \
	  if printf '%s\n' "$$out" | grep -qE 'not a dynamic executable|statically linked'; then \
	    echo "ok: $(BIN) is static"; \
	  else \
	    echo "FAIL: $(BIN) is dynamically linked:"; printf '%s\n' "$$out"; exit 1; \
	  fi; \
	else \
	  echo "ldd not available; skipping static check"; \
	fi

.PHONY: gate
gate: ## Run the canonical gate (scripts/check.sh).
	./scripts/check.sh

.PHONY: check
check: gate ## Alias for gate.

.PHONY: gate-strict
gate-strict: ## Gate that fails if lint/vuln tools are missing.
	BURROW_REQUIRE_TOOLS=1 ./scripts/check.sh

.PHONY: integration
integration: ## Run the loopback cross-device trash test (requires root).
	BURROW_INTEGRATION=1 ./scripts/check.sh

.PHONY: matrix
matrix: ## Run the container integration matrix (requires docker/podman).
	scripts/matrix.sh

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin

.PHONY: tidy
tidy: ## Sync go.mod/go.sum.
	$(GO) mod tidy