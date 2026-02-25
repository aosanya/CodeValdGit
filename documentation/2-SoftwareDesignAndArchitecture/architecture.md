# CodeValdGit — Architecture

## 1. Core Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Git engine | [go-git](https://github.com/go-git/go-git) pure-Go | No system `git` binary dependency; embeddable in Go services |
| Repo granularity | 1 repo per Agency | Mirrors CodeValdCortex's database-per-agency isolation |
| Agent write policy | Always on a branch, never `main` | Prevents concurrent agent writes from corrupting shared history |
| Branch naming | `task/{task-id}` | Short-lived, traceable back to CodeValdCortex task records |
| Merge strategy | Auto-merge on task completion | No human approval gate for now; policy layer can extend this later |
| Storage backend | Pluggable via `storage.Storer` interface | go-git's open/closed design; caller injects the storer — filesystem and ArangoDB are both valid implementations |
| Worktree filesystem | Pluggable via `billy.Filesystem` interface | go-git separates object storage from the working tree; both are independently injectable |

---

## 2. Storage Backends

### go-git Pluggable Interfaces

go-git separates storage into two injectable interfaces:

| Interface | Package | Purpose |
|---|---|---|
| `storage.Storer` | `github.com/go-git/go-git/v5/storage` | Git objects, refs, index, config |
| `billy.Filesystem` | `github.com/go-git/go-billy/v5` | Working tree (checked-out files) |

### CodeValdGit `Backend` Interface

CodeValdGit adds a thin `Backend` interface on top of `storage.Storer`. It captures the operations that differ per storage type — repo lifecycle (init, archive/flag, purge) and storer construction — while the shared `Repo` implementation (branches, files, history) sits in `internal/repo/` and is backend-agnostic.

```go
// Backend abstracts storage-specific repo lifecycle.
// Implemented by storage/filesystem and storage/arangodb.
type Backend interface {
    // InitRepo provisions a new store for agencyID.
    InitRepo(ctx context.Context, agencyID string) error
    // OpenStorer returns a go-git storage.Storer and billy.Filesystem for agencyID.
    OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error)
    // DeleteRepo archives or flags the repo as deleted (behaviour is backend-specific).
    DeleteRepo(ctx context.Context, agencyID string) error
    // PurgeRepo permanently removes all storage for agencyID.
    PurgeRepo(ctx context.Context, agencyID string) error
}
```

The single `repoManager` implementation in `internal/manager/` holds a `Backend` and delegates lifecycle calls to it. `NewRepoManager(b Backend)` is the sole constructor — the caller (CodeValdCortex) picks and constructs the backend.

### Filesystem Backend (`storage/filesystem/`)

```
{base_path}/
└── {agency-id}/          ← One real .git repo per Agency
    └── .git/
```

| Operation | Implementation |
|---|---|
| `InitRepo` | `git.PlainInit` on disk; empty commit on `main` |
| `DeleteRepo` | `os.Rename` to `{archive_path}/{agency-id}/` (non-destructive) |
| `PurgeRepo` | `os.RemoveAll` of archive directory |
| `OpenStorer` | `filesystem.NewStorage` + `osfs.New` |

Simple, portable, works on any mounted volume (local disk, PVC, NFS).

### ArangoDB Backend (`storage/arangodb/`)

| Operation | Implementation |
|---|---|
| `InitRepo` | Insert seed documents into `git_objects`, `git_refs`, `git_config`, `git_index` |
| `DeleteRepo` | Set `deleted: true` flag on all agency documents (non-destructive; auditable) |
| `PurgeRepo` | Delete all documents where `agencyID == target` from all four collections |
| `OpenStorer` | `arango.NewStorage(db, agencyID)` + `memfs.New()` (or `osfs` for a durable worktree) |

The working tree (`billy.Filesystem`) remains on a local or in-memory filesystem — only the Git object store moves to ArangoDB. This mirrors the existing database-per-agency model in CodeValdCortex and means repos survive container restarts without a mounted volume.

| Collection | Contents |
|---|---|
| `git_objects` | Encoded Git objects (blobs, trees, commits, tags) keyed by SHA |
| `git_refs` | Branch and tag references |
| `git_index` | Staging area index |
| `git_config` | Per-repo Git config |

> **Selection**: The caller (CodeValdCortex) constructs the desired `Backend` implementation and passes it to `NewRepoManager`. CodeValdGit's core logic is backend-agnostic.

### Package Layout

```
github.com/aosanya/CodeValdGit/
├── codevaldgit.go          # RepoManager + Repo + Backend interfaces
├── types.go                # FileEntry, Commit, FileDiff, AuthorInfo, ErrMergeConflict
├── errors.go               # Sentinel errors (ErrRepoNotFound, ErrBranchNotFound, etc.)
├── config.go               # NewRepoManager constructor
├── internal/
│   ├── manager/            # Concrete repoManager — implements RepoManager, delegates to Backend
│   ├── repo/               # Shared Repo implementation — used by both storage backends
│   └── gitutil/            # Shared go-git helper utilities
└── storage/
    ├── filesystem/         # NewFilesystemBackend() — implements Backend (filesystem lifecycle)
    └── arangodb/           # NewArangoBackend()    — implements Backend (ArangoDB lifecycle)
```

