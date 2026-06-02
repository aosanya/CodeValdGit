# MVP — Active Task Backlog

## Overview
- **Objective**: Deliver CodeValdGit as a production-ready standalone gRPC microservice with full Git semantics, entity-graph storage, and documentation layer.
- **Completed tasks**: see [`mvp_done.md`](mvp_done.md)
- **Detailed specs**: see [`mvp-details/`](mvp-details/)

## Workflow

### Completion Process (MANDATORY)
1. Implement and validate (`go build ./...`, `go vet ./...`, `go test -race ./...`)
2. Add row to `mvp_done.md`
3. Remove task from this file
4. Mark dependency references as `~~GIT-XXX~~ ✅`
5. Merge feature branch to main and delete it

### Branch Management
```bash
git checkout -b feature/GIT-XXX_description
# implement + validate
git checkout main
git merge feature/GIT-XXX_description --no-ff
git branch -d feature/GIT-XXX_description
```

### Status Legend
- 📋 **Not Started** — ready to begin (dependencies met)
- 🚀 **In Progress** — currently being worked on
- ⏸️ **Blocked** — waiting on dependencies

---

## Outstanding feature work

| Task ID | Title | Status | Depends On |
|---|---|---|---|
| FEAT-20260602-001 | `workflow_run_id` on `Branch` / `MergeRequest` + every `git.*` event payload (Git sibling of the [Cross umbrella](../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-001_workflow_run_id_propagation_umbrella.md)) | 📋 Not Started | FEAT-20260602-001 in CodeValdFunctions (start-pipeline) |

See [mvp-details/FEAT-20260602-001_workflow_run_id_in_git.md](mvp-details/FEAT-20260602-001_workflow_run_id_in_git.md).

---

## P1: `.git-graph/` Push Sync (GIT-025)

### GIT-025 — `.git-graph/` Push Sync

| Task | Status | Depends On |
|------|--------|------------|
| GIT-025e: Schema v2 — extend `MappingFile` and parser to support `depths[]` keyword entries with `signal` + `note` fields; `.signals.json` loader | ✅ Done | GIT-025a |
| GIT-025b: Sync logic — `Syncer` type, keyword upsert, edge hard-sync (`internal/gitgraph/sync.go`) | ✅ Done | GIT-025a |
| GIT-025c: Hook `IndexPushedBranch` to call `syncGitGraph` after commit/blob phase (`git_impl_index.go`, `git_impl_push.go`) | ✅ Done | GIT-025b |

**Scope**: After every push, `IndexPushedBranch` reads all `.git-graph/*.json` files at the
new branch tip, upserts keywords (agency-scoped, never deleted), and hard-syncs `tagged_with`
and `references` edges for touched files. Sync errors are logged but never fail the push.

GIT-025e adds support for the v2 keyword depth schema:
```json
{ "name": "development-tracking", "depths": [{ "signal": "authority", "note": "..." }] }
```
The `.signals.json` file at `.git-graph/.signals.json` defines the repo's valid signal names
and their layer numbers; the parser validates `signal` values against it.

See: [mvp-details/git-graph-sync.md](mvp-details/git-graph-sync.md)

---

## P0: Production Safety (CRITICAL)

~~### GIT-011 — Concurrency and Atomic Ref Updates~~ ✅

---

### GIT-012 — Squash Merge Strategy

| Task | Status | Depends On |
|------|--------|------------|
| GIT-012: Fork-point tracking in `CreateBranch` + tree-diff squash merge in `MergeBranch` | 📋 Not Started | GIT-011 |

**Scope**: Add `ForkPointCommitID` to `Branch` model and `CreateBranch` entity write.
Replace HEAD-pointer-advance in `MergeBranch` with: fast-forward if no divergence,
tree-diff apply if diverged, `ErrMergeConflict` on conflict.
See: [mvp-details/critical-merge-strategy.md](mvp-details/critical-merge-strategy.md)

---

### GIT-013 — Transaction Boundaries and Idempotency

| Task | Status | Depends On |
|------|--------|------------|
| GIT-013: `MergeRequest` struct with `IdempotencyKey` + in-process idempotency store | 📋 Not Started | GIT-012 |

**Scope**: Replace `MergeBranch(ctx, branchID string)` with `MergeBranch(ctx, MergeRequest)`.
Add `MergeRequest` to `models.go`. Implement in-process `sync.Map` idempotency cache on
`gitManager`. Document retry contract for gRPC server layer.
See: [mvp-details/critical-transactions.md](mvp-details/critical-transactions.md)

---

### GIT-014 — ArangoDB Backend: Deduplication, Documentation, and Production Gate

| Task | Status | Depends On |
|------|--------|------------|
| GIT-014: `(agencyID, sha)` unique index + update `storage-backends.md` + experimental flag + benchmarks | 📋 Not Started | GIT-011 |

**Scope**: Three subtasks — (A) add unique `(agencyID, sha)` constraint to `git_objects` and
handle duplicate inserts gracefully in writers; (B) replace the stale v1 `storage-backends.md`
with the v2 entitygraph collection spec; (C) add `Config.Backend` with filesystem default,
startup warning for ArangoDB, and a benchmarking plan with measurable promotion criteria.
See: [mvp-details/critical-arangodb.md](mvp-details/critical-arangodb.md)
