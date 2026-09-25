SHELL := /bin/sh

ROOT := $(CURDIR)
PYTHON_LOGGING_DIR := sdk/python/logging
PYTHON_STORAGE_DIR := sdk/python/storage
PYTHON_OBJECTSTORAGE_DIR := sdk/python/objectstorage
UV ?= uv
PYTHON ?= python3
NPM ?= npm
FRONTEND_DIR := sdk/frontend
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
	image-storage images integration-storage integration integration-aws \
	frontend-bootstrap frontend-browser-bootstrap frontend-format frontend-check \
	frontend-build frontend-test frontend-browser-test frontend-consumer frontend-verify integration-session \
	docs-check go-doc-check python-logging-artifact-check python-storage-artifact-check

bootstrap: python-logging-bootstrap python-storage-bootstrap python-objectstorage-bootstrap frontend-bootstrap frontend-browser-bootstrap

.PHONY: python-objectstorage-bootstrap python-objectstorage-check python-objectstorage-test python-objectstorage-artifact-check python-objectstorage-consumer integration-objectstorage

python-objectstorage-bootstrap:
	$(UV) sync --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen

python-objectstorage-check: python-objectstorage-bootstrap
	$(UV) lock --project $(PYTHON_OBJECTSTORAGE_DIR) --check
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen ruff check $(PYTHON_OBJECTSTORAGE_DIR)
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen ruff format --check $(PYTHON_OBJECTSTORAGE_DIR)
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen mypy --config-file $(PYTHON_OBJECTSTORAGE_DIR)/pyproject.toml $(PYTHON_OBJECTSTORAGE_DIR)/src $(PYTHON_OBJECTSTORAGE_DIR)/tests
	$(UV) pip check --python $(PYTHON_OBJECTSTORAGE_DIR)/.venv/bin/python

python-objectstorage-test: python-objectstorage-bootstrap
	cd $(PYTHON_OBJECTSTORAGE_DIR) && $(UV) run --frozen pytest

python-objectstorage-artifact-check: python-objectstorage-bootstrap
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen python tests/docs/python_artifact.py --build $(PYTHON_OBJECTSTORAGE_DIR)

python-objectstorage-consumer: python-objectstorage-bootstrap
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen python tests/package-consumer/python-objectstorage/consume.py --build

integration-objectstorage: python-objectstorage-bootstrap
	STELLARMESH_OBJECTSTORAGE_TEST_PYTHON=$(ROOT)/$(PYTHON_OBJECTSTORAGE_DIR)/.venv/bin/python ./tests/integration/objectstorage-rustfs.sh

python-logging-bootstrap:
	$(UV) sync --project $(PYTHON_LOGGING_DIR) --frozen

python-storage-bootstrap:
	$(UV) sync --project $(PYTHON_STORAGE_DIR) --frozen

frontend-bootstrap:
	$(NPM) --prefix $(FRONTEND_DIR) ci

frontend-browser-bootstrap: frontend-bootstrap
	cd $(FRONTEND_DIR) && $(NPM) exec -- playwright install chromium

frontend-format:
	$(NPM) --prefix $(FRONTEND_DIR) run format

frontend-check:
	$(NPM) --prefix $(FRONTEND_DIR) run check

frontend-build:
	$(NPM) --prefix $(FRONTEND_DIR) run build

frontend-test:
	$(NPM) --prefix $(FRONTEND_DIR) test

frontend-browser-test: frontend-build
	$(NPM) --prefix $(FRONTEND_DIR) run test:browser

frontend-consumer: frontend-build
	$(NPM) --prefix $(FRONTEND_DIR) run test:consumer

frontend-verify: frontend-check frontend-test frontend-browser-test frontend-consumer

format: frontend-format
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen ruff check $(PYTHON_OBJECTSTORAGE_DIR) --fix
	$(UV) run --project $(PYTHON_OBJECTSTORAGE_DIR) --frozen ruff format $(PYTHON_OBJECTSTORAGE_DIR)
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
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen mypy $(PYTHON_LOGGING_DIR)/src $(PYTHON_LOGGING_DIR)/tests
	$(UV) pip check --python $(PYTHON_LOGGING_DIR)/.venv/bin/python

python-storage-check: python-storage-bootstrap
	$(UV) lock --project $(PYTHON_STORAGE_DIR) --check
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check $(PYTHON_STORAGE_DIR)
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy $(PYTHON_STORAGE_DIR)/src $(PYTHON_STORAGE_DIR)/tests
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy --strict tests/integration/storage-pipeline.py
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff check tests/docs
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen ruff format --check tests/docs
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen mypy --strict tests/docs
	$(UV) pip check --python $(PYTHON_STORAGE_DIR)/.venv/bin/python

shell-check:
	sh -n tests/integration/rustfs-setup.sh
	sh -n tests/integration/objectstorage-rustfs.sh
	sh -n tests/integration/storage-rustfs.sh
	sh -n tests/integration/gateway-session-redis.sh
	sh -n tests/go-module-consumer.sh
	git diff --check

docs-check:
	$(PYTHON) -m unittest discover -s tests/docs -v
	$(PYTHON) tests/docs/check.py

go-doc-check:
	$(PYTHON) tests/docs/go_doc.py

python-logging-artifact-check: python-logging-bootstrap
	$(UV) run --project $(PYTHON_LOGGING_DIR) --frozen python tests/docs/python_artifact.py --build $(PYTHON_LOGGING_DIR)

python-storage-artifact-check: python-storage-bootstrap
	$(UV) run --project $(PYTHON_STORAGE_DIR) --frozen python tests/docs/python_artifact.py --build $(PYTHON_STORAGE_DIR)

check: docs-check go-doc-check go-check python-logging-check python-storage-check python-objectstorage-check shell-check frontend-check

go-test:
	go test $(GO_PACKAGES)

python-logging-test: python-logging-bootstrap
	cd $(PYTHON_LOGGING_DIR) && $(UV) run --frozen pytest

python-storage-test: python-storage-bootstrap
	cd $(PYTHON_STORAGE_DIR) && $(UV) run --frozen pytest

test: python-logging-artifact-check python-storage-artifact-check python-objectstorage-artifact-check python-objectstorage-consumer go-test python-logging-test python-storage-test python-objectstorage-test go-module-consumer frontend-test frontend-browser-test frontend-consumer

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
	STELLARMESH_STORAGE_TEST_PYTHON=$(ROOT)/$(PYTHON_STORAGE_DIR)/.venv/bin/python ./tests/integration/storage-rustfs.sh

integration-session:
	./tests/integration/gateway-session-redis.sh

integration: integration-storage integration-session integration-objectstorage

integration-aws:
	STELLARMESH_STORAGE_AWS_INTEGRATION=1 go test ./sdk/go/objectstorage/s3store -run '^TestAWSManualIntegration$$' -count=1
