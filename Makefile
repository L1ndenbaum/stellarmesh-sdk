SHELL := /bin/sh

ROOT := $(CURDIR)
PYTHON_LOGGING_DIR := sdk/python/logging
PYTHON_STORAGE_DIR := sdk/python/storage
UV ?= uv
GO_PACKAGES := ./sdk/go/... ./sdk/go/gateway/... ./sdk/go/gateway/loggingadapter/... ./sdk/go/logging/... ./sdk/go/mq/kafka/... ./sdk/go/objectstorage/... ./services/logging/... ./services/storage/... ./sinks/logging/clickhouse/...
DOCKER_PROXY_ARGS := --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy
GOPROXY ?= https://proxy.golang.org,direct
GOSUMDB ?= sum.golang.org
DOCKER_GO_ARGS := $(DOCKER_PROXY_ARGS) --build-arg GOPROXY=$(GOPROXY) --build-arg GOSUMDB=$(GOSUMDB)

export GOWORK := $(ROOT)/go.work
export GOCACHE ?= $(ROOT)/.cache/go-build
export GOMODCACHE ?= $(ROOT)/.cache/go-mod

.PHONY: bootstrap format check test race verify go-module-consumer images integration integration-aws

bootstrap:
	$(UV) sync --project $(PYTHON_LOGGING_DIR) --frozen
	$(UV) sync --project $(PYTHON_STORAGE_DIR) --frozen

format:
	gofmt -w $$(rg --files sdk/go services/logging services/storage sinks/logging/clickhouse -g '*.go')
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff check $(PYTHON_LOGGING_DIR) --fix
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff format $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check $(PYTHON_STORAGE_DIR) --fix
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/integration/storage-pipeline.py --fix
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format tests/integration/storage-pipeline.py

check:
	test -z "$$(gofmt -l sdk/go services/logging services/storage sinks/logging/clickhouse)"
	go vet $(GO_PACKAGES)
	$(UV) lock --project $(PYTHON_LOGGING_DIR) --check
	$(UV) lock --project $(PYTHON_STORAGE_DIR) --check
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff check $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff format --check $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen mypy $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy --strict tests/integration/storage-pipeline.py
	$(UV) pip check --python $(PYTHON_LOGGING_DIR)/.venv/bin/python
	$(UV) pip check --python $(PYTHON_STORAGE_DIR)/.venv/bin/python
	sh -n services/logging/docker-entrypoint.sh
	sh -n tests/integration/logging-pipeline.sh
	sh -n tests/integration/storage-minio.sh
	sh -n tests/go-module-consumer.sh
	git diff --check

test:
	go test $(GO_PACKAGES)
	cd $(PYTHON_LOGGING_DIR) && $(UV) run --frozen pytest
	cd $(PYTHON_STORAGE_DIR) && $(UV) run --frozen pytest
	$(MAKE) go-module-consumer

go-module-consumer:
	./tests/go-module-consumer.sh

race:
	go test -race $(GO_PACKAGES)

verify: check test

images:
	docker build --network=host $(DOCKER_GO_ARGS) -f services/logging/Dockerfile -t stellarmesh-logging-service:test .
	docker build --network=host $(DOCKER_GO_ARGS) -f services/storage/Dockerfile -t stellarmesh-storage-service:test .
	docker build --network=host $(DOCKER_GO_ARGS) -f sinks/logging/clickhouse/Dockerfile -t stellarmesh-logging-clickhouse-sink:test .
	docker build -f sinks/logging/clickhouse/Dockerfile.migrate -t stellarmesh-logging-clickhouse-migrate:test sinks/logging/clickhouse

integration: images
	./tests/integration/logging-pipeline.sh
	./tests/integration/storage-minio.sh

integration-aws:
	STELLARMESH_STORAGE_AWS_INTEGRATION=1 go test ./sdk/go/objectstorage/s3store -run '^TestAWSManualIntegration$$' -count=1
