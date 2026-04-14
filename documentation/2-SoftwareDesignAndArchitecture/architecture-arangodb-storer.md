# ArangoDB as go-git `storage.Storer` — Architecture (v3)

## 1. Motivation

### The v2 Split Problem

The v2 design (GIT-001 through GIT-010) introduced two **independent** storage
layers inside one process:

| Path | Storage | Collections |
|---|---|---|
| gRPC `GitManager` | `entitygraph.DataManager` → ArangoDB | `git_entities`, `git_objects` (entity UUIDs) |
| Git Smart HTTP | `codevaldgit.Backend` → **filesystem** | `.git/` directories on disk |

Data written by the gRPC path (entities with UUID keys, JSON properties) never
reaches the filesystem. Data the git wire protocol needs (SHA-keyed raw binary
objects, packed refs, git config) never reaches ArangoDB. The two stores are
structurally incompatible and never synchronise.

**Consequence**: a `git clone http://cross:8080/{agencyID}` returns an error
because no `.git/` directory exists on disk for an agency whose repository was
created via the gRPC `InitRepo` call.

### v3 Goal

Replace both storage layers with a **single ArangoDB-backed `storage.Storer`**
that satisfies the go-git plumbing interface directly. Both the gRPC
`GitManager` and the git Smart HTTP handler read and write the same ArangoDB
documents. No filesystem. No entity graph. One source of truth.

---

## 2. go-git `storage.Storer` Interface

go-git separates storage from repository logic via five composable interfaces:

```go
// storage.Storer — what we implement in ArangoDB
type Storer interface {
    storer.EncodedObjectStorer // blob/tree/commit/tag objects (SHA-keyed)
    storer.ReferenceStorer     // branch/tag refs and HEAD
    storer.IndexStorer         // staging area index
    storer.ShallowStorer       // shallow clone markers
    config.ConfigStorer        // per-repo git config
    ModuleStorer               // submodule storers (return self)
}
```

`EncodedObjectStorer` is the most critical — it stores raw encoded git objects
(the bytes written into `.git/objects/`). Once this interface is satisfied,
go-git's `Repository.CommitObject`, `Repository.TreeObject`, worktree
operations, ref walking, and the `gogitserver.Transport` (Smart HTTP) all work
without modification.

---

## 3. ArangoDB Collection Design

All collections live in the existing `codevaldgit` database. The `agencyID` is
embedded in every document key so multiple agencies share collections without
per-agency collection creation.

### 3.1 Collection Inventory

| Collection | ArangoDB Type | Key Format | Purpose |
|---|---|---|---|
| `gitraw_objects` | Document | `{agencyID}/{sha}` | Raw encoded git objects |
| `gitraw_refs` | Document | `{agencyID}/{refName}` | Branch/tag references and HEAD |
| `gitraw_config` | Document | `{agencyID}` | Per-repo git config |
| `gitraw_index` | Document | `{agencyID}` | Staging area index |
| `gitraw_shallow` | Document | `{agencyID}` | Shallow commit hash list |

These are **separate from** the existing entitygraph collections (`git_entities`,
`git_objects`, `git_relationships`). During GIT-015 both sets coexist. The
entitygraph collections are removed in GIT-016 once the gRPC implementation is
rewritten.

### 3.2 Document Schemas

**`gitraw_objects`** — one document per unique git object per agency:
```json
{
  "_key":     "demo-agency/e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
  "agencyID": "demo-agency",
  "sha":      "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
  "objType":  1,
  "size":     0,
  "data":     "<base64-encoded raw object bytes>"
}
```

`objType` mirrors go-git's `plumbing.ObjectType` enum: `1`=Commit, `2`=Tree,
`3`=Blob, `4`=Tag. `data` holds the **raw encoded bytes** go-git writes for that
object type — identical to what would appear inside `.git/objects/`.

Object writes are idempotent: two concurrent inserts with the same `_key` → the
second returns `409 Conflict` → writer ignores it (content is identical by the
SHA contract).

