---
applyTo: '**'
---

# CodeValdGit — Code Structure Rules

## Library Design Principles

CodeValdGit is a **Go library** — not an application. These rules reflect that:

- **No HTTP handlers, no web framework, no templating engine**
- **No `main` package** — the root package is the library entry point
- **Callers inject dependencies** — storage backends and filesystems are never hardcoded
- **Exported API surface is minimal** — expose only what consumers need

---

## Interface-First Design

**Always define interfaces before concrete types.**

```go
// ✅ CORRECT — interface in root package, consumed by CodeValdCortex
type Repo interface {
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    // ...
}

// ❌ WRONG — leaking a concrete type to callers
type GitRepo struct {
    repo *gogit.Repository
}
```

**File layout — one primary concern per file:**

```
manager.go   → RepoManager interface + filesystem implementation
repo.go      → Repo interface + implementation
errors.go    → all exported error types (ErrMergeConflict, ErrNotFound, ErrRepoExists)
models.go    → FileEntry, Commit, FileDiff (pure value types, no methods)
```

---

## Branch Enforcement Rules

**ALL write operations must go through task branches.**

```go
// ✅ CORRECT — agent writes via task branch
repo.WriteFile(ctx, "task-abc-001", "output/report.md", content, agentID, "Add report")

// ❌ WRONG — writing directly to main (must be rejected by the library)
repo.WriteFile(ctx, "main", "output/report.md", content, agentID, "Add report")
```

**`MergeBranch` must**:
1. Attempt fast-forward merge
2. If not possible, auto-rebase (cherry-pick) task branch commits onto `main`
3. If rebase conflicts → return `ErrMergeConflict{Files: [...]}` and leave branch in clean state
4. Never silently succeed with an incomplete merge

---

## go-git Usage Rules

- **Use pure-Go go-git** — never shell out to the `git` binary
- **Inject `storage.Storer` and `billy.Filesystem`** — never hardcode filesystem paths in core logic
- **For rebase**: use `object.Commit` and `Worktree.Commit` from go-git's plumbing layer (go-git v5 has no native rebase)
- **For fast-forward merge**: use `gogit.MergeOptions{Strategy: git.FastForwardMerge}` (added v5.12.0)

---

## Storage Backend Rules

The `Backend` interface is the injection point. The caller (CodeValdCortex) constructs the desired `Backend` implementation from `storage/filesystem` or `storage/arangodb` and passes it to `NewRepoManager`. The root package never imports either storage driver.

```go
// ✅ CORRECT — Backend injected by caller; root package stays backend-agnostic
b, _ := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
    BasePath:    "/data/repos",
    ArchivePath: "/data/archive",
})
mgr, _ := codevaldgit.NewRepoManager(b)

// ❌ WRONG — hardcoded backend inside library
func NewRepoManager(basePath string) RepoManager {
    storer := filestorage.NewStorage(osfs.New(basePath), cache.NewObjectLRUDefault())
    // ...
}
```

- `storage/filesystem/` and `storage/arangodb/` are **public** packages — callers can import them directly
- `internal/manager/` holds the concrete `repoManager` — not importable outside this module
- `internal/repo/` holds the shared `Repo` implementation — not importable outside this module
- **Never import ArangoDB drivers from the root package or `internal/manager/`**

---

## Error Handling Rules

**All exported errors must be typed and structured:**

```go
// errors.go

// ErrMergeConflict is returned by MergeBranch when a rebase conflict
// cannot be resolved automatically. The caller is responsible for routing
// the conflict back to the agent for resolution.
type ErrMergeConflict struct {
    Files []string // conflicting file paths
}

func (e ErrMergeConflict) Error() string {
    return fmt.Sprintf("merge conflict in %d file(s): %v", len(e.Files), e.Files)
}

var ErrNotFound = errors.New("repository not found")
var ErrRepoExists = errors.New("repository already exists")
```

- **Never use `log.Fatal`** in library code — return errors to caller
- **Never panic** in exported functions
- **Wrap errors with context**: `fmt.Errorf("MergeBranch %s: %w", taskID, err)`

