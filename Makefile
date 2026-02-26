.PHONY: build build-server run-server restart kill proto test cover vet lint clean

# ── Build ─────────────────────────────────────────────────────────────────────

## Verify the module compiles cleanly.
build:
	go build ./...

## Build the service binary to bin/codevaldgit.
build-server:
	go build -o bin/codevaldgit ./cmd/...

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
	./bin/codevaldgit

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
