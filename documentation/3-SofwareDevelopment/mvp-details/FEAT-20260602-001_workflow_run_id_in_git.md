# FEAT-20260602-001 — `workflow_run_id` propagation in CodeValdGit

**Status:** ✅ Done (2026-06-02, branch `feature/Dev-GIT-001_workflow-run-id-propagation`)
**Severity:** High — sibling of the umbrella; branches and merge-requests are the most observable side-effects of a pipeline (state lives in git, not just the DB), so the rollback transaction *must* be able to enumerate them
**Owner:** CodeValdGit
**Estimated effort:** ~1.5 days (schema + Branch/MergeRequest proto + handler propagation + list filter + integration tests)
**Source finding:** This conversation (2026-06-02) — sibling of [umbrella FEAT-20260602-001 in Cross](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-001_workflow_run_id_propagation_umbrella.md)

---

## Problem

CodeValdGit creates `Branch` and `MergeRequest` entities when pipelines push code (typically driven by `compile-flutter` succeeding → `merge-flutter-branch` running). Today these entities have no link to the originating `WorkflowRun`, so:

- The closure view can't render which branches the pipeline pushed.
- Rollback can't enumerate "every branch this run created" to know what to revert/delete.
- Operators investigating a failed scenario have to manually match branch names against task names.

## Goal

Make `workflow_run_id` a first-class typed field on:

- `Branch` entity
- `MergeRequest` entity
- Every `git.branch.*` event payload (`git.branch.created`, `git.branch.deleted`)
- Every `git.merge.*` event payload (`git.merge.requested`, `git.merge.completed`, `git.merge.failed`)
- List filters: `GET /git/{agencyId}/branches?workflow_run_id=X`, `GET /git/{agencyId}/merge-requests?workflow_run_id=X`

## Non-goals

- Adding `workflow_run_id` to `Repository` config entity or commit metadata.
- Storing the run-id inside git itself (e.g. as a commit trailer). For v1 we keep this in the entity-graph only; commit-trailer encoding is a future option.

---

## Design

### Schema changes

In `schema.go`, under both `Branch` and `MergeRequest` `TypeDefinition`s:

```go
{Name: "workflow_run_id", Type: types.PropertyTypeString},
```

### Proto changes

In `proto/codevaldgit/v1/`:

- `Branch` message: `string workflow_run_id = N;`
- `MergeRequest` message: `string workflow_run_id = N;`
- `CreateBranchRequest` accepts `string workflow_run_id`.
- `MergeRequestRequest` accepts `string workflow_run_id`.
- `ListBranchesRequest`, `ListMergeRequestsRequest` accept the filter.

### Event payload changes

Every event emitted from `git.*` handlers gains `workflow_run_id`. Read from inbound trigger events; copy onto created entities; include in emitted events.

### Chain-through behaviour

| Operation | Triggered by | Reads `workflow_run_id` from |
|---|---|---|
| Create feature branch | `work.task.assigned` (AI creates branch) or direct API call from AI run | inbound event / API param |
| Push commits | (no separate event in scope; commits inherit Branch.workflow_run_id) | — |
| Open merge request | API call from `merge-flutter-branch` function | inbound trigger event |
| Merge | API call from `merge-flutter-branch` function | inbound trigger event |
| Delete branch | API call after successful merge | parent MergeRequest.workflow_run_id |

### Cross-service identification

`MergeRequest` already references a `Branch` (by name + repo). The `workflow_run_id` should match between them — verify at create time. If they differ, log a warning but persist as supplied (the actual rollback engine can reconcile).

---

## Implementation plan

### Phase 1 — Schema + proto (~0.5 day)

1. Add property to `Branch` and `MergeRequest` in `schema.go`.
2. Add proto fields; `make proto`.

### Phase 2 — Handlers + events (~0.5 day)

1. Update branch create, merge open, merge complete handlers to read + persist + propagate `workflow_run_id`.
2. Update event payload structs.

### Phase 3 — Tests (~0.5 day)

- Unit: create branch with run-id → persists.
- Integration: pipeline → branch created via API has run-id → merge completed event carries run-id.

---

## Verification

- `go test -race -count=1 ./...` clean.
- Run scenario 09; `GET /git/utility-app-builder/branches?workflow_run_id=$RUN` returns the feature branch the pipeline created.
- `GET /git/utility-app-builder/merge-requests?workflow_run_id=$RUN` returns the MR the pipeline opened.
- Each `git.merge.completed` event in the SSE log carries `workflow_run_id`.

---

## Open design questions

1. **Branch deletion after merge.** The post-merge delete-branch operation: is the deleted-branch event still under the same `workflow_run_id`? Recommend yes — the delete is the final step of the same pipeline.
2. **Manual operations.** A branch pushed via `git push` directly (not via API) bypasses CodeValdGit's create path. It will have `workflow_run_id = ""`. Acceptable for v1; rollback can ignore those.

---

## Dependencies

- Part of umbrella: [FEAT-20260602-001 in Cross](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-001_workflow_run_id_propagation_umbrella.md).
- Pairs with: [Functions sibling FEAT](../../../../CodeValdFunctions/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-002_workflow_run_id_in_functions.md) — `merge-flutter-branch` is the primary caller that produces branches/merges with a run context.
