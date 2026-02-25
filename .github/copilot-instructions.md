# CodeValdGit — AI Agent Development Instructions

## Project Overview

**CodeValdGit** is a **Go library** that provides Git-based artifact versioning for [CodeValdCortex](../CodeValdCortex/README.md) — the enterprise multi-agent AI orchestration platform.

It replaces the hand-rolled Git engine (`internal/git/`) in CodeValdCortex with a proper Git implementation backed by [go-git](https://github.com/go-git/go-git). All Git semantics run in pure Go — no `git` binary dependency.

**Core Concept**: Each Agency in CodeValdCortex has exactly **one Git repository**. AI agents produce artifacts (code, Markdown, configs, reports, any file type). CodeValdGit manages their storage, versioning, and lifecycle using real Git semantics — one task branch per task, auto-merged to `main` on completion.

---

## Library Architecture

### 1. Two-Interface Design

The library exposes two top-level interfaces:

```go
// RepoManager — top-level lifecycle management (init, open, archive, purge).
// Configured with BasePath (live repos) and ArchivePath (archived repos).
type RepoManager interface {
    InitRepo(ctx context.Context, agencyID string) error
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)
    DeleteRepo(ctx context.Context, agencyID string) error // archives to ArchivePath
    PurgeRepo(ctx context.Context, agencyID string) error  // hard-deletes archived repo
}

// Repo — per-agency repository operations.
type Repo interface {
    // Branch operations
    CreateBranch(ctx context.Context, taskID string) error
    MergeBranch(ctx context.Context, taskID string) error
    DeleteBranch(ctx context.Context, taskID string) error

    // File operations (writes always on a task branch)
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    DeleteFile(ctx context.Context, taskID, path, author, message string) error
    ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)

    // History (read-only, safe for concurrent access)
    Log(ctx context.Context, ref, path string) ([]Commit, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

### 2. Branch-Per-Task Workflow (MANDATORY)

**Agents MUST NOT commit directly to `main`.** Every write happens on a task branch named `task/{task-id}`:

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

**Branch lifecycle** (enforced by the library):
1. `CreateBranch(taskID)` → creates `task/{task-id}` from current `main`
2. `WriteFile(taskID, ...)` → commits files to `task/{task-id}`
3. `MergeBranch(taskID)` → auto-rebase onto `main` if needed, then fast-forward merge
4. `DeleteBranch(taskID)` → cleans up the task branch

### 3. Merge Conflict Handling

go-git only natively supports `FastForwardMerge` (added v5.12.0). When `main` has advanced since the branch was created:

- The library **auto-rebases** the task branch onto the latest `main` (manual cherry-pick via go-git plumbing layer: `object.Commit`, `Worktree.Commit`)
- If rebase succeeds → fast-forward merge proceeds automatically
- If rebase has content conflicts → returns `ErrMergeConflict{Files: [...]}` to caller; branch is left in a clean state for retry
- The caller (CodeValdCortex) routes the conflict back to the agent for resolution

### 4. Storage Backends

go-git separates storage into two injectable interfaces:

| Interface | Package | Purpose |
|---|---|---|
| `storage.Storer` | `github.com/go-git/go-git/v5/storage` | Git objects, refs, index, config |
| `billy.Filesystem` | `github.com/go-git/go-billy/v5` | Working tree (checked-out files) |

**Filesystem (default)**:
```
{base_path}/{agency-id}/.git
```

**ArangoDB** (custom `storage.Storer` in `storage/arangodb/`):

| Collection | Contents |
|---|---|
| `git_objects` | Blobs, trees, commits, tags (keyed by SHA) |
| `git_refs` | Branch and tag references |
| `git_index` | Staging area index |
| `git_config` | Per-repo Git config |

> The caller (CodeValdCortex) passes the chosen `storage.Storer` and `billy.Filesystem` when calling `InitRepo` / `OpenRepo`. **CodeValdGit itself is backend-agnostic.**

### 5. Repository Archiving

- `DeleteRepo(agencyID)` → **archives** repo by moving it to `{archive_base_path}/{agency-id}/` (non-destructive; repo remains a valid `.git` repo)
- `PurgeRepo(agencyID)` → `os.RemoveAll` (irreversible; for operators who want permanent removal)
- `RepoManager` must be configured with both `base_path` (live) and `archive_base_path`

---

## Project Structure

```
/workspaces/CodeValdGit/
├── documentation/
│   ├── README.md           # Documentation index
│   ├── requirements.md     # FR / NFR / open questions (all resolved)
│   └── architecture.md     # Design decisions, branching model, full API draft
├── .github/
│   ├── copilot-instructions.md
│   ├── instructions/
│   │   └── rules.instructions.md
│   ├── prompts/
│   └── workflows/
│       └── ci.yml
└── [Go module root — to be scaffolded]
    ├── go.mod
    ├── manager.go          # RepoManager interface + filesystem implementation
    ├── repo.go             # Repo interface + implementation
    ├── errors.go           # ErrMergeConflict, ErrNotFound, ErrRepoExists
    ├── models.go           # FileEntry, Commit, FileDiff value types
    ├── storage/
    │   └── arangodb/       # ArangoDB storage.Storer implementation
    └── internal/
        └── rebase/         # Manual cherry-pick rebase logic (go-git plumbing)
```

---

## Developer Workflows

### Build & Test Commands

```bash
# Run all tests with race detector
go test -v -race ./...

# Run tests with coverage
go test -v -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Build check (library — verifies compilation, no binary produced)
go build ./...

# Static analysis
go vet ./...

# Format code
go fmt ./...

# Lint
golangci-lint run ./...
```

**There is no `make run`, no binary, no templates.** This is a library — `go build ./...` only verifies it compiles cleanly.

### Task Management Workflow

**Every task follows strict branch management:**

```bash
# 1. Create feature branch from main
git checkout -b feature/GIT-XXX_description

# 2. Implement changes
# ... development work ...

# 3. Build validation before merge
go build ./...           # Must succeed
go vet ./...             # Must show 0 issues
go test -v -race ./...   # Must pass
golangci-lint run ./...  # Must pass

# 4. Merge when complete
git checkout main
git merge feature/GIT-XXX_description --no-ff
git branch -d feature/GIT-XXX_description
```

---

## Technology Stack

| Component | Choice | Rationale |
|---|---|---|
| Language | Go 1.21+ | Matches CodeValdCortex; native concurrency |
| Git engine | go-git (pure Go) | No system `git` binary dependency; embeddable |
| Storage (default) | Filesystem via `osfs` | Simple, portable, works on any mounted volume |
| Storage (optional) | ArangoDB via custom `storage.Storer` | Survives container restarts without PVC |
| Working tree | `billy.Filesystem` (pluggable) | Decouples object store from worktree |

---

## Code Quality Rules

### Library-Specific Rules

- **No web framework dependencies** — no Gin, no HTTP handlers, no templ
- **No database driver in the core package** — ArangoDB storer lives in `storage/arangodb/`, never imported by root package
- **Interface-first** — callers depend on `RepoManager` and `Repo` interfaces, not concrete types
- **Exported API is minimal** — expose only what callers need; keep implementation details unexported
- **All public functions must have godoc comments**
- **Context propagation** — every public method takes `context.Context` as first argument

### Naming Conventions

- **Package name**: `codevaldgit` (root), `arangodb` (storage subpackage), `rebase` (internal)
- **Interfaces**: `RepoManager`, `Repo` — noun-only, no `I` prefix
- **Errors**: `Err` prefix — `ErrMergeConflict`, `ErrNotFound`, `ErrRepoExists`
- **No abbreviations in exported names** — prefer `AgencyID` over `AgID`
- **Singular package names** — `storage`, not `storages`

### File Organisation

- **Max file size**: 500 lines (prefer smaller, focused files)
- **Max function length**: 50 lines (prefer 20-30)
- **One primary concern per file** — `manager.go`, `repo.go`, `errors.go`, `models.go`
- **Error types in `errors.go`** — never scatter sentinel errors across files
- **Value types in `models.go`** — `FileEntry`, `Commit`, `FileDiff`
- **Split `storage/arangodb/` by collection** if it grows beyond 500 lines (e.g., `objects.go`, `refs.go`, `index.go`)

### Anti-Patterns to Avoid

- ❌ **Writing directly to `main` branch** — always use task branches (`task/{task-id}`)
- ❌ **Hard-deleting repos on agency deletion** — always archive first (`DeleteRepo`), hard-delete only via `PurgeRepo`
- ❌ **Coupling to a specific storage backend** — inject via `storage.Storer`
- ❌ **Panicking in exported functions** — return structured errors
- ❌ **Ignoring context cancellation** — check `ctx.Err()` in loops and long operations
- ❌ **Mixing read and write operations in one function** — read ops are concurrent-safe; write ops need branch isolation
- ❌ **Using the `git` binary** — pure Go only via go-git

---

## Integration with CodeValdCortex

CodeValdCortex calls CodeValdGit at these lifecycle points:

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

## What Gets Removed from CodeValdCortex

Once CodeValdGit is integrated, the following will be deleted from CodeValdCortex:

- `internal/git/ops/operations.go` — custom SHA-1 blob/tree/commit engine
- `internal/git/storage/repository.go` — ArangoDB Git object storage
- `internal/git/fileindex/service.go` + `repository.go` — ArangoDB file index
- `internal/git/models/` — custom Git object models
- ArangoDB collections: `git_objects`, `git_refs`, `repositories`

---

## Documentation References

- `documentation/requirements.md` — functional requirements (FR-001–FR-008), NFR, resolved open questions
- `documentation/architecture.md` — design decisions, storage backends, branching model, draft `Repo` + `RepoManager` interfaces, integration table

---

## When in Doubt

1. **Check documentation first**: `documentation/requirements.md` and `documentation/architecture.md` are the source of truth
2. **Interface before implementation**: define the interface, write tests against the interface, then implement
3. **Inject dependencies**: storage and filesystem are always caller-provided
4. **Write tests for every exported function** — aim for >80% coverage; use table-driven tests
5. **go-git plumbing layer for rebase**: `MergeBranch` requires manual cherry-pick via `object.Commit` and `Worktree.Commit` — no native rebase in go-git v5
