# Git Pull / Clone Flow — Architecture & Gap Analysis

> **Last updated**: 2026-04-15
> **Status**: 🔴 Gap 7 (gRPC-data gap) identified — fix tracked as GIT-017.

This document answers two questions that were not covered by the GIT-015 gap
analysis:

1. **Where does the content of a file come from when a client runs `git pull`
   (or `git clone`)?**
2. **How does the server select the correct version of a file when a client
   checks out a specific branch?**

It also documents a newly discovered gap (Gap 7 — the gRPC-data gap) that
makes files written via the gRPC `WriteFile` RPC invisible to `git pull`.

---

## 1. What Happens During `git pull` / `git clone`

Git uses the **Smart HTTP protocol** (RFC 1951-style pkt-line framing). The
sequence is two HTTP round-trips.

### Round-trip 1 — Reference Advertisement (`info/refs`)

```
Client → GET /{agencyID}/{repoName}/info/refs?service=git-upload-pack
Server ← 200 OK (Content-Type: application/x-git-upload-pack-advertisement)
         pkt-line: "# service=git-upload-pack\n" + flush
         pkt-line: capabilities (ofs-delta, side-band-64k, …)
         pkt-line: "sha1 refs/heads/main\n"
         pkt-line: "sha1 refs/heads/task/abc-001\n"
         pkt-line: "sha1 refs/tags/v1.0.0\n"
         flush
```

**Server path** (`internal/server/githttp.go` → `infoRefs`):

1. `srv.NewUploadPackSession(ep, nil)` opens the go-git session with the
   ArangoDB-backed storer for the requested `agencyID/repoName`.
2. `sess.AdvertisedReferencesContext(ctx)` calls
   **`arangoStorer.IterReferences()`**, which issues three `ListEntities`
   queries:
   - `TypeID=Repository` → reads `head_ref` property → emits
     `HEAD → refs/heads/main` (symbolic)
   - `TypeID=Branch` → reads every Branch entity's `name` + `sha` → emits
     `refs/heads/{name} → {sha}` (hash ref); branches with empty or zero SHA
     are omitted (no commits yet)
   - `TypeID=Tag` → reads every Tag entity's `name` + `sha` → emits
     `refs/tags/{name} → {sha}`

The client compares the advertised SHAs against its local refs to decide which
objects it is missing.

### Round-trip 2 — Pack Transfer (`git-upload-pack`)

```
Client → POST /{agencyID}/{repoName}/git-upload-pack
         pkt-line: "want sha1\n"   ← commit SHA the client needs
         pkt-line: "have sha1\n"   ← commit SHA the client already has
         pkt-line: "done\n"
Server ← 200 OK (Content-Type: application/x-git-upload-pack-result)
         PACK header + delta-compressed stream of all missing objects
```

**Server path** (`internal/server/githttp.go` → `uploadPack`):

1. `sess.UploadPack(ctx, req)` — go-git computes the minimal set of objects
   the client needs (commit → tree → blobs, back to the first commit the client
   already has).
2. For every missing object, go-git calls **`arangoStorer.EncodedObject(type,
   sha)`**:
   - `ListEntities(AgencyID=agencyID, TypeID="Commit"|"Tree"|"Blob", sha=sha)`
   - Reads the `data` property from the matching entity
   - `base64.StdEncoding.DecodeString(data)` → raw bytes
   - Returns `plumbing.MemoryObject{type, size, raw_bytes}`
3. go-git encodes all objects into a delta-compressed packfile and streams it
   back.

The client unpacks: for each commit it needs to materialise, it reads the root
Tree object, walks child Tree and Blob objects, and writes each Blob's content
bytes to the working-tree file at the Blob's path.

---

## 2. How the Correct File Version Is Selected

Every `git pull` for a specific branch follows this object chain:

```
Branch entity         Commit entity         Tree entity          Blob entity
Branch.sha ──────────► Commit.sha           Tree.sha             Blob.sha
(refs/heads/main)      Commit.tree_sha ────► Tree.entries JSON   Blob.data (raw)
                       (go-git reads         [{name, mode, sha}]  ↑
                        the tree)             each sha → Blob      file content
```

Step by step:

