# GIT-008: ArangoDB Storage Backend

**Date**: 2026-02-25
**Branch**: `feature/GIT-008_arangodb_storage`
**Task**: MVP-GIT-008 — ArangoDB Storage Backend
**FR**: FR-008 (ArangoDB Storage Backend)

---

## Summary

Replaced the placeholder `storage/arangodb/arangodb.go` stub with a complete
implementation of go-git's `storage.Storer` backed by ArangoDB. Git objects,
references, index, and config are persisted in four shared ArangoDB collections
partitioned by `agencyID`. The working tree remains in-memory (`memfs`) so
repositories survive container restarts without a mounted volume.

Added the official `github.com/arangodb/go-driver v1.6.0` dependency.

---

## Files Changed

| File | Change |
|------|--------|
| `storage/arangodb/arangodb.go` | Replaced stub with full implementation (~450 LOC) |
| `storage/arangodb/arangodb_test.go` | New — 9 tests (2 pass standalone, 7 skip without ArangoDB server) |
| `go.mod` / `go.sum` | Added `github.com/arangodb/go-driver v1.6.0` |

---

## Architecture

### Collections (Option A — shared, keyed by `{agencyID}/{key}`)

| Collection | Key pattern | Purpose |
|---|---|---|
| `git_objects` | `{agencyID}/{sha}` | Blobs, trees, commits, tags (base64-encoded) |
| `git_refs` | `{agencyID}/{refName}` | Branch + symbolic references |
| `git_index` | `{agencyID}/index` | Staging area (base64-encoded index file) |
| `git_config` | `{agencyID}/config` | Per-repo git config + existence sentinel |

### Interface Implementation

`Storage` implements the full `storage.Storer` composite interface:

| Sub-interface | Methods |
|---|---|
| `storer.EncodedObjectStorer` | `NewEncodedObject`, `SetEncodedObject`, `HasEncodedObject`, `EncodedObjectSize`, `EncodedObject`, `IterEncodedObjects`, `AddAlternate` |
| `storer.ReferenceStorer` | `SetReference`, `CheckAndSetReference`, `Reference`, `IterReferences`, `RemoveReference`, `CountLooseRefs`, `PackRefs` |
| `storer.ShallowStorer` | `SetShallow`, `Shallow` |
| `storer.IndexStorer` | `SetIndex`, `Index` |
| `config.ConfigStorer` | `SetConfig`, `Config` |
| `storage.ModuleStorer` | `Module` |

### Key Design Decisions

- **Existence sentinel**: The `git_config` document at `{agencyID}/config` is used as the repo existence check (for `InitRepo`, `OpenStorer`, `DeleteRepo`, `PurgeRepo`)
- **Soft delete**: `DeleteRepo` sets `deleted: true` on the config sentinel rather than removing documents — auditable
- **Hard purge**: `PurgeRepo` runs an AQL `REMOVE` on all four collections filtered by `STARTS_WITH(doc._key, "{agencyID}/")`
- **Idempotent writes**: `SetEncodedObject` ignores HTTP 409 (conflict) from ArangoDB — same SHA twice is a no-op
- **Index encoding**: The git staging index is serialised using `gitindex.Encoder` and stored as base64 in `git_index`; decoded with `gitindex.Decoder`
- **Config encoding**: The repo config is marshalled using `config.Marshal()` and stored as base64
- **Module submodules**: `Module(name)` creates a sub-Storage with agencyID `{agencyID}/module/{name}` using the same underlying collections

### `NewArangoBackend`

```go
func NewArangoBackend(cfg ArangoConfig) (codevaldgit.Backend, error)
```

Opens or creates all four collections in the provided `driver.Database`. Returns an error if `Database` is nil or collection access fails. No credentials stored in the Backend — the caller owns the connection lifecycle.

---

## Tests — `arangodb_test.go`

| Test | Type | What it verifies |
|------|------|-----------------|
| `TestArangoBackend_NilDatabase` | Unit (no server) | Nil database → clear error, no panic |
| `TestArangoStorage_SetGet` | Integration | Store blob by hash; retrieve and compare content |
| `TestArangoStorage_Idempotent` | Integration | Same object written twice → same hash, no error |
| `TestArangoStorage_RefLifecycle` | Integration | Set / read / update / remove a branch ref |
| `TestArangoStorage_Index` | Integration | Write and read staging index with one entry |
| `TestArangoStorage_Config` | Integration | Write and read repo config (`IsBare=false`) |
| `TestArangoStorage_Concurrent` | Integration | 10 goroutines write distinct objects — no race, all retrievable |
| `TestArangoBackend_FullWorkflow` | Integration | Init → branch → commit → fast-forward merge end-to-end via `git.Open` |
| `TestArangoStorage_ConnectionError` | Unit (no server) | Nil database → returns wrapped error, not panic |
| `TestArangoBackend_DeleteAndPurge` | Integration | Delete (soft) then Purge; OpenStorer returns ErrRepoNotFound after purge |

**Integration test skip condition**: All tests requiring a live server call `openTestDB(t)` which skips with `t.Skipf` if `ARANGODB_URL` is unreachable (default `http://localhost:8529`). Environment variables: `ARANGODB_URL`, `ARANGODB_USER`, `ARANGODB_PASS`.

---

## Test Run

```
go test -v -race -count=1 ./...
# Root package:     62 PASS (unchanged)
# storage/arangodb: 2 PASS, 7 SKIP (no ArangoDB server)
# storage/filesystem: 11 PASS (unchanged)
go vet ./...    # 0 issues
go build ./...  # clean
```
