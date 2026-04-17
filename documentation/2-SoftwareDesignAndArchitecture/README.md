# 2 — Software Design & Architecture

## Overview

This section captures the **how** — design decisions, data models, component architecture, and technical constraints for CodeValdGit.

---

## Index

| Document | Description |
|---|---|
| [architecture.md](architecture.md) | Core design decisions, storage backends, branching model, draft API interfaces, CodeValdCross integration table |
| [architecture-concurrency.md](architecture-concurrency.md) | Concurrency model — `RefLocker` interface, CAS on branch HEAD, per-agency merge serialisation (GIT-011) |
| [architecture-merge.md](architecture-merge.md) | Merge strategy — squash merge, fork-point tracking, conflict surface (GIT-012) |
| [architecture-transactions.md](architecture-transactions.md) | Transaction boundaries — atomicity rules, `MergeRequest` idempotency key, retry-safety matrix (GIT-013) |
| [architecture-arangodb.md](architecture-arangodb.md) | ArangoDB v2 entitygraph backend — design evolution, sub-gap analysis, deduplication, production criteria (GIT-014) |
| [architecture-arangodb-storer.md](architecture-arangodb-storer.md) | ArangoDB v3 `storage.Storer` — unified single backend for gRPC + Smart HTTP; eliminates filesystem (GIT-015) |
| [architecture-storer-gaps.md](architecture-storer-gaps.md) | Structural gaps in the storer implementation — Gaps 1–6 resolved (GIT-015); Gap 7 (gRPC-data) pending (GIT-017) |
| [architecture-pull-flow.md](architecture-pull-flow.md) | Git pull / clone object-serving flow; how file versions are resolved; Gap 7 analysis |

---

## Key Design Decisions at a Glance

| Decision | Choice | Rationale |
|---|---|---|
| Git engine | go-git (pure Go) | No system `git` binary dependency; embeddable in Go services |
| Repo granularity | Multiple repos per Agency | An agency may own any number of named repositories |
| Agent write policy | Always on a branch, never `main` | Prevents concurrent agent writes from corrupting shared history |
| Branch naming | `task/{task-id}` | Short-lived, traceable back to CodeValdWork task records |
| Merge strategy | Squash merge (V1) — tree-diff apply onto default HEAD | Simpler than cherry-pick rebase; atomic apply; preserves task branch history for audit |
| Storage backend (gRPC / GitManager) | Pluggable via `entitygraph.DataManager` | Filesystem and ArangoDB are both valid; ArangoDB is experimental — see [architecture-arangodb.md](architecture-arangodb.md) |
| Storage backend (Smart HTTP + unified) | ArangoDB `storage.Storer` (GIT-015 🚀) | Single backend for both gRPC and Smart HTTP — no filesystem needed; see [architecture-arangodb-storer.md](architecture-arangodb-storer.md) |
| Worktree filesystem | Pluggable via `billy.Filesystem` | Both storage and worktree are independently injectable |
| Merge conflict handling | Tree-diff conflict detection | Returns `ErrMergeConflict{Files}` — caller routes back to agent |

---

## Component Architecture

```
github.com/aosanya/CodeValdGit    ← root package (library entry point)
├── git.go                        # GitManager interface + CrossPublisher
├── codevaldgit.go                # Backend, RepoManager, Repo interfaces
├── errors.go                     # Exported error types (ErrMergeConflict, etc.)
├── models.go                     # FileEntry, Commit, FileDiff, Branch, Tag value types
├── schema.go                     # DefaultGitSchema() — 7 TypeDefinitions for entitygraph
├── git_impl_repo.go              # gitManager: repo lifecycle, branch/tag management
├── git_impl_fileops.go           # gitManager: file operations, commit history, diff
├── storage/
│   ├── filesystem/               # Filesystem backend — real .git dirs; production default
│   │   └── filesystem.go
│   └── arangodb/                 # ArangoDB backend — thin entitygraph adapter; experimental
│       └── arangodb.go           # Delegates to CodeValdSharedLib/entitygraph/arangodb
└── internal/
    └── server/                   # Inbound gRPC + Smart HTTP server
        ├── server.go             # gRPC GitService handlers
        ├── githttp.go            # Git Smart HTTP handler (filesystem-only)
        └── mappers.go            # Proto ↔ domain model conversion
```