---

## Context Rules

**Every exported method must accept `context.Context` as the first argument.**

```go
// ✅ CORRECT
func (r *repo) WriteFile(ctx context.Context, taskID, path, content, author, message string) error

// ❌ WRONG
func (r *repo) WriteFile(taskID, path, content, author, message string) error
```

Respect context cancellation in loops and long-running operations (e.g., the rebase cherry-pick loop).

---

## Godoc Rules

**Every exported type, function, interface, and method must have a godoc comment.**

```go
// WriteFile commits a single file to the task branch identified by taskID.
// The branch must already exist — call CreateBranch first.
// Returns ErrNotFound if the branch does not exist.
func (r *repo) WriteFile(ctx context.Context, taskID, path, content, author, message string) error {
```

- **Package comment** on the primary file of every package
- **Examples** in `_test.go` files for non-obvious API usage patterns

---

## File Size and Complexity Limits

- **Max file size**: 500 lines (hard limit)
- **Max function length**: 50 lines (prefer 20-30)
- **One primary concern per file**
- **Split `storage/arangodb/` by collection** when it grows (e.g., `objects.go`, `refs.go`, `index.go`)

**Example of compliant file breakdown:**
```
✅ CORRECT:
storage/arangodb/
├── objects.go    # Blob, tree, commit, tag storage (~250 lines)
├── refs.go       # Branch and tag references (~150 lines)
├── index.go      # Staging area index (~100 lines)
└── config.go     # Per-repo Git config (~100 lines)

❌ WRONG:
storage/arangodb/
└── storage.go    # Everything in one 600-line file
```

---

## Concurrency Rules

- **Read operations** (`ReadFile`, `ListDirectory`, `Log`, `Diff`) must be **safe to call concurrently**
- **Write operations** (`WriteFile`, `DeleteFile`, `MergeBranch`) are **isolated per task branch** by design
- Avoid shared mutable state in `Repo` implementations
- If locking is needed (e.g., for the archive operation), document it explicitly

---

## Naming Conventions

```go
// ✅ CORRECT — singular package names, noun-only interfaces, Err prefix for errors
package codevaldgit

type RepoManager interface{}
type Repo interface{}
var ErrNotFound = errors.New("repository not found")
type ErrMergeConflict struct{}

// ❌ WRONG
package codevaldgits         // plural
type IRepoManager interface{} // I prefix
var notFoundError = ...      // unexported sentinel exposed via behaviour
```

---

## Task Management and Workflow

### Branch Management (MANDATORY)

```bash
# Create feature branch from main
git checkout -b feature/GIT-XXX_description

# Implement and validate
go build ./...           # must succeed
go test -v -race ./...   # must pass
go vet ./...             # must show 0 issues
golangci-lint run ./...  # must pass

# Merge when complete
git checkout main
git merge feature/GIT-XXX_description --no-ff
git branch -d feature/GIT-XXX_description
```

### Pre-Development Checklist

Before adding new code:
1. ✅ Is this type already defined in `models.go` or `errors.go`?
2. ✅ Am I adding logic to the right layer (root package vs `storage/arangodb/` vs `internal/rebase/`)?
3. ✅ Does this function accept `context.Context` as its first argument?
4. ✅ Will the file exceed 500 lines after this change?
5. ✅ Am I injecting storage instead of hardcoding it?
6. ✅ Does every new exported symbol have a godoc comment?
7. ✅ Are all write paths going through a task branch?

### Code Review Requirements

Every PR must verify:
- [ ] No hardcoded storage backends in root package
- [ ] All exported symbols have godoc comments
- [ ] Context propagated through all public calls
- [ ] Errors are typed (`ErrMergeConflict`, not raw strings) for public errors
- [ ] No files exceeding 500 lines
- [ ] Tests added for all new exported functions
- [ ] `go vet ./...` shows 0 issues
- [ ] `go test -race ./...` passes
- [ ] No use of `git` binary (pure go-git only)
