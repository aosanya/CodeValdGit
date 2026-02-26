# CodeValdGit — MVP Details

## Domain Overview

CodeValdGit is a Go library that provides Git-based artifact versioning for **CodeValdCortex**, the enterprise multi-agent AI orchestration platform. It replaces the custom hand-rolled Git engine (`internal/git/`) in CodeValdCortex with a proper Git implementation backed by [go-git](https://github.com/go-git/go-git).

---

## Architecture Summary

| Concern | Approach |
|---|---|
| Git engine | [go-git](https://github.com/go-git/go-git) pure-Go — no system `git` binary |
| Repo granularity | 1 repo per Agency (mirrors CodeValdCortex's database-per-agency isolation) |
| Agent write policy | Always on a `task/{task-id}` branch — never directly to `main` |
| Merge strategy | Auto-merge on task completion; auto-rebase when `main` has advanced |
| Storage | Pluggable via `storage.Storer` + `billy.Filesystem` — filesystem (default) and ArangoDB |
| Conflict model | Return structured `ErrMergeConflict` to caller; branch left clean for retry |

### Key Interfaces

```go
// RepoManager — top-level entry point
type RepoManager interface {
    InitRepo(ctx context.Context, agencyID string) error
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)
    DeleteRepo(ctx context.Context, agencyID string) error  // archive
    PurgeRepo(ctx context.Context, agencyID string) error   // hard-delete
}

// Repo — single agency's Git repository
type Repo interface {
    CreateBranch(ctx context.Context, taskID string) error
    MergeBranch(ctx context.Context, taskID string) error
    DeleteBranch(ctx context.Context, taskID string) error
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    DeleteFile(ctx context.Context, taskID, path, author, message string) error
    ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)
    Log(ctx context.Context, ref, path string) ([]Commit, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

---

## Task Index

| Task ID | Title | Topic File | Status |
|---|---|---|---|
| [MVP-GIT-001](#) | Library Scaffolding | [repo-management.md](repo-management.md) | ✅ Complete |
| [MVP-GIT-002](#) | Filesystem Repo Lifecycle | [repo-management.md](repo-management.md) | ✅ Complete |
| [MVP-GIT-003](#) | Branch-Per-Task Workflow | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| [MVP-GIT-004](#) | File Operations & Commit Attribution | [file-operations.md](file-operations.md) | ✅ Complete |
| [MVP-GIT-005](#) | Fast-Forward Merge | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| [MVP-GIT-006](#) | Auto-Rebase & Conflict Resolution | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| [MVP-GIT-007](#) | History & Diff (UI Read Access) | [history-and-diff.md](history-and-diff.md) | ✅ Complete |
| [MVP-GIT-008](#) | ArangoDB Storage Backend | [storage-backends.md](storage-backends.md) | ✅ Complete |
| [MVP-GIT-009](#) | gRPC Service Proto & Codegen | [grpc-service.md](grpc-service.md) | 📋 Not Started |
| [MVP-GIT-010](#) | gRPC Server Implementation | [grpc-service.md](grpc-service.md) | 📋 Not Started |

---

## Execution Order

```
MVP-GIT-001  ← Foundation: scaffolding must come first
     ↓
MVP-GIT-002  ← Filesystem repo lifecycle (InitRepo, OpenRepo, DeleteRepo, PurgeRepo)
     ↓
MVP-GIT-003  ← Branch-per-task workflow (CreateBranch, DeleteBranch)
     ↓
MVP-GIT-004  ← File operations (WriteFile, ReadFile, DeleteFile, ListDirectory)
     ↓
MVP-GIT-005  ← Fast-forward merge (happy path)
     ↓
MVP-GIT-006  ← Auto-rebase + conflict resolution (builds on merge)
     ↓
MVP-GIT-007  ← History & diff (read-only; builds on file ops)
     ↓
MVP-GIT-008  ← ArangoDB backend (parallel track — depends on MVP-GIT-001 interfaces only)
     ↓
MVP-GIT-009  ← CodeValdCortex integration (depends on all above)
```

---

## What Gets Removed from CodeValdCortex (after MVP-GIT-009)

| File / Package | Reason |
|---|---|
| `internal/git/ops/operations.go` | Custom SHA-1 object engine → replaced by go-git |
| `internal/git/storage/repository.go` | ArangoDB Git object storage → replaced |
| `internal/git/fileindex/service.go` | ArangoDB file index service → replaced |
| `internal/git/fileindex/repository.go` | ArangoDB file index repository → replaced |
| `internal/git/models/` | Custom GitObject, GitTree, GitCommit → replaced by go-git types |
| ArangoDB: `git_objects`, `git_refs`, `repositories` | Collections dropped entirely |

---

## Topic Files

| File | Tasks Covered |
|---|---|
| [repo-management.md](repo-management.md) | MVP-GIT-001, MVP-GIT-002 |
| [branch-workflow.md](branch-workflow.md) | MVP-GIT-003, MVP-GIT-005, MVP-GIT-006 |
| [file-operations.md](file-operations.md) | MVP-GIT-004 |
| [history-and-diff.md](history-and-diff.md) | MVP-GIT-007 |
| [storage-backends.md](storage-backends.md) | MVP-GIT-008 |
| [grpc-service.md](grpc-service.md) | MVP-GIT-009, MVP-GIT-010 |
| [integration.md](integration.md) | ⚠️ Superseded — see grpc-service.md |
