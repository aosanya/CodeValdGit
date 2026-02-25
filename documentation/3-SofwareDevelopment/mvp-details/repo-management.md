# Repo Management

Topics: Library Scaffolding · Filesystem Repo Lifecycle

---

## MVP-GIT-001 — Library Scaffolding

### Overview
Establish the Go module, public package structure, core interfaces, and shared types that all other MVP tasks build on. Nothing else can proceed without this foundation.

### Acceptance Criteria
- [ ] `go.mod` declares module `github.com/aosanya/CodeValdGit`
- [ ] Core interfaces `RepoManager` and `Repo` are defined in the public package
- [ ] Shared types `FileEntry`, `Commit`, `FileDiff`, `ErrMergeConflict` are defined
- [ ] Package compiles cleanly with `go build ./...`
- [ ] `go-git` v5 and `go-billy` v5 are declared as dependencies in `go.mod`
- [ ] At least one sentence of GoDoc comment on every exported symbol

### Package Structure

```
github.com/aosanya/CodeValdGit/
├── codevaldgit.go          # Package-level doc, RepoManager + Repo + Backend interfaces
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

### Core Interfaces

```go
// Backend abstracts storage-specific repo lifecycle.
// Implemented by storage/filesystem and storage/arangodb.
// The caller constructs the desired backend and passes it to NewRepoManager.
type Backend interface {
    // InitRepo provisions a new store for agencyID.
    InitRepo(ctx context.Context, agencyID string) error

    // OpenStorer returns a go-git storage.Storer and billy.Filesystem for agencyID.
    // Called internally by RepoManager.OpenRepo to construct a Repo.
    OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error)

    // DeleteRepo archives or flags the repo as deleted (behaviour is backend-specific).
    // Filesystem: os.Rename to ArchivePath. ArangoDB: sets deleted flag on agency documents.
    DeleteRepo(ctx context.Context, agencyID string) error

    // PurgeRepo permanently removes all storage for agencyID.
    // Filesystem: os.RemoveAll. ArangoDB: deletes all agency documents.
    PurgeRepo(ctx context.Context, agencyID string) error
}

// NewRepoManager constructs the shared RepoManager backed by the given Backend.
// Use storage/filesystem.NewFilesystemBackend or storage/arangodb.NewArangoBackend
// to obtain a Backend, then pass it here.
func NewRepoManager(b Backend) (RepoManager, error)

// RepoManager is the top-level entry point for creating and managing
// per-agency Git repositories. Obtain via NewRepoManager.
type RepoManager interface {
    // InitRepo creates a new empty Git repository for the given agency.
    // Returns ErrRepoAlreadyExists if a repo already exists.
    InitRepo(ctx context.Context, agencyID string) error

    // OpenRepo opens an existing repository. Returns ErrRepoNotFound if absent.
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)

    // DeleteRepo delegates to Backend.DeleteRepo.
    // For the filesystem backend this archives the repo; for ArangoDB it sets a deleted flag.
    DeleteRepo(ctx context.Context, agencyID string) error

    // PurgeRepo delegates to Backend.PurgeRepo and permanently removes all storage.
    // Only call after DeleteRepo has been called. Returns ErrRepoNotFound if absent.
    PurgeRepo(ctx context.Context, agencyID string) error
}

