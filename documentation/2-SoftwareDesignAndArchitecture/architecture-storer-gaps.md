# GIT-015 — ArangoDB Storer: Gap Analysis and Resolved Solutions

> **Status**: ✅ Six gaps resolved and implemented (2026-04-14). ⚠️ Gap 7 identified
> (2026-04-15) — fix tracked as GIT-017.
> GIT-015a through GIT-015e are complete; `go build ./...` and
> `go test -race ./...` pass. See
> [mvp-details/arangodb-storer.md](../../3-SofwareDevelopment/mvp-details/arangodb-storer.md)
> for the implementation notes and final acceptance criteria status.

Design review of the GIT-015 `storage.Storer` implementation identified six
structural gaps between the entitygraph data model and what go-git's interface
requires. This document records each gap and its resolved solution.

**Governing design decision**: Use the real git binary SHA (SHA-1 computed by
go-git's plumbing layer over the binary object format) as the primary
identifier for **all** git objects — Blob, Tree, Commit — across both the gRPC
path and the storer path. The synthetic SHA produced by `contentSHA()` in
`git_impl_fileops.go` is removed. One SHA space; both paths are fully
interoperable.

---

## Summary

| # | Gap | Difficulty | Touches | Status |
|---|---|---|---|---|
| 1 | Tree binary reconstruction | Hard | `schema.go`, `git_impl_fileops.go`, `storer.go` | ✅ |
| 2 | `advanceBranchHead` missing `sha` write | Easy | `git_impl_repo.go` (~3 lines) | ✅ |
| 3 | Real vs synthetic SHA for Commit/Tree/Blob | Hard | `git_impl_fileops.go`, `storer.go` | ✅ |
| 4 | `InitRepo` ownership | Easy | `storage/arangodb/backend.go` | ✅ |
| 5 | `storer.go` full DataManager-backed rewrite | Medium | `storage/arangodb/storer.go`, `backend.go` | ✅ |
| 6 | Blob name vs full path in tree entries | Easy | Resolved by Gap 1 | ✅ |
| 7 | gRPC-written objects missing `data` property — invisible to `git pull` | Medium | `git_impl_fileops.go` | 🔴 GIT-017 |

---

## Gap 1 — Tree Binary Reconstruction

### Problem

`EncodedObject(TreeObject, hash)` must return the raw binary git tree object.
Reconstructing it requires each child entry's **name** (basename), **mode**,
and **SHA**. The current Tree entity stores only `sha` and `path`; children are
reachable only via `has_blob` / `has_subtree` relationships. Resolving every
child is N+1 reads per tree — prohibitive during `git clone`.

### Solution

Add an `entries` property (`PropertyTypeString`) to the Tree `TypeDefinition`
in `schema.go`. Its value is a JSON-encoded array of child entry objects:

```json
[
  {"name": "README.md", "mode": "100644", "sha": "e69de29..."},
  {"name": "src",       "mode": "040000", "sha": "4b825dc..."}
]
```

- `name` — basename only (what go-git's `object.TreeEntry.Name` holds)
- `mode` — git file-mode string: `100644` regular, `100755` executable,
  `040000` subtree, `120000` symlink
- `sha` — real git binary SHA of the child object (see Gap 3)

**Writer**: `git_impl_fileops.go`'s `WriteFile`. Before creating the Tree
entity, collect all child `object.TreeEntry` values (from the go-git plumbing
layer) and serialise them to JSON for the `entries` property.

**Reader**: `storer.go`'s `EncodedObject(TreeObject, hash)`. One `GetEntity`
call suffices — no relationship traversal needed.

**Retained**: `has_blob` and `has_subtree` relationships are kept because the
gRPC path (`ListDirectory`, `TraverseGraph`) relies on them.

### Schema Change

In `schema.go`, Tree `TypeDefinition`, add after the `path` property:

```go
// entries is a JSON-encoded array of child entries used by the go-git
// storage.Storer to reconstruct the binary tree object without N+1 reads.
// Each element: {"name":"<basename>","mode":"<git-mode>","sha":"<40hex>"}.
{Name: "entries", Type: types.PropertyTypeString},
```

---

## Gap 2 — `advanceBranchHead` Missing `sha` Write

### Problem

`schema.go` already declares `sha` on the Branch entity as the storer's
fast-path for `Reference("refs/heads/X")`. However, `advanceBranchHead` in
`git_impl_repo.go` only writes `head_commit_id` (the Commit entity UUID) and
never populates `sha`. The storer therefore returns a zero hash for every
branch ref, causing `git clone` to fail immediately.

### Solution

After creating the `points_to` edge and writing `head_commit_id`, fetch the
new Commit entity and copy its `sha` property onto the Branch entity:

```go
// in git_impl_repo.go — advanceBranchHead, after writing head_commit_id
commit, err := m.dm.GetEntity(ctx, m.agencyID, newCommitID)
if err != nil {
    return Branch{}, fmt.Errorf("advanceBranchHead: fetch commit sha: %w", err)
}
sha, _ := commit.Properties["sha"].(string)
updated, err = m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
    Properties: map[string]any{
        "sha":        sha,
        "updated_at": now,
    },
})
```

`sha` here is the real git commit SHA (40-char hex) written by `WriteFile`
after Gap 3 is applied.

---

## Gap 3 — Commit/Tree/Blob SHA: Real vs Synthetic

### Problem

`git_impl_fileops.go` computes SHAs using `contentSHA()` — SHA-1 over a
composite of Go string values. go-git computes SHAs over the **binary git
object format** (`{type} {size}\0{payload}`). These two SHA spaces produce
different hashes for the same content. A Blob created via gRPC has SHA `A`
(synthetic); the same blob the storer reads back has SHA `B` (real). They
cannot share the same entity document.

### Resolution ✅ — Real git binary SHA everywhere

`git_impl_fileops.go` must compute SHAs using go-git's plumbing layer:

```go
// Blob SHA
blobObj := plumbing.MemoryObject{}
blobObj.SetType(plumbing.BlobObject)
w, _ := blobObj.Writer()
_, _ = w.Write([]byte(req.Content))
_ = w.Close()
blobSHA := blobObj.Hash().String() // real git blob SHA

// Tree SHA — build object.Tree, call Encode, read Hash()
treeObj := plumbing.MemoryObject{}
treeObj.SetType(plumbing.TreeObject)
tree := object.Tree{Entries: []object.TreeEntry{…}}
_ = tree.Encode(&treeObj)
treeSHA := treeObj.Hash().String()

// Commit SHA — build object.Commit, call Encode, read Hash()
commitObj := plumbing.MemoryObject{}
commitObj.SetType(plumbing.CommitObject)
commit := object.Commit{Message: req.Message, TreeHash: …}
_ = commit.Encode(&commitObj)
commitSHA := commitObj.Hash().String()
```

The entity `sha` property and the DataManager lookup key are both the real git
SHA. `contentSHA()` and `commitSHA()` helpers are **removed** from
`git_impl_fileops.go`.

**Consequence**: `storer.go`'s `SetEncodedObject` and `EncodedObject` use the
same SHA that `WriteFile` stored. Both paths read and write the same entity
document. No secondary `git_sha` property is needed.

---

## Gap 4 — `InitRepo` Ownership

### Problem

`gitManager.InitRepo` (gRPC path) already creates Agency, Repository, and
Branch entities. `arangoBackend.InitRepo` (called by the old `RepoManager`
path) also tries to write an initial commit, creating a double-write hazard.
`gitHTTPHandler` calls only `OpenStorer`, never `InitRepo`.

### Resolution

- **`gitManager.InitRepo`** owns all entity creation: Agency + Repository +
  Branch + the initial empty Commit/Tree written with real git SHAs.
- **`Backend.InitRepo`** is a no-op stub (or delegates back to
  `gitManager.InitRepo` if called from an independent context). It does not
  create any ArangoDB documents on its own.
- **`Backend.OpenStorer`** verifies that a Repository entity exists for the
  agency (returns `ErrRepoNotInitialised` if not), then returns a
  DataManager-backed `arangoStorer`. It performs no writes.

---

## Gap 5 — `storer.go`: DataManager-Backed Rewrite

### Problem

`storer.go` (576 lines) holds a raw `driver.Database` and owns five
`gitraw_*` collections that duplicate the entitygraph collections. The schema
already provides `git_objects` (Blob/Tree/Commit), `git_entities`
(Branch/Tag/Repository), and `GitInternalState` (config/index/shallow). The
two storage layers must be unified.

### Solution

Replace `driver.Database` with `entitygraph.DataManager`. The `gitraw_*`
collections and all associated bootstrap code (`ensureGitRawCollections`,
`colObjects`, `colRefs`, etc.) are deleted.

**Constructor change**:

```go
// Before
func newArangoStorer(db driver.Database, agencyID string) *arangoStorer

// After
func newArangoStorer(dm entitygraph.DataManager, agencyID string) *arangoStorer
```

**Interface-to-DataManager mapping**:

| go-git method | DataManager operation |
|---|---|
| `SetEncodedObject(blob)` | `CreateEntity` TypeBlob, `sha`=hash, `content`=base64 |
| `SetEncodedObject(tree)` | `CreateEntity` TypeTree, `sha`=hash, `entries`=JSON |
| `SetEncodedObject(commit)` | `CreateEntity` TypeCommit, `sha`=hash, properties |
| `EncodedObject(type, hash)` | `ListEntities` filter `TypeID`+`sha`=hash → deserialise |
| `HasEncodedObject(hash)` | Same as above; check for `ErrEntityNotFound` |
| `IterEncodedObjects(type)` | `ListEntities` filter `TypeID`=Blob\|Tree\|Commit |
| `SetReference(refs/heads/X)` | `ListEntities` TypeBranch filter `name`=X → `UpdateEntity` `sha`=hash |
| `SetReference(HEAD symbolic)` | `UpdateEntity` Repository `head_ref`=target |
| `Reference(refs/heads/X)` | `ListEntities` TypeBranch filter `name`=X → `plumbing.NewHashReference` |
| `Reference(HEAD)` | `GetEntity` Repository → read `head_ref` → `plumbing.NewSymbolicReference` |
| `IterReferences()` | `ListEntities` TypeBranch + TypeTag → wrap as `storer.ReferenceSliceIter` |
| `RemoveReference(name)` | `DeleteEntity` Branch or Tag |
| `SetConfig(cfg)` | `UpsertEntity` GitInternalState `state_type`=`"config"`, `data`=base64 |
| `Config()` | `ListEntities` GitInternalState filter `state_type`=`"config"` → deserialise |
| `SetIndex(idx)` | `UpsertEntity` GitInternalState `state_type`=`"index"`, `data`=base64 |
| `Index()` | `ListEntities` GitInternalState filter `state_type`=`"index"` → deserialise |
| `SetShallow(hashes)` | `UpsertEntity` GitInternalState `state_type`=`"shallow"`, `data`=newline-joined |
| `Shallow()` | `ListEntities` GitInternalState filter `state_type`=`"shallow"` → split |
| `Module(name)` | Return `newArangoStorer(dm, agencyID+"/module/"+name)` |
| `PackRefs()` | no-op |
| `AddAlternate(…)` | no-op |

`CheckAndSetReference` for concurrent `MergeBranch` safety: read the Branch
entity's current `sha`, verify it matches `old.Hash()`, then update with the
DataManager's optimistic-lock (`_rev`-based) update call. If the update
returns a conflict, return `storage.ErrReferenceHasChanged`.

**`git_objects` index**: add a persistent index on `[agencyID, sha]` in
`storage/arangodb/arangodb.go` collection bootstrap. This makes
`EncodedObject` lookups O(1) rather than a full collection scan.

---

## Gap 6 — Blob Name vs Full Path in Tree Entries

### Problem

go-git `object.TreeEntry.Name` holds the **basename** only (`file.go`). The
current Blob entity stores `path` as the full repo-relative path
(`src/pkg/file.go`). Deriving the correct basename from the full path requires
knowing the parent directory prefix, which is not always available in the
storer context.

### Solution

Resolved entirely by Gap 1. The `entries` property on each Tree entity
explicitly stores `name` (basename), `mode`, and `sha` for every child. The
storer reconstructs the binary tree object directly from `entries` — no
basename derivation from `Blob.path` is required. `Blob.path` is unchanged and
continues to serve the gRPC file-operations path (`ReadFile`, `ListDirectory`).

---

## Gap 7 — gRPC-Written Objects Missing `data` Property

> **Identified**: 2026-04-15. **Fix**: GIT-017.
> Full analysis in
> [architecture-pull-flow.md](architecture-pull-flow.md).

### Problem

`WriteFile` creates Blob, Tree, and Commit entities via `dm.CreateEntity` with
human-readable properties (`path`, `name`, `content`, `entries`, etc.) but
**does not populate the `data` property** (the base64-encoded raw git object
bytes) that `arangoStorer.EncodedObject` requires to reconstruct an object for
the git wire protocol.

When a client runs `git pull` / `git clone` after a gRPC `WriteFile` call:

1. `IterReferences()` advertises the branch tip SHA correctly (from
   `Branch.sha`, written by `advanceBranchHead` — Gap 2 fix).
2. `EncodedObject(CommitObject, sha)` queries the Commit entity → reads
   `Properties["data"]` → the key is absent → returns error.
3. `IterEncodedObjects` silently skips entities without `data` (comment:
   `// skip entities without raw data (e.g. created by GitManager layer)`).
4. go-git cannot assemble the packfile → the client receives an incomplete or
   empty pack → the files are missing from the working tree.

### Root Cause

The `WriteFile` code path already computes `plumbing.MemoryObject` instances
for all three object types (blob, tree, commit) in order to derive their SHAs.
It reads the bytes only to hash them, then discards them without storing them
as `data`.

### Solution (GIT-017)

After computing each `plumbing.MemoryObject`, read the raw bytes and
base64-encode them into a `"data"` property alongside the existing properties:

```go
// blob
blobR, _ := blobObj.Reader()
blobRaw, _ := io.ReadAll(blobR)
blobR.Close()
// pass "data": base64.StdEncoding.EncodeToString(blobRaw)
// alongside sha, path, name, … to dm.CreateEntity(TypeID="Blob", …)

// tree
treeR, _ := treeMemObj.Reader()
treeRaw, _ := io.ReadAll(treeR)
treeR.Close()
// pass "data": base64.StdEncoding.EncodeToString(treeRaw)

// commit
commitR, _ := commitMemObj.Reader()
commitRaw, _ := io.ReadAll(commitR)
commitR.Close()
// pass "data": base64.StdEncoding.EncodeToString(commitRaw)
```

No schema change is required. `EncodedObject` and `IterEncodedObjects` already
handle the `data` property; they will now find it on gRPC-written entities.

### Acceptance Criteria (GIT-017)

- [ ] `WriteFile` stores `"data"` (base64 raw bytes) on Blob, Tree, and Commit
  entities
- [ ] `git clone` after gRPC `WriteFile` produces the correct working tree
- [ ] `git pull` on a branch that has only gRPC commits downloads all files
- [ ] `go test -race ./...` passes (existing `file_operations_test.go`
  must remain green)
- [ ] `EncodedObject(BlobObject, sha)` succeeds for blobs written by `WriteFile`
- [ ] `IterEncodedObjects(BlobObject)` includes blobs written by `WriteFile`

---

## Cross-References

| Document | Relevance |
|---|---|
| [architecture-arangodb-storer.md](architecture-arangodb-storer.md) | Earlier v3 design using `gitraw_*` collections — superseded by Gap 5 |
| [architecture-arangodb.md](architecture-arangodb.md) | Entitygraph collection schema (git_entities, git_objects, git_relationships, git_internal) |
| [architecture-concurrency.md](architecture-concurrency.md) | `CheckAndSetReference` CAS pattern for concurrent `MergeBranch` |
| [mvp-details/arangodb-storer.md](../../3-SofwareDevelopment/mvp-details/arangodb-storer.md) | GIT-015 task specification — updated to reflect these resolutions |