---

## 3. Repository Identity

Naming convention: the Agency ID is the repository key in both backends.
- Filesystem: `{base_path}/{agency-id}/.git`
- ArangoDB: documents in `git_objects` etc. carry an `agency_id` field as the partition key (mirrors the existing database-per-agency isolation).

---

## 4. Branching Model

```
main
 │
 ├── task/task-abc-001     ← Agent A works here
 │     commits...
 │     └── auto-merged → main on task completion
 │
 └── task/task-xyz-002     ← Agent B works here (concurrent, isolated)
       commits...
       └── auto-merged → main on task completion
```

### Branch Lifecycle
1. **Task starts** → `CreateBranch("task/{task-id}", from: "main")`
2. **Agent writes files** → `Commit(branch: "task/{task-id}", files, author, message)`
3. **Task completes** → `MergeBranch("task/{task-id}", into: "main")`
   - If fast-forward is possible → merge directly
   - If `main` has advanced → **auto-rebase** task branch onto `main`, then fast-forward merge
   - If rebase conflicts → return `ErrMergeConflict{Files: [...]}` to caller; branch left clean for retry
4. **Branch deleted** → `DeleteBranch("task/{task-id}")`

> **Implementation note**: go-git only supports `FastForwardMerge`. The rebase step must be implemented by cherry-picking commits from the task branch onto the latest `main` using go-git's plumbing layer (`object.Commit`, `Worktree.Commit`).

---

## 5. Proposed Library API (Draft)

```go
// Backend abstracts storage-specific repo lifecycle.
// Implemented by storage/filesystem and storage/arangodb.
// The caller constructs the desired backend and passes it to NewRepoManager.
type Backend interface {
    InitRepo(ctx context.Context, agencyID string) error
    OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error)
    DeleteRepo(ctx context.Context, agencyID string) error
    PurgeRepo(ctx context.Context, agencyID string) error
}

// NewRepoManager constructs the shared RepoManager backed by the given Backend.
// Use storage/filesystem.NewFilesystemBackend or storage/arangodb.NewArangoBackend
// to obtain a Backend, then pass it here.
func NewRepoManager(b Backend) RepoManager

// RepoManager is the top-level entry point for managing per-agency Git repositories.
// Obtain via NewRepoManager. One instance is typically shared process-wide.
type RepoManager interface {
    InitRepo(ctx context.Context, agencyID string) error                   // delegates to Backend.InitRepo
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)
    DeleteRepo(ctx context.Context, agencyID string) error                 // delegates to Backend.DeleteRepo
    PurgeRepo(ctx context.Context, agencyID string) error                  // delegates to Backend.PurgeRepo
}

// Repo represents a single agency's Git repository. Obtained via RepoManager.OpenRepo.
// Backed by internal/repo — backend-agnostic; works over any storage.Storer.
type Repo interface {
    // Branch operations
    CreateBranch(ctx context.Context, taskID string) error
    MergeBranch(ctx context.Context, taskID string) error
    DeleteBranch(ctx context.Context, taskID string) error

    // File operations (always on a task branch)
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    DeleteFile(ctx context.Context, taskID, path, author, message string) error
    ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)

    // History
    Log(ctx context.Context, ref, path string) ([]Commit, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

---

## 6. Integration with CodeValdCortex

CodeValdCortex will call CodeValdGit at these lifecycle points:

| CodeValdCortex Event | CodeValdGit Call |
|---|---|
| Agency created | `RepoManager.InitRepo(agencyID)` |
| Task started | `Repo.CreateBranch(taskID)` |
| Agent writes output | `Repo.WriteFile(taskID, path, content, ...)` |
| Task completed | `Repo.MergeBranch(taskID)` → `Repo.DeleteBranch(taskID)` |
| Agency deleted | `RepoManager.DeleteRepo(agencyID)` |
| UI file browser | `Repo.ListDirectory("main", path)` |
| UI file view | `Repo.ReadFile("main", path)` |
| UI history view | `Repo.Log("main", path)` |

---

## 7. What Gets Removed from CodeValdCortex

Once CodeValdGit is integrated, the following will be deleted:

- `internal/git/ops/operations.go` — custom SHA-1 blob/tree/commit engine
- `internal/git/storage/repository.go` — ArangoDB Git object storage
- `internal/git/fileindex/service.go` — ArangoDB file index service
- `internal/git/fileindex/repository.go` — ArangoDB file index repository
- `internal/git/models/` — custom Git object models
- ArangoDB collections: `git_objects`, `git_refs`, `repositories`
