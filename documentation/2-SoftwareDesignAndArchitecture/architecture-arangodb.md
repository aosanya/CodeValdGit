# ArangoDB Backend — Architecture

## 1. Design Evolution

### v1 (Superseded): go-git `storage.Storer` Approach

The original MVP-GIT-008 specification described the ArangoDB backend as an
implementation of go-git's `storage.Storer` — the same low-level interface used
internally by go-git for all plumbing operations. Four collections would mirror
go-git's internal storage split:

| Collection | go-git role |
|---|---|
| `git_objects` | Blob, tree, commit, tag objects (keyed by SHA) |
| `git_refs` | Branch and tag references |
| `git_index` | Staging area index (one mutable document per repository) |
| `git_config` | Per-repository Git configuration |

This approach was **superseded**. Implementing `storage.Storer` in full requires
tracking go-git's internal interface evolution; every internal plumbing operation
(loose-object lookup, packed-ref negotiation, index encoding) would be surfaced
directly to the ArangoDB schema, making the implementation fragile and hard to
test in isolation.

### v2 (Current): EntityGraph Domain Model

The v2 design models the Git domain as **named entities** in CodeVald's
entitygraph abstraction layer (provided by `CodeValdSharedLib/entitygraph`):

| Collection | Type | Contents |
|---|---|---|
| `git_entities` | Document | Mutable refs: Agency, Repository, Branch, Tag |
| `git_objects` | Document | Immutable content-addressed objects: Commit, Tree, Blob |
| `git_relationships` | **Edge** | All directed graph edges (has_repository, has_branch, points_to, has_tree, has_blob) |
| `git_schemas_draft` | Document | Draft TypeDefinition schemas per agency |
| `git_schemas_published` | Document | Published schema snapshots (append-only) |

Named graph: `git_graph`

**Key differences from v1:**

- No `git_index` — the staging area is eliminated; commits are written as entities
  directly without a staging step.
- No `git_refs` — branch refs are Branch entities in `git_entities` carrying a
  `head_commit_id` property; ref updates are entity updates.
- No `git_config` — repository configuration is stored as properties on the
  Repository entity.
- Immutable content-addressed objects (Commit, Tree, Blob) live in `git_objects`;
  mutable refs (Branch, Tag) live in `git_entities`.
- The schema is seeded from `DefaultGitSchema()` in `schema.go` via the
  `entitygraph.SchemaManager` interface.

The `storage/arangodb` package in CodeValdGit is a **thin adapter** — it maps
CodeValdGit-specific collection and graph names into the shared config and
delegates all operations to `CodeValdSharedLib/entitygraph/arangodb`.

---

## 2. Sub-Gap Analysis

### 2.1 Index Semantics (`git_index`) — RESOLVED by v2 Design

The v1 design's `git_index` collection held one mutable staging-area document per
repository, updated on every `git add` equivalent. With multiple concurrent task
branches in flight, a single index document per repository is a serialisation
bottleneck and a corruption risk: two concurrent writes would race to update the
same document.

**v2 resolution**: the entitygraph model has no staging area. `WriteFile` creates
Blob entities directly. `MergeBranch` creates Tree and Commit entities and then
advances the Branch `head_commit_id`. There is no shared mutable index document.
This sub-gap is closed by the v2 design change.

---

### 2.2 Ref Consistency (CAS) — ADDRESSED by GIT-011

Concurrent `MergeBranch` calls race to update the same Branch entity's
`head_commit_id`. Without an atomic check-and-set, the second writer silently
overwrites the first's update — a lost update.

**Mechanism**: ArangoDB's `_rev` field provides optimistic locking. When
`UpdateEntity` is called with the document's last-known `_rev`, ArangoDB rejects
the write with `412 Precondition Failed` if the document has changed since the
ref was read.

**Status**: The CAS path is specified in GIT-011. The concrete changes required
in `advanceBranchHead` are:
- Read the Branch entity including `_rev`.
- Pass expected `_rev` to `UpdateEntity`.
- Map `412` responses to `ErrMergeConcurrencyConflict`.

