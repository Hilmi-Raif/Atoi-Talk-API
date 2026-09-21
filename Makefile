APP_NAME_API       ?= atoitalk-api
APP_NAME_SCHEDULER ?= atoitalk-scheduler
APP_NAME_WORKER    ?= atoitalk-message-worker
APP_NAME_GATEWAY   ?= atoitalk-websocket-gateway
BIN_DIR            ?= ./bin

GO                       ?= go
UNIT_COVERAGE_MIN        ?= 85
INTEGRATION_COVERAGE_MIN ?= 68
COVERAGE_EXCLUDE         ?= internal/domain/model,internal/bootstrap,scripts,ent,docs,cmd,internal/infrastructure/mocks,internal/infrastructure/database/repository/mocks,internal/api/application/mocks,internal/messaging/events/mocks
GOLANGCI_LINT      ?= $(shell which ~/go/bin/golangci-lint.exe 2>/dev/null || which ~/go/bin/golangci-lint 2>/dev/null || which golangci-lint 2>/dev/null || echo golangci-lint)
GOSEC              ?= gosec
GOVULNCHECK        ?= govulncheck
K6                 ?= k6
K6_ARGS            ?=
K6_DOCKER          ?= docker
K6_IMAGE           ?= grafana/k6:latest
K6_DOCKER_NETWORK  ?= host
LOADTEST_MOUNT     ?= $(CURDIR)/loadtest
LOADTEST_RUNNER    ?= docker

BASE_URL            ?= http://127.0.0.1:8080
WS_BASE_URL         ?= ws://127.0.0.1:8081
SCENARIO            ?= loadtest/scenarios/isolated_endpoint.js
ENDPOINT            ?= chats
TARGET_RPS          ?= 300
DURATION            ?= 30s
MAX_VUS             ?= 900
CHAT_IDS            ?=
AROUND_MESSAGE_ID   ?=
ATTACHMENT_IDS      ?=

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
	$(GOSEC) -exclude=G104,G117 -exclude-dir=ent -exclude-dir=internal/infrastructure/mocks -exclude-dir=internal/infrastructure/database/repository/mocks -exclude-dir=internal/api/application/mocks -exclude-dir=internal/messaging/events/mocks ./...
	$(GOVULNCHECK) ./...

.PHONY: generate
generate: ## run code generation and mockery
	$(GO) generate ./internal

.PHONY: mock-verify
mock-verify: generate ## regenerate and verify mockery files match git state
	git diff --exit-code -- internal/infrastructure/mocks internal/infrastructure/database/repository/mocks internal/api/application/mocks internal/messaging/events/mocks

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
coverage-check: coverage-check-unit coverage-check-integration ## verify independent unit and integration coverage thresholds

.PHONY: coverage-integration
coverage-integration: ## generate integration coverage profile
	$(GO) test -tags=integration -coverpkg=./... -covermode=atomic -coverprofile=coverage-integration.out ./integration/... -count=1
	$(GO) tool cover -func=coverage-integration.out

.PHONY: coverage-check-unit
coverage-check-unit: ## verify total statement-weighted unit coverage
	$(GO) run ./scripts/check-coverage -profile=coverage-unit.out -min=$(UNIT_COVERAGE_MIN) -exclude=$(COVERAGE_EXCLUDE)

.PHONY: coverage-check-integration
coverage-check-integration: ## verify total statement-weighted integration coverage
	$(GO) run ./scripts/check-coverage -profile=coverage-integration.out -min=$(INTEGRATION_COVERAGE_MIN) -exclude=$(COVERAGE_EXCLUDE)

.PHONY: test-env-up
test-env-up: ## start local test backing services with docker compose
	docker compose -f deployments/integration/docker-compose.yaml up -d

.PHONY: test-env-down
test-env-down: ## stop local test backing services
	docker compose -f deployments/integration/docker-compose.yaml down -v

.PHONY: local-up
local-up: ## start local development application stack with docker compose
	docker compose -f deployments/local/docker-compose.yaml up -d

.PHONY: local-down
local-down: ## stop and remove local development application stack
	docker compose -f deployments/local/docker-compose.yaml down -v

.PHONY: loadtest-smoke
loadtest-smoke: ## run k6 smoke load test
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/smoke.js

.PHONY: loadtest-stress
loadtest-stress: ## run k6 breakpoint stress load test
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/breakpoint_stress.js

.PHONY: loadtest-ws
loadtest-ws: ## run k6 concurrent websocket load test
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/websocket_stress.js

.PHONY: loadtest-upload
loadtest-upload: ## run k6 media upload load test
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/media_upload.js

.PHONY: loadtest-full
loadtest-full: ## run k6 full lifecycle stress load test
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/full_stress.js

.PHONY: loadtest-api-endpoint
loadtest-api-endpoint: ## benchmark one API endpoint; use ENDPOINT=chats|messages|send|users|around|media
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/isolated_endpoint.js