| Step | go-git call | `arangoStorer` query | Result |
|---|---|---|---|
| 1 | Resolve `refs/heads/main` | `ListEntities(TypeID=Branch, name=main)` → read `sha` | `abc123…` (commit SHA) |
| 2 | `EncodedObject(Commit, abc123)` | `ListEntities(TypeID=Commit, sha=abc123)` → read `data` | raw commit bytes |
| 3 | Decode commit → `TreeHash` | (go-git in-process decode) | `def456…` (tree SHA) |
| 4 | `EncodedObject(Tree, def456)` | `ListEntities(TypeID=Tree, sha=def456)` → read `data` | raw tree bytes |
| 5 | Decode tree → `Entries[]` | (go-git in-process decode) | `[{name=README.md, sha=ghi789, …}]` |
| 6 | `EncodedObject(Blob, ghi789)` | `ListEntities(TypeID=Blob, sha=ghi789)` → read `data` | raw blob bytes |
| 7 | Write working-tree file | (git client, local) | `README.md` written to disk |

**Different versions** are served by selecting a different `Branch.sha` (step 1).
Because commits are content-addressed by SHA, a commit on `task/abc-001` has
a different tree SHA → different blob SHAs for any changed files → the storer
returns the specific bytes for that snapshot. Unchanged files share the same
blob SHA across commits — they are served from the same ArangoDB entity.

---

## 3. ⚠️ Gap 7 — gRPC-Written Files Are Invisible to `git pull`

> **Severity**: High. Any file written via the gRPC `WriteFile` RPC cannot be
> retrieved by a `git pull` / `git clone`.
> **Fix**: GIT-017. See
> [mvp-details/arangodb-storer.md](../3-SofwareDevelopment/mvp-details/arangodb-storer.md)
> for the task.

### Root Cause

`WriteFile` (in `git_impl_fileops.go`) creates Blob, Tree, and Commit entities
using `dm.CreateEntity`. These entities carry human-readable properties but
**do not include the `data` property** (the base64-encoded raw git object
bytes) required by `arangoStorer.EncodedObject`.

| Property | gRPC `WriteFile` path | Smart HTTP push path |
|---|---|---|
| `sha` | ✅ real git SHA (go-git plumbing) | ✅ real git SHA |
| `data` | ❌ **missing** | ✅ base64(raw object bytes) |
| `size` | ✅ byte length of content string | ✅ raw byte length |
| `path` | ✅ repo-relative file path | ❌ (backfilled later by `backfillBlobEntity`) |
| `name` | ✅ basename | ❌ (backfilled later) |
| `extension` | ✅ | ❌ (backfilled later) |
| `encoding` | ✅ `"utf-8"` / `"base64"` | ❌ |
| `content` | ✅ raw file content string | ❌ |
| `entries` | ✅ (Tree only — JSON entries array) | ❌ (not stored by SetEncodedObject) |

`arangoStorer.decodeEntityToObject` — the function used by `EncodedObject` —
reads `e.Properties["data"]` and returns an error when the property is absent:

```go
dataRaw, ok := e.Properties["data"]
if !ok {
    return nil, fmt.Errorf("entity %s has no data property", e.ID)
}
```

`IterEncodedObjects` explicitly skips these entities rather than failing:

```go
obj, err := decodeEntityToObject(e)
if err != nil {
    continue // skip entities without raw data (e.g. created by GitManager layer)
}
```

This means:
- `EncodedObject(Blob, sha)` returns `plumbing.ErrObjectNotFound` for any blob
  written by `WriteFile`.
- `EncodedObject(Tree, sha)` returns `plumbing.ErrObjectNotFound` for any tree
  written by `WriteFile`.
- `EncodedObject(Commit, sha)` returns `plumbing.ErrObjectNotFound` for any
  commit written by `WriteFile`.
- go-git's upload-pack session cannot assemble the packfile → `git pull` for a
  branch that has only gRPC commits returns an error or an empty pack.

### The Backfill Asymmetry

The existing backfill mechanism (`backfillBlobsFromSHA`) runs in the
**opposite direction**: it fills human-readable metadata (`name`, `path`,
`extension`) onto blobs that arrive via `SetEncodedObject` (which only stores
`sha`, `data`, `size`). It does **not** synthesise `data` bytes for entities
that already have `name`/`path` but lack `data`.

```
Push path:   Blob{sha, data, size}  ──backfill──►  Blob{sha, data, size, name, path, ext}
gRPC path:   Blob{sha, name, path, size, content}  ← NO backfill of data ← git pull fails
```

### Fix (GIT-016)

