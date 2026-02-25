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

go-git separates storage into two injectable interfaces:

| Interface | Package | Purpose |
|---|---|---|
| `storage.Storer` | `github.com/go-git/go-git/v5/storage` | Git objects, refs, index, config |
| `billy.Filesystem` | `github.com/go-git/go-billy/v5` | Working tree (checked-out files) |

CodeValdGit supports two backends out of the box:

### Filesystem (default)
```
{base_path}/
└── {agency-id}/          ← One real .git repo per Agency
    └── .git/
```
Uses `storage/filesystem` + `osfs.New(path)`. Simple, portable, works on any mounted volume (local disk, PVC, NFS).

### ArangoDB
A custom `storage.Storer` implementation backed by ArangoDB collections:

| Collection | Contents |
|---|---|
| `git_objects` | Encoded Git objects (blobs, trees, commits, tags) keyed by SHA |
| `git_refs` | Branch and tag references |
| `git_index` | Staging area index |
| `git_config` | Per-repo Git config |

The working tree (`billy.Filesystem`) remains on a local or in-memory filesystem — only the Git object store moves to ArangoDB. This mirrors the existing database-per-agency model in CodeValdCortex and means repos survive container restarts without a mounted volume.

> **Selection**: The caller (CodeValdCortex) passes the chosen `storage.Storer` and `billy.Filesystem` when calling `InitRepo` / `OpenRepo`. CodeValdGit itself is backend-agnostic.

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
// RepoManager is the top-level entry point.
// Configured with BasePath (live repos) and ArchivePath (archived repos).
type RepoManager interface {
    // Lifecycle
    InitRepo(ctx context.Context, agencyID string) error
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)
    DeleteRepo(ctx context.Context, agencyID string) error  // moves repo to ArchivePath
    PurgeRepo(ctx context.Context, agencyID string) error   // hard-deletes archived repo
}

// Repo represents a single agency's Git repository
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
