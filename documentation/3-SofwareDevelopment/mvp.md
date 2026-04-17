# MVP - Minimum Viable Product Task Breakdown

## Task Overview
- **Objective**: Deliver CodeValdGit as a standalone gRPC microservice that provides real Git semantics for artifact versioning in the CodeVald platform, replacing the custom `internal/git/` implementation. Platform services communicate via generated gRPC client stubs proxied through CodeValdCross.
- **Success Criteria**: All 10 tasks (MVP-GIT-001 through MVP-GIT-010) implemented and tested; CodeValdGit deployed as a standalone gRPC microservice; legacy `internal/git/` implementation retired
- **Dependencies**: Go module infrastructure, go-git v5, ArangoDB connectivity

## Platform Documentation
- **Requirements**: [requirements.md](../1-SoftwareRequirements/requirements.md) - Functional and non-functional requirements, scope, open questions
- **Architecture**: [architecture.md](../2-SoftwareDesignAndArchitecture/architecture.md) - Core design decisions, storage backends, branching model, proposed API

## Documentation Structure
- **High-Level Overview**: This file (`mvp.md`) provides task tables, priorities, dependencies, and brief descriptions
- **Detailed Specifications**: Each task with detailed requirements is documented in `mvp-details/{TASK_SPEC}.md`
- **Reference Pattern**: Tasks reference their detail files using the format `See: mvp-details/{spec}.md`

## Workflow Integration

### Task Management Process
1. **Task Assignment**: Pick tasks based on priority (P0 first) and dependencies
2. **Implementation**: Update "Status" column as work progresses (Not Started → In Progress → Testing → Complete)
3. **Completion Process** (MANDATORY):
   - Create detailed coding session document in `coding_sessions/` using format: `{TaskID}_{description}.md`
   - Add completed task to summary table in `mvp_done.md` with completion date
   - Remove completed task from this active `mvp.md` file
   - Update any dependent task references using notation: `~~MVP-GIT-XXX~~ ✅` (strikethrough + checkmark)
   - Merge feature branch to main
4. **Dependencies**: Ensure prerequisite tasks are completed before starting dependent work

### Dependency Notation Convention
- **Active dependencies**: `MVP-GIT-XXX` (plain text)
- **Completed dependencies**: `~~MVP-GIT-XXX~~ ✅` (strikethrough + checkmark)
- **Multiple dependencies**: Comma-separated, e.g., `MVP-GIT-001, ~~MVP-GIT-002~~ ✅`
- **None**: Use `None` for tasks with no prerequisites

### Branch Management (MANDATORY)
```bash
# Create feature branch
git checkout -b feature/MVP-GIT-XXX_description

# Work, build validation, test
# ... development work ...

# Merge when complete and tested
git checkout main
git merge feature/MVP-GIT-XXX_description --no-ff
git branch -d feature/MVP-GIT-XXX_description
```

---

## Status Legend
- ✅ **Completed** - Task done, merged to main (see `mvp_done.md`)
- 🚀 **In Progress** - Currently being worked on
- 📋 **Not Started** - Ready to begin (dependencies met)
- ⏸️ **Blocked** - Waiting on dependencies
- ⚠️ **Deprecated** - Superseded by other work

---

## P0: Foundation (CRITICAL)

*~~GIT-001~~ ✅ and ~~GIT-002~~ ✅ complete — see `mvp_done.md`.*

---

## P0: Core Operations (CRITICAL)

*~~MVP-GIT-003~~ ✅ and ~~MVP-GIT-004~~ ✅ complete — see `mvp_done.md`.*

---

## P0: Merge Workflow (CRITICAL)

*~~MVP-GIT-005~~ ✅ and ~~MVP-GIT-006~~ ✅ complete — see `mvp_done.md`.*

---

## P1: History & Diff (IMPORTANT)

*~~MVP-GIT-007~~ ✅ complete — see `mvp_done.md`.*

---

## P1: Storage & Integration (IMPORTANT)

*~~MVP-GIT-008~~ ✅ and ~~MVP-GIT-009~~ ✅ complete — see `mvp_done.md`.*

---

