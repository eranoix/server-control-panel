# server-control-panel Makefile
# Single-tenant Linux control plane. Stack: Go 1.25 backend + embedded HTML SPA.

.PHONY: help build lab-agent dev test fmt vet check tools tailwind minify deploy rollback health logs backup clean install-scripts health-ai docs docs-check docs-embed docs-data mobile-openapi-spec sdui-contract sdui-golden sdui-check

BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/vps-manager-new
CTL_BIN := $(BIN_DIR)/vpsmctl-new
WAD_BIN := $(BIN_DIR)/wad-new
AGENT_BIN := $(BIN_DIR)/lab-agent-new
DEPLOY_SCRIPT := scripts/deploy.sh

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: tailwind docs-embed minify ## Regenerate tailwind.css + sync the embedded doc + build cmd/server, cmd/vpsmctl and cmd/wad
	CGO_ENABLED=0 go build -o $(SERVER_BIN) ./cmd/server
	CGO_ENABLED=0 go build -o $(CTL_BIN) ./cmd/vpsmctl
	CGO_ENABLED=0 go build -o $(WAD_BIN) ./cmd/wad
	@echo "✓ binaries in $(BIN_DIR)/"

# lab-agent has its OWN target and is deliberately NOT a dependency of `build`.
#
# The reason is operational, not aesthetic: the deploy script runs
# `make build` to ship the PANEL, which is the operator's main working
# tool. Coupling the two would let a compile error in the agent BLOCK the
# panel deploy — the agent would take down the very tool used to fix the
# agent.
#
# What is NOT lost by the split: the gate's `go build ./...` already ensures
# the agent COMPILES on every deploy. What is avoided is coupling the ARTIFACT:
# each binary ships and rolls back on its own.
lab-agent: ## Build only cmd/lab-agent (kept out of the panel build on purpose)
	@# BUILD STAMP, and why it is worth it here.
	@# A rollback drill once left FIVE artifacts in bin/ on the node, with names
	@# that differed only by timestamp — and TWO of them with the same content, because
	@# the source was identical and the Go build is reproducible. Finding out which one
	@# was live meant hashing all five. The process could not say where it came from.
	@# The stamp trades bit-for-bit reproducibility for TRACEABILITY: every build is
	@# distinct, and the agent itself announces in the journal which artifact it is.
	CGO_ENABLED=0 go build -ldflags "-X main.carimbo=$$(git rev-parse --short HEAD 2>/dev/null || echo no-git)-$$(date -u +%Y%m%dT%H%M%SZ)" -o $(AGENT_BIN) ./cmd/lab-agent
	@echo "✓ $(AGENT_BIN)"

ESBUILD := .tools/node_modules/.bin/esbuild

tools: ## Install the local tooling into .tools/ (esbuild to minify, playwright-core for the rendering pin test)
	@# .tools/ is not versioned (.gitignore:61), so the dependency has to be
	@# declared HERE — otherwise a clean clone cannot run the pin tests and the
	@# operator has no way to find out why. Project rule: everything must be
	@# rebuildable from documentation + git.
	@mkdir -p .tools
	cd .tools && npm install --no-audit --no-fund esbuild playwright-core
	@echo "✓ tooling in .tools/ — the browser for the screen pin test comes from"
	@echo "  /root/.cache/ms-playwright or the system Chrome; if it is missing:"
	@echo "  npx playwright install chromium"

