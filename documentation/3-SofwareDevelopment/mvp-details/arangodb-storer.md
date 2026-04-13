# GIT-015 — ArangoDB `storage.Storer` + Unified Backend

## Overview

| Field | Value |
|---|---|
| Task ID | GIT-015 |
| Priority | P0 |
| Status | 📋 Not Started |
| Branch | `feature/GIT-010_arangodb-git-storer` |
| Depends On | GIT-010 ✅ |
| Architecture ref | [architecture-arangodb-storer.md](../../2-SoftwareDesignAndArchitecture/architecture-arangodb-storer.md) |

---

## Problem Statement

CodeValdGit has two incompatible storage layers:

- **gRPC path**: `gitManager` → `entitygraph.DataManager` → ArangoDB. Stores high-level
  entities (Repository, Branch, Blob) with UUID keys and JSON properties.
- **Smart HTTP path**: `GitHTTPHandler` → `filesystem.Backend` → disk `.git/` dirs.

A repo initialised via `InitRepo` gRPC exists only in ArangoDB as entity documents.
When a git client clones via Smart HTTP, the handler looks for a `.git/` directory that
does not exist — the clone fails with a 500.

**Goal**: implement `storage.Storer` backed by ArangoDB so both paths share the same
storage layer, removing the filesystem dependency entirely.

---

## Acceptance Criteria

- [ ] `storage/arangodb/storer.go` — `arangoStorer` implements `storage.Storer` fully
- [ ] `storage/arangodb/backend.go` — `arangoBackend` implements `codevaldgit.Backend`
- [ ] `git_impl_repo.go` — rewritten using go-git plumbing on `arangoStorer`
- [ ] `git_impl_fileops.go` — rewritten using go-git plumbing on `arangoStorer`
- [ ] `cmd/main.go` — single `arangoBackend` passed to both `GitManager` and `GitHTTPHandler`
- [ ] `storage/filesystem/` — no longer imported from `cmd/main.go`
- [ ] `internal/config/` — `GIT_REPOS_BASE_PATH` and `GIT_REPOS_ARCHIVE_PATH` removed
- [ ] `go build ./...` passes with no errors
- [ ] `go vet ./...` passes with no issues
- [ ] `go test -race ./...` passes (all existing unit tests still pass)
- [ ] `git clone http://localhost:50053/{agencyID}` succeeds after `InitRepo` via gRPC
- [ ] `git push` to a clone succeeds and is readable via gRPC `ReadFile`

---

## New Files

### `storage/arangodb/storer.go`

```go
package arangodb

type arangoStorer struct {
    db       driver.Database
    agencyID string
}

func newArangoStorer(db driver.Database, agencyID string) *arangoStorer {
    return &arangoStorer{db: db, agencyID: agencyID}
}
```

Implements all methods of `storage.Storer`. See the interface map in
[architecture-arangodb-storer.md §4.1](../../2-SoftwareDesignAndArchitecture/architecture-arangodb-storer.md).

**Key implementation notes**:

- `SetEncodedObject`: read the object via `obj.Reader()`, base64-encode the raw bytes,
  upsert into `gitraw_objects` at `_key={agencyID}/{obj.Hash().String()}`.
  If ArangoDB returns a conflict (409), the object already exists — return the hash
  without error (idempotent write).
- `EncodedObject(type, hash)`: look up by `_key`, base64-decode, reconstruct a
  `plumbing.MemoryObject` with the correct type, size, and reader.
- `IterEncodedObjects(type)`: AQL query over `gitraw_objects` filtered by `agencyID`
  and `objType`; wrap results in a `storer.EncodedObjectSliceIter`.
- `HasEncodedObject(hash)`: HTTP `HEAD` via go-driver `DocumentExists` on `gitraw_objects`.
- `EncodedObjectSize(hash)`: read the `size` field only (AQL `RETURN doc.size`).
- `AddAlternate`: no-op (return `nil` — alternates not needed for ArangoDB).
- `CheckAndSetReference`: read existing ref's `_rev`, compare with `old.Hash()`,
  update with `_rev` as the optimistic lock key — maps directly to ArangoDB's
  `_rev`-based CAS.