## P2: CodeValdCross Integration Pattern

*~~MVP-GIT-011~~ ✅ complete — see `mvp_done.md`.*

---

## P3: CodeValdSharedLib Migration

*~~MVP-GIT-012~~ ✅ complete — see `mvp_done.md`.*

**MVP-GIT-012 scope** (completed 2026-03-02):
- Replace `internal/registrar/` with `github.com/aosanya/CodeValdSharedLib/registrar` (caller passes `"codevaldgit"`, its topics, and its `declaredRoutes`).
- Replace `envOrDefault` / `parseDuration` helpers in `cmd/server/main.go` with `serverutil.EnvOrDefault` / `serverutil.ParseDurationSeconds`.
- Replace the gRPC server setup block in `cmd/server/main.go` with `serverutil.NewGRPCServer()` + `serverutil.RunWithGracefulShutdown()`.
- Replace the ArangoDB `driverhttp.NewConnection` / auth / database bootstrap in `storage/arangodb/arangodb.go` with `arangoutil.Connect(ctx, cfg)`.
- Replace the local copy of `gen/go/codevaldcross/v1/` with the SharedLib copy; update `cmd/cross.go` and `internal/registrar/` import paths.
- Remove `internal/registrar/` package entirely.
- Update `go.mod` with `require github.com/aosanya/CodeValdSharedLib` and a `replace ../CodeValdSharedLib` directive.

See [CodeValdSharedLib mvp.md](../../../CodeValdSharedLib/documentation/3-SofwareDevelopment/mvp.md) for the full SharedLib task breakdown.

---

---

## P0: v2 Redesign — entitygraph Schema + Git Smart HTTP (CRITICAL)

### GIT-001 — Pre-delivered Schema & Domain Value Types

| Task | Status | Depends On |
|------|--------|------------|
| GIT-001: Pre-delivered schema (`schema.go`) + domain value types (`models.go`) — `Repository`, `Branch`, `Commit`, `Tag`, `Tree`, `Blob` TypeDefinitions | ✅ Done | — |

**Scope**: Create `schema.go` (exposes `DefaultGitSchema()` seeded on startup) and
`models.go` (Go value types mirroring the TypeDefinitions). Foundation for the entire v2
entitygraph redesign; all other GIT v2 tasks depend on the schema and models being defined first.

### GIT-002 — Flat `GitManager` Interface
| Task | Status | Depends On |
|------|--------|------------|
| GIT-002: Flat `GitManager` interface (`git.go`) — replaces nested `RepoManager`+`Repo`+`Backend` | ✅ Done | GIT-001 |

### GIT-003 — Proto & Codegen
| Task | Status | Depends On |
|------|--------|------------|
| GIT-003: Update proto (`service.proto`) — `GitService` RPCs aligned to flat `GitManager`; regenerate Go stubs | ✅ Done | ~~GIT-002~~ ✅ |

### GIT-004 — ArangoDB entitygraph Backend
| Task | Status | Depends On |
|------|--------|------------|
| GIT-004: ArangoDB entitygraph backend (`storage/arangodb/`) | ✅ Done | ~~GIT-001~~ ✅ |

### GIT-005 — Concrete GitManager Implementation
| Task | Status | Depends On |
|------|--------|------------|
| GIT-005: Concrete `GitManager` implementation (`internal/manager/`) | ✅ Done | ~~GIT-002~~ ✅, ~~GIT-004~~ ✅ |

### GIT-006 — gRPC GitService Server
| Task | Status | Depends On |
|------|--------|------------|
| GIT-006: gRPC `GitService` server (`internal/server/server.go`) | ✅ Done | ~~GIT-002~~ ✅, ~~GIT-003~~ ✅, ~~GIT-005~~ ✅ |

### GIT-007 — Git Smart HTTP Handler
| Task | Status | Depends On |
|------|--------|------------|
| GIT-007: Git Smart HTTP handler (`internal/server/githttp.go`) | ✅ Done | ~~GIT-005~~ ✅ |

