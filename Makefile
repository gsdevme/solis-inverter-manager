BINARY := solis-inverter-manager
BIN_DIR := bin
PKG     := ./cmd

GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint

PY        := python3
SIDECAR    := sidecar

.PHONY: build vet lint test test-e2e run \
        sidecar-install sidecar-run sidecar-test sidecar-lint

## build: compile the binary into ./bin
build:
	go build -o $(BIN_DIR)/$(BINARY) $(PKG)

## vet: run go vet across all packages
vet:
	go vet ./...

## test: run unit + integration tests (excludes the godog features suite)
test:
	go test $(shell go list ./... | grep -v /features)

## test-e2e: run the godog acceptance suite
test-e2e:
	go test ./features/...

## run: build then run the poll -> sidecar -> MQTT manager (respects MODE from .env)
run: build
	./$(BIN_DIR)/$(BINARY) serve

$(GOLANGCI_LINT):
	GOBIN=$(abspath $(BIN_DIR)) go install \
	  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

## lint: run golangci-lint (installs the pinned binary into ./bin on first use)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

## sidecar-install: install the sidecar dev deps (pysolarmanv5, pytest, ruff)
sidecar-install:
	$(PY) -m pip install -r $(SIDECAR)/requirements-dev.txt

## sidecar-run: run the sidecar against the Phase 0 fixtures (no hardware)
sidecar-run:
	MODE=mock $(PY) -m sidecar

## sidecar-test: run the sidecar pytest suite
sidecar-test:
	$(PY) -m pytest $(SIDECAR)

## sidecar-lint: lint + format-check the sidecar package with ruff
sidecar-lint:
	$(PY) -m ruff check $(SIDECAR)
	$(PY) -m ruff format --check $(SIDECAR)
