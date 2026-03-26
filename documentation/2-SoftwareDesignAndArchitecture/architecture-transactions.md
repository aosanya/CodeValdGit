# CodeValdGit — Transaction Boundaries and Idempotency

> Source: `review/review.md` (March 2026 design review)
> Status: Defined — implementation tracked in `mvp.md` (GIT-013)

---

## Problem

`MergeBranch` is a multi-step operation with a single visibility gate
(`advanceBranchHead`). A process crash between steps produces ambiguous state:

| Step | Crash here → state |
|---|---|
| 1–3 reads | Nothing written — safe to retry |
| Speculative object writes | Orphaned immutable entities — safe to retry (idempotent) |
| `advanceBranchHead` | **Merge visible** — must not re-execute |
| `DeleteBranch` (caller) | Merge done, branch leaked — caller retries delete |

---

## Atomicity Rules

| Rule | Rationale |
|---|---|
| Object writes before `advanceBranchHead` are speculative and idempotent | Content-addressed; re-writing the same content is a no-op |
| Only `advanceBranchHead` makes the merge visible | Single atomic write = the visibility gate |
| `DeleteBranch` must only be called after `advanceBranchHead` succeeds | Prevents losing work on a crash between the two |
| Every merge attempt carries an idempotency key | Enables safe retry without re-executing a completed merge |

---

## `MergeRequest` — Idempotency Key

Replace the bare `branchID string` parameter on `GitManager.MergeBranch` with
a `MergeRequest` struct. Add to `models.go`:

```go
// MergeRequest carries the inputs for a [GitManager.MergeBranch] call.
type MergeRequest struct {
    // BranchID is the entitygraph ID of the branch to merge into the
    // repository's default branch.
    BranchID string

    // IdempotencyKey is an optional caller-provided key for safe retry.
    // A completed merge with the same key returns the cached result without
    // re-executing. Recommended value: taskID + ":" + forkPointCommitID.
    IdempotencyKey string
}
```

Update `git.go`:

```go
// MergeBranch merges req.BranchID into the repository's default branch.
// If req.IdempotencyKey matches a previously completed merge, the cached
// result is returned without re-executing.
MergeBranch(ctx context.Context, req MergeRequest) (Branch, error)
```

---

## Retry Safety Matrix

| Operation | Safe to retry? | Behaviour on duplicate |
|---|---|---|
| `InitRepo` | Yes | Returns `ErrRepoAlreadyExists` |
| `CreateBranch` | Yes | Returns `ErrBranchExists` |
| `CommitFiles` | Yes | Idempotent if content + message unchanged |
| `MergeBranch` | Yes (with `IdempotencyKey`) | Returns cached result if already merged |
| `DeleteBranch` | Yes | Returns `ErrBranchNotFound` — caller ignores |

---

## Idempotency Store

V1: a `sync.Map` keyed by `IdempotencyKey` → `Branch` result, held on the
`gitManager` instance. Cleared on process restart (acceptable for V1
single-instance deployment).

Future: persist idempotency records as entities in the entitygraph so they
survive restarts, with a TTL-based cleanup policy.

---

## Caller Contract (CodeValdCross / gRPC server)

The gRPC `MergeTaskBranch` handler must:

1. Construct `IdempotencyKey = taskID + ":" + forkPointCommitID`.
2. Call `GitManager.MergeBranch(ctx, MergeRequest{BranchID, IdempotencyKey})`.
3. On success → call `GitManager.DeleteBranch(ctx, branchID)`.
4. On `ErrMergeConcurrencyConflict` → retry `MergeBranch` (key unchanged).
5. On `ErrMergeConflict` → surface conflict files to caller; do **not** delete branch.

See [architecture-concurrency.md](architecture-concurrency.md) for the lock
that guards step 2, and [architecture-merge.md](architecture-merge.md) for the
squash strategy executed inside it.