### GIT-008 — Config + Cross Registrar
| Task | Status | Depends On |
|------|--------|------------|
| GIT-008: Config + Cross registrar | ✅ Done | ~~GIT-006~~ ✅, ~~GIT-007~~ ✅ |

### GIT-009 — cmd/main.go
| Task | Status | Depends On |
|------|--------|------------|
| GIT-009: `cmd/main.go` — cmux wiring, ArangoDB backend, schema seed | ✅ Done | ~~GIT-004~~ ✅, ~~GIT-006~~ ✅, ~~GIT-007~~ ✅, GIT-008 |

### GIT-010 — Unit & Integration Tests
| Task | Status | Depends On |
|------|--------|------------|
| GIT-010: Unit & integration tests | ✅ Done | ~~GIT-005~~ ✅, ~~GIT-006~~ ✅, ~~GIT-007~~ ✅ |

---

## P0: Production Safety (CRITICAL)

### GIT-011 — Concurrency and Atomic Ref Updates

| Task | Status | Depends On |
|------|--------|------------|
| GIT-011: `RefLocker` interface + CAS in `advanceBranchHead` + `ErrMergeConcurrencyConflict` | 📋 Not Started | ~~GIT-005~~ ✅ |

**Scope**: Add `RefLocker` to `git.go`. Add in-process `sync.Mutex` default implementation.
Wrap `MergeBranch` inside `WithMergeLock`. Pass expected `head_commit_id` through
`advanceBranchHead` and return `ErrMergeConcurrencyConflict` on CAS mismatch.
See: [mvp-details/critical-concurrency.md](mvp-details/critical-concurrency.md)

### GIT-012 — Squash Merge Strategy

| Task | Status | Depends On |
|------|--------|------------|
| GIT-012: Fork-point tracking in `CreateBranch` + tree-diff squash merge in `MergeBranch` | 📋 Not Started | GIT-011 |

**Scope**: Add `ForkPointCommitID` to `Branch` model and `CreateBranch` entity write.
Replace HEAD-pointer-advance in `MergeBranch` with: fast-forward if no divergence,
tree-diff apply if diverged, `ErrMergeConflict` on conflict.
See: [mvp-details/critical-merge-strategy.md](mvp-details/critical-merge-strategy.md)

### GIT-013 — Transaction Boundaries and Idempotency

| Task | Status | Depends On |
|------|--------|------------|
| GIT-013: `MergeRequest` struct with `IdempotencyKey` + in-process idempotency store | 📋 Not Started | GIT-012 |

**Scope**: Replace `MergeBranch(ctx, branchID string)` with `MergeBranch(ctx, MergeRequest)`.
Add `MergeRequest` to `models.go`. Implement in-process `sync.Map` idempotency cache on
`gitManager`. Document retry contract for gRPC server layer.
See: [mvp-details/critical-transactions.md](mvp-details/critical-transactions.md)

### GIT-014 — ArangoDB Backend: Deduplication, Documentation, and Production Gate

| Task | Status | Depends On |
|------|--------|------------|
| GIT-014: `(agencyID, sha)` unique index + update `storage-backends.md` + experimental flag + benchmarks | 📋 Not Started | GIT-011 |

**Scope**: Three subtasks — (A) add unique `(agencyID, sha)` constraint to `git_objects` and
handle duplicate inserts gracefully in writers; (B) replace the stale v1 `storage-backends.md`
with the v2 entitygraph collection spec; (C) add `Config.Backend` with filesystem default,
startup warning for ArangoDB, and a benchmarking plan with measurable promotion criteria.
See: [mvp-details/critical-arangodb.md](mvp-details/critical-arangodb.md)

---

## P1: Repository Import (IMPORTANT)

### GIT-016 — Import External Repository

| Task | Status | Depends On |
|------|--------|------------|
| GIT-016a: `ImportJob` TypeDefinition + `git_importjobs` collection in `schema.go` | ✅ Complete | ~~GIT-001~~ ✅ |
| GIT-016b: Types (`ImportRepoRequest`, `ImportJob`) + errors + `GitManager` interface additions | ✅ Complete | GIT-016a |
| GIT-016c: Core implementation — background goroutine, go-git clone, all-branch entity walk, cancel map | ✅ Complete | GIT-016b |
| GIT-016d: Proto additions (3 RPCs) + `buf generate` | ✅ Complete | GIT-016b |
| GIT-016e: gRPC server handlers + error mapping for all 3 RPCs | ✅ Complete | GIT-016c, GIT-016d |
| GIT-016f: Unit tests — import manager, cancel, concurrency rejection | ✅ Complete | GIT-016e |

