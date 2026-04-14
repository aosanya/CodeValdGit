# gRPC Service: Proto Definition & Codegen (MVP-GIT-009)

Topics: Proto Service Definition · Buf Toolchain · Error Proto

---

## Overview

Define the canonical `.proto` file that describes the full CodeValdGit API
surface. Generate Go server stubs (used by the CodeValdGit server in
MVP-GIT-010) and Go client stubs (for any consumer to import).

### Why gRPC?

| Concern | Go Module (original plan) | gRPC Microservice (new plan) |
|---|---|---|
| Deployment coupling | CodeValdGit runs inside CodeValdCross process | Independent process; independently scalable |
| Language lock-in | Go only | Any gRPC client language |
| Version upgrades | Full CodeValdCross rebuild | Re-deploy CodeValdGit service only |
| Failure isolation | git failure crashes CodeValdCross | git failure isolated to CodeValdGit pod |
| Resource limits | Shares CodeValdCross CPU/memory | Separate pod limits for git-heavy workloads |
| Contract enforcement | Go interfaces (compile-time) | Proto schema (cross-service boundary) |

---

## Acceptance Criteria

- [ ] `proto/codevaldgit/v1/service.proto` defined with all RPCs
- [ ] `proto/codevaldgit/v1/errors.proto` defines `MergeConflictInfo` detail message
- [ ] `buf.yaml` and `buf.gen.yaml` in repo root for repeatable codegen
- [ ] Generated Go stubs committed under `gen/go/codevaldgit/v1/`
- [ ] `make proto` target in `Makefile` regenerates stubs cleanly
- [ ] All RPCs have request/response messages (no bare `google.protobuf.Empty`)
- [ ] `agency_id` field present in every request message

---

## Toolchain

Use [`buf`](https://buf.build) for proto linting, breaking-change detection,
and codegen.

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

---

## Proto Service Definition

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

---

## `errors.proto` — Merge Conflict Detail

```protobuf
syntax = "proto3";

package codevaldgit.v1;

option go_package = "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1;codevaldgitv1";

// MergeConflictInfo is packed into a google.rpc.Status detail field
// when MergeBranch returns codes.Aborted due to a content conflict.
// Clients unpack this from status.Details() to get the file list.
message MergeConflictInfo {
  string          task_id           = 1;
  repeated string conflicting_files = 2;
}
```

---

## gRPC Error Code Mapping

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
all Go errors to the correct `status.Error` or `status.Errorf` call before
returning.
