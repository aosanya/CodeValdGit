.PHONY: build build-server run-server restart kill proto test cover vet lint clean

export PATH := /usr/local/go/bin:$(PATH)

# ── Build ─────────────────────────────────────────────────────────────────────

## Verify the module compiles cleanly.
build:
	go build ./...

## Build the service binary to bin/codevaldgit.
build-server:
	go build -o bin/codevaldgit ./cmd
	go build -o bin/codevaldgit-server ./cmd/server

## Build and run the service.
## Override config with env vars or a .env file.
run-server: build-server
	@if [ -f .env ]; then \
		set -a && . ./.env && set +a; \
	fi; \
	./bin/codevaldgit

## Stop any running instance, rebuild, and run.
restart: kill build-server
	@echo "Running codevaldgit..."
	@if [ -f .env ]; then \
		set -a && . ./.env && set +a; \
	fi; \
	./bin/codevaldgit-server

## Stop any running instances of codevaldgit.
kill:
	@echo "Stopping any running instances..."
	-@pkill -9 -f "bin/codevaldgit" 2>/dev/null || true
	@sleep 1

# ── Proto Codegen ─────────────────────────────────────────────────────────────

## Regenerate Go stubs from proto/codevaldgit/v1/*.proto.
## Requires: buf, protoc-gen-go, protoc-gen-go-grpc on PATH.
## Install: go install github.com/bufbuild/buf/cmd/buf@latest
##          go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
##          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
proto:
	buf generate

# ── Tests ─────────────────────────────────────────────────────────────────────

## Run all unit tests with race detector.
test:
	go test -v -race -count=1 ./...

## Run tests and produce an HTML coverage report (coverage.html).
cover:
	go test -v -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# ── Quality ───────────────────────────────────────────────────────────────────

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	go clean ./...
	rm -rf bin/
	rm -f coverage.out coverage.html
.PHONY: build build-server proto test test-arango test-all vet lint clean

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	go build ./...

## Build the standalone gRPC server binary to bin/codevaldgit-server.
build-server:
	go build -o bin/codevaldgit-server ./cmd/server

## Run the gRPC server locally using the filesystem backend (default).
## Override backend:  CODEVALDGIT_BACKEND=arangodb make run-server
## ArangoDB vars can be placed in a .env file (loaded automatically).
run-server: build-server
	@if [ -f .env ]; then \
		set -a && . ./.env && set +a; \
	fi; \
	CODEVALDGIT_PORT=$${CODEVALDGIT_PORT:-50052} \
	CODEVALDGIT_BACKEND=$${CODEVALDGIT_BACKEND:-filesystem} \
	./bin/codevaldgit-server

# ── Proto Codegen ─────────────────────────────────────────────────────────────

## Regenerate Go stubs from proto/codevaldgit/v1/*.proto.
## Requires: buf, protoc-gen-go, protoc-gen-go-grpc on PATH.
## Install: go install github.com/bufbuild/buf/cmd/buf@latest
##          go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
##          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
proto:
	buf generate

# ── Tests ─────────────────────────────────────────────────────────────────────

## Run all unit tests (skips integration tests that need ArangoDB)
test:
	go test -v -race -count=1 ./...

## Run ArangoDB integration tests.
## Loads .env if it exists, otherwise falls back to environment variables.
## Usage: make test-arango
##        GIT_ARANGO_ENDPOINT=http://host:8529 GIT_ARANGO_USER=root GIT_ARANGO_PASSWORD=pw make test-arango
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