.PHONY: loadtest-reset
loadtest-reset: ## reset mutable benchmark data and restore deterministic chat fixtures
	docker exec -i atoitalk-local-postgres psql -v ON_ERROR_STOP=1 -U postgres -d atoitalk_dev < scripts/loadtest/reset.sql
	docker exec -i atoitalk-local-postgres psql -v ON_ERROR_STOP=1 -U postgres -d atoitalk_dev < scripts/seed/multi_chat_fixture.sql
	docker exec atoitalk-local-postgres psql -v ON_ERROR_STOP=1 -U postgres -d atoitalk_dev -c "VACUUM (ANALYZE) messages, message_outboxes, chats, private_chats, group_members;"

.PHONY: loadtest-observability-reset
loadtest-observability-reset: ## enable and reset PostgreSQL statement statistics
	docker exec -i atoitalk-local-postgres psql -v ON_ERROR_STOP=1 -U postgres -d atoitalk_dev < scripts/loadtest/postgres-observability.sql

.PHONY: loadtest-mixed
loadtest-mixed: ## benchmark the mixed API scenario
	$(MAKE) loadtest-runner SCENARIO=loadtest/scenarios/steady_breakpoint.js

.PHONY: loadtest-run
loadtest-run: ## run a k6 scenario with native k6
	BASE_URL="$(BASE_URL)" WS_BASE_URL="$(WS_BASE_URL)" ENDPOINT="$(ENDPOINT)" TARGET_RPS="$(TARGET_RPS)" DURATION="$(DURATION)" MAX_VUS="$(MAX_VUS)" CHAT_IDS="$(CHAT_IDS)" AROUND_MESSAGE_ID="$(AROUND_MESSAGE_ID)" ATTACHMENT_IDS="$(ATTACHMENT_IDS)" $(K6) $(K6_ARGS) run "$(SCENARIO)"

.PHONY: loadtest-runner
loadtest-runner: ## run k6 using LOADTEST_RUNNER=native or docker
ifeq ($(LOADTEST_RUNNER),native)
	$(MAKE) loadtest-run SCENARIO="$(SCENARIO)"
else ifeq ($(LOADTEST_RUNNER),docker)
	$(MAKE) loadtest-run-docker SCENARIO="$(SCENARIO)"
else
	$(error LOADTEST_RUNNER must be native or docker)
endif

.PHONY: loadtest-run-docker
loadtest-run-docker: ## run a k6 scenario with a configurable docker runner
	$(K6_DOCKER) run --rm -i --network=$(K6_DOCKER_NETWORK) -v "$(LOADTEST_MOUNT):/loadtest" -e BASE_URL="$(BASE_URL)" -e WS_BASE_URL="$(WS_BASE_URL)" -e ENDPOINT="$(ENDPOINT)" -e TARGET_RPS="$(TARGET_RPS)" -e DURATION="$(DURATION)" -e MAX_VUS="$(MAX_VUS)" -e CHAT_IDS="$(CHAT_IDS)" -e AROUND_MESSAGE_ID="$(AROUND_MESSAGE_ID)" -e ATTACHMENT_IDS="$(ATTACHMENT_IDS)" $(K6_IMAGE) $(K6_ARGS) run "/loadtest/$(patsubst loadtest/%,%,$(SCENARIO))"

.PHONY: local-observability-up
local-observability-up: ## start local application and signoz observability stack
	docker compose -f deployments/local/docker-compose.yaml -f deployments/observability/docker-compose.yaml up -d

.PHONY: local-observability-down
local-observability-down: ## stop local application and signoz observability stack
	docker compose -f deployments/local/docker-compose.yaml -f deployments/observability/docker-compose.yaml down -v

.PHONY: observability-up
observability-up: ## start local signoz observability stack with docker compose
	docker compose -f deployments/observability/docker-compose.yaml up -d

.PHONY: observability-down
observability-down: ## stop and remove local signoz observability stack
	docker compose -f deployments/observability/docker-compose.yaml down -v

.PHONY: build-api
build-api: ## build api server binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_API) ./cmd/api

.PHONY: build-scheduler
build-scheduler: ## build scheduler binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_SCHEDULER) ./cmd/scheduler

.PHONY: build-message-worker
build-message-worker: ## build message worker binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_WORKER) ./cmd/message_worker

.PHONY: build-websocket-gateway
build-websocket-gateway: ## build websocket gateway binary
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(APP_NAME_GATEWAY) ./cmd/websocket_gateway

.PHONY: seed
seed: ## insert initial development seed data
	$(GO) run ./scripts/seed

.PHONY: docker-build
docker-build: ## build docker images for api, worker, gateway, and scheduler
	docker build -f build/package/api/Dockerfile -t $(APP_NAME_API):latest .
	docker build -f build/package/message_worker/Dockerfile -t $(APP_NAME_WORKER):latest .
	docker build -f build/package/websocket_gateway/Dockerfile -t $(APP_NAME_GATEWAY):latest .
	docker build -f build/package/scheduler/Dockerfile -t $(APP_NAME_SCHEDULER):latest .

.PHONY: clean
clean: ## remove build binaries and coverage profiles
	rm -rf $(BIN_DIR) coverage*.out coverage*.html
