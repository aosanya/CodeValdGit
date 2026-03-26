# GIT-014 — ArangoDB Backend: Deduplication, Documentation, and Production Gate

## Overview

| Field | Value |
|---|---|
| Task ID | GIT-014 |
| Priority | P0 |
| Status | 📋 Not Started |
| Depends On | GIT-011 (CAS on `head_commit_id`) |
| Architecture ref | [architecture-arangodb.md](../../2-SoftwareDesignAndArchitecture/architecture-arangodb.md) |

---

## Problem Statement

The v2 entitygraph design closed the `git_index` staging-area problem and moved
ref management to entity properties. Three concrete gaps remain:

1. **No uniqueness constraint on `(agencyID, sha)` in `git_objects`** — identical
   blobs written concurrently produce separate duplicate documents, breaking Git's
   content-addressing invariant.

2. **`storage-backends.md` is stale** — it documents the superseded v1 go-git
   `storage.Storer` design (collections `git_refs`, `git_index`, `git_config`,
   constructor `NewArangoStorage`). Anyone reading it gets a false picture of the
   current implementation.

3. **ArangoDB is undocumented as experimental** — there is no config flag, no
   startup warning, and no measurable promotion criteria. Callers cannot tell
   whether the ArangoDB backend is production-ready.

---

## Subtask A — Add `(agencyID, sha)` Unique Index

### Goal

Ensure that `git_objects` contains at most one entity per `(agencyID, sha)` pair,
turning duplicate inserts into a deterministic constraint violation instead of
silent duplication.

### Schema Change

**File**: `schema.go` → `DefaultGitSchema()`

Investigate whether the `entitygraph.TypeDefinition` or
`entitygraph.PropertyDefinition` type in
`CodeValdSharedLib/entitygraph` exposes a `UniqueFields` or `Indexes` directive
that instructs the ArangoDB backend to create a unique persistent index on
startup.

- **If supported**: add `UniqueFields: [][]string{{"agencyID", "sha"}}` to the
  Commit, Tree, and Blob TypeDefinitions in `DefaultGitSchema()`.
- **If not supported**: file a SHAREDLIB issue requesting the feature, document
  the risk explicitly in `architecture-arangodb.md` §2.3, and proceed with the
  application-level check-before-insert workaround below.

### Writer Behaviour

**Files**: `git_impl_fileops.go`, `git_impl_repo.go`

On `CreateEntity` returning a unique-constraint violation for `(agencyID, sha)`:

```go
// Object deduplication helper — call before every Blob/Tree/Commit CreateEntity.
func lookupBySHA(ctx context.Context, dm entitygraph.DataManager,
    agencyID, typeName, sha string) (string, bool, error) {
    // Query git_objects for an entity with matching agencyID and sha.
    // Returns (entityID, true, nil) if found, ("", false, nil) if not found.
}

id, err := dm.CreateEntity(ctx, agencyID, typeBlob, props)
if isUniqueConstraintError(err) {
    // Deduplication: fetch the existing entity's ID and proceed.
    id, _, err = lookupBySHA(ctx, dm, agencyID, TypeBlob.Name, sha)
}
```

This ensures the caller always receives the canonical entity ID for the given SHA,
regardless of whether the insert was a fresh write or a duplicate.

### Acceptance Criteria

- [ ] `git_objects` collection has a unique persistent index on `[agencyID, sha]`
  (or the risk is documented and a SHAREDLIB task is filed).
- [ ] Concurrent identical `WriteFile` calls produce exactly one Blob entity in
  `git_objects`.
- [ ] Unit test `TestBlobDeduplication_Concurrent` passes with `-race`.
- [ ] Unit test `TestLookupBySHA_ExistingObject` covers the check-before-insert
  path.

---

## Subtask B — Update `storage-backends.md` to the v2 Design

### Goal

Replace the body of `storage-backends.md` with a v2 storage specification that
accurately describes the entitygraph collection inventory.

**File**: `documentation/3-SofwareDevelopment/mvp-details/storage-backends.md`

### Required Sections

| Section | Content |
|---|---|
| Overview | Why the entitygraph domain model instead of go-git `storage.Storer`; link to this file |
| Collection inventory | `git_entities`, `git_objects`, `git_relationships`, `git_schemas_draft`, `git_schemas_published` with purpose and ArangoDB collection type |
| TypeDefinition property tables | One table per type: Agency, Repository, Branch, Tag, Commit, Tree, Blob — all properties with Go type and mutability |
| Relationship table | All edge types: `has_repository`, `has_branch`, `points_to`, `has_tree`, `has_blob` — with From/To types |
| Index specifications | `(agencyID, sha)` unique on `git_objects`; `agencyID` non-unique on `git_entities` and `git_objects` for agency-scoped queries |
| Smart HTTP note | Filesystem-only for v1; ArangoDB does not implement `codevaldgit.Backend`; cross-reference `architecture-arangodb.md §2.4` |

### Acceptance Criteria

