# gRPC Microservice Integration

Topics: Proto Service Definition · gRPC Server

---

## Overview

CodeValdGit is promoted from an importable Go library to a **standalone gRPC microservice**.
CodeValdCortex communicates with it over gRPC instead of importing
`github.com/aosanya/CodeValdGit` as a Go module dependency.

This replaces the original MVP-GIT-009 (Go module wiring) with two sequential tasks:

| Task | Description |
|---|---|
| MVP-GIT-009 | gRPC Service Proto & Codegen |
| MVP-GIT-010 | gRPC Server Implementation |

### Why gRPC?

| Concern | Go Module (original plan) | gRPC Microservice (new plan) |
|---|---|---|
| Deployment coupling | CodeValdGit runs inside CodeValdCortex process | Independent process; independently scalable |
| Language lock-in | Go only | Any gRPC client language |
| Version upgrades | Full CodeValdCortex rebuild | Re-deploy CodeValdGit service only |
| Failure isolation | git failure crashes Cortex | git failure isolated to CodeValdGit pod |
| Resource limits | Shares Cortex CPU/memory | Separate pod limits for git-heavy workloads |
| Contract enforcement | Go interfaces (compile-time) | Proto schema (cross-service boundary) |

---

## MVP-GIT-009 — gRPC Service Proto & Codegen

### Overview

Define the canonical `.proto` file that describes the full CodeValdGit API surface.
Generate Go server stubs (used by the CodeValdGit server in MVP-GIT-010) and Go
client stubs (for any consumer to import).

### Acceptance Criteria

- [ ] `proto/codevaldgit/v1/service.proto` defined with all RPCs
- [ ] `proto/codevaldgit/v1/errors.proto` defines `MergeConflictInfo` detail message
- [ ] `buf.yaml` and `buf.gen.yaml` in repo root for repeatable codegen
- [ ] Generated Go stubs committed under `gen/go/codevaldgit/v1/`
- [ ] `make proto` target in `Makefile` regenerates stubs cleanly
- [ ] All RPCs have request/response messages (no bare `google.protobuf.Empty`)
- [ ] `agency_id` field present in every request message

### Toolchain

