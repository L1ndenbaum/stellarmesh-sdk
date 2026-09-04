SHELL := /bin/sh

ROOT := $(CURDIR)
PYTHON_LOGGING_DIR := sdk/python/logging
PYTHON_STORAGE_DIR := sdk/python/storage
UV ?= uv
GO_PACKAGES := ./sdk/go/... ./sdk/go/gateway/... ./sdk/go/logging/... ./sdk/go/mq/kafka/... ./sdk/go/objectstorage/... ./services/storage/...
GO_SOURCE_DIRS := sdk/go services/storage
DOCKER_PROXY_ARGS := --build-arg HTTP_PROXY --build-arg HTTPS_PROXY --build-arg NO_PROXY --build-arg http_proxy --build-arg https_proxy --build-arg no_proxy
GOPROXY ?= https://proxy.golang.org,direct
GOSUMDB ?= sum.golang.org
DOCKER_GO_ARGS := $(DOCKER_PROXY_ARGS) --build-arg GOPROXY=$(GOPROXY) --build-arg GOSUMDB=$(GOSUMDB)

export GOWORK := $(ROOT)/go.work
export GOCACHE ?= $(ROOT)/.cache/go-build
export GOMODCACHE ?= $(ROOT)/.cache/go-mod

.PHONY: bootstrap python-logging-bootstrap python-storage-bootstrap format check test race verify \
	go-check go-test go-race python-logging-check python-logging-test \
	python-storage-check python-storage-test shell-check go-module-consumer \
	image-storage images integration-storage integration integration-aws

bootstrap: python-logging-bootstrap python-storage-bootstrap

python-logging-bootstrap:
	$(UV) sync --project $(PYTHON_LOGGING_DIR) --frozen

python-storage-bootstrap:
	$(UV) sync --project $(PYTHON_STORAGE_DIR) --frozen

format:
	gofmt -w $$(rg --files $(GO_SOURCE_DIRS) -g '*.go')
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff check $(PYTHON_LOGGING_DIR) --fix
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff format $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check $(PYTHON_STORAGE_DIR) --fix
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/integration/storage-pipeline.py --fix
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format tests/integration/storage-pipeline.py

go-check:
	test -z "$$(gofmt -l $(GO_SOURCE_DIRS))"
	go vet $(GO_PACKAGES)

python-logging-check: python-logging-bootstrap
	$(UV) lock --project $(PYTHON_LOGGING_DIR) --check
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff check $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen ruff format --check $(PYTHON_LOGGING_DIR)
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen mypy $(PYTHON_LOGGING_DIR)
	$(UV) pip check --python $(PYTHON_LOGGING_DIR)/.venv/bin/python

python-storage-check: python-storage-bootstrap
	$(UV) lock --project $(PYTHON_STORAGE_DIR) --check
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy --strict tests/integration/storage-pipeline.py
	$(UV) pip check --python $(PYTHON_STORAGE_DIR)/.venv/bin/python

shell-check:
	sh -n tests/integration/storage-minio.sh
	sh -n tests/go-module-consumer.sh
	git diff --check

check: go-check python-logging-check python-storage-check shell-check

go-test:
	go test $(GO_PACKAGES)

python-logging-test: python-logging-bootstrap
	cd $(PYTHON_LOGGING_DIR) && $(UV) run --frozen pytest

python-storage-test: python-storage-bootstrap
	cd $(PYTHON_STORAGE_DIR) && $(UV) run --frozen pytest

test: go-test python-logging-test python-storage-test go-module-consumer

go-module-consumer:
	./tests/go-module-consumer.sh

go-race:
	go test -race $(GO_PACKAGES)

race: go-race

verify: check test

image-storage:
	docker build --network=host $(DOCKER_GO_ARGS) -f services/storage/Dockerfile -t stellarmesh-storage-service:test .

images: image-storage

integration-storage: python-storage-bootstrap image-storage
	STELLARMESH_STORAGE_TEST_PYTHON=$(ROOT)/$(PYTHON_STORAGE_DIR)/.venv/bin/python ./tests/integration/storage-minio.sh

integration: integration-storage

integration-aws:
	STELLARMESH_STORAGE_AWS_INTEGRATION=1 go test ./sdk/go/objectstorage/s3store -run '^TestAWSManualIntegration$$' -count=1