**`gitraw_refs`** — one document per reference per agency:
```json
{ "_key": "demo-agency/refs/heads/main", "agencyID": "demo-agency",
  "refName": "refs/heads/main", "target": "abc123..." }
```
Symbolic refs (e.g. HEAD → refs/heads/main) use a `symbolic` field instead of
`target`:
```json
{ "_key": "demo-agency/HEAD", "agencyID": "demo-agency",
  "refName": "HEAD", "symbolic": "refs/heads/main" }
```

**`gitraw_config`** — one document per repo:
```json
{ "_key": "demo-agency", "agencyID": "demo-agency", "data": "<base64 config>" }
```

**`gitraw_index`** — one document per repo (updated on every staging operation):
```json
{ "_key": "demo-agency", "agencyID": "demo-agency", "data": "<base64 index>" }
```

**`gitraw_shallow`** — one document per repo:
```json
{ "_key": "demo-agency", "agencyID": "demo-agency", "hashes": ["sha1", "sha2"] }
```

### 3.3 Indexes

| Collection | Index | Type | Purpose |
|---|---|---|---|
| `gitraw_objects` | `[agencyID, sha]` | Unique persistent | Deduplication + lookup by SHA without `_key` |
| `gitraw_refs` | `[agencyID, refName]` | Persistent | Iteration over all refs for an agency |

---

## 4. Component Architecture

```
        ┌──────────────────────────────────┐
        │  cmd/main.go                     │
        │  ArangoStorer per agency or      │
        │  shared DB — Backend constructed │
        │  once; passed to both consumers  │
        └────────────┬─────────────────────┘
                     │
          ┌──────────┴──────────┐
          │                     │
   gRPC GitService        Git Smart HTTP
   (GitManager)           (GitHTTPHandler)
          │                     │
          ▼                     ▼
   go-git Repository     gogitserver.Transport
   (plumbing layer)       (wire protocol)
          │                     │
          └──────────┬──────────┘
                     │
              ArangoStorer
              (storage.Storer)
                     │
              ArangoDB DB
              gitraw_objects
              gitraw_refs
              gitraw_config
              gitraw_index
              gitraw_shallow
```

### 4.1 `storage/arangodb/storer.go` — New File

`arangoStorer` implements all five sub-interfaces of `storage.Storer`. It holds
a `driver.Database` reference and an `agencyID` string. Every method translates
to an ArangoDB document read or write.

Key method contracts:

| Method | ArangoDB operation |
|---|---|
| `SetEncodedObject(obj)` | Upsert into `gitraw_objects` at `_key={agencyID}/{obj.Hash()}` |
| `EncodedObject(type, hash)` | Read from `gitraw_objects` by `_key`; deserialise |
| `HasEncodedObject(hash)` | `HEAD` check on `gitraw_objects` |
| `IterEncodedObjects(type)` | AQL `FOR doc IN gitraw_objects FILTER doc.agencyID == @a AND doc.objType == @t` |
| `SetReference(ref)` | Upsert into `gitraw_refs` |
| `Reference(name)` | Read from `gitraw_refs` by `_key` |
| `IterReferences()` | AQL `FOR doc IN gitraw_refs FILTER doc.agencyID == @a` |
| `RemoveReference(name)` | Delete from `gitraw_refs` by `_key` |
| `SetConfig(cfg)` | Serialise and upsert into `gitraw_config` |
| `Config()` | Read from `gitraw_config` by `_key`; deserialise |
| `SetIndex(idx)` | Serialise and upsert into `gitraw_index` |
| `Index()` | Read from `gitraw_index` by `_key`; deserialise |
| `SetShallow(hashes)` | Upsert into `gitraw_shallow` |
| `Shallow()` | Read from `gitraw_shallow` by `_key` |
| `Module(name)` | Return `newArangoStorer(db, agencyID+"/"+name)` |

### 4.2 `storage/arangodb/backend.go` — New File

`arangoBackend` implements `codevaldgit.Backend`:

