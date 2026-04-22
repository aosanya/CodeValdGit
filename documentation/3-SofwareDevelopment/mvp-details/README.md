# CodeValdGit — MVP Details

## Domain Overview

CodeValdGit is a Go library that provides Git-based artifact versioning for **CodeValdCross**, the enterprise multi-agent AI orchestration platform. It replaces the custom hand-rolled Git engine (`internal/git/`) in CodeValdCross with a proper Git implementation backed by [go-git](https://github.com/go-git/go-git).

---

## Architecture Summary

| Concern | Approach |
|---|---|
| Git engine | [go-git](https://github.com/go-git/go-git) pure-Go — no system `git` binary |
| Repo granularity | Multiple repos per Agency |
| Agent write policy | Always on a `task/{task-id}` branch — never directly to `main` |
| Merge strategy | Auto-merge on task completion; auto-rebase when `main` has advanced |
| Storage (v2) | Single `entitygraph.DataManager` for both gRPC and Smart HTTP paths |
| Storer gaps | Six structural gaps identified and resolved — see [architecture-storer-gaps.md](../../2-SoftwareDesignAndArchitecture/architecture-storer-gaps.md) |
| Conflict model | Return structured `ErrMergeConflict` to caller; branch left clean for retry |

### Key Interface (v2 flat `GitManager`)

```go
// GitManager — single flat interface; replaces v1 RepoManager + Repo
type GitManager interface {
    InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error)
    GetRepository(ctx context.Context) (Repository, error)
    DeleteRepo(ctx context.Context) error
    CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error)
    GetBranch(ctx context.Context, branchID string) (Branch, error)
    ListBranches(ctx context.Context) ([]Branch, error)
    DeleteBranch(ctx context.Context, branchID string) error
    MergeBranch(ctx context.Context, branchID string) (Branch, error)
    WriteFile(ctx context.Context, req WriteFileRequest) (Commit, error)
    ReadFile(ctx context.Context, branchID, path string) (Blob, error)
    DeleteFile(ctx context.Context, req DeleteFileRequest) (Commit, error)
    ListDirectory(ctx context.Context, branchID, path string) ([]Blob, error)
    Log(ctx context.Context, branchID string, filter LogFilter) ([]CommitEntry, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

---

## Task Index

### v1 — Filesystem Library (all complete)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| MVP-GIT-001 | Library Scaffolding | [repo-management.md](repo-management.md) | ✅ Complete |
| MVP-GIT-002 | Filesystem Repo Lifecycle | [repo-management.md](repo-management.md) | ✅ Complete |
| MVP-GIT-003 | Branch-Per-Task Workflow | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| MVP-GIT-004 | File Operations & Commit Attribution | [file-operations.md](file-operations.md) | ✅ Complete |
| MVP-GIT-005 | Fast-Forward Merge | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| MVP-GIT-006 | Auto-Rebase & Conflict Resolution | [branch-workflow.md](branch-workflow.md) | ✅ Complete |
| MVP-GIT-007 | History & Diff (UI Read Access) | [history-and-diff.md](history-and-diff.md) | ✅ Complete |
| MVP-GIT-008 | ArangoDB Storage Backend | [storage-backends.md](storage-backends.md) | ✅ Complete |
| MVP-GIT-009 | gRPC Service Proto & Codegen | [grpc-service.md](grpc-service.md) | ✅ Complete |
| MVP-GIT-010 | gRPC Server Implementation | [grpc-service.md](grpc-service.md) | ✅ Complete |
| MVP-GIT-011 | Service-Driven Route Registration | [route-registrar.md](route-registrar.md) | ✅ Complete |
| MVP-GIT-012 | SharedLib Migration | — | ✅ Complete |

### v2 — entitygraph Redesign + gRPC Microservice (all complete)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| GIT-001 | Pre-delivered schema & domain value types | — | ✅ Complete |
| GIT-002 | Flat `GitManager` interface + request types + v2 errors | — | ✅ Complete |
| GIT-003 | Proto (`service.proto`) — `GitService` RPCs; codegen | — | ✅ Complete |
| GIT-004 | ArangoDB entitygraph backend | — | ✅ Complete |
| GIT-008 | Config + Cross registrar routes | — | ✅ Complete |
| GIT-009 | `cmd/main.go` — cmux wiring + schema seed | — | ✅ Complete |
| GIT-010 | Unit & integration tests | — | ✅ Complete |

### Unified Backend — ArangoDB `storage.Storer` (active)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| GIT-015 | ArangoDB `storage.Storer` + unified backend | [arangodb-storer.md](arangodb-storer.md) | 🚀 In Progress |
| GIT-017 | gRPC `WriteFile` must store `data` bytes for git pull | [arangodb-storer.md](arangodb-storer.md) | 📋 Not Started |

Six implementation gaps were identified and resolved during design; a seventh
gap was found post-implementation. See
[architecture-storer-gaps.md](../../2-SoftwareDesignAndArchitecture/architecture-storer-gaps.md)
and
[architecture-pull-flow.md](../../2-SoftwareDesignAndArchitecture/architecture-pull-flow.md)
for the full analysis:

| Gap | Summary | Difficulty |
|---|---|---|
| 1 | Tree binary reconstruction — `entries` JSON property | Hard |
| 2 | `advanceBranchHead` must write `Branch.sha` | Easy |
| 3 | Real git binary SHA everywhere (removes `contentSHA()`) | Hard |
| 4 | `Backend.InitRepo` is a no-op; `OpenStorer` verifies only | Easy |
| 5 | `arangoStorer` rewritten to use `entitygraph.DataManager` | Medium |
| 6 | Blob basename — resolved by Gap 1 `entries[].name` | Easy |
| 7 | ⚠️ gRPC-written objects missing `data` — invisible to `git pull` | Medium |

### `.git-graph/` Push Sync (not started)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| GIT-025a | JSON parser + validator (`internal/gitgraph/parser.go`) | [git-graph-sync.md](git-graph-sync.md) | 📋 Not Started |
| GIT-025b | Sync logic — keyword upsert + edge hard-sync | [git-graph-sync.md](git-graph-sync.md) | 📋 Not Started |
| GIT-025c | Hook integration in `IndexPushedBranch` | [git-graph-sync.md](git-graph-sync.md) | 📋 Not Started |
| GIT-025d | Update `map-folder-keywords.prompt.md` to output `.git-graph/` files | [git-graph-sync.md](git-graph-sync.md) | ✅ Done |

### Production Safety (not started)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| GIT-011 | Concurrency — `RefLocker` + CAS in `advanceBranchHead` | [critical-concurrency.md](critical-concurrency.md) | 📋 Not Started |
| GIT-012 | Squash merge strategy | [critical-merge-strategy.md](critical-merge-strategy.md) | 📋 Not Started |
| GIT-013 | Transaction boundaries + idempotency | [critical-transactions.md](critical-transactions.md) | 📋 Not Started |
| GIT-014 | ArangoDB deduplication, docs, production gate | [critical-arangodb.md](critical-arangodb.md) | 📋 Not Started |

### Repository Import (not started)

| Task ID | Title | Spec File | Status |
|---|---|---|---|
| GIT-016a | `ImportJob` TypeDefinition + `git_importjobs` schema | [repo-import.md](repo-import.md) | 📋 Not Started |
| GIT-016b | Types + errors + `GitManager` interface additions | [repo-import.md](repo-import.md) | 📋 Not Started |
| GIT-016c | Core implementation: goroutine, go-git clone, entity walk, cancel | [repo-import.md](repo-import.md) | 📋 Not Started |
| GIT-016d | Proto additions (3 RPCs) + `buf generate` | [repo-import.md](repo-import.md) | 📋 Not Started |
| GIT-016e | gRPC server handlers + error mapping | [repo-import.md](repo-import.md) | 📋 Not Started |
| GIT-016f | Unit tests — import manager, cancel, concurrency | [repo-import.md](repo-import.md) | 📋 Not Started |

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
| [route-registrar.md](route-registrar.md) | MVP-GIT-011 |
| [arangodb-storer.md](arangodb-storer.md) | GIT-015 — unified `storage.Storer` |
| [critical-concurrency.md](critical-concurrency.md) | GIT-011 — `RefLocker` + CAS |
| [critical-merge-strategy.md](critical-merge-strategy.md) | GIT-012 — squash merge |
| [critical-transactions.md](critical-transactions.md) | GIT-013 — idempotency |
| [critical-arangodb.md](critical-arangodb.md) | GIT-014 — ArangoDB production gate |
| [integration.md](integration.md) | ⚠️ Superseded — see grpc-service.md |
| [repo-import.md](repo-import.md) | GIT-016 — async import of external public HTTPS repository |
| [grpc-proto.md](grpc-proto.md) | GIT-021 — proto + gRPC handlers + route registration for documentation layer |
| [git-graph-sync.md](git-graph-sync.md) | GIT-025 — `.git-graph/` push sync (file-driven keyword + edge authoring) |

> **DR-023 / DR-024 pivot** — The documentation layer no longer uses four named
> Blob→Blob edge types (`documents`, `documented_by`, `depends_on`, `imported_by`).
> They are replaced by a single `references` / `referenced_by` pair. Both directions
> carry a required `descriptor` string property (open vocabulary; well-known values:
> `"documents"`, `"depends_on"`, `"contradicts"`, `"test_for"`, `"obsoletes"`).
> The inverse auto-copies the `descriptor` map (DR-024).
> See [requiements_documentation.md](../../1-SoftwareRequirements/requiements_documentation.md)
> §DR-023 and §DR-024.