In `git_impl_fileops.go`'s `WriteFile`, the raw `plumbing.MemoryObject` bytes
are already computed for all three object types. The fix is to also base64-
encode them and include a `"data"` key in each `CreateEntity` call:

```go
// -- already in WriteFile --
blobObj := &plumbing.MemoryObject{}
blobObj.SetType(plumbing.BlobObject)
blobW, _ := blobObj.Writer()
_, _ = blobW.Write([]byte(req.Content))
_ = blobW.Close()
blobHash := blobObj.Hash()

// -- add this --
blobR, _ := blobObj.Reader()
blobRaw, _ := io.ReadAll(blobR)
blobR.Close()
blobDataB64 := base64.StdEncoding.EncodeToString(blobRaw)
```

Then pass `"data": blobDataB64` alongside the other properties when calling
`dm.CreateEntity` for the Blob, Tree, and Commit entities.

No schema change is required: the `data` property is already stored on
Blob/Tree/Commit entities by the push path; `WriteFile` simply needs to
populate it too.

After this fix:
- `EncodedObject` finds `data` → decodes → returns `plumbing.MemoryObject` ✅
- `IterEncodedObjects` includes the entity ✅
- `git pull` after a gRPC `WriteFile` serves the file correctly ✅

The inverse asymmetry (push-written blobs lacking `encoding`/`content`) is
intentional: gRPC `ReadFile` reads the `content` property directly; blobs from
a push do not need it because they are only accessed via the git wire protocol
(which uses `data`). The backfill mechanism (`backfillBlobsFromSHA`) fills
`name`/`path` on push blobs so `ListDirectory` and `ReadFile` work on
push-written files.

---

## 4. Full Object Lifecycle Comparison

| Event | Blob entity properties after | `git pull` works? | gRPC `ReadFile` works? |
|---|---|---|---|
| `WriteFile` (gRPC) | `sha`, `path`, `name`, `ext`, `size`, `encoding`, `content` | ❌ — no `data` | ✅ reads `content` |
| `WriteFile` + GIT-016 fix | + `data` (raw bytes) | ✅ | ✅ |
| `git push` | `sha`, `data`, `size` (then `name`, `path`, `ext` backfilled) | ✅ reads `data` | ✅ reads `content` after backfill? |

> **Note on gRPC `ReadFile` for push-written blobs**: `ReadFile` traverses
> the entity graph (Branch → HeadCommit → Tree → Blob) and reads
> `Blob.content`. Push-written blobs have `sha` and `data` but no `content`
> property — `ReadFile` returns an empty string or an error in this case.
> This is a **separate gap** tracked separately from GIT-016.

---

## 5. Object Lookup Performance

`EncodedObject(type, sha)` issues a `ListEntities` call filtered by
`TypeID + sha`. Across a busy repo with many blobs this becomes a per-object
collection scan unless an ArangoDB persistent index on `[sha]` (or
`[type, sha]`) exists in the relevant collections (`git_objects`, `git_blobs`,
`git_trees`, `git_commits`).

**Current state**: the entitygraph ArangoDB backend does not document per-SHA
indexes. Without them, every `EncodedObject` call is O(n) in the number of
objects for that agency. A 1,000-file repo with 10 commits requires up to
11,000 `ListEntities` scans during `git clone`.

**GIT-017 should also**: ensure persistent ArangoDB indexes on
`[agency_id, sha]` exist for `git_objects`, `git_blobs`, `git_trees`, and
`git_commits`. The original GIT-015 architecture doc listed this (section 3.3)
but the implementation did not add the indexes.

---

## 6. Cross-References

| Document | Relevance |
|---|---|
| [architecture-storer-gaps.md](architecture-storer-gaps.md) | Gaps 1–6 resolved in GIT-015; Gap 7 (this doc) is GIT-017 |
| [architecture-arangodb-storer.md](architecture-arangodb-storer.md) | v3 storer design — superseded collection design but reference architecture |
| [mvp-details/arangodb-storer.md](../3-SofwareDevelopment/mvp-details/arangodb-storer.md) | GIT-015 implementation notes |
| [mvp-details/file-operations.md](../3-SofwareDevelopment/mvp-details/file-operations.md) | `WriteFile` acceptance criteria |
| [mvp-details/grpc-server.md](../3-SofwareDevelopment/mvp-details/grpc-server.md) | GIT-016 fix task |