minify: ## Generate <x>.min.js for the app assets (best-effort; without esbuild the originals are served)
	@# PROVENANCE STAMP: every .min.js ends with the sha256 of the .js that
	@# produced it. The server only swaps the original for the minified file when the
	@# stamp matches (internal/webassets/minificado.go). Without it, the .min.js — which
	@# is GENERATED and UNTRACKED — survives a change to its source and keeps being
	@# served: that is how a bundle from 08/26 erased AdGuard from the app on
	@# 08/30 and took down the whole SPA with a ReferenceError during Alpine boot.
	@if [ -x "$(ESBUILD)" ]; then \
	  for f in internal/webassets/web/vendor/vpsm/app/*.js; do \
	    case "$$f" in *.min.js) continue;; esac; \
	    o="$${f%.js}.min.js"; \
	    if "$(ESBUILD)" "$$f" --minify-whitespace --minify-syntax --target=es2020 > "$$o.tmp" 2>/dev/null; then \
	      printf '\n//# vpsm-src-sha256=%s\n' "$$(sha256sum "$$f" | cut -d" " -f1)" >> "$$o.tmp"; \
	      mv "$$o.tmp" "$$o"; \
	    else \
	      rm -f "$$o.tmp" "$$o"; \
	      echo "  minify failed on $$f -- serving the original"; \
	    fi; \
	  done; \
	  echo "\342\234\223 app assets minified (with provenance stamp)"; \
	else \
	  : "Delete rather than keep: an orphaned minified file would be embedded in the binary"; \
	  rm -f internal/webassets/web/vendor/vpsm/app/*.min.js; \
	  echo "  esbuild missing from .tools/ -- serving the assets unminified"; \
	fi

docs-embed: docs-check ## Copy the technical report (.docs/) into the gated /_docs embed (generated artifact)
	@cp ".docs/Documentacao Tecnica - VPS Manager.html" internal/webassets/docs/report.html
	@echo "✓ internal/webassets/docs/report.html synced with .docs/"

docs-check: ## Validate the nesting of the doc pages (blocks content outside .page)
	@python3 scripts/check-docs-structure.py ".docs/Documentacao Tecnica - VPS Manager.html"

docs-data: ## Regenerate the report's metrics/routes from the repo (GEN blocks)
	@./scripts/gen-docs-data.sh

dev: build ## Build + run locally on :8766 (dev mode)
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

deploy: build ## Build (which runs tailwind first) + deploy.sh (auto-rollback)
	@$(DEPLOY_SCRIPT) $(SERVER_BIN)

rollback: ## Roll back to the previous binary
	@$(DEPLOY_SCRIPT) --rollback

health: ## vpsmctl health (deep check)
	@$(CTL_BIN) health 2>/dev/null || vpsmctl health

logs: ## Tail the last 50 audit events
	@$(CTL_BIN) logs 2>/dev/null || vpsmctl logs

backup: build ## vpsmctl backup → ~/vpsm-backup-<ts>.tar.gz
	@$(CTL_BIN) backup

backup-containers: build ## backup + tarball /var/lib/vpsm-whatsapp/
	@$(CTL_BIN) backup --containers

stt-test: ## Run the STT E2E suite (needs a running vps-manager + WhisperLive)
	@python3 /tmp/stt_e2e_suite.py

DOCS_HTML := .docs/Documentacao Tecnica - VPS Manager.html

docs: ## Open the HTML technical report
	@test -f "$(DOCS_HTML)" || { echo "✗ $(DOCS_HTML) does not exist; regenerate it with make docs-data"; exit 1; }
	@if command -v xdg-open >/dev/null 2>&1; then xdg-open "$(DOCS_HTML)" >/dev/null 2>&1 & \
	else echo "Technical report (self-contained — open it in a browser):"; echo "  $(CURDIR)/$(DOCS_HTML)"; fi
	@echo "✓ To refresh its metrics after new commits, run make docs-data"

# AI Toolkit wrappers — installed into /usr/local/bin so they can be called from
# any terminal pane (with the default PATH). Run this EVERY time scripts/* changes.
# _venice_pane_home.sh is sourced by absolute path from the wrappers, so it
# MUST stay in /opt/panel/scripts/ (not in /usr/local/bin).
WRAPPERS := venice-repl venice-agent venice-search-repl venice-image-repl \
            venice-tts-repl venice-transcribe-repl venice-upscale-repl \
            venice-video-repl venice-openclaw chatgpt-cli

install-scripts: ## Install the AI Toolkit wrappers into /usr/local/bin (atomic install)
	@for w in $(WRAPPERS); do \
	  install -m 0755 scripts/$$w /usr/local/bin/$$w && echo "✓ /usr/local/bin/$$w"; \
	done
	@echo "✓ scripts/_venice_pane_home.sh stays in /opt/panel/scripts/ (sourced by absolute path)"

health-ai: ## Full checks of the AI terminals (preflight + local smoke test)
	@echo "═══ env files ═══"
	@for f in /etc/claude-router/env /etc/claude-router/users/sam.env; do \
	  if [ -L "$$f" ] && ! [ -e "$$f" ]; then \
	    echo "✗ $$f: broken/loop symlink"; \
	  elif [ -f "$$f" ]; then \
	    if grep -q '^BRIDGE_VENICE_KEY=sk-priv-' "$$f" 2>/dev/null; then \
	      echo "✓ $$f: BRIDGE_VENICE_KEY present"; \
	    else \
	      echo "✗ $$f: BRIDGE_VENICE_KEY missing/empty"; \
	    fi; \
	  else \
	    echo "- $$f: does not exist (legacy fallback)"; \
	  fi; \
	done
	@echo
	@echo "═══ installed wrappers ═══"
	@for w in $(WRAPPERS); do \
	  if [ -x /usr/local/bin/$$w ]; then \
	    echo "✓ /usr/local/bin/$$w"; \
	  else \
	    echo "✗ /usr/local/bin/$$w (missing — run: make install-scripts)"; \
	  fi; \
	done
	@echo
	@echo "═══ upstream binaries ═══"
	@for b in venice codex openclaw bwrap jq dtach; do \
	  if command -v $$b >/dev/null 2>&1; then \
	    echo "✓ $$b ($$($$b --version 2>&1 | head -1))"; \
	  else \
	    echo "✗ $$b not installed"; \
	  fi; \
	done
	@echo
	@echo "═══ services ═══"
	@for s in venice-api claude-router private-ai-api; do \
	  state=$$(systemctl is-active $$s 2>/dev/null || echo unknown); \
	  if [ "$$state" = "active" ]; then \
	    echo "✓ $$s ($$state)"; \
	  else \
	    echo "✗ $$s ($$state)"; \
	  fi; \
	done
	@echo
	@echo "═══ codex login status ═══"
	@codex login status 2>&1 | head -2 || true
	@echo
	@echo "═══ openclaw gateway ═══"
	@XDG_RUNTIME_DIR=/run/user/0 openclaw gateway status 2>&1 | grep -E '^Runtime:|^Connectivity' || echo "(openclaw is not responding)"

clean: ## Remove the binaries staged as bin/*-new
	rm -f $(SERVER_BIN) $(CTL_BIN)

.DEFAULT_GOAL := help
