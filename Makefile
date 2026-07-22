#-----------------------------------------------------------------------------------------------------------------------
# Variables
#-----------------------------------------------------------------------------------------------------------------------
BINARIES_DIR = $(CURDIR)/bin
BINARY_NAME  = ofga
MAIN         = ./cmd/ofga
GOLANGCI_LINT_VERSION = v2.12.2
GOVULNCHECK_VERSION   = v1.6.0

# Build metadata baked into the binary via -ldflags so local builds report
# something useful from `ofga version` (goreleaser overrides these in CI).
VERSION_PKG := github.com/sergiught/openfga-cli/internal/version
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
               -X $(VERSION_PKG).Version=$(VERSION) \
               -X $(VERSION_PKG).Commit=$(COMMIT) \
               -X $(VERSION_PKG).Date=$(BUILD_DATE)

.DEFAULT_GOAL := help

#-----------------------------------------------------------------------------------------------------------------------
# Help
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: help
help: ## Show this help message and exit
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?## / { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

#-----------------------------------------------------------------------------------------------------------------------
# Tooling (installed into ./bin on first use)
#-----------------------------------------------------------------------------------------------------------------------
$(BINARIES_DIR)/golangci-lint:
	@echo "==> Installing golangci-lint into $(BINARIES_DIR)"
	@GOBIN=$(BINARIES_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(BINARIES_DIR)/govulncheck:
	@echo "==> Installing govulncheck into $(BINARIES_DIR)"
	@GOBIN=$(BINARIES_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(BINARIES_DIR)/commitlint:
	@echo "==> Installing commitlint into $(BINARIES_DIR)"
	@GOBIN=$(BINARIES_DIR) go install github.com/conventionalcommit/commitlint@cbc379cadff4bf80bdb621b556e617cca39e1029 # v0.12.0

#-----------------------------------------------------------------------------------------------------------------------
# Build & run
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: build
build: ## Build the ofga binary into bin/
	@echo "==> Building $(BINARY_NAME) $(VERSION) into $(BINARIES_DIR)"
	@go build -ldflags="$(LDFLAGS)" -o "$(BINARIES_DIR)/$(BINARY_NAME)" "$(MAIN)"

.PHONY: install
install: ## Install ofga into GOBIN
	@go install -ldflags="$(LDFLAGS)" "$(MAIN)"

.PHONY: run
run: ## Run the TUI (bare ofga)
	@go run "$(MAIN)"

#-----------------------------------------------------------------------------------------------------------------------
# Test
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: test
test: ## Run tests with the race detector
	@go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out

#-----------------------------------------------------------------------------------------------------------------------
# Format, vet, lint, security
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: fmt
fmt: ## Format all Go files
	@gofmt -w $(shell git ls-files '*.go' | grep -v '^vendor/')

.PHONY: vet
vet: ## Run go vet
	@go vet ./...

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	@go mod tidy

.PHONY: lint
lint: $(BINARIES_DIR)/golangci-lint ## Run golangci-lint
	@$(BINARIES_DIR)/golangci-lint run ./...

.PHONY: vuln
vuln: $(BINARIES_DIR)/govulncheck ## Scan for known Go vulnerabilities
	@$(BINARIES_DIR)/govulncheck ./...

.PHONY: lint-commits
lint-commits: $(BINARIES_DIR)/commitlint ## Lint the current commit message against commitlint.yaml
	@$(BINARIES_DIR)/commitlint lint

.PHONY: check
check: fmt vet lint test ## Run fmt, vet, lint and test

#-----------------------------------------------------------------------------------------------------------------------
# Release
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: release-snapshot
release-snapshot: ## Build a full release locally without publishing (needs goreleaser)
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed: https://goreleaser.com/install"; exit 1; }
	@goreleaser release --snapshot --clean --skip=publish,sign

#-----------------------------------------------------------------------------------------------------------------------
# Demo stack (local OpenFGA + auth0-mock, seeded)
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: demo
demo: ## Bootstrap a seeded OpenFGA + auth0-mock demo stack (see test/oauth)
	@$(MAKE) -C test/oauth demo

.PHONY: demo-down
demo-down: ## Tear down the demo stack
	@$(MAKE) -C test/oauth down

.PHONY: gifs
gifs: build ## Record example GIFs from examples/tapes/*.tape (run `make demo` first)
	@command -v vhs >/dev/null 2>&1 || { echo "vhs not found, install charmbracelet/vhs and ttyd"; exit 1; }
	@curl -sf http://localhost:8080/healthz >/dev/null 2>&1 || { echo "demo stack not reachable on :8080, run 'make demo' first"; exit 1; }
	@for tape in examples/tapes/*.tape; do \
		case "$$tape" in */_setup.tape) continue;; esac; \
		echo "▶ recording $$tape"; PATH="$(CURDIR)/bin:$$PATH" vhs "$$tape" || exit 1; \
	done
	@echo "✓ recorded: $$(ls examples/*.gif 2>/dev/null)"

#-----------------------------------------------------------------------------------------------------------------------
# Clean
#-----------------------------------------------------------------------------------------------------------------------
.PHONY: clean
clean: ## Remove build artifacts
	@rm -rf $(BINARIES_DIR) dist coverage.out
