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

## P0: ArangoDB `storage.Storer` — Unified Single Backend (CRITICAL)

*~~GIT-015a~~ ✅, ~~GIT-015b~~ ✅, ~~GIT-015c~~ ✅, ~~GIT-015d~~ ✅, ~~GIT-015e~~ ✅ complete — see `mvp_done.md`.*

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
| GIT-016a: `ImportJob` TypeDefinition + `git_importjobs` collection in `schema.go` | 📋 Not Started | ~~GIT-001~~ ✅ |
| GIT-016b: Types (`ImportRepoRequest`, `ImportJob`) + errors + `GitManager` interface additions | 📋 Not Started | GIT-016a |
| GIT-016c: Core implementation — background goroutine, go-git clone, all-branch entity walk, cancel map | 📋 Not Started | GIT-016b |
| GIT-016d: Proto additions (3 RPCs) + `buf generate` | 📋 Not Started | GIT-016b |
| GIT-016e: gRPC server handlers + error mapping for all 3 RPCs | 📋 Not Started | GIT-016c, GIT-016d |
| GIT-016f: Unit tests — import manager, cancel, concurrency rejection | 📋 Not Started | GIT-016e |

**Scope**: Full async import of a public HTTPS Git repository into the entity graph.
Six sub-tasks cover the schema addition, interface contract, core ingestion goroutine,
proto codegen, gRPC server wiring, and unit tests.
See: [mvp-details/repo-import.md](mvp-details/repo-import.md)

---

## Bugs and Issues

### Active Bugs

_(None)_

### Resolved Bugs

_(None yet)_

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
