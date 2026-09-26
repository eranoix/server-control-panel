# server-control-panel Makefile
# Single-tenant Linux control plane. Stack: Go 1.25 backend + embedded HTML SPA.

.PHONY: help build node-agent dev test fmt vet check tools tailwind minify health logs backup backup-containers clean docs docs-check docs-data mobile-openapi-spec sdui-contract sdui-golden sdui-check

BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/server-control-panel-new
CTL_BIN := $(BIN_DIR)/panelctl-new
WAD_BIN := $(BIN_DIR)/wad-new
AGENT_BIN := $(BIN_DIR)/node-agent-new

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: minify ## Build cmd/server, cmd/panelctl and cmd/wad into bin/ (run `make tailwind` first after adding CSS classes)
	CGO_ENABLED=0 go build -o $(SERVER_BIN) ./cmd/server
	CGO_ENABLED=0 go build -o $(CTL_BIN) ./cmd/panelctl
	CGO_ENABLED=0 go build -o $(WAD_BIN) ./cmd/wad
	@echo "✓ binaries in $(BIN_DIR)/"

# node-agent has its OWN target and is deliberately NOT a dependency of `build`.
#
# The reason is operational, not aesthetic: a deploy runs `make build` to
# ship the PANEL, which is the operator's main working
# tool. Coupling the two would let a compile error in the agent BLOCK the
# panel deploy — the agent would take down the very tool used to fix the
# agent.
#
# What is NOT lost by the split: `go build ./...` in CI already ensures the
# agent COMPILES on every change. What is avoided is coupling the ARTIFACT:
# each binary ships and rolls back on its own.
node-agent: ## Build only cmd/node-agent (kept out of the panel build on purpose)
	@# BUILD STAMP, and why it is worth it here.
	@# A rollback drill once left FIVE artifacts in bin/ on the node, with names
	@# that differed only by timestamp — and TWO of them with the same content, because
	@# the source was identical and the Go build is reproducible. Finding out which one
	@# was live meant hashing all five. The process could not say where it came from.
	@# The stamp trades bit-for-bit reproducibility for TRACEABILITY: every build is
	@# distinct, and the agent itself announces in the journal which artifact it is.
	CGO_ENABLED=0 go build -ldflags "-X main.stamp=$$(git rev-parse --short HEAD 2>/dev/null || echo no-git)-$$(date -u +%Y%m%dT%H%M%SZ)" -o $(AGENT_BIN) ./cmd/node-agent
	@echo "✓ $(AGENT_BIN)"

ESBUILD := .tools/node_modules/.bin/esbuild

tools: ## Install the local tooling into .tools/ (esbuild to minify, playwright-core for the rendering pin test)
	@# .tools/ is not versioned, so the dependency is declared here: without
	@# it a clean clone cannot run the pin tests and nothing says why.
	@mkdir -p .tools
	cd .tools && npm install --no-audit --no-fund esbuild playwright-core
	@echo "✓ tooling in .tools/. The screen pin tests use the system Chrome or a"
	@echo "  playwright browser; if neither is installed: npx playwright install chromium"

minify: ## Generate <x>.min.js for the app assets (best-effort; without esbuild the originals are served)
	@# PROVENANCE STAMP: every .min.js ends with the sha256 of the .js that
	@# produced it. The server only swaps the original for the minified file when the
	@# stamp matches (internal/webassets/minified.go). Without it, the .min.js — which
	@# is GENERATED and UNTRACKED — survives a change to its source and keeps being
	@# served: that is how a bundle from 08/26 erased AdGuard from the app on
	@# 08/30 and took down the whole SPA with a ReferenceError during Alpine boot.
	@if [ -x "$(ESBUILD)" ]; then \
	  for f in internal/webassets/web/vendor/panel/app/*.js; do \
	    case "$$f" in *.min.js) continue;; esac; \
	    o="$${f%.js}.min.js"; \
	    if "$(ESBUILD)" "$$f" --minify-whitespace --minify-syntax --target=es2020 > "$$o.tmp" 2>/dev/null; then \
	      printf '\n//# panel-src-sha256=%s\n' "$$(sha256sum "$$f" | cut -d" " -f1)" >> "$$o.tmp"; \
	      mv "$$o.tmp" "$$o"; \
	    else \
	      rm -f "$$o.tmp" "$$o"; \
	      echo "  minify failed on $$f -- serving the original"; \
	    fi; \
	  done; \
	  echo "\342\234\223 app assets minified (with provenance stamp)"; \
	else \
	  : "Delete rather than keep: an orphaned minified file would be embedded in the binary"; \
	  rm -f internal/webassets/web/vendor/panel/app/*.min.js; \
	  echo "  esbuild missing from .tools/ -- serving the assets unminified"; \
	fi

DOCS_HTML := internal/webassets/docs/report.html

docs-check: ## Validate the nesting of the technical report pages (served gated at /_docs)
	@python3 scripts/check-docs-structure.py "$(DOCS_HTML)"

docs-data: ## Regenerate the report's metrics/routes from the repo (GEN blocks)
	@./scripts/gen-docs-data.sh

dev: build ## Build + run the server from bin/
	$(SERVER_BIN)

test: ## Run go test ./...
	go test ./...

fmt: ## gofmt -w
	gofmt -w cmd/ internal/

vet: ## go vet ./...
	go vet ./...

check: vet test ## vet + test (CI-style)

mobile-openapi-spec: ## Generate android/data/mobile-api-client/openapi/mobile-v1.yaml from the huma registry in internal/mobilebff
	go run ./cmd/mobile-openapi-gen

sdui-contract: ## Generate contracts/sdui/contract.json from the Go types in internal/mobilebff/sdui
	go run ./cmd/sdui-contract

sdui-golden: ## Regenerate contracts/sdui/fixtures/screens/*.json from the real Builders (admin + non-admin)
	go test ./internal/mobilebff/sdui/ -run TestGoldenScreens -update

sdui-check: ## SDUI contract gate: committed contract + compatibility with already published apps + screen goldens
	./scripts/check-sdui-contract.sh

tailwind: ## Regenerate /vendor/tailwind.css (run after adding a new class)
	./scripts/build-tailwind.sh

health: ## panelctl health (deep check)
	@$(CTL_BIN) health 2>/dev/null || panelctl health

logs: ## Tail the last 50 audit events
	@$(CTL_BIN) logs 2>/dev/null || panelctl logs

backup: build ## panelctl backup → ~/panel-backup-<ts>.tar.gz
	@$(CTL_BIN) backup

backup-containers: build ## backup + tarball /var/lib/panel-whatsapp/
	@$(CTL_BIN) backup --containers

docs: ## Open the HTML technical report
	@if command -v xdg-open >/dev/null 2>&1; then xdg-open "$(DOCS_HTML)" >/dev/null 2>&1 & \
	else echo "Technical report (self-contained, open it in a browser):"; echo "  $(CURDIR)/$(DOCS_HTML)"; fi

clean: ## Remove the binaries staged as bin/*-new
	rm -f $(SERVER_BIN) $(CTL_BIN) $(WAD_BIN) $(AGENT_BIN)

.DEFAULT_GOAL := help
