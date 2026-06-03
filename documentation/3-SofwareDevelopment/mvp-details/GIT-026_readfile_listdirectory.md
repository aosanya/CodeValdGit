# GIT-026 — `ReadFile` and `ListDirectory` absent from CodeValdGit v1

**Status:** 📋 Not Started
**Severity:** High — AI agents write files via `WriteFile` but have no read-back path; agents cannot verify their own output, inspect existing source, or confirm a file exists before overwriting
**Owner:** CodeValdGit
**Estimated effort:** ~1 day (proto + impl + HTTP bindings + tests)
**Source finding:** Platform gap analysis (2026-06-03) — write-only interface identified as blocker for agent-inspection and frontend file browsers

---

## Problem

CodeValdGit v1 exposes `WriteFile` and `DeleteFile` but no corresponding `ReadFile` or
`ListDirectory` RPC. This creates an asymmetric, write-only interface:

- AI agents cannot read back files they just wrote (no post-write verification)
- Code-inspection tasks cannot fetch source files via the entity-graph API
- Compile jobs fall back to `git clone` via Smart HTTP instead of the gRPC surface, adding
  an extra network hop and a dependency on the Smart HTTP layer being healthy
- Frontend file browsers have no gRPC path to render repository contents

## Goal

Add two RPCs to `GitService`:

1. **`ReadFile`** — returns the raw bytes of a file at a given path on a named branch
2. **`ListDirectory`** — returns names + kinds (file / dir) for all direct children at a given
   path prefix on a named branch

Both operations always read the tip commit of the named branch (no historical SHA reads in v1 scope).

## Proposed proto API

```proto
// Read the content of a single file at the tip of a branch.
message ReadFileRequest {
  string agency_id  = 1;
  string repo_id    = 2;
  string branch_id  = 3;
  string path       = 4;  // e.g. "src/main.dart"
}

message ReadFileResponse {
  bytes  content      = 1;
  string commit_id    = 2;  // tip commit SHA at time of read
  string content_type = 3;  // detected MIME type (best-effort)
}

// List direct children of a directory at the tip of a branch.
message ListDirectoryRequest {
  string agency_id  = 1;
  string repo_id    = 2;
  string branch_id  = 3;
  string path       = 4;  // empty string = repo root
}

message ListDirectoryResponse {
  repeated DirectoryEntry entries = 1;
}

message DirectoryEntry {
  string name       = 1;
  string path       = 2;  // full path from repo root
  enum Kind {
    FILE = 0;
    DIR  = 1;
  }
  Kind   kind       = 3;
  int64  size_bytes = 4;  // populated for FILE entries only
}
```

## Fix plan

### Phase 1 — Proto

1. Add the four messages and `DirectoryEntry` enum to `proto/codevaldgit/v1/git.proto`
2. Add `ReadFile` and `ListDirectory` RPCs to `service GitService`
3. Run `make proto` to regenerate `gen/go/`

### Phase 2 — Interface & implementation

1. Add `ReadFile(ctx context.Context, branchID, path string) ([]byte, string, error)` and
   `ListDirectory(ctx context.Context, branchID, path string) ([]DirectoryEntry, error)` to
   the `GitManager` interface in `git.go`
2. Implement both in `git_impl_fileops.go`:
   - Resolve branch tip commit SHA from entity graph (`DataManager.GetEntity` on the `Branch` node)
   - Use `go-git` memory-storer / the existing `arangoStorer` to walk the object tree to `path`
   - `ReadFile`: return the blob bytes + commit SHA
   - `ListDirectory`: enumerate immediate tree entries, return name/path/kind/size
3. Wire gRPC handlers in `internal/server/server.go`
4. Register HTTP route bindings in `internal/registrar/registrar.go`:
   - `GET /git/{agencyId}/repos/{repoName}/branches/{branchId}/files/{path}` → `ReadFile`
   - `GET /git/{agencyId}/repos/{repoName}/branches/{branchId}/dirs/{path}` → `ListDirectory`

### Phase 3 — Tests

1. Unit tests: extend `fakeDataManager` / `fakeGitManager` stubs for both methods
2. Integration tests (ArangoDB):
   - Write a file, read it back, assert bytes match
   - Write two files to the same directory, list that directory, assert both appear with correct kinds
   - List a non-existent path, assert `ErrFileNotFound` (or equivalent)

## Verification

After implementation:

1. `make build` and `go vet ./...` succeed
2. `go test -race ./...` all pass
3. End-to-end: `WriteFile` followed immediately by `ReadFile` on the same `branch_id` + `path`
   returns identical bytes
4. End-to-end: `ListDirectory` on the repo root after writing two files returns both entries
   with `Kind = FILE`
5. HTTP route: `GET .../files/src/main.dart` returns the correct file content with 200

## Non-goals

- Recursive tree traversal (callers can loop `ListDirectory`)
- Streaming reads for large files (v1 scope: single gRPC response; `WriteFile` already uses bytes)
- Read-by-commit-SHA (always reads branch tip in v1)
- Backfilling `content_type` for existing blobs

## Dependencies

- Depends on: ~~GIT-011~~ ✅ — bare-repo storage is stable; `arangoStorer` is in place
- Related: [BUG-09-020](../bug-details/BUG-09-020_filewrite_flush_race.md) — the write/flush
  race means `ReadFile` immediately after `WriteFile` may return stale bytes; BUG-09-020 Phase 1
  must land before `ReadFile` results can be considered authoritative for compile jobs
