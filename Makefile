GO      ?= go
SQLC    ?= sqlc
BIN     := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)

.PHONY: all build wasm generate test race bench lint db-up db-down check

all: lint test

build:
	$(GO) build -o $(BIN)/sqlc-dynamic ./cmd/sqlc-dynamic

wasm:
	GOOS=wasip1 GOARCH=wasm $(GO) build -o $(BIN)/sqlc-dynamic.wasm ./cmd/sqlc-dynamic
	sha256sum $(BIN)/sqlc-dynamic.wasm

# Regenerate the sqlc and sqlc-dynamic code of the test project and example.
generate: build
	cd internal/testdb && sqlc-dynamic generate -sqlc $(SQLC)
	cd examples/sqlc-pgx && sqlc-dynamic generate -sqlc $(SQLC)

db-up:
	docker compose up -d --wait

db-down:
	docker compose down

test:
	SQLCX_REQUIRE_DB=1 $(GO) test ./...

race:
	SQLCX_REQUIRE_DB=1 $(GO) test -race ./...

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./sqlcx/
	cd bench && $(GO) test -run '^$$' -bench . -benchmem

lint:
	test -z "$$($$($(GO) env GOROOT)/bin/gofmt -l . | tee /dev/stderr)"
	$(GO) vet ./...
	cd bench && $(GO) vet ./...

# Fails when generated code is stale.
check: build
	cd internal/testdb && sqlc-dynamic link -check
	cd examples/sqlc-pgx && sqlc-dynamic link -check
