.PHONY: build test test-arango test-all vet lint clean

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	go build ./...

# ── Tests ─────────────────────────────────────────────────────────────────────

## Run all unit tests (skips integration tests that need ArangoDB)
test:
	go test -v -race -count=1 ./...

## Run ArangoDB integration tests.
## Loads .env if it exists, otherwise falls back to environment variables.
## Usage: make test-arango
##        ARANGODB_URL=http://host:8529 ARANGODB_USER=root ARANGODB_PASS=pw make test-arango
test-arango:
	@if [ -f .env ]; then \
		set -a && . ./.env && set +a; \
	fi; \
	go test -v -race -count=1 ./storage/arangodb/

## Run everything: unit tests + ArangoDB integration tests
test-all:
	@if [ -f .env ]; then \
		set -a && . ./.env && set +a; \
	fi; \
	go test -v -race -count=1 ./...

# ── Quality ───────────────────────────────────────────────────────────────────

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	go clean ./...
	rm -f coverage.out coverage.html