See [architecture-concurrency.md](architecture-concurrency.md) for the full
`RefLocker` design and `advanceBranchHead` signature changes.

---

### 2.3 Object Deduplication — OPEN

**Current state**: Blob, Tree, and Commit entities in `git_objects` are assigned
UUID primary keys by the entitygraph layer. Each entity also carries a `sha`
property holding the Git content-address hash.

**Risk**: Two concurrent `WriteFile` calls with identical file content generate
two separate Blob entities sharing the same `(agencyID, sha)` pair. This:

- Wastes storage proportionally to write concurrency on repeated content.
- Prevents SHA-based object lookup (which entity is the canonical copy?).
- Breaks the Git invariant that a given `(agency, sha)` maps to exactly one
  object.

**Fix**: Add an ArangoDB unique persistent index on `[agencyID, sha]` in the
`git_objects` collection. The entitygraph TypeDefinition schema should express
this constraint declaratively:

```
Unique index: git_objects — fields: [agencyID, sha]
```

With a unique constraint in place, a duplicate insert returns a conflict error
that the writer can interpret as "object already exists — safe to skip". The
writer fetches the existing entity ID and proceeds.

**Writer behaviour after the fix** (to be implemented in `git_impl_fileops.go`):

```go
// On CreateEntity for Commit/Tree/Blob:
id, err := dm.CreateEntity(ctx, agencyID, TypeBlob, props)
if isUniqueConstraintError(err) {
    // Deduplicate: fetch the existing entity instead.
    id, err = lookupBySHA(ctx, dm, agencyID, sha)
}
```

GIT-014 (Subtask A) covers the schema change and the writer behaviour update.

---

### 2.4 Smart HTTP / `OpenStorer` Gap — OPEN (filesystem-only for v1)

The Smart HTTP handler (`internal/server/githttp.go`) routes fetch, clone, and
push operations via go-git's server transport. The `backendLoader` calls
`codevaldgit.Backend.OpenStorer()`, which must return a go-git `storage.Storer`
and a `billy.Filesystem`.

**The ArangoDB package does NOT implement `codevaldgit.Backend`.**

The type `arangodb.Backend` in `storage/arangodb` is a type alias for
`sharedadb.Backend`. It implements `entitygraph.DataManager` and
`entitygraph.SchemaManager`. It does not implement `codevaldgit.Backend`, which
requires `InitRepo`, `OpenStorer`, `DeleteRepo`, and `PurgeRepo`.

Consequences:
- `NewGitHTTPHandler(arangoBackend)` does not compile.
- Smart HTTP (clone/fetch/push) is only supported by the filesystem backend.
- The `backendLoader` in `githttp.go` is filesystem-only.

**Options for future ArangoDB + Smart HTTP support:**

| Option | Complexity | Notes |
|---|---|---|
| Implement go-git `storage.Storer` over entitygraph | High | Maps all go-git plumbing ops to AQL; fragile against go-git API changes; reverts back toward v1 design |
| Clone-on-demand: materialise entities to a temp `.git` dir at pack-request time | Medium | Stale-view risk; ephemeral disk required; sync on every fetch |
| Smart HTTP is filesystem-only, permanently | Low | Simplest; correct if filesystem is the production default |

**v1 decision**: Smart HTTP is filesystem-only. The ArangoDB backend serves the
`GitManager` gRPC interface only. This is documented as a known limitation.

---

### 2.5 Query / Load Profile — OPEN

Git access patterns are bursty and small-read-heavy. `git-upload-pack` traverses
the reachable-objects DAG from the advertised refs down to every Blob the client
needs. For a repository with 200 commits and an average of 100 files per tree,
a full clone would require approximately:

| Operation | Estimated round-trips |
|---|---|
| Branch entity lookup (ref advertisement) | 1 |
| Commit entity reads (reachability walk, 200 commits) | ≤ 200 |
| Tree entity reads (one per commit, 200 trees) | ≤ 200 |
| Blob entity reads (100 unique files × 200 commits at worst) | ≤ 20,000 |