- [ ] All v1 content removed: `git_refs`, `git_index`, `git_config`,
  `NewArangoStorage` constructor, and `storage.Storer` interface references.
- [ ] Document length ≤ 300 lines (documentation file size limit).
- [ ] Every entity TypeDefinition property matches the current `schema.go`.

---

## Subtask C — Experimental Flag and Promotion Criteria

### Goal

Make the ArangoDB backend's experimental status explicit and measurable.

### Config Change

**File**: `internal/config/config.go`

Add a `Backend` field with valid values `"filesystem"` (default) and `"arangodb"`:

```go
type Config struct {
    // ... existing fields ...

    // Backend selects the storage implementation.
    // Valid values: "filesystem" (default), "arangodb".
    // The ArangoDB backend is experimental — see architecture-arangodb.md.
    Backend string `yaml:"backend" env:"CODEVALDGIT_BACKEND"`
}
```

Default to `"filesystem"` when the field is empty.

### Startup Warning

**File**: `cmd/main.go` (pending GIT-009 full wiring)

```go
if cfg.Backend == "arangodb" {
    log.Println("WARNING: ArangoDB backend is experimental — " +
        "see documentation/2-SoftwareDesignAndArchitecture/architecture-arangodb.md")
}
```

### Benchmarking Plan

Create `storage/arangodb/arangodb_bench_test.go` with the following benchmarks.
Each benchmark requires a live ArangoDB connection; skip with `testing.Short()`:

| Benchmark | Setup | Measure |
|---|---|---|
| `BenchmarkClone_500Commits` | Seed 500 commits, 100 files per commit | Time for full reachable-objects traversal from main HEAD |
| `BenchmarkConcurrentWrite_10Agents` | 10 goroutines, each writing 100 unique files | Total duration; assert zero duplicate-key errors after Subtask A |
| `BenchmarkMergeBranch_50Concurrent` | 50 goroutines each calling `MergeBranch` on separate branches | Assert zero lost updates after GIT-011 |
| `BenchmarkGraphTraversal_5Levels` | Seed a 5-level directory tree | p95 AQL traversal latency |

### Promotion Criteria (in `architecture-arangodb.md §4`)

| Criterion | Threshold |
|---|---|
| Full clone — 500-commit repo, 100 files per commit | < 5 s on single-node ArangoDB 3.11 |
| Concurrent write safety — 10 branches, 100 writes each | Zero duplicate-key errors |
| Merge safety — 50 concurrent `MergeBranch` calls | Zero lost updates (GIT-011 required) |
| AQL traversal depth — 5-level tree | < 100 ms p95 |
| Smart HTTP | Supported or explicitly accepted as out-of-scope via ADR |

### Acceptance Criteria

- [ ] `Config.Backend` defaults to `"filesystem"` when unset.
- [ ] Binary logs `WARNING: ArangoDB backend is experimental` at startup when
  `Backend == "arangodb"`.
- [ ] `arangodb_bench_test.go` file created with all four benchmarks.
- [ ] Promotion criteria table added to `architecture-arangodb.md §4`.

---

## Test Plan

| Test | Location | Covers |
|---|---|---|
| `TestBlobDeduplication_Concurrent` | `storage/arangodb/arangodb_test.go` | Subtask A — duplicate SHA insert under concurrency |
| `TestLookupBySHA_ExistingObject` | `storage/arangodb/arangodb_test.go` | Subtask A — deduplication helper |
| `TestConfig_BackendDefault` | `internal/config/config_test.go` | Subtask C — filesystem is default |
| `BenchmarkClone_500Commits` | `storage/arangodb/arangodb_bench_test.go` | Subtask C — clone latency |
| `BenchmarkConcurrentWrite_10Agents` | `storage/arangodb/arangodb_bench_test.go` | Subtask C — concurrent write safety |
| `BenchmarkMergeBranch_50Concurrent` | `storage/arangodb/arangodb_bench_test.go` | Subtask C — merge lost-update check |
| `BenchmarkGraphTraversal_5Levels` | `storage/arangodb/arangodb_bench_test.go` | Subtask C — AQL traversal latency |

---

## Acceptance Criteria Summary

- [ ] `(agencyID, sha)` uniqueness enforced in `git_objects` — or risk documented
  and SHAREDLIB task filed (Subtask A).
- [ ] Duplicate blob write handled gracefully — writer receives existing entity ID,
  not an error (Subtask A).
- [ ] `storage-backends.md` reflects v2 entitygraph design; all v1 content removed
  (Subtask B).
- [ ] `Config.Backend` defaults to `"filesystem"` with explicit opt-in for
  `"arangodb"` (Subtask C).
- [ ] Startup log warns when the ArangoDB backend is selected (Subtask C).
- [ ] Benchmarking targets documented and measurable in `architecture-arangodb.md`
  (Subtask C).
- [ ] `go build ./...` succeeds.
- [ ] `go vet ./...` shows 0 issues.
- [ ] `go test -race ./...` passes.