| Method | Implementation |
|---|---|
| `InitRepo(ctx, agencyID)` | `Init()` on a new `arangoStorer`; write initial empty commit; set HEAD → refs/heads/main |
| `OpenStorer(ctx, agencyID)` | Return `arangoStorer` + `memfs.New()` (in-memory working tree; Smart HTTP only reads the object store) |
| `DeleteRepo(ctx, agencyID)` | AQL delete all docs in all `gitraw_*` collections where `agencyID == @a` |
| `PurgeRepo(ctx, agencyID)` | Same as `DeleteRepo` (no archive distinction; ArangoDB is durable) |

### 4.3 `GitManager` Rewrite — gRPC Implementation

The current entitygraph-based implementation (`git_impl_repo.go`,
`git_impl_fileops.go`) is replaced with go-git plumbing calls on the
`arangoStorer`. The `GitManager` interface (`git.go`) is unchanged — callers
(gRPC handlers) do not change.

Representative translations:

| GitManager method | Current (entitygraph) | New (go-git plumbing) |
|---|---|---|
| `InitRepo` | `dm.CreateEntity(…TypeRepository…)` | `backend.InitRepo(ctx, agencyID)` |
| `CreateBranch` | `dm.CreateEntity(…TypeBranch…)` + relationship | `gogit.Open(storer, nil)` → `repo.CreateRemoteAnonymous()` + ref write |
| `WriteFile` | `dm.CreateEntity(…TypeBlob…)` | Open repo → worktree `Commit` via go-git's `AddFile` + `Commit` |
| `ReadFile` | `dm.GetEntity(…TypeBlob…)` | `repo.CommitObject(ref)` → `tree.File(path)` → `.Contents()` |
| `ListBranches` | `dm.ListEntities(filter)` | `repo.References()` filtered to `refs/heads/` |
| `Log` | walk Commit entities via relationships | `repo.Log(opts)` |
| `Diff` | compare Tree entities | `gogit.Diff(a, b)` |

---

## 5. Migration Path

### Phase 1 — GIT-015 (This Task)

1. Implement `storage/arangodb/storer.go` (`arangoStorer` — `storage.Storer`).
2. Implement `storage/arangodb/backend.go` (`arangoBackend` — `codevaldgit.Backend`).
3. Rewrite `git_impl_repo.go` using go-git plumbing on the storer.
4. Rewrite `git_impl_fileops.go` using go-git plumbing on the storer.
5. Update `cmd/main.go`: remove filesystem backend; pass `arangoBackend` to both
   `NewGitManager` (replacing `DataManager`) and `NewGitHTTPHandler`.
6. Remove `storage/filesystem/` import from `cmd/main.go`; remove env vars
   `GIT_REPOS_BASE_PATH` and `GIT_REPOS_ARCHIVE_PATH` from `internal/config/`.
7. Remove entitygraph dependency from `gitManager` (keep it for schema seed only).
8. `go build ./...` → `go test -race ./...` → `make integration-test` must all pass.

### Phase 2 — GIT-016 (Future)

Remove the entitygraph schema seed entirely. Drop `git_entities`, `git_objects`
(entitygraph), `git_relationships`, `git_schemas_draft`, `git_schemas_published`
from the ArangoDB schema. The `GitSchemaManager` / `DefaultGitSchema()` surface
is removed. `storage/arangodb/arangodb.go` (the entitygraph adapter) is deleted.

---

## 6. Cross-References

| Document | Relevance |
|---|---|
| [architecture-arangodb.md](architecture-arangodb.md) | v2 entitygraph design — being superseded by this doc |
| [architecture-concurrency.md](architecture-concurrency.md) | CAS for concurrent `MergeBranch` — still needed; refs are now ArangoDB docs with `_rev` |
| [mvp-details/arangodb-storer.md](../../3-SofwareDevelopment/mvp-details/arangodb-storer.md) | GIT-015 task specification |
| [CodeValdCross architecture.md](../../../CodeValdCross/documentation/2-SoftwareDesignAndArchitecture/architecture.md) | Cross HTTP transit for git Smart HTTP (CROSS-010) |
