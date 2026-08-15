SHELL := /bin/sh

ROOT := $(CURDIR)
PYTHON_LOGGING_DIR := sdk/python
PYTHON_STORAGE_DIR := sdk/python/storage
VENV := $(ROOT)/.venv
GO_PACKAGES := ./sdk/go/... ./services/logging/... ./services/storage/... ./sinks/clickhouse/...
DOCKER_PROXY_ARGS := --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy
GOPROXY ?= https://proxy.golang.org,direct
GOSUMDB ?= sum.golang.org
DOCKER_GO_ARGS := $(DOCKER_PROXY_ARGS) --build-arg GOPROXY=$(GOPROXY) --build-arg GOSUMDB=$(GOSUMDB)

export GOWORK := $(ROOT)/go.work
export GOCACHE ?= $(ROOT)/.cache/go-build
export GOMODCACHE ?= $(ROOT)/.cache/go-mod

.PHONY: bootstrap format check test race verify images integration integration-aws

bootstrap:
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install -e '$(PYTHON_LOGGING_DIR)[dev]'
	$(VENV)/bin/pip install -e '$(PYTHON_STORAGE_DIR)[dev]'

format:
	gofmt -w $$(rg --files sdk/go services/logging services/storage sinks/clickhouse -g '*.go')
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/ruff check src tests --fix
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/ruff format src tests
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/ruff check . --fix
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/ruff format .
	$(VENV)/bin/ruff check tests/integration/storage-pipeline.py --fix
	$(VENV)/bin/ruff format tests/integration/storage-pipeline.py

check:
	test -z "$$(gofmt -l sdk/go services/logging services/storage sinks/clickhouse)"
	go vet $(GO_PACKAGES)
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/ruff check src tests
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/ruff format --check src tests
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/mypy src tests
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/ruff check .
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/ruff format --check .
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/mypy .
	$(VENV)/bin/ruff check tests/integration/storage-pipeline.py
	$(VENV)/bin/ruff format --check tests/integration/storage-pipeline.py
	$(VENV)/bin/mypy --strict tests/integration/storage-pipeline.py
	sh -n services/logging/docker-entrypoint.sh
	sh -n tests/integration/logging-pipeline.sh
	sh -n tests/integration/storage-minio.sh
	git diff --check

test:
	go test $(GO_PACKAGES)
	cd $(PYTHON_LOGGING_DIR) && $(VENV)/bin/pytest
	cd $(PYTHON_STORAGE_DIR) && $(VENV)/bin/pytest

race:
	go test -race $(GO_PACKAGES)

verify: check test

images:
	docker build --network=host $(DOCKER_GO_ARGS) -f services/logging/Dockerfile -t stellarmesh-logging-service:test .
	docker build --network=host $(DOCKER_GO_ARGS) -f services/storage/Dockerfile -t stellarmesh-storage-service:test .
	docker build --network=host $(DOCKER_GO_ARGS) -f sinks/clickhouse/Dockerfile -t stellarmesh-logging-clickhouse-sink:test .
	docker build -f sinks/clickhouse/Dockerfile.migrate -t stellarmesh-logging-clickhouse-migrate:test sinks/clickhouse

integration: images
	./tests/integration/logging-pipeline.sh
	./tests/integration/storage-minio.sh

integration-aws:
	STELLARMESH_STORAGE_AWS_INTEGRATION=1 go test ./sdk/go/objectstorage/s3store -run '^TestAWSManualIntegration$$' -count=1