**Scope**: Full async import of a public HTTPS Git repository into the entity graph.
Six sub-tasks cover the schema addition, interface contract, core ingestion goroutine,
proto codegen, gRPC server wiring, and unit tests.
See: [mvp-details/repo-import.md](mvp-details/repo-import.md)

---

## P1: gRPC-Data Gap & Persistent Indexes (IMPORTANT)

### GIT-017 — gRPC-Data Gap Fix + Persistent `[agency_id, sha]` Indexes

| Task | Status | Depends On |
|------|--------|------------|
| GIT-017a: Add `"data"` (base64 raw git object bytes) to `WriteFile`-created Blob, Tree, and Commit entities in `git_impl_fileops.go` | � In Progress | ~~GIT-005~~ ✅, ~~GIT-007~~ ✅ |
| GIT-017b: Add persistent ArangoDB indexes on `[agency_id, sha]` for `git_objects`, `git_blobs`, `git_trees`, and `git_commits` | 🚀 In Progress | ~~GIT-004~~ ✅ |

**Scope**: Resolves Gap 7 — files written via gRPC `WriteFile` are invisible to `git pull` / `git clone` because the Blob, Tree, and Commit entities created by `WriteFile` do not carry a `"data"` property (base64-encoded raw git object bytes). The Smart HTTP layer's `arangoStorer.EncodedObject` reads `e.Properties["data"]` and returns `plumbing.ErrObjectNotFound` when the property is absent. Fix (GIT-017a): the raw `plumbing.MemoryObject` bytes are already computed inside `WriteFile` — base64-encode and include them as `"data"` in each `CreateEntity` call for Blob, Tree, and Commit. No schema change required. GIT-017b adds persistent `[agency_id, sha]` composite indexes to eliminate O(n) per-object collection scans during `git clone`; a 1 000-file repo with 10 commits otherwise requires up to 11 000 collection scans. The original GIT-015 architecture doc listed these indexes (section 3.3) but the implementation did not add them.
See: [architecture-pull-flow.md](../2-SoftwareDesignAndArchitecture/architecture-pull-flow.md) · [mvp-details/arangodb-storer.md](mvp-details/arangodb-storer.md)

---

## Bugs and Issues

### GIT-018 — Repo-Scoped Sub-Resource Routes

| Task | Status | Depends On |
|------|--------|------------|
| GIT-018a: Update `branchRoutes()`, `tagRoutes()`, `fileRoutes()`, and `historyRoutes()` in `internal/registrar/routes.go` to nest all sub-resource patterns under `/git/{agencyId}/repositories/{repoId}/` | ✅ Done | ~~GIT-011~~ ✅ |
| GIT-018b: Update gRPC server HTTP handlers (`internal/server/server.go`) to extract `repoName` from the URL path and pass it to the corresponding `GitManager` calls (`ListBranches(repoID)`, `ListTags(repoID)`, etc.) | ✅ Done | GIT-018a |
| GIT-018c: Update integration tests in `CodeValdCross/integration/` to use the new repo-scoped URL structure | ✅ Done | GIT-018b |

**Scope**: The registered HTTP routes for branches, tags, files, tree, log, and diff (`/git/{agencyId}/branches`, `/git/{agencyId}/tags`, etc.) are not scoped to a repository. The `GitManager` methods (`ListBranches`, `ListTags`, `CreateBranch`, `CreateTag`, `WriteFile`, etc.) already require a `repoID` argument — there is no way to route these calls correctly without knowing which repository to target. The fix nests all sub-resource routes under `/git/{agencyId}/repositories/{repoName}/` (using `repoName` as the stable user-chosen identifier, consistent with how `RepoManager.OpenRepo(ctx, agencyID, repoName)` resolves repos). The Smart HTTP routes (`/{agencyId}/{repoId}/info/refs`, etc.) already use this convention and are unaffected.

