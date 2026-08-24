APP_NAME_API       ?= atoitalk-api
APP_NAME_SCHEDULER ?= atoitalk-scheduler
BIN_DIR            ?= ./bin

GO                 ?= go
GOLANGCI_LINT      ?= $(shell which ~/go/bin/golangci-lint.exe 2>/dev/null || which ~/go/bin/golangci-lint 2>/dev/null || which golangci-lint 2>/dev/null || echo golangci-lint)
GOSEC              ?= gosec
GOVULNCHECK        ?= govulncheck

.DEFAULT_GOAL      := help

.PHONY: help
help: ## show available make targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nusage:\n  make \033[36m<target>\033[0m\n\ntargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## format go source files
	$(GO) fmt ./...

.PHONY: vet
vet: ## run go vet analysis
	$(GO) vet ./...

.PHONY: lint
lint: ## run golangci-lint
	$(GOLANGCI_LINT) run ./...

.PHONY: security
security: ## run security scans with gosec and govulncheck
	$(GOSEC) -exclude=G104,G117 -exclude-dir=ent -exclude-dir=internal/adapter/mocks -exclude-dir=internal/repository/mocks -exclude-dir=internal/service/mocks -exclude-dir=internal/websocket/mocks ./...
	$(GOVULNCHECK) ./...

.PHONY: generate
generate: ## run code generation and mockery
	$(GO) generate ./internal

.PHONY: mock-verify
mock-verify: generate ## regenerate and verify mockery files match git state
	git diff --exit-code -- internal/adapter/mocks internal/repository/mocks internal/service/mocks internal/websocket/mocks

.PHONY: test
test: ## run all unit tests
	$(GO) test ./internal/... -count=1

.PHONY: test-race
test-race: ## run unit tests with race detector
	$(GO) test -race ./internal/... -count=1

.PHONY: test-integration
test-integration: ## run integration tests against backing services
	$(GO) test -tags=integration ./integration -count=1

.PHONY: coverage-unit
coverage-unit: ## generate unit test coverage profile
	$(GO) test ./internal/... -covermode=atomic -coverprofile=coverage-unit.out -count=1
	$(GO) tool cover -func=coverage-unit.out

.PHONY: coverage-check
coverage-check: ## verify unit coverage threshold for production packages
	$(GO) run ./scripts/check-coverage -profile=coverage-unit.out -min=85

.PHONY: test-env-up
test-env-up: ## start local test backing services with docker compose
	docker compose -f integration/docker-compose.yml up -d

.PHONY: test-env-down
test-env-down: ## stop and remove local test backing services
	docker compose -f integration/docker-compose.yml down -v

.PHONY: build-api
build-api: ## build api server binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_API) ./cmd/api

.PHONY: build-scheduler
build-scheduler: ## build scheduler binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_SCHEDULER) ./cmd/scheduler

.PHONY: docker-build
docker-build: ## build docker images for api and scheduler
	docker build -f build/package/api/Dockerfile -t $(APP_NAME_API):latest .
	docker build -f build/package/scheduler/Dockerfile -t $(APP_NAME_SCHEDULER):latest .

.PHONY: clean
clean: ## remove build binaries and coverage profiles
	rm -rf $(BIN_DIR) coverage*.out coverage*.html