Use [`buf`](https://buf.build) for proto linting, breaking-change detection, and codegen.

```bash
# Install
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Generate
buf generate
```

### File Layout

```
proto/
└── codevaldgit/
    └── v1/
        ├── service.proto          # RPC service definition
        └── errors.proto           # MergeConflictInfo detail message
gen/
└── go/
    └── codevaldgit/
        └── v1/
            ├── service.pb.go      # generated message types
            ├── service_grpc.pb.go # generated server/client interfaces
            └── errors.pb.go       # generated MergeConflictInfo
buf.yaml
buf.gen.yaml
```

### `buf.yaml`

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

### `buf.gen.yaml`

```yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen/go
    opt:
      - paths=source_relative
  - remote: buf.build/grpc/go
    out: gen/go
    opt:
      - paths=source_relative
```

### Proto Service Definition

```protobuf
syntax = "proto3";

package codevaldgit.v1;

option go_package = "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1;codevaldgitv1";

import "google/protobuf/timestamp.proto";

// RepoService is the single gRPC service exposed by CodeValdGit.
// All operations are stateless: agency_id is passed in every request.
// Task branches are always "task/{task_id}" — the proto takes task_id only.
service RepoService {

  // ── Lifecycle ──────────────────────────────────────────────────────────────

  // InitRepo creates a new empty Git repository for an agency.
  // Error: ALREADY_EXISTS if a live repo exists for agency_id.
  rpc InitRepo(InitRepoRequest) returns (InitRepoResponse);

  // DeleteRepo archives/flags the agency repo as deleted (non-destructive).
  // Error: NOT_FOUND if no live repo exists.
  rpc DeleteRepo(DeleteRepoRequest) returns (DeleteRepoResponse);

  // PurgeRepo permanently removes all Git storage for the agency.
  // Irreversible. Error: NOT_FOUND if target does not exist.
  rpc PurgeRepo(PurgeRepoRequest) returns (PurgeRepoResponse);

  // ── Branch Operations ──────────────────────────────────────────────────────

  // CreateBranch creates refs/heads/task/{task_id} from current main HEAD.
  // Error: ALREADY_EXISTS if the branch exists; NOT_FOUND if repo missing.
  rpc CreateBranch(CreateBranchRequest) returns (CreateBranchResponse);

  // MergeBranch merges task/{task_id} into main (fast-forward or auto-rebase).
  // Error: ABORTED with MergeConflictInfo detail on content conflict.
  // Error: NOT_FOUND if task branch or repo does not exist.
  rpc MergeBranch(MergeBranchRequest) returns (MergeBranchResponse);

  // DeleteBranch removes refs/heads/task/{task_id}.
  // Error: NOT_FOUND if the branch or repo does not exist.
  rpc DeleteBranch(DeleteBranchRequest) returns (DeleteBranchResponse);

  // ── File Operations ────────────────────────────────────────────────────────

  // WriteFile commits content to path on task/{task_id}.
  // Branch must already exist (call CreateBranch first).
  // Error: NOT_FOUND if branch or repo does not exist.
  rpc WriteFile(WriteFileRequest) returns (WriteFileResponse);

  // ReadFile returns the content of path at the given ref.
  // ref may be a branch name, tag name, or full commit SHA.
  // Error: NOT_FOUND if ref or path does not exist; NOT_FOUND if repo missing.
  rpc ReadFile(ReadFileRequest) returns (ReadFileResponse);

  // DeleteFile removes path from task/{task_id} as a new commit.
  // Error: NOT_FOUND if branch, path, or repo does not exist.
  rpc DeleteFile(DeleteFileRequest) returns (DeleteFileResponse);

  // ListDirectory returns immediate children of path at the given ref.
  // An empty path lists the repository root.
  // Error: NOT_FOUND if ref or repo does not exist.
  rpc ListDirectory(ListDirectoryRequest) returns (ListDirectoryResponse);

  // ── History ────────────────────────────────────────────────────────────────

  // Log returns commits that touched path, newest first.
  // An empty path returns the full commit history from ref.
  // Error: NOT_FOUND if ref or repo does not exist.
  rpc Log(LogRequest) returns (LogResponse);

  // Diff returns per-file changes between two refs.
  // Error: NOT_FOUND if either ref or repo does not exist.
  rpc Diff(DiffRequest) returns (DiffResponse);
}

// ── Request / Response Messages ──────────────────────────────────────────────

message InitRepoRequest   { string agency_id = 1; }
message InitRepoResponse  {}

message DeleteRepoRequest { string agency_id = 1; }
message DeleteRepoResponse {}

message PurgeRepoRequest  { string agency_id = 1; }
message PurgeRepoResponse {}

message CreateBranchRequest {
  string agency_id = 1;
  string task_id   = 2;
}
message CreateBranchResponse {}

message MergeBranchRequest {
  string agency_id = 1;
  string task_id   = 2;
}
message MergeBranchResponse {}

message DeleteBranchRequest {
  string agency_id = 1;
  string task_id   = 2;
}
message DeleteBranchResponse {}

message WriteFileRequest {
  string agency_id = 1;
  string task_id   = 2;
  string path      = 3;
  string content   = 4;
  string author    = 5;
  string message   = 6;
}
message WriteFileResponse {}

message ReadFileRequest {
  string agency_id = 1;
  string ref       = 2;
  string path      = 3;
}
message ReadFileResponse {
  string content = 1;
}

message DeleteFileRequest {
  string agency_id = 1;
  string task_id   = 2;
  string path      = 3;
  string author    = 4;
  string message   = 5;
}
message DeleteFileResponse {}

message ListDirectoryRequest {
  string agency_id = 1;
  string ref       = 2;
  string path      = 3;
}
message ListDirectoryResponse {
  repeated FileEntry entries = 1;
}

message FileEntry {
  string name   = 1;
  string path   = 2;
  bool   is_dir = 3;
  int64  size   = 4;
}

message LogRequest {
  string agency_id = 1;
  string ref       = 2;
  string path      = 3;
}
message LogResponse {
  repeated CommitInfo commits = 1;
}

message CommitInfo {
  string                    sha       = 1;
  string                    author    = 2;
  string                    message   = 3;
  google.protobuf.Timestamp timestamp = 4;
}

message DiffRequest {
  string agency_id = 1;
  string from_ref  = 2;
  string to_ref    = 3;
}
message DiffResponse {
  repeated FileDiff diffs = 1;
}

message FileDiff {
  string path      = 1;
  string operation = 2;  // "add" | "modify" | "delete"
  string patch     = 3;
}
```

### `errors.proto` — Merge Conflict Detail

```protobuf
syntax = "proto3";

package codevaldgit.v1;

option go_package = "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1;codevaldgitv1";

// MergeConflictInfo is packed into a google.rpc.Status detail field
// when MergeBranch returns codes.Aborted due to a content conflict.
// Clients unpack this from status.Details() to get the file list.
message MergeConflictInfo {
  string          task_id          = 1;
  repeated string conflicting_files = 2;
}
```

### gRPC Error Code Mapping

| Go Error | gRPC `codes` | Notes |
|---|---|---|
| `ErrRepoNotFound` | `codes.NotFound` | |
| `ErrRepoAlreadyExists` | `codes.AlreadyExists` | |
| `ErrBranchNotFound` | `codes.NotFound` | |
| `ErrBranchExists` | `codes.AlreadyExists` | |
| `ErrFileNotFound` | `codes.NotFound` | |
| `ErrRefNotFound` | `codes.NotFound` | |
| `*ErrMergeConflict` | `codes.Aborted` | Pack `MergeConflictInfo` into `status.Details()` |
| Any other error | `codes.Internal` | Log server-side; return generic message to client |

Implement a shared `mapError(err error) error` helper in the server that converts
all Go errors to the correct `status.Error` or `status.Errorf` call before returning.

---

## MVP-GIT-010 — gRPC Server Implementation

### Overview

Add a gRPC server entrypoint to CodeValdGit so it can run as a standalone service.
The server wraps the existing `RepoManager` and `Repo` interfaces in generated gRPC
handler implementations, maps Go errors to gRPC status codes, and exposes the standard
gRPC health protocol.

### Acceptance Criteria

- [ ] `cmd/server/main.go` starts a gRPC listener; port configurable via `CODEVALDGIT_PORT` (default `50051`)
- [ ] `internal/grpcserver/server.go` implements `RepoServiceServer` using `RepoManager`/`Repo`
- [ ] All error types correctly mapped to gRPC status codes (see error table in MVP-GIT-009)
- [ ] `MergeBranch` packs `MergeConflictInfo` into `status.Details()` on conflict
- [ ] `grpc.health.v1.Health/Check` responds `SERVING` when ready
- [ ] Graceful shutdown on `SIGTERM` / `SIGINT` (drain in-flight RPCs, default 30 s)
- [ ] `Dockerfile.server` builds a minimal image (~20 MB with `scratch` or `distroless`)
- [ ] Backend selected via `CODEVALDGIT_BACKEND` env var (`filesystem` | `arangodb`)
- [ ] Reflection enabled in non-production builds for `grpcurl` / `grpc-client-cli` debugging

### File Layout

```
cmd/
└── server/
    └── main.go              # entrypoint: parse config, init backend, start gRPC server
internal/
└── grpcserver/
    ├── server.go            # RepoServiceServer implementation
    ├── errors.go            # mapError(err error) → gRPC status
    └── server_test.go       # unit tests with mock RepoManager
Dockerfile.server            # multi-stage build for the server binary
```

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `CODEVALDGIT_PORT` | `50051` | gRPC listener port |
| `CODEVALDGIT_BACKEND` | `filesystem` | Storage backend (`filesystem` or `arangodb`) |
| `CODEVALDGIT_FS_BASE` | `/data/repos` | Base path for filesystem backend |
| `CODEVALDGIT_FS_ARCHIVE` | `/data/archive` | Archive path for filesystem backend |
| `ARANGODB_URL` | — | ArangoDB URL (arangodb backend only) |
| `ARANGODB_USER` | `root` | ArangoDB user |
| `ARANGODB_PASS` | — | ArangoDB password |
| `ARANGODB_DB` | `cortex` | ArangoDB database name |
| `CODEVALDGIT_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |

### Server Skeleton

```go
// internal/grpcserver/server.go
package grpcserver

import (
    "context"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    codevaldgit "github.com/aosanya/CodeValdGit"
    pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

type Server struct {
    pb.UnimplementedRepoServiceServer
    mgr codevaldgit.RepoManager
}

func New(mgr codevaldgit.RepoManager) *Server {
    return &Server{mgr: mgr}
}

func (s *Server) InitRepo(ctx context.Context, req *pb.InitRepoRequest) (*pb.InitRepoResponse, error) {
    if err := s.mgr.InitRepo(ctx, req.AgencyId); err != nil {
        return nil, mapError(err)
    }
    return &pb.InitRepoResponse{}, nil
}

func (s *Server) MergeBranch(ctx context.Context, req *pb.MergeBranchRequest) (*pb.MergeBranchResponse, error) {
    repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
    if err != nil {
        return nil, mapError(err)
    }
    if err := repo.MergeBranch(ctx, req.TaskId); err != nil {
        return nil, mapError(err)
    }
    return &pb.MergeBranchResponse{}, nil
}

// ... remaining methods follow the same pattern
```

```go
// internal/grpcserver/errors.go
package grpcserver

import (
    "errors"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/types/known/anypb"

    codevaldgit "github.com/aosanya/CodeValdGit"
    pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

func mapError(err error) error {
    if err == nil {
        return nil
    }
    var conflict *codevaldgit.ErrMergeConflict
    if errors.As(err, &conflict) {
        detail, _ := anypb.New(&pb.MergeConflictInfo{
            TaskId:           conflict.TaskID,
            ConflictingFiles: conflict.ConflictingFiles,
        })
        st, _ := status.New(codes.Aborted, "merge conflict").WithDetails(detail)
        return st.Err()
    }
    switch {
    case errors.Is(err, codevaldgit.ErrRepoNotFound),
         errors.Is(err, codevaldgit.ErrBranchNotFound),
         errors.Is(err, codevaldgit.ErrFileNotFound),
         errors.Is(err, codevaldgit.ErrRefNotFound):
        return status.Error(codes.NotFound, err.Error())
    case errors.Is(err, codevaldgit.ErrRepoAlreadyExists),
         errors.Is(err, codevaldgit.ErrBranchExists):
        return status.Error(codes.AlreadyExists, err.Error())
    default:
        return status.Error(codes.Internal, "internal error")
    }
}
```

### `Dockerfile.server`

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /codevaldgit-server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /codevaldgit-server /codevaldgit-server
EXPOSE 50051
ENTRYPOINT ["/codevaldgit-server"]
```

### Health Check

Register the standard gRPC health service alongside `RepoService`:

```go
import (
    "google.golang.org/grpc/health"
    "google.golang.org/grpc/health/grpc_health_v1"
)

healthSrv := health.NewServer()
grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
```

### Graceful Shutdown

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit
log.Println("shutting down...")
grpcServer.GracefulStop()
```

### Testing

| Test | Strategy |
|---|---|
| `TestServer_InitRepo` | Mock `RepoManager` returns nil → expect `InitRepoResponse{}` |
| `TestServer_InitRepo_AlreadyExists` | Mock returns `ErrRepoAlreadyExists` → expect `codes.AlreadyExists` |
| `TestServer_MergeBranch_Conflict` | Mock returns `*ErrMergeConflict` → expect `codes.Aborted` + `MergeConflictInfo` detail |
| `TestServer_ErrorMapping` | Table-driven: all 7 error types → correct gRPC codes |
| Integration | Start real server with ArangoDB backend; run lifecycle sequence |



### Overview

Replace CodeValdCortex's `internal/git/` packages with a thin gRPC client adapter.
The adapter implements a local `GitClient` interface (defined in CodeValdCortex) using
the generated gRPC stubs published from `github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1`.
CodeValdCortex's Agency and Task services call the adapter; they never import the main
`codevaldgit` package (no go-git dependency in Cortex).

### Acceptance Criteria

- [ ] `internal/git/` fully deleted from CodeValdCortex
- [ ] `go.mod` in CodeValdCortex references ONLY `github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1` (not the root module — import path is the generated package)
- [ ] `internal/gitclient/` package wraps `pb.RepoServiceClient` in a thin typed adapter
- [ ] Agency service calls `InitRepo` on create, `DeleteRepo` on delete
- [ ] Task service calls `CreateBranch` on start, `MergeBranch`+`DeleteBranch` on complete
- [ ] Agent handler calls `WriteFile` on every file write
- [ ] `MergeBranch` conflict response (gRPC `codes.Aborted` + `MergeConflictInfo`) is unpacked and routed as a conflict event
- [ ] `git_objects`, `git_refs`, `repositories` ArangoDB collections dropped via migration script
- [ ] All integration tests pass; no regressions in CodeValdCortex test suite

### Dependency Change in CodeValdCortex

CodeValdCortex adds the generated stubs package (contains only proto-generated types
and the gRPC client interface — no go-git, no billy, no ArangoDB driver):

```bash
# In CodeValdCortex
go get github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1
```

> **Note**: The generated package `gen/go/codevaldgit/v1` is a sub-path of the same
> Go module (`github.com/aosanya/CodeValdGit`). CodeValdCortex imports only this
> sub-package; it does not use the top-level `codevaldgit` package and gains no
> dependency on go-git or billy.

### `internal/gitclient/` Adapter

```go
// internal/gitclient/client.go
package gitclient

import (
    "context"
    "errors"

    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/types/known/anypb"

    pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// Client wraps the generated gRPC client and maps proto responses back
// to typed errors matching the CodeValdCortex domain.
type Client struct {
    pb pb.RepoServiceClient
}

// New dials the CodeValdGit service and returns a Client.
func New(conn *grpc.ClientConn) *Client {
    return &Client{pb: pb.NewRepoServiceClient(conn)}
}

func (c *Client) InitRepo(ctx context.Context, agencyID string) error {
    _, err := c.pb.InitRepo(ctx, &pb.InitRepoRequest{AgencyId: agencyID})
    return mapStatus(err)
}

func (c *Client) CreateBranch(ctx context.Context, agencyID, taskID string) error {
    _, err := c.pb.CreateBranch(ctx, &pb.CreateBranchRequest{AgencyId: agencyID, TaskId: taskID})
    return mapStatus(err)
}

func (c *Client) MergeBranch(ctx context.Context, agencyID, taskID string) ([]string, error) {
    _, err := c.pb.MergeBranch(ctx, &pb.MergeBranchRequest{AgencyId: agencyID, TaskId: taskID})
    if err != nil {
        st, ok := status.FromError(err)
        if ok && st.Code() == codes.Aborted {
            for _, detail := range st.Details() {
                if info, ok := detail.(*pb.MergeConflictInfo); ok {
                    return info.ConflictingFiles, ErrMergeConflict
                }
            }
        }
        return nil, mapStatus(err)
    }
    return nil, nil
}

// ErrMergeConflict is returned by MergeBranch when CodeValdGit signals a conflict.
var ErrMergeConflict = errors.New("merge conflict")

// ErrNotFound wraps all gRPC NOT_FOUND responses.
var ErrNotFound = errors.New("not found")

func mapStatus(err error) error {
    if err == nil {
        return nil
    }
    st, ok := status.FromError(err)
    if !ok {
        return err
    }
    switch st.Code() {
    case codes.NotFound:
        return fmt.Errorf("%w: %s", ErrNotFound, st.Message())
    case codes.AlreadyExists:
        return fmt.Errorf("already exists: %s", st.Message())
    default:
        return err
    }
}
```

### Agency Service Wiring

```go
// internal/agency/service.go
type Service struct {
    db         Database
    gitClient  *gitclient.Client  // ← replaces internal/git dependency
}

func (s *Service) CreateAgency(ctx context.Context, agency Agency) error {
    if err := s.db.InsertAgency(ctx, agency); err != nil {
        return err
    }
    return s.gitClient.InitRepo(ctx, agency.ID)
}

func (s *Service) DeleteAgency(ctx context.Context, agencyID string) error {
    if err := s.db.DeleteAgency(ctx, agencyID); err != nil {
        return err
    }
    return s.gitClient.DeleteRepo(ctx, agencyID)
}
```

### Task Service Wiring

```go
// internal/task/service.go
func (s *Service) StartTask(ctx context.Context, task Task) error {
    if err := s.db.InsertTask(ctx, task); err != nil {
        return err
    }
    return s.gitClient.CreateBranch(ctx, task.AgencyID, task.ID)
}

func (s *Service) CompleteTask(ctx context.Context, taskID, agencyID string) error {
    conflicting, err := s.gitClient.MergeBranch(ctx, agencyID, taskID)
    if errors.Is(err, gitclient.ErrMergeConflict) {
        return s.routeConflictToAgent(ctx, taskID, conflicting)
    }
    if err != nil {
        return err
    }
    return s.gitClient.DeleteBranch(ctx, agencyID, taskID)
}
```

### Connection Setup in `cmd/main.go`

```go
// In cmd/main.go
conn, err := grpc.NewClient(
    cfg.CodeValdGit.Addr,  // e.g. "codevaldgit:50051"
    grpc.WithTransportCredentials(insecure.NewCredentials()), // or TLS in prod
)
if err != nil {
    log.Fatalf("connecting to codevaldgit: %v", err)
}
defer conn.Close()

gitClient := gitclient.New(conn)

agencyService := agency.NewService(agencyDB, gitClient)
taskService   := task.NewService(taskDB, gitClient)
```

### `config.yaml` Changes in CodeValdCortex

```yaml
codevaldgit:
  addr: "codevaldgit:50051"    # service discovery via Docker/k8s DNS
  # tls: true                  # enable for production mTLS
```

### `docker-compose.yml` Changes in CodeValdCortex

```yaml
services:
  codevaldgit:
    image: ghcr.io/aosanya/codevaldgit-server:latest
    environment:
      CODEVALDGIT_BACKEND: arangodb
      ARANGODB_URL: http://arangodb:8529
      ARANGODB_USER: root
      ARANGODB_PASS: rootpassword
      ARANGODB_DB: cortex
    ports:
      - "50051:50051"
    healthcheck:
      test: ["CMD", "grpc_health_probe", "-addr=:50051"]
      interval: 10s
      timeout: 5s
      retries: 3
    depends_on:
      arangodb:
        condition: service_healthy

  cortex:
    # ... existing config ...
    environment:
      CODEVALDGIT_ADDR: "codevaldgit:50051"
    depends_on:
      codevaldgit:
        condition: service_healthy
```

### Files to Delete in CodeValdCortex

```
internal/git/ops/operations.go
internal/git/storage/repository.go
internal/git/fileindex/service.go
internal/git/fileindex/repository.go
internal/git/models/
```

### ArangoDB Legacy Collection Migration

```bash
#!/bin/bash
# scripts/migrate-drop-legacy-git-collections.sh
# Run AFTER verifying CodeValdGit service is working.
set -e
ARANGO_URL="${ARANGO_URL:-http://localhost:8529}"
DB="${ARANGO_DB:-cortex}"
for collection in git_objects git_refs repositories; do
    echo "Dropping $collection from $DB..."
    curl -s -X DELETE "${ARANGO_URL}/_db/${DB}/_api/collection/${collection}" \
        -u "${ARANGO_USER}:${ARANGO_PASS}" | jq .
done
echo "Done."
```

### Integration Test Plan

| Test | Coverage |
|---|---|
| `TestAgencyCreate_CallsInitRepo` | gRPC `InitRepo` called with correct `agency_id` |
| `TestAgencyDelete_CallsDeleteRepo` | gRPC `DeleteRepo` called |
| `TestTaskStart_CallsCreateBranch` | gRPC `CreateBranch` called with `agency_id` + `task_id` |
| `TestTaskComplete_CallsMergeThenDelete` | gRPC `MergeBranch` then `DeleteBranch` on success |
| `TestTaskComplete_ConflictRouted` | `codes.Aborted` + `MergeConflictInfo` → conflict event published |
| `TestEndToEnd_WithRealServer` | Docker-compose: full agency→task→write→complete cycle |

### Dependencies

- MVP-GIT-009 (proto + generated stubs must be committed)
- MVP-GIT-010 (server running for integration tests)

### Known Risks

| Risk | Mitigation |
|---|---|
| Network latency for every git operation | Operations are async to agent writes; latency is acceptable; add connection pooling if needed |
| gRPC connection failure crashes Cortex | Wrap client calls with retry + circuit breaker (`grpc.WithDefaultServiceConfig`) |
| Breaking proto changes | `buf breaking` CI check prevents accidental wire-format breaks |
| go-git in-memory state lost on server restart | ArangoDB backend (MVP-GIT-008) resolves this; restart-safe by design |
| Multiple Cortex replicas → same CodeValdGit server | Write operations are per-task-branch; isolation is at branch level — safe for MVP |
