# Every target here is also what CI runs, so a green local run means a green CI
# run. Where they differ, CI is the authority.

SHELL := /usr/bin/env bash
COMPOSE := docker compose -f deploy/docker-compose.yml

# Pinned, and the same versions CI runs. @latest would let a scanner or a linter
# change between two runs of the same commit, which turns a green tree red with
# no diff anywhere to point at.
GOVULNCHECK_VERSION := v1.7.0

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: init
init: ## Step 1 for any clone: install the pre-push gate
	@git config core.hooksPath .githooks
	@echo "core.hooksPath = $$(git config --get core.hooksPath)"
	@command -v gitleaks >/dev/null 2>&1 \
		|| { echo "gitleaks is not installed. The pre-push gate fails closed without it: brew install gitleaks"; exit 1; }
	@echo "pre-push gate installed"

.PHONY: verify-pattern
verify-pattern: ## Prove the hook's bracket patterns still match their literals
	@./.githooks/selftest.sh

.PHONY: test-hook
test-hook: verify-pattern ## Alias for verify-pattern

.PHONY: fmt
fmt: ## Format Go source
	@gofmt -w cmd internal

.PHONY: lint
lint: ## Run golangci-lint, as CI runs it
	@command -v golangci-lint >/dev/null 2>&1 \
		|| { echo "golangci-lint is not installed: brew install golangci-lint"; exit 1; }
	@golangci-lint run ./...

.PHONY: vuln
vuln: ## Check dependencies for advisories on paths this code reaches
	@go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: leaks
leaks: ## Scan the full history for secrets
	@gitleaks git --no-banner --redact .

.PHONY: check
check: ## gofmt, vet, lint, race tests, hook selftest
	@echo "== gofmt"; test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "run make fmt"; exit 1; }
	@echo "== vet";   go vet ./...
	@echo "== lint";  $(MAKE) --no-print-directory lint
	@echo "== test";  go test -race ./...
	@echo "== hook";  ./.githooks/selftest.sh

# Everything CI enforces. Separate from `check` because govulncheck resolves its
# own module graph on every run, which is a slow thing to put in the loop
# somebody runs twenty times an afternoon.
.PHONY: check-all
check-all: check vuln leaks ## Everything CI enforces, including the slow scans

.PHONY: identity
identity: ## Run the pre-push gate over all of this repository's history
	@./.githooks/pre-push --all-history

.PHONY: up
up: ## Start the whole stack, then print where everything is
	@$(COMPOSE) up -d --build
	@echo
	@echo "  Grafana     http://localhost:$${GRAFANA_PORT:-3000}      (no login; opens on the dashboard)"
	@echo "  Jaeger      http://localhost:$${JAEGER_PORT:-16686}"
	@echo "  Prometheus  http://localhost:$${PROMETHEUS_PORT:-9090}"
	@echo "  Service     http://localhost:$${SERVICE_PORT:-8080}/api/quote"
	@echo "  Injector    http://localhost:$${ADMIN_PORT:-8082}/admin/inject   (its own listener; see SECURITY.md)"
	@echo
	@echo "  Traffic is already flowing. Give it ~30s, then click a dot on the p99 panel."

.PHONY: down
down: ## Stop the stack and remove its volumes
	@$(COMPOSE) down -v

.PHONY: logs
logs: ## Follow the service logs (JSON, with trace_id on every line)
	@$(COMPOSE) logs -f service

.PHONY: slow
slow: ## Make the tail worse, so the p99 panel has something to show
	@curl -sS -X POST localhost:$${ADMIN_PORT:-8082}/admin/inject \
		-H 'content-type: application/json' \
		-d '{"tail_percent":0.25,"tail_latency_ms":900}' | tee /dev/stderr >/dev/null
	@echo

.PHONY: errors
errors: ## Inject a 20% error rate, so the 5xx panel moves
	@curl -sS -X POST localhost:$${ADMIN_PORT:-8082}/admin/inject \
		-H 'content-type: application/json' -d '{"error_rate":0.2}' | tee /dev/stderr >/dev/null
	@echo

.PHONY: reset
reset: ## Back to healthy
	@curl -sS -X POST localhost:$${ADMIN_PORT:-8082}/admin/inject \
		-H 'content-type: application/json' \
		-d '{"error_rate":0,"tail_percent":0.04,"tail_latency_ms":400,"base_latency_ms":8}' | tee /dev/stderr >/dev/null
	@echo

.PHONY: ask
ask: ## Send one request and show the response
	@curl -sS "localhost:$${SERVICE_PORT:-8080}/api/quote?sku=SKU-1"