- `PackRefs`: no-op (loose refs and packed refs are identical in the doc store).
- `Module(name)`: return `newArangoStorer(db, agencyID+"/module/"+name)`.

### `storage/arangodb/backend.go`

```go
package arangodb

// arangoBackend implements codevaldgit.Backend backed by ArangoDB raw git collections.
type arangoBackend struct {
    db driver.Database
}

// NewBackendFromDB constructs an arangoBackend from an existing database handle.
// Ensures all gitraw_* collections and their indexes exist.
func NewBackendFromDB(db driver.Database) (codevaldgit.Backend, error)
```

`InitRepo` implementation:
1. Construct `arangoStorer` for the agency.
2. Call `storer.Init()` (satisfies the `storer.Initializer` interface — writes the
   default config document).
3. Call `gogit.Init(arangoStorer, nil)` — writes initial git config to `gitraw_config`.
4. Set HEAD symbolic ref to `refs/heads/main` via `storer.SetReference`.
5. Write an initial empty commit on `refs/heads/main` using go-git's `Worktree` with an
   in-memory `billy.Filesystem` (only needed for the init commit; discarded afterward).

`OpenStorer` implementation:
1. Verify `HEAD` exists in `gitraw_refs` for the agency (check it has been initialised).
2. Return `arangoStorer` + `memfs.New()` as the working tree.
   The Smart HTTP transport only uses the object store — the in-memory working tree is
   never written to persistent storage.

`DeleteRepo` / `PurgeRepo`:
- Issue an AQL `FOR doc IN {collection} FILTER doc.agencyID == @a REMOVE doc IN {collection}`
  for each of the five `gitraw_*` collections.
- `DeleteRepo` and `PurgeRepo` have identical behaviour (no archive concept in ArangoDB;
  ArangoDB provides its own audit log and backup).

---

## Modified Files

### `git_impl_repo.go` — Rewrite

Replace all `entitygraph.DataManager` calls with go-git plumbing operations on
an `arangoStorer` obtained from `backend.OpenStorer`.

Representative changes:

| GitManager method | Old call | New call |
|---|---|---|
| `InitRepo` | `dm.CreateEntity(…TypeRepository…)` | `backend.InitRepo(ctx, agencyID)` |
| `CreateBranch(req)` | `dm.CreateEntity(…TypeBranch…)` | Open repo → `plumbing.NewBranchReferenceName` → `storer.SetReference` |
| `ListBranches` | `dm.ListEntities(TypeBranch, filter)` | `repo.References()` filtered to `refs/heads/` prefix |
| `GetBranch(id)` | `dm.GetEntity(TypeBranch, id)` | `storer.Reference(plumbing.NewBranchReferenceName(name))` |
| `DeleteBranch(id)` | `dm.DeleteEntity(TypeBranch, id)` | `storer.RemoveReference(plumbing.NewBranchReferenceName(name))` |
| `MergeBranch(req)` | advance `head_commit_id` entity property | fast-forward ref update via `storer.SetReference`; rebase via plumbing cherry-pick |

The `GitManager` struct changes from holding `entitygraph.DataManager` to holding
`codevaldgit.Backend`. The single backend is used to open a go-git `Repository` at the
start of each method.

### `git_impl_fileops.go` — Rewrite

| GitManager method | Old call | New call |
|---|---|---|
| `WriteFile` | `dm.CreateEntity(…TypeBlob…)` chain | Open repo → `worktree.Add` → `worktree.Commit` (in-memory worktree) |
| `ReadFile` | `dm.GetEntity(…TypeBlob…)` | `repo.CommitObject(ref)` → `commit.Tree()` → `tree.File(path)` → `.Contents()` |
| `DeleteFile` | `dm.DeleteEntity(TypeBlob, id)` | Open repo → `worktree.Remove` → `worktree.Commit` |
| `ListDirectory` | `dm.ListEntities(TypeBlob/Tree, filter)` | `commit.Tree()` → `tree.Entries` |
| `Log` | walk Commit entities via graph traversal | `repo.Log(&gogit.LogOptions{From: hash, FileName: path})` |
| `Diff` | compare Tree entities | `gogit.Diff(fromTree, toTree)` |

