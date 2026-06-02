# FEAT-20260602-004 — Workflow-run rollback (Git leg)

**Status:** ✅ Shipped (2026-06-02) — manager + gRPC + Cross route + tests
**Severity:** Medium — wraps up the Git leg of [Work coordinator FEAT-20260602-004](../../../../CodeValdWork/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-004_workflow_run_rollback_semantics.md)
**Owner:** CodeValdGit (executes); CodeValdWork orchestrates
**Branch:** `feature/Dev-GIT-FEAT20260602004_workflow_run_rollback`

---

## Goal

Implement the Git-side `DELETE /git/{agencyId}/by-workflow-run/{id}` endpoint
required by the CodeValdWork rollback coordinator. The Work doc
([phase 2 table](../../../../CodeValdWork/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-004_workflow_run_rollback_semantics.md#phase-2--per-service-delete-endpoints--in-progress))
calls for:

> Revert merged commits via a compensating merge commit on `main`.
> Hard-delete unmerged feature branches.
> Mark MergeRequests as `rolled_back`.

This shipped FEAT lands two of the three guarantees and surfaces the third
as a documented follow-up.

## Shipped behaviour

`GitManager.RollbackByWorkflowRun(ctx, workflowRunID)`:

1. **MergeRequests** — every MR carrying `workflow_run_id == workflowRunID` is
   transitioned to status `"rolled_back"`. `merged_commit_sha` is preserved
   (audit-only). MRs already in `"rolled_back"` are skipped so re-entry is a
   no-op. One [`TopicMergeRolledBack`](../../../events.go) event fires per
   transition with the prior status attached for observers.
2. **Branches** — every Branch carrying `workflow_run_id == workflowRunID`
   (and `is_default == false`) is hard-deleted via the existing
   `DeleteBranch` plumbing, which also tears down branch-scoped documentation
   edges. Default branches are preserved even when tagged (defensive — the
   counter `default_branches_skipped` surfaces the condition for operators).
3. **Summary event** — one [`TopicWorkflowRunRolledBack`](../../../events.go)
   event fires regardless of how many artifacts existed, carrying the three
   counters so the Work coordinator can mark the Git leg complete.

The whole operation is idempotent: re-invocation with the same run-id
produces a zero-counter `RollbackResult` and emits only the summary event.

### Trigger

```
DELETE /git/{agencyId}/by-workflow-run/{workflowRunId}
```

Wired via [`internal/registrar/rollback_routes.go`](../../../internal/registrar/rollback_routes.go);
the route is appended to `gitRoutes()` so Cross advertises it on every
heartbeat. gRPC method: `/codevaldgit.v1.GitService/RollbackByWorkflowRun`.

### Error mapping

| Domain error | gRPC code | Trigger |
|---|---|---|
| `ErrWorkflowRunIDRequired` | `INVALID_ARGUMENT` | empty `workflow_run_id` |
| (any other) | `INTERNAL` | unexpected manager failure |

## Code surface

| File | Purpose |
|---|---|
| `git_impl_rollback.go` | Manager implementation + helpers (`rollbackMergeRequestsForRun`, `deleteBranchesForRun`) |
| `git.go` | `RollbackByWorkflowRun` added to the `GitManager` interface |
| `models.go` | `RollbackResult` struct + `MergeRequestStatusRolledBack` constant |
| `events.go` | `TopicMergeRolledBack`, `TopicWorkflowRunRolledBack`, `MergeRequestRolledBackPayload`, `WorkflowRunRolledBackPayload` + `AllTopics` extended |
| `errors.go` | `ErrWorkflowRunIDRequired` sentinel |
| `proto/codevaldgit/v1/service.proto` | `RollbackByWorkflowRun` RPC + request/response messages |
| `internal/server/server_rollback.go` | gRPC handler (wiring only) |
| `internal/server/errors.go` | `ErrWorkflowRunIDRequired` → `INVALID_ARGUMENT` |
| `internal/registrar/rollback_routes.go` | HTTP route declaration |
| `internal/registrar/registrar.go` | `rollbackRoutes()` appended to `gitRoutes()` |

## Test coverage

`git_rollback_test.go` (manager-level, 5 cases):

- Happy path — deletes target-run branches, flips target-run MRs, leaves
  other-run artifacts intact, emits the right number of events.
- Preserves `merged_commit_sha` on previously-merged MRs after rollback.
- No-op rollback (run produced nothing) still fires the summary event.
- Idempotent re-invocation reports zero work.
- Empty `workflow_run_id` returns `ErrWorkflowRunIDRequired`.
- Default branch tagged with the run-id is preserved and counted into
  `DefaultBranchesSkipped`.

`internal/server/server_rollback_test.go` (gRPC-level, 3 cases):

- Forwards `workflow_run_id` to the manager; echoes counters in the response.
- `ErrWorkflowRunIDRequired` maps to `codes.InvalidArgument`.
- Unexpected manager error maps to `codes.Internal`.

`go build`, `go vet`, `go test -race ./...` all green for the new code. One
pre-existing failure (`TestGitManager_Diff`) is unrelated — fails on `main`
without this branch applied. Filed as a follow-up cleanup outside this FEAT.

## Explicit MVP limitation — "compensating merge"

The Work coordinator's published contract says:

> Revert merged commits via a compensating merge commit on `main`.

This FEAT **does not** create a compensating commit on the target branch when
a merged MR is rolled back. The commit chain is left intact (commits in this
service are content-addressed and immutable; the default branch HEAD is
unchanged). The MR row is flipped to `rolled_back` and a
`git.merge.rolled_back` event fires so observers can react — but anyone
reading `main` will still see the merged tree.

**Why ship it this way:** a clean compensating-commit implementation needs
the squash-merge / tree-diff rework in [GIT-012](critical-merge-strategy.md)
and the transaction boundaries in [GIT-013](critical-transactions.md). Both
are open P0s with no current owner. Blocking the cross-service rollback
coordinator on them would freeze every downstream rollback path; landing the
branch-and-MR unwind unblocks Work + AI + Comm immediately while leaving
the compensating-commit work as a clean, isolated follow-up.

**Follow-up:** once GIT-012 lands, extend
`rollbackMergeRequestsForRun` to walk the merged commit chain and produce a
proper compensating merge commit. The event topic + payload shape is already
in place, so consumers don't need to change.

## Dependencies

- ✅ `workflow_run_id` propagation (FEAT-20260602-001 Git sibling — shipped).
- ✅ MergeRequest entity + lifecycle (also FEAT-20260602-001).
- Pairs with: [Work coordinator FEAT-20260602-004](../../../../CodeValdWork/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-004_workflow_run_rollback_semantics.md)
  (already shipped — the coordinator currently logs the Git leg as a stub;
  wiring it to the real gRPC client is the Work-side follow-up).
