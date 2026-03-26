# GIT-013 — Transaction Boundaries and Idempotency

**Status**: 📋 Not Started
**Depends on**: GIT-012
**See also**: [architecture-transactions.md](../../2-SoftwareDesignAndArchitecture/architecture-transactions.md)

---

## Objective

Make `MergeBranch` crash-safe and safely retryable by:

1. Introducing `MergeRequest` with an `IdempotencyKey` field.
2. Adding an in-process idempotency cache on `gitManager`.
3. Documenting the caller retry contract for the gRPC server layer.

---

## Files to Change

| File | Change |
|---|---|
| `models.go` | Add `MergeRequest` struct |
| `git.go` | Update `GitManager.MergeBranch` signature to accept `MergeRequest` |
| `git_impl_repo.go` — `MergeBranch` | Check idempotency cache before executing; store result after `advanceBranchHead` succeeds |
| `internal/manager/manager.go` | Add `idempotency sync.Map` field to `gitManager` |
| `internal/server/server.go` | Construct `MergeRequest` with `IdempotencyKey = taskID + ":" + forkPointCommitID` |
| `gen/go/codevaldgit/v1/` | Regenerate if proto `MergeTaskBranchRequest` gains an `idempotency_key` field |

---

## Acceptance Criteria

- [ ] `MergeRequest` defined in `models.go` with godoc
- [ ] `GitManager.MergeBranch` signature updated to `MergeBranch(ctx, MergeRequest) (Branch, error)`
- [ ] All call sites updated (server handler, tests)
- [ ] `gitManager` holds a `sync.Map` for idempotency: key `IdempotencyKey` → `Branch` result
- [ ] If `IdempotencyKey` is non-empty and matches a cached entry → return cached `Branch`, skip execution
- [ ] Idempotency entry written only after `advanceBranchHead` succeeds (not before)
- [ ] Empty `IdempotencyKey` → no caching (backwards-compatible path)
- [ ] Unit test: same `MergeRequest` called twice → second call returns cached result without modifying state
- [ ] Unit test: empty `IdempotencyKey` → both calls execute independently
- [ ] gRPC server constructs `IdempotencyKey = taskID + ":" + forkPointCommitID`
- [ ] `go vet ./...` passes; `go test -race ./...` passes

---

## Implementation Notes

### `MergeRequest` (models.go)

```go
// MergeRequest carries the inputs for [GitManager.MergeBranch].
type MergeRequest struct {
    // BranchID is the entitygraph ID of the branch to merge into the
    // repository's default branch.
    BranchID string

    // IdempotencyKey is an optional caller-provided key for safe retry.
    // A completed merge with the same key returns the cached result without
    // re-executing. Recommended value: taskID + ":" + forkPointCommitID.
    // Empty string disables idempotency caching.
    IdempotencyKey string
}
```

### Idempotency Cache in `gitManager`

```go
type gitManager struct {
    agencyID    string
    dm          entitygraph.DataManager
    publisher   CrossPublisher
    locker      RefLocker
    idempotency sync.Map // key: IdempotencyKey (string) → Branch
}
```

In `MergeBranch`:

```go
func (m *gitManager) MergeBranch(ctx context.Context, req MergeRequest) (Branch, error) {
    if req.IdempotencyKey != "" {
        if cached, ok := m.idempotency.Load(req.IdempotencyKey); ok {
            return cached.(Branch), nil
        }
    }
    // ... execute merge inside WithMergeLock ...
    // After advanceBranchHead succeeds:
    if req.IdempotencyKey != "" {
        m.idempotency.Store(req.IdempotencyKey, updatedBranch)
    }
    return updatedBranch, nil
}
```

### Caller Retry Contract (gRPC server)

```
1. Construct key = taskID + ":" + forkPointCommitID
2. Call MergeBranch(ctx, MergeRequest{BranchID, key})
3. On success              → call DeleteBranch(ctx, branchID)
4. On ErrMergeConcurrencyConflict → retry MergeBranch (same key)
5. On ErrMergeConflict     → surface Files to caller; do NOT delete branch
6. On other error          → surface error; do NOT delete branch
```

---

## Branch

`feature/GIT-013_transaction-boundaries-idempotency`
