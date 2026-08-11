# Every tier has a target, and every target is the same command CI runs.
# A tier that only runs from somebody's shell history is not a gate.

GO      ?= go
PKG     := ./...
COVER   := coverage.out

.DEFAULT_GOAL := help

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'

## test: the unit tier — no Docker, no network, no setup
test:
	$(GO) test -race -timeout=60s $(PKG)

## test-unit: alias for test
test-unit: test

## test-integration: real HTTP stack and a real Postgres in Docker
test-integration:
	$(GO) test -tags=integration -race -timeout=5m ./test/integration/...

## test-e2e: build the binary and drive it as a black box
test-e2e:
	$(GO) test -tags=e2e -timeout=5m ./test/e2e/...

## test-all: every tier, in the order that fails fastest first
test-all: test test-integration test-e2e

## cover: unit coverage as a percentage per package
cover:
	$(GO) test -race -coverprofile=$(COVER) -covermode=atomic $(PKG)
	$(GO) tool cover -func=$(COVER) | tail -20

## cover-html: open the line-by-line coverage report
cover-html: cover
	$(GO) tool cover -html=$(COVER)

## cover-all: coverage across every tier, merged into one profile
cover-all:
	$(GO) test -coverprofile=$(COVER) -covermode=atomic \
		-coverpkg=./internal/...,./cmd/... \
		-tags="integration e2e" $(PKG) ./test/...
	$(GO) tool cover -func=$(COVER) | tail -20

## fuzz: run the property tests for 30s (they run as normal tests otherwise)
fuzz:
	$(GO) test -run=Fuzz -fuzz=FuzzNormaliseCity -fuzztime=30s ./internal/weather

## bench: run benchmarks with allocation counts
bench:
	$(GO) test -run=XXX -bench=. -benchmem $(PKG)

## golden: rewrite the recorded golden files, then review the diff before committing
golden:
	$(GO) test -tags=integration ./test/integration/... -update

## lint: vet plus golangci-lint if it is installed
lint:
	$(GO) vet $(PKG)
	$(GO) vet -tags=integration ./test/...
	$(GO) vet -tags=e2e ./test/...
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint not installed; skipping (see README)"

## fmt: format every file in place
fmt:
	$(GO) fmt $(PKG)

## fmt-check: fail if anything is unformatted — this is the CI form
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

## tidy: prune and verify go.mod
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## ci: exactly what the GitHub workflow runs
ci: fmt-check lint test-all

## clean: remove build and coverage artifacts
clean:
	rm -f $(COVER) weatherd
	$(GO) clean -testcache

.PHONY: help test test-unit test-integration test-e2e test-all cover cover-html \
	cover-all fuzz bench golden lint fmt fmt-check tidy ci clean
