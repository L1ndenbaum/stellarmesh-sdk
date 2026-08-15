SHELL := /bin/sh

ROOT := $(CURDIR)
PYTHON_DIR := sdk/python
VENV := $(ROOT)/.venv
GO_PACKAGES := ./sdk/go/... ./services/logging/... ./services/storage/... ./sinks/clickhouse/...

export GOWORK := $(ROOT)/go.work
export GOCACHE ?= $(ROOT)/.cache/go-build
export GOMODCACHE ?= $(ROOT)/.cache/go-mod

.PHONY: bootstrap format check test race verify images integration

bootstrap:
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install -e '$(PYTHON_DIR)[dev]'

format:
	gofmt -w $$(rg --files sdk/go services/logging services/storage sinks/clickhouse -g '*.go')
	cd $(PYTHON_DIR) && $(VENV)/bin/ruff check . --fix
	cd $(PYTHON_DIR) && $(VENV)/bin/ruff format .

check:
	test -z "$$(gofmt -l sdk/go services/logging services/storage sinks/clickhouse)"
	go vet $(GO_PACKAGES)
	cd $(PYTHON_DIR) && $(VENV)/bin/ruff check .
	cd $(PYTHON_DIR) && $(VENV)/bin/ruff format --check .
	cd $(PYTHON_DIR) && $(VENV)/bin/mypy .
	sh -n services/logging/docker-entrypoint.sh
	sh -n tests/integration/logging-pipeline.sh
	git diff --check

test:
	go test $(GO_PACKAGES)
	cd $(PYTHON_DIR) && $(VENV)/bin/pytest

race:
	go test -race $(GO_PACKAGES)

verify: check test

images:
	docker build --network=host --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy -f services/logging/Dockerfile -t stellarmesh-logging-service:test .
	docker build --network=host --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy -f services/storage/Dockerfile -t stellarmesh-storage-service:test .
	docker build --network=host --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy -f sinks/clickhouse/Dockerfile -t stellarmesh-logging-clickhouse-sink:test .
	docker build -f sinks/clickhouse/Dockerfile.migrate -t stellarmesh-logging-clickhouse-migrate:test sinks/clickhouse

integration: images
	./tests/integration/logging-pipeline.sh
