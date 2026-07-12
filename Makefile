.DEFAULT_GOAL := help
BINARY := servicedesk
IMAGE  := servicedesk:local

# Read the current version from the VERSION file (no external tooling required).
VERSION_CURRENT := $(shell cat VERSION 2>/dev/null || echo 0.0.0)

.PHONY: help
help: ## Show this menu
	@echo "ServiceDesk $(VERSION_CURRENT) - available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Release cycle:"
	@echo "  make release VERSION=1.1.0   # bump VERSION (amended into your last commit), push, tag, push -> CI"
	@echo "                                # (commit your work yourself, but don't push - let this push it)"

## --- develop ---------------------------------------------------------------

.PHONY: build
build: ## Build the servicedesk binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) ./cmd/servicedesk

.PHONY: run
run: ## Run the server locally (sqlite, ./servicedesk.db)
	go run ./cmd/servicedesk

.PHONY: demo
demo: ## Run the server locally in demo mode (sqlite, seeds demo data on first boot)
	DEMO_MODE=true go run ./cmd/servicedesk

.PHONY: demo-curl-test
demo-curl-test: ## Curl-only smoke test against an already-running demo-mode server (see DEMO.md)
	./scripts/demo.sh

.PHONY: test
test: ## Run the full test suite
	go test ./...

.PHONY: test-verbose
test-verbose: ## Run the full test suite with verbose per-test output
	go test ./... -v

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go source with gofmt
	gofmt -l -w .

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove local build artifacts and the dev sqlite DB
	rm -rf bin servicedesk.db servicedesk.db-wal servicedesk.db-shm

## --- docker: sqlite (default) -----------------------------------------------

.PHONY: docker-build
docker-build: ## Build the servicedesk Docker image
	docker build -t $(IMAGE) .

.PHONY: up
up: ## Start the sqlite stack (docker-compose.yaml) in the foreground
	docker compose -f docker-compose.yaml up --build

.PHONY: up-d
up-d: ## Start the sqlite stack in the background
	docker compose -f docker-compose.yaml up --build -d

.PHONY: down
down: ## Stop the sqlite stack and remove its volume
	docker compose -f docker-compose.yaml down -v

## --- docker: mysql -----------------------------------------------------------

.PHONY: up-mysql
up-mysql: ## Start the MySQL-backed stack in the background
	docker compose -f docker-compose-mysql.yml up --build -d

.PHONY: down-mysql
down-mysql: ## Stop the MySQL-backed stack and remove its volume
	docker compose -f docker-compose-mysql.yml down -v

## --- docker: postgresql --------------------------------------------------------

.PHONY: up-postgres
up-postgres: ## Start the PostgreSQL-backed stack in the background
	docker compose -f docker-compose-postgresql.yml up --build -d

.PHONY: down-postgres
down-postgres: ## Stop the PostgreSQL-backed stack and remove its volume
	docker compose -f docker-compose-postgresql.yml down -v

## --- kubernetes ---------------------------------------------------------------

.PHONY: k8s-apply
k8s-apply: ## Apply the k8s/ manifests to the current kubectl context
	kubectl apply -f k8s/

.PHONY: k8s-delete
k8s-delete: ## Delete the k8s/ manifests from the current kubectl context
	kubectl delete -f k8s/

.PHONY: k8s-logs
k8s-logs: ## Tail logs from the servicedesk deployment in k8s
	kubectl logs -f deployment/servicedesk

## --- supply-chain hardening -------------------------------------------------

.PHONY: github-action-bump
github-action-bump: ## Pin .github/workflows/*.yml actions to latest release, full commit SHA (uses pinact)
	@# Unauthenticated GitHub API calls are capped at 60/hour and this touches
	@# ~10 actions x (list tags + verify); export GITHUB_TOKEN to raise that limit.
	go run github.com/suzuki-shunsuke/pinact/cmd/pinact@latest run --update
	go run github.com/suzuki-shunsuke/pinact/cmd/pinact@latest run --verify
	@echo "Actions bumped and verified. Review the diff (check .pinact.yaml's ignore_actions"
	@echo "weren't silently downgraded), then run 'make vet test' and re-check semgrep before committing."

## --- release --------------------------------------------------------------

.PHONY: version
version: ## Print the version currently in VERSION
	@echo $(VERSION_CURRENT)

.PHONY: bump
bump: ## Rewrite the VERSION file (VERSION=x.y.z required)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make bump VERSION=x.y.z"; exit 1; fi
	@echo "$(VERSION)" > VERSION
	@echo "VERSION -> $(VERSION)"

.PHONY: release
release: ## Bump VERSION (amended into HEAD), push, tag, push the tag - triggers GitHub Actions (VERSION=x.y.z required)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=x.y.z"; exit 1; fi
	$(MAKE) bump VERSION=$(VERSION)
	git add VERSION
	git commit --amend --no-edit
	git push --force-with-lease origin HEAD
	git tag v$(VERSION)
	git push origin v$(VERSION)
	@echo "Released v$(VERSION) - GitHub Actions will build and publish."