ArangoDB's AQL graph traversal
(`FOR v, e IN 1..N OUTBOUND 'branches/X' GRAPH 'git_graph'`) can batch multi-hop
reads in a single query, but the round-trip cost per level is still materially
different from reading a packed `.git` directory sequentially from disk.

**No benchmarks exist today.** Acceptance criteria for ArangoDB production
promotion are defined in GIT-014 (Subtask C).

---

## 3. `storage-backends.md` is Stale

The file
`documentation/3-SofwareDevelopment/mvp-details/storage-backends.md`
documents the old v1 approach (MVP-GIT-008) — a go-git `storage.Storer`
implementation backed by four collections (`git_objects`, `git_refs`, `git_index`,
`git_config`). This design was superseded by the v2 entitygraph model.

The stale document currently describes:

- A `NewArangoStorage(db, agencyID)` constructor that no longer exists.
- Collections `git_refs`, `git_index`, and `git_config` that are not present in
  the current schema.
- The go-git `storage.Storer` interface implementation that was never built.

**GIT-014 (Subtask B) replaces the body of `storage-backends.md`** with a v2
storage specification covering the entitygraph collection inventory, entity
property tables, relationship types, and index specifications.

---

## 4. Production Guidance

### Filesystem Backend — Production Default

The filesystem backend stores real `.git` directories on disk. It satisfies every
requirement for v1 production use:

- `OpenStorer` returns a real `gogitfs.Storage` backed by an osfs working tree —
  full go-git `storage.Storer` compatibility and Smart HTTP support.
- All automated tests run against the filesystem backend.
- Object write and read latency is bounded by the local filesystem; no network
  round-trips per object.
- Survives process restarts if backed by a persistent volume (a Kubernetes PVC
  or any mounted network filesystem).

**Use the filesystem backend for all production deployments until ArangoDB
benchmarks pass the promotion criteria below.**

### ArangoDB Backend — Experimental

The ArangoDB backend is appropriate for:

- Stateless container deployments where a persistent volume is unavailable.
- Environments where ArangoDB is already the shared persistence layer and
  operational overhead for a separate disk mount is unacceptable.

It is **not yet production-ready** because:

- No uniqueness constraint on `(agencyID, sha)` — duplicate objects are possible
  under concurrent load (tracked as Subtask A of GIT-014).
- Smart HTTP (clone/fetch/push) is unsupported — the backend does not implement
  `codevaldgit.Backend`.
- No benchmarks against realistic repository sizes and concurrent agent load.

### Promotion Criteria (Gate to Production)

All of the following must be met before the ArangoDB backend can be used in
production alongside the filesystem backend:

| Criterion | Threshold |
|---|---|
| Full clone latency — 500-commit repo, 100 files per commit | < 5 s on a single-node ArangoDB 3.11 instance |
| Concurrent write safety — 10 branches, 100 file writes each | Zero duplicate-key errors after Subtask A |
| Merge safety — 50 concurrent `MergeBranch` calls | Zero lost updates (requires GIT-011 CAS) |
| AQL graph traversal depth — 5-level tree | < 100 ms p95 |
| Smart HTTP | Either supported or explicitly accepted as out-of-scope by a documented ADR |

---

## 5. Cross-References

| Document | Relevance |
|---|---|
| [architecture-concurrency.md](architecture-concurrency.md) | GIT-011 — CAS on `head_commit_id`; closes sub-gap 2.2 |
| [architecture-merge.md](architecture-merge.md) | GIT-012 — squash merge and fork-point tracking |
| [architecture-transactions.md](architecture-transactions.md) | GIT-013 — atomicity rules and idempotency key |
| [mvp-details/critical-arangodb.md](../../3-SofwareDevelopment/mvp-details/critical-arangodb.md) | GIT-014 task specification |