// Repo represents a single agency's Git repository. Obtained via RepoManager.OpenRepo.
// All write operations require a taskID (branch name suffix); reads accept any ref.
type Repo interface {
    // Branch operations
    CreateBranch(ctx context.Context, taskID string) error
    MergeBranch(ctx context.Context, taskID string) error
    DeleteBranch(ctx context.Context, taskID string) error

    // File operations — writes always target task/{taskID} branch
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    DeleteFile(ctx context.Context, taskID, path, author, message string) error
    ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)

    // History — reads only, safe to call concurrently
    Log(ctx context.Context, ref, path string) ([]Commit, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

### Shared Types

```go
// FileEntry is a single item returned by ListDirectory.
type FileEntry struct {
    Name  string
    Path  string
    IsDir bool
    Size  int64
}

// Commit is a summary of a single Git commit.
type Commit struct {
    SHA       string
    Author    string
    Message   string
    Timestamp time.Time
}

// FileDiff describes changes to one file between two refs.
type FileDiff struct {
    Path      string
    Operation string // "add" | "modify" | "delete"
    Patch     string // unified diff text
}

// AuthorInfo is passed into write operations.
type AuthorInfo struct {
    Name  string
    Email string
}

// ErrMergeConflict is returned by MergeBranch when auto-rebase encounters
// a content conflict. The task branch is left clean (rebase aborted).
type ErrMergeConflict struct {
    TaskID         string
    ConflictingFiles []string
}

func (e *ErrMergeConflict) Error() string {
    return fmt.Sprintf("merge conflict on task branch %q: files %v", e.TaskID, e.ConflictingFiles)
}
```

### Sentinel Errors

```go
var (
    ErrRepoNotFound      = errors.New("repository not found")
    ErrRepoAlreadyExists = errors.New("repository already exists")
    ErrBranchNotFound    = errors.New("branch not found")
    ErrBranchExists      = errors.New("branch already exists")
    ErrFileNotFound      = errors.New("file not found")
    ErrRefNotFound       = errors.New("ref not found (branch, tag, or SHA)")
)
```

### Dependencies

- None (first task)

### Tests
- Compile-time: interfaces are satisfied by mock structs (table-driven)
- All exported types serialize/deserialize correctly via `encoding/json`

---

## MVP-GIT-002 — Filesystem Repo Lifecycle

### Overview
Implement the filesystem-backed `RepoManager` using `go-git`'s `filesystem` storage and `osfs` working tree. This is the default backend for all subsequent MVP tasks. Covers `InitRepo`, `OpenRepo`, `DeleteRepo` (archive), and `PurgeRepo` (hard delete).

### Acceptance Criteria
- [ ] `InitRepo(agencyID)` creates `{base_path}/{agency-id}/.git` as a bare-like repo with an initial empty commit on `main`
- [ ] `OpenRepo(agencyID)` returns a `Repo` backed by the on-disk `.git`; returns `ErrRepoNotFound` for unknown agency IDs
- [ ] `DeleteRepo(agencyID)` moves `{base_path}/{agency-id}/` → `{archive_path}/{agency-id}/` atomically (using `os.Rename`; falls back to copy+delete across mount points)
- [ ] `PurgeRepo(agencyID)` calls `os.RemoveAll` on `{archive_path}/{agency-id}/`; returns `ErrRepoNotFound` if the archive directory does not exist
- [ ] `OpenRepo` on an archived repo returns `ErrRepoNotFound` (live path only)
- [ ] Concurrent `OpenRepo` calls for the same agency ID are safe
- [ ] Unit tests pass with a temp directory as `base_path`

### Configuration

```go
// RepoManagerConfig holds paths and backend selection for NewRepoManager.
type RepoManagerConfig struct {
    // BasePath is the root directory for live repositories.
    // Each agency gets a subdirectory: {BasePath}/{agencyID}/
    BasePath string

    // ArchivePath is the root directory for archived repositories.
    // DeleteRepo moves repos here; PurgeRepo removes from here.
    ArchivePath string
}

// NewRepoManager constructs a filesystem-backed RepoManager.
func NewRepoManager(cfg RepoManagerConfig) (RepoManager, error)
```

### Repository Layout on Disk

```
{BasePath}/
└── {agencyID}/
    └── .git/
        ├── HEAD
        ├── config
        ├── objects/
        └── refs/

{ArchivePath}/
└── {agencyID}/      ← moved here on DeleteRepo
    └── .git/
```

### InitRepo Detail

1. Validate `agencyID` is non-empty and path-safe (no `/`, `..`, null bytes)
2. Check that `{BasePath}/{agencyID}` does not already exist → `ErrRepoAlreadyExists`
3. Call `git.PlainInit(path, false)` via go-git (non-bare; worktree at same path)
4. Create an initial empty commit on `main` with author `system` and message `init`
   - go-git requires at least one commit before branches can be referenced
5. Return nil on success

### DeleteRepo Detail (Archive, Not Delete)

```
{BasePath}/{agencyID}/  →  os.Rename  →  {ArchivePath}/{agencyID}/
```

- If `os.Rename` fails (different mount points): copy directory tree, then remove source
- If `{ArchivePath}/{agencyID}` already exists: append timestamp suffix `{agencyID}-{unix-ts}` to avoid collision
- Log the archive operation with agencyID and destination path

### PurgeRepo Detail

1. Resolve `{ArchivePath}/{agencyID}/`
2. If directory does not exist → return `ErrRepoNotFound`
3. Call `os.RemoveAll` — this is irreversible; callers must confirm intent
4. Log the purge operation

### go-git Usage Pattern

```go
import (
    "github.com/go-git/go-git/v5"
    "github.com/go-git/go-git/v5/storage/filesystem"
    "github.com/go-git/go-billy/v5/osfs"
)

// Open existing repo
dotGit := osfs.New(filepath.Join(cfg.BasePath, agencyID, ".git"))
storer := filesystem.NewStorage(dotGit, cache.NewObjectLRUDefault())
wt := osfs.New(filepath.Join(cfg.BasePath, agencyID))
repo, err := git.Open(storer, wt)
```

### Dependencies

- MVP-GIT-001 (interfaces and types must exist)

### Tests

| Test | Approach |
|---|---|
| `TestInitRepo_CreatesGitDir` | Verify `.git` dir and HEAD exist after init |
| `TestInitRepo_AlreadyExists` | Second init returns `ErrRepoAlreadyExists` |
| `TestOpenRepo_Success` | Open freshly-initialized repo |
| `TestOpenRepo_NotFound` | Returns `ErrRepoNotFound` for unknown agency |
| `TestDeleteRepo_Archives` | Source path gone, dest path has `.git` |
| `TestDeleteRepo_AlreadyArchived` | Collision → timestamp suffix applied |
| `TestPurgeRepo_HardDeletes` | Archive dir removed after purge |
| `TestPurgeRepo_NotFound` | Returns `ErrRepoNotFound` for unknown archive |
| `TestConcurrentOpen` | Race-condition test: 10 goroutines call `OpenRepo` simultaneously |

### Edge Cases & Constraints

- **Path traversal**: Reject agencyIDs containing `/`, `..`, or null bytes to prevent escaping `BasePath`
- **Cross-mount rename**: `os.Rename` fails with `EXDEV` when source and dest are on different filesystems — must fall back to copy+delete
- **Empty agencyID**: Return `ErrRepoNotFound` immediately (don't create blank path)
- **Initial commit on `main`**: go-git cannot create a branch reference until at least one commit exists — `InitRepo` must make this commit
