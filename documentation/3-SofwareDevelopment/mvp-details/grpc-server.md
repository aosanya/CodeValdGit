# gRPC Service: Server Implementation (MVP-GIT-010)

Topics: gRPC Server · Error Mapping · Dockerfile · Health Check · Graceful Shutdown

---

## Overview

Add a gRPC server entrypoint to CodeValdGit so it can run as a standalone
service. The server wraps the existing `RepoManager` and `Repo` interfaces in
generated gRPC handler implementations, maps Go errors to gRPC status codes,
and exposes the standard gRPC health protocol.

---

## Acceptance Criteria

- [ ] `cmd/server/main.go` starts a gRPC listener; port configurable via `CODEVALDGIT_PORT` (default `50051`)
- [ ] `internal/grpcserver/server.go` implements `RepoServiceServer` using `RepoManager`/`Repo`
- [ ] All error types correctly mapped to gRPC status codes (see error table in `grpc-proto.md`)
- [ ] `MergeBranch` packs `MergeConflictInfo` into `status.Details()` on conflict
- [ ] `grpc.health.v1.Health/Check` responds `SERVING` when ready
- [ ] Graceful shutdown on `SIGTERM` / `SIGINT` (drain in-flight RPCs, default 30 s)
- [ ] `Dockerfile.server` builds a minimal image (~20 MB with `scratch` or `distroless`)
- [ ] Backend selected via `CODEVALDGIT_BACKEND` env var (`filesystem` | `arangodb`)
- [ ] Reflection enabled in non-production builds for `grpcurl` / `grpc-client-cli` debugging

---

## File Layout

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

---

## Environment Variables

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

---

## Server Skeleton

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

---

## Error Mapping

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

---

## `Dockerfile.server`

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

---

## Health Check

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

---

## Graceful Shutdown

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
<-quit
log.Println("shutting down...")
grpcServer.GracefulStop()
```

---

## Testing

| Test | Strategy |
|---|---|
| `TestServer_InitRepo` | Mock `RepoManager` returns nil → expect `InitRepoResponse{}` |
| `TestServer_InitRepo_AlreadyExists` | Mock returns `ErrRepoAlreadyExists` → expect `codes.AlreadyExists` |
| `TestServer_MergeBranch_Conflict` | Mock returns `*ErrMergeConflict` → expect `codes.Aborted` + `MergeConflictInfo` detail |
| `TestServer_ErrorMapping` | Table-driven: all 7 error types → correct gRPC codes |
| Integration | Start real server with ArangoDB backend; run lifecycle sequence |