### `cmd/main.go` — Simplification

```go
// Before (two backends):
arangoBackend, _ := gitarangodb.NewBackend(gitarangodb.Config{…})
fsBackend, _ := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{…})
mgr := codevaldgit.NewGitManager(arangoBackend, arangoBackend, pub, cfg.AgencyID)
gitHTTPHandler := server.NewGitHTTPHandler(fsBackend)

// After (one backend):
arangoBackend, _ := gitarangodb.NewBackend(gitarangodb.Config{…})
mgr := codevaldgit.NewGitManager(arangoBackend, pub, cfg.AgencyID)
gitHTTPHandler := server.NewGitHTTPHandler(arangoBackend)
```

Remove the `filesystem` import and the `GIT_REPOS_BASE_PATH` / `GIT_REPOS_ARCHIVE_PATH`
config fields from `internal/config/config.go`.

### `storage/arangodb/arangodb.go` — Collection Bootstrap

Add the five `gitraw_*` collections to the `toSharedConfig` / collection-ensure step.
Add unique persistent indexes on `gitraw_objects[agencyID, sha]`.

---

## ArangoDB Schema Changes

### New Collections (created in `storage/arangodb/arangodb.go` on startup)

| Collection | Type |
|---|---|
| `gitraw_objects` | Document |
| `gitraw_refs` | Document |
| `gitraw_config` | Document |
| `gitraw_index` | Document |
| `gitraw_shallow` | Document |

### Indexes

```
gitraw_objects: unique persistent index on [agencyID, sha]
gitraw_refs:    persistent index on [agencyID, refName]
```

---

## Test Plan

### Unit Tests

- `storage/arangodb/storer_test.go`:
  - Table-driven tests for `SetEncodedObject` / `EncodedObject` round-trip (blob, tree, commit).
  - `HasEncodedObject` after set; `HasEncodedObject` for unknown hash.
  - `SetReference` / `Reference` round-trip for direct and symbolic refs.
  - `CheckAndSetReference` CAS: success case, conflict case (stale `old`).
  - `SetConfig` / `Config` round-trip.
  - `SetIndex` / `Index` round-trip.
  - `SetShallow` / `Shallow` round-trip.
  - Skip if `GIT_ARANGO_ENDPOINT` not set.

### End-to-End

```bash
# 1. Start ArangoDB
docker run -p 8529:8529 -e ARANGO_NO_AUTH=1 arangodb:3.11

# 2. Start CodeValdGit
GIT_ARANGO_ENDPOINT=http://localhost:8529 ./bin/codevaldgit-server

# 3. Create a repo via gRPC (using grpcurl or integration test)
# 4. Clone via Smart HTTP — must succeed
git clone http://localhost:50053/{agencyID} /tmp/test-clone

# 5. Write a file via gRPC
# 6. Fetch in the clone — must show the new commit
git -C /tmp/test-clone fetch && git -C /tmp/test-clone log
```

---

## File Size Budget

| File | Estimated Lines | Within Limit |
|---|---|---|
| `storage/arangodb/storer.go` | ~350 | Split at 500 if needed: `storer_objects.go`, `storer_refs.go` |
| `storage/arangodb/backend.go` | ~150 | ✅ |
| `git_impl_repo.go` | ~200 | ✅ |
| `git_impl_fileops.go` | ~180 | ✅ |

---

## Dependencies

No new Go module dependencies required. `go-git/go-git/v5` is already in `go.mod` and
provides all plumbing types needed (`plumbing.MemoryObject`, `plumbing.Hash`,
`plumbing.Reference`, `config.Config`, `format/index`, etc.).
