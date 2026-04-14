# GIT-015 — ArangoDB `storage.Storer` + Unified Backend

**Date**: 2026-04-14  
**Branch**: `feature/GIT-010_arangodb-git-storer` (merged to main)  
**Status**: ✅ Complete  
**Build**: `go build ./...` ✅ · `go vet ./...` ✅ · `go test -race ./...` ✅

---

## Problem Solved

CodeValdGit had two incompatible storage layers:

- **gRPC path** (`gitManager`): wrote Blob/Tree/Commit/Branch entities via `entitygraph.DataManager` → ArangoDB.
- **Smart HTTP path** (`GitHTTPHandler`): used a filesystem `Backend` expecting a `.git/` directory on disk.

A repo initialised via `InitRepo` gRPC existed only in ArangoDB. A `git clone` via Smart HTTP failed with a 500 because no `.git/` directory was present. GIT-015 unified both paths under a single `entitygraph.DataManager`-backed `storage.Storer`.

---

## Subtask Summary

### GIT-015a — Schema additions

- Added `sha` (string) to **Branch** TypeDefinition: stores the current commit SHA so `arangoStorer.Reference("refs/heads/X")` resolves in a single `ListEntities` call without traversing the `points_to` edge.
- Added `head_ref` (string) to **Repository** TypeDefinition: symbolic HEAD target (e.g. `"refs/heads/main"`), written by `InitRepo`.
- Added **GitInternalState** TypeDefinition (`StorageCollection: "git_internal"`, `UniqueKey: ["state_type"]`) with properties `state_type` and `data` (base64); used by `Config`/`Index`/`Shallow` storers.

### GIT-015b — Real git SHA + `entries` + `advanceBranchHead` sha write

**`git_impl_fileops.go`**:
- Removed synthetic `contentSHA()`/`commitSHA()` helpers.
- Blob SHA: `plumbing.MemoryObject{}.Writer()` + write content + `.Hash()` — real git blob SHA (`blob {size}\x00{content}`).
- Tree SHA: `object.Tree{Entries: […]}.Encode(treeMemObj)` + `treeMemObj.Hash()`.
- Commit SHA: `object.Commit{…}.Encode(commitMemObj)` + `commitMemObj.Hash()`.
- Tree entity now carries an `entries` property — a JSON-encoded array `[{"name":"<basename>","mode":"<git-mode>","sha":"<40hex>"}]` — enabling `EncodedObject(TreeObject, hash)` to reconstruct the binary tree in one read (Gap 1).

**`git_impl_repo.go` — `advanceBranchHead`**:
- After writing `head_commit_id`, fetches the new Commit entity and copies its `sha` to `Branch.sha` via `dm.UpdateEntity` (Gap 2).

### GIT-015c — `storer.go` EncodedObjectStorer via DataManager

`storage/arangodb/storer.go` (`arangoStorer` struct, `entitygraph.DataManager` field):

| Method | Implementation |
|---|---|
| `SetEncodedObject` | `dm.CreateEntity` TypeID=Blob/Tree/Commit/Tag, `sha`=hash, `data`=base64(raw), `size`; idempotent on conflict |
| `EncodedObject` | `dm.ListEntities` filter TypeID+`sha`; base64-decode `data` → `plumbing.MemoryObject` |
| `HasEncodedObject` | same list; `plumbing.ErrObjectNotFound` on empty |
| `IterEncodedObjects` | `dm.ListEntities` by TypeID; `storer.NewEncodedObjectSliceIter` |
| `EncodedObjectSize` | list by `sha`; read `size` field |
| `AddAlternate` | no-op |

All `gitraw_objects`/`colObjects` references removed.

### GIT-015d — `storer.go` ReferenceStorer + internal-state storers

| Method | Implementation |
|---|---|
| `SetReference(HEAD)` | `dm.ListEntities(Repository)` → `dm.UpdateEntity` `head_ref`=target |
| `SetReference(refs/heads/X)` | `dm.ListEntities(Branch, name=X)` → `dm.UpdateEntity` `sha`=hash |
| `Reference(HEAD)` | read `head_ref` from Repository → `plumbing.NewSymbolicReference` |
| `Reference(refs/heads/X)` | read `sha` from Branch → `plumbing.NewHashReference` |
| `Reference(refs/tags/X)` | read `sha` from Tag → `plumbing.NewHashReference` |
| `IterReferences` | HEAD + all Branch + all Tag entities → `storer.NewReferenceSliceIter` |
| `RemoveReference` | `dm.DeleteEntity` on Branch or Tag |
| `CheckAndSetReference` | read `Branch.sha`; return `storage.ErrReferenceHasChanged` on mismatch; else `SetReference` |
| `SetConfig`/`Config` | `GitInternalState` entity, `state_type="config"`, `data`=base64(marshalled config) |
| `SetIndex`/`Index` | `state_type="index"` |
| `SetShallow`/`Shallow` | `state_type="shallow"` |
| `Module(name)` | `newArangoStorer(dm, agencyID+"/module/"+name)` |

All `gitraw_refs`/`gitraw_config`/`gitraw_index`/`gitraw_shallow`/`ensureGitRawCollections` removed.

### GIT-015e — Backend refactor + `cmd/main.go` unified backend + filesystem removal

**`storage/arangodb/backend.go`**:
- Struct field changed: `db driver.Database` → `dm entitygraph.DataManager`.
- `InitRepo` is a no-op (entity creation owned by `gitManager.InitRepo`).
- `OpenStorer`: verifies Repository entity exists → returns `newArangoStorer(dm, agencyID)` + `memfs.New()`.
- `DeleteRepo`/`PurgeRepo`: `dm.DeleteEntity` on Repository entity (identical behaviour; ArangoDB handles audit).

**`storage/arangodb/arangodb.go`**:
- Added `NewArangoStorerBackend(dm entitygraph.DataManager) codevaldgit.Backend` — constructs `arangoBackend{dm}`.

**`cmd/main.go`**:
- Removed `filesystem` import.
- `gitarangodb.NewArangoStorerBackend(arangoBackend)` passed to `server.NewGitHTTPHandler`.
- No `fsBackend` construction; no `GIT_REPOS_BASE_PATH`/`GIT_REPOS_ARCHIVE_PATH` references.

**`internal/config/config.go`**:
- No `ReposBasePath`/`ReposArchivePath` fields.
- `ArangoWorktreePath` retained (optional; defaults to memfs when empty).

---

## Key Design Decision: DataManager delegation (not go-git plumbing rewrite)

The original spec called for rewriting `git_impl_repo.go` and `git_impl_fileops.go`
to use go-git `Worktree` operations on an `arangoStorer`. That approach was **not
taken**. Instead:

- `gitManager` (gRPC path) continues to own entity creation via `entitygraph.DataManager` — preserving graph semantics for `ListDirectory`, `Log`, and `Diff`.
- `arangoStorer` delegates **down** to the same `entitygraph.DataManager`, reading the entities that `gitManager` writes.
- Both paths share one storage layer without rewriting the gRPC business logic.

This avoids the risk of a full rewrite while achieving the same unified-storage goal.

---

## Post-implementation outstanding work

- Live end-to-end smoke test: `InitRepo` via gRPC → `git clone http://localhost:50053/{agencyID}` (pending ArangoDB instance).
- `storer.go` is 726 lines — above the soft 500-line limit. Split into `storer_objects.go` + `storer_refs.go` is a candidate for a future cleanup task.
- GIT-011 (concurrency/CAS in `advanceBranchHead`) is independent and still pending.