See: `internal/registrar/routes.go` · `internal/server/server.go`

---

### Active Bugs

_(None)_

### Resolved Bugs

_(None yet)_

---

## P2: Documentation Layer — Keywords & Graph Navigation

### GIT-019 — Keyword Entity Type + Documentation Edges

| Task | Status | Depends On |
|------|--------|------------|
| GIT-019a: Add `Keyword` TypeDefinition to `schema.go` (`git_keywords` collection) with `name`, `description`, `scope`, timestamps | ✅ Done | ~~GIT-001~~ ✅ |
| GIT-019b: Add `tagged_with`, `references`/`referenced_by` (with `descriptor` edge property), `has_child`/`belongs_to_parent` relationship definitions to `schema.go`; add `Properties []PropertyDefinition` to `RelationshipDefinition` in SharedLib | ✅ Done | GIT-019a |
| GIT-019c: Add `GitManager` interface methods — `CreateKeyword`, `GetKeyword`, `ListKeywords`, `GetKeywordTree`, `UpdateKeyword`, `DeleteKeyword` | ✅ Done | GIT-019a |
| GIT-019d: Concrete `GitManager` implementation for keyword CRUD in `internal/manager/` | ✅ Done | GIT-019c |
| GIT-019e: Add `GitManager` interface methods — `CreateEdge`, `DeleteEdge` (branch-scoped, follows DR-010 lifecycle) | ✅ Done | GIT-019b |
| GIT-019f: Concrete `GitManager` implementation for edge CRUD (with inverse auto-creation) | ✅ Done | GIT-019e |

**Scope**: Foundation for the documentation layer. Adds the `Keyword` entity type and all new
relationship types to the existing `git-schema-v1`:
- `tagged_with` — Blob/Branch/Commit → Keyword
- `references` / `referenced_by` — generic Blob→Blob edge with a `descriptor` string property
  (open vocabulary; well-known values: `"documents"`, `"depends_on"`, `"contradicts"`,
  `"test_for"`, `"obsoletes"`). The inverse auto-copies the `descriptor` so both traversal
  directions carry full context (DR-023, DR-024).
- `has_child` / `belongs_to_parent` — Keyword taxonomy

Also adds `Properties []PropertyDefinition` to `RelationshipDefinition` in SharedLib so edge
property schemas are declared alongside the relationship (not just stored at runtime).
Implements keyword CRUD and branch-scoped edge creation/deletion with automatic inverse edges.
See: [requiements_documentation.md](../1-SoftwareRequirements/requiements_documentation.md)

### GIT-020 — Graph Query Endpoints

| Task | Status | Depends On |
|------|--------|------------|
| GIT-020a: Add `GitManager` interface methods — `GetNeighborhood(ctx, branchID, entityID, depth)` returning generic `{ nodes, edges }` graph response | ✅ Done | GIT-019f |
| GIT-020b: Add `GitManager` interface method — `SearchByKeywords(ctx, branchID, keywords, matchMode, cascade)` | ✅ Done | GIT-019d, GIT-019f |
| GIT-020c: Concrete implementation — AQL graph traversal for neighborhood query (depth 1-3, 100-node cap) | ✅ Done | GIT-020a |
| GIT-020d: Concrete implementation — AQL keyword search with cascading hierarchy + AND/OR match mode | ✅ Done | GIT-020b |

**Scope**: Graph query layer. Neighborhood query returns a generic `{ nodes, edges }` response
with configurable depth (1-3) and a 100-node hard cap. SearchByKeywords traverses the keyword
hierarchy when `cascade=true` and supports AND/OR match modes.
See: [requiements_documentation.md](../1-SoftwareRequirements/requiements_documentation.md)

### GIT-021 — Proto, gRPC Handlers & Route Registration

