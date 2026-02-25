# Storage Backends

Topics: ArangoDB Storage Backend · go-git Pluggable Storage

---

## MVP-GIT-008 — ArangoDB Storage Backend

### Overview
Implement a custom `storage.Storer` backed by ArangoDB so that Git object stores survive container restarts without a mounted volume. The working tree (`billy.Filesystem`) remains on local/in-memory storage; only the Git object DAG (blobs, trees, commits, refs, config, index) moves to ArangoDB.

This mirrors CodeValdCortex's existing database-per-agency isolation model and is the preferred backend for production deployments on Kubernetes without persistent volumes.

### Why ArangoDB

| Concern | Filesystem Backend | ArangoDB Backend |
|---|---|---|
| Durability | Requires PVC or NFS | Handled by ArangoDB replication |
| Container restart | Data lost unless volume mounted | Data persists in ArangoDB |
| Backup | Volume snapshots | ArangoDB hot backup |
| Portability | Tied to node/mount | Available from any replica |
| Operational complexity | Simple | Requires ArangoDB connection |

### go-git Storage Interface

go-git separates Git storage into two pluggable interfaces:

```go
// storage.Storer — what we must implement for ArangoDB
type Storer interface {
    storer.EncodedObjectStorer   // blobs, trees, commits, tags
    storer.ReferenceStorer       // branches, tags, HEAD
    storer.IndexStorer           // staging area
    storer.ShallowStorer         // shallow clone markers
    config.ConfigStorer          // per-repo git config
}
```

The working tree (`billy.Filesystem`) stays on `memfs` (in-memory) or `osfs` (local). Only `storage.Storer` moves to ArangoDB.

### Acceptance Criteria

- [ ] Implement all methods of `storage.Storer` backed by ArangoDB collections
- [ ] `NewArangoStorage(db arangodb.Database, agencyID string)` constructs the storer
- [ ] All collection names are namespaced by `agencyID` to maintain per-agency isolation
- [ ] `git.Open(arangoStorer, memfsWorktree)` produces a fully functional `*git.Repository`
- [ ] All MVP-GIT-002 through MVP-GIT-007 tests pass when run against the ArangoDB backend (backend-agnostic test suite)
- [ ] Connection errors from ArangoDB are wrapped and returned as Go errors (not panics)
- [ ] Storer is safe for concurrent reads; writes are serialised per object type

### ArangoDB Collections

Each collection is **partitioned by `agencyID`** (one logical partition per agency within a shared collection, or one collection per agency — choose the simpler approach for MVP).

**Option A — Shared collections with `agencyID` field (recommended for MVP):**

| Collection | Key | Fields | Purpose |
|---|---|---|---|
| `git_objects` | `{agencyID}/{sha}` | `agencyID`, `sha`, `type`, `encoded` | Git objects (blobs, trees, commits, tags) |
| `git_refs` | `{agencyID}/{refName}` | `agencyID`, `name`, `target`, `type` | Branch and tag references |
| `git_index` | `{agencyID}/index` | `agencyID`, `entries` | Staging area index |
| `git_config` | `{agencyID}/config` | `agencyID`, `sections` | Per-repo git config |

**Option B — Per-agency collections (simpler queries, more collections):**

```
git_objects_{agencyID}
git_refs_{agencyID}
```

> **Recommendation**: Use Option A (shared collections with `agencyID` field) and compound indexes on `(agencyID, sha)` / `(agencyID, name)`. This avoids creating new ArangoDB collections for every agency.

### Collection Schema

#### git_objects

```json
{
  "_key":     "agency-001/abc123def456...",
  "agencyID": "agency-001",
  "sha":      "abc123def456...",
  "type":     "blob",
  "encoded":  "<base64-encoded git object>"
}
```

`type` is one of: `"blob"`, `"tree"`, `"commit"`, `"tag"`

#### git_refs

```json
{
  "_key":     "agency-001/refs/heads/main",
  "agencyID": "agency-001",
  "name":     "refs/heads/main",
  "target":   "abc123...",
  "symbolic": false
}
```

Symbolic refs (like `HEAD → refs/heads/main`):
```json
{
  "_key":     "agency-001/HEAD",
  "agencyID": "agency-001",
  "name":     "HEAD",
  "target":   "refs/heads/main",
  "symbolic": true
}
```

#### git_index

```json
{
  "_key":     "agency-001/index",
  "agencyID": "agency-001",
  "entries":  [
    { "name": "README.md", "sha": "abc123...", "mode": "100644", "size": 256 }
  ]
}
```

#### git_config

```json
{
  "_key":     "agency-001/config",
  "agencyID": "agency-001",
  "sections": { "core": { "bare": false, "filemode": true } }
}
```

### Interface Implementation

```go
// Package arango implements go-git's storage.Storer backed by ArangoDB.
package arango

import (
    "github.com/arangodb/go-driver"
    gogitstorage "github.com/go-git/go-git/v5/plumbing/storage"
)

// Storage implements storage.Storer using ArangoDB.
type Storage struct {
    db       driver.Database
    agencyID string
    objects  driver.Collection  // git_objects
    refs     driver.Collection  // git_refs
    idx      driver.Collection  // git_index
    cfg      driver.Collection  // git_config
}

// NewArangoStorage constructs a Storage for the given agency.
// The four collections must already exist in the provided database.
func NewArangoStorage(db driver.Database, agencyID string) (*Storage, error) {
    // Open or create each collection
    // ...
    return &Storage{...}, nil
}
```

#### EncodedObjectStorer methods (partial)

```go
// SetEncodedObject stores a Git object; returns its hash.
func (s *Storage) SetEncodedObject(obj plumbing.EncodedObject) (plumbing.Hash, error) {
    encoded, err := encodeObject(obj)
    if err != nil {
        return plumbing.ZeroHash, err
    }
    key := s.agencyID + "/" + obj.Hash().String()
    doc := bson.M{
        "_key":     key,
        "agencyID": s.agencyID,
        "sha":      obj.Hash().String(),
        "type":     obj.Type().String(),
        "encoded":  base64.StdEncoding.EncodeToString(encoded),
    }
    _, err = s.objects.CreateDocument(context.Background(), doc)
    if driver.IsArangoError(err) && err.(*driver.ArangoError).Code == 409 {
        return obj.Hash(), nil // already exists (idempotent)
    }
    return obj.Hash(), err
}

// EncodedObject retrieves a Git object by hash.
func (s *Storage) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
    key := s.agencyID + "/" + h.String()
    var doc struct {
        Encoded string `json:"encoded"`
        Type    string `json:"type"`
    }
    _, err := s.objects.ReadDocument(context.Background(), key, &doc)
    if driver.IsNotFound(err) {
        return nil, plumbing.ErrObjectNotFound
    }
    // Decode and return ...
}
```

### Indexes Required

```
// On git_objects collection:
PERSISTENT index on ["agencyID", "sha"]    ← for EncodedObject lookups
PERSISTENT index on ["agencyID", "type"]   ← for IterEncodedObjects by type

// On git_refs collection:
PERSISTENT index on ["agencyID", "name"]   ← for Reference lookups
```

### ArangoDB Client Dependency

```go
// go.mod addition
require (
    github.com/arangodb/go-driver v1.6.0
)
```

Use the official ArangoDB Go driver. Connection is passed in by the caller (CodeValdCortex injects its existing ArangoDB connection).

### Backend Selection in RepoManager

The caller constructs the desired `Backend` implementation and passes it to the single `NewRepoManager` constructor. There is no `BackendType` enum — the backend is injected, not selected by string.

```go
// --- storage/arangodb/arangodb.go ---

// ArangoConfig holds the ArangoDB connection and worktree settings
// for the ArangoDB backend.
type ArangoConfig struct {
    Database driver.Database
    // WorktreePath is the local path for the billy.Filesystem worktree.
    // Use "" for an in-memory worktree (memfs) — the default for ArangoDB backend.
    WorktreePath string
}

// NewArangoBackend constructs an ArangoDB-backed Backend.
// The four collections (git_objects, git_refs, git_index, git_config) must
// already exist in the provided database.
func NewArangoBackend(cfg ArangoConfig) (codevaldgit.Backend, error)

// --- storage/filesystem/filesystem.go ---

// FilesystemConfig holds path settings for the filesystem backend.
type FilesystemConfig struct {
    // BasePath is the root directory for live repositories.
    BasePath string
    // ArchivePath is the root directory for archived repositories.
    ArchivePath string
}

// NewFilesystemBackend constructs a filesystem-backed Backend.
func NewFilesystemBackend(cfg FilesystemConfig) (codevaldgit.Backend, error)
```

**Wiring in CodeValdCortex:**

```go
// Filesystem backend (default / dev)
b, err := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
    BasePath:    "/data/repos",
    ArchivePath: "/data/archive",
})
mgr, err := codevaldgit.NewRepoManager(b)

// ArangoDB backend (production / containerised)
b, err := arangodb.NewArangoBackend(arangodb.ArangoConfig{
    Database:     db, // existing driver.Database from CodeValdCortex
    WorktreePath: "",  // "" = in-memory worktree
})
mgr, err := codevaldgit.NewRepoManager(b)
```

### Dependencies
- MVP-GIT-001 (interfaces — the ArangoDB backend satisfies the same `Repo`/`RepoManager` interfaces)
- An ArangoDB instance is required for integration tests (use Docker for local dev)

### Tests

| Test | Type | Approach |
|---|---|---|
| `TestArangoStorage_SetGet` | Unit | Store and retrieve a blob by hash |
| `TestArangoStorage_RefLifecycle` | Unit | Set, read, update, delete a branch ref |
| `TestArangoStorage_Index` | Unit | Write and read staging index |
| `TestArangoStorage_Config` | Unit | Write and read repo config |
| `TestArangoBackend_FullWorkflow` | Integration | Run full MVP-GIT-002–007 test suite against ArangoDB backend |
| `TestArangoStorage_Idempotent` | Unit | Setting same object twice returns same hash (no error) |
| `TestArangoStorage_Concurrent` | Unit | 10 goroutines writing different objects concurrently |
| `TestArangoStorage_ConnectionError` | Unit | DB unreachable → returns wrapped error, no panic |

> Integration tests require `ARANGODB_URL` environment variable. Skip with `t.Skip()` if not set.

### Edge Cases & Constraints

- **Object deduplication**: Git objects are content-addressed; writing the same SHA twice must be idempotent (HTTP 409 → return existing hash, not error)
- **Large blobs**: Binary files may be multi-MB. ArangoDB documents have a 512MB default limit — well above typical artifacts. No chunking required for MVP.
- **Agency deletion**: When `DeleteRepo` is called on the ArangoDB backend, delete all documents with `agencyID == target` from all four collections (no archive concept in DB backend — or treat as a "deleted" flag for auditability)
- **Atomic operations**: go-git's `SetReference` and `SetEncodedObject` are called individually; full atomicity across collections is not required for MVP
- **In-memory worktree**: Using `memfs` for the working tree means file contents are lost on restart, but all committed objects survive in ArangoDB — this is the intended behaviour