| Task | Status | Depends On |
|------|--------|------------|
| GIT-021a: Proto additions — `KeywordService` RPCs + `GraphService` RPCs; `buf generate` | ✅ Done | GIT-019c, GIT-020a |
| GIT-021b: gRPC server handlers for keyword CRUD, edge CRUD, and graph queries | ✅ Done | GIT-021a |
| GIT-021c: Register keyword, edge, and graph HTTP routes in `internal/registrar/routes.go` | ✅ Done | GIT-021b |
| GIT-021d: Unit tests for all new handlers and graph queries | ✅ Done | GIT-021c |

**Scope**: Exposes the documentation layer via gRPC and registers HTTP routes through
CodeValdCross. 10 new HTTP routes covering keyword CRUD, edge management, and graph queries.
See: [requiements_documentation.md](../1-SoftwareRequirements/requiements_documentation.md) §7

### GIT-022 — Edge Lifecycle on Merge/Delete/Revert

| Task | Status | Depends On |
|------|--------|------------|
| GIT-022a: Replicate `tagged_with` and `references` edges (with their `descriptor` property) from branch blobs to `main` blobs (by path) on `MergeBranch` | ✅ Done | GIT-019f, GIT-012 |
| GIT-022b: Delete documentation edges when branch is deleted without merge | ✅ Done | GIT-022a |
| GIT-022c: Migrate edges on file rename/move; remove edges on file delete | ✅ Done | GIT-022a |

**Scope**: Implements the DR-010 edge lifecycle rules. `tagged_with` and `references` edges
(including the `descriptor` property) are replicated to `main` on merge (matched by path),
deleted on branch delete, migrated on rename, and removed on file delete.

---

## Deprecated / Removed Tasks

_(None)_

---

## Task Summary by Priority

### P0 (Blocking — Must Complete First)
- **Foundation**: ~~2 tasks (MVP-GIT-001, MVP-GIT-002)~~ ✅ both complete
- **Core Operations**: ~~2 tasks (MVP-GIT-003, MVP-GIT-004)~~ ✅ both complete
- **Merge Workflow**: ~~2 tasks (MVP-GIT-005, MVP-GIT-006)~~ ✅ both complete

**Total P0**: 6 tasks ✅ ALL COMPLETE

### P1 (Important — Core Library Features)
- **History & Diff**: ~~1 task (MVP-GIT-007)~~ ✅ complete
- **Storage & Integration**: ~~2 tasks (MVP-GIT-008, MVP-GIT-009)~~ ✅ both complete
- **gRPC Microservice Integration**: ~~2 tasks (MVP-GIT-010, MVP-GIT-011)~~ ✅ both complete

**Total P1**: 5 tasks ✅ ALL COMPLETE

### P2 (CodeValdCross Integration)
- **Route Registration**: ~~MVP-GIT-011~~ ✅ complete

**Grand Total**: 11 tasks ✅ ALL COMPLETE

---

**Note**: This document contains only active and pending tasks. All completed tasks are moved to `mvp_done.md` to maintain a clean, actionable backlog.

Follow this sequence:

**Phase 1 — Foundation & Core:**
1. ~~MVP-GIT-001~~ ✅ — Library Scaffolding
2. ~~MVP-GIT-002~~ ✅ — Filesystem Repo Lifecycle
3. ~~MVP-GIT-003~~ ✅ — Branch-Per-Task Workflow
4. ~~MVP-GIT-004~~ ✅ — File Operations & Commit Attribution
5. ~~MVP-GIT-005~~ ✅ — Fast-Forward Merge
6. ~~MVP-GIT-006~~ ✅ — Auto-Rebase & Conflict Resolution

**Phase 2 — Read Access & Persistence:**
7. ~~MVP-GIT-007~~ ✅ — History & Diff
8. ~~MVP-GIT-008~~ ✅ — ArangoDB Storage Backend

**Phase 3 — gRPC Microservice Integration:**
9. ~~MVP-GIT-009~~ ✅ — gRPC Service Proto & Codegen
10. ~~MVP-GIT-010~~ ✅ — gRPC Server Implementation
11. ~~MVP-GIT-011~~ ✅ — Service-Driven Route Registration (declared via registrar)

---

## Done

_(Move tasks here as they complete — see `mvp_done.md`)_
