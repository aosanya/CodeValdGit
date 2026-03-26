# GIT-011 — Concurrency and Atomic Ref Updates

**Status**: 📋 Not Started
**Depends on**: ~~GIT-005~~ ✅
**See also**: [architecture-concurrency.md](../../2-SoftwareDesignAndArchitecture/architecture-concurrency.md)

---

## Objective

`MergeBranch` currently defers concurrency to the caller. Two concurrent
calls for the same agency produce a lost update. This task adds:

1. A `RefLocker` interface to serialise per-agency default-branch mutations.
2. CAS (compare-and-swap) validation in `advanceBranchHead` so a stale write
   is detected and returned as `ErrMergeConcurrencyConflict`.

---

## Files to Change

| File | Change |
|---|---|
| `git.go` | Add `RefLocker` interface |
| `errors.go` | Add `ErrMergeConcurrencyConflict` |
| `internal/manager/manager.go` | Accept `RefLocker` in `NewGitManager`; store on `gitManager` |
| `git_impl_repo.go` — `MergeBranch` | Wrap entire operation in `RefLocker.WithMergeLock` |
| `git_impl_repo.go` — `advanceBranchHead` | Accept `expectedHeadCommitID string`; pass to `UpdateEntity`; detect version conflict |

---

## Acceptance Criteria

- [ ] `RefLocker` interface defined in `git.go` with godoc
- [ ] Default in-process implementation (`mutexLocker`) using `sync.Mutex` keyed by `agencyID`
- [ ] `ErrMergeConcurrencyConflict` defined in `errors.go` with godoc
- [ ] `NewGitManager` accepts a `RefLocker` argument (nil → use `mutexLocker` default)
- [ ] `MergeBranch` calls `WithMergeLock` and only executes `advanceBranchHead` inside the lock
- [ ] `advanceBranchHead` passes expected `head_commit_id` to `entitygraph.UpdateEntity`
- [ ] `advanceBranchHead` returns `ErrMergeConcurrencyConflict` if entity revision has changed
- [ ] Unit test: two goroutines merge different task branches concurrently — both succeed, no lost update
- [ ] Unit test: `advanceBranchHead` with stale expected head → `ErrMergeConcurrencyConflict`
- [ ] `go vet ./...` passes; `go test -race ./...` passes

---

## Implementation Notes

### `RefLocker` interface (git.go)

```go
// RefLocker serialises default-branch mutations per agency.
// The default implementation is an in-process sync.Mutex keyed by agencyID.
// Inject a distributed lock implementation for multi-instance deployments.
type RefLocker interface {
    WithMergeLock(ctx context.Context, agencyID string, fn func() error) error
}
```

### Default `mutexLocker`

```go
type mutexLocker struct {
    mu sync.Map // key: agencyID → *sync.Mutex
}

func (l *mutexLocker) WithMergeLock(ctx context.Context, agencyID string, fn func() error) error {
    raw, _ := l.mu.LoadOrStore(agencyID, &sync.Mutex{})
    mu := raw.(*sync.Mutex)
    // Respect context cancellation while waiting.
    done := make(chan struct{})
    var fnErr error
    go func() {
        mu.Lock()
        defer mu.Unlock()
        defer close(done)
        fnErr = fn()
    }()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-done:
        return fnErr
    }
}
```

### CAS in `advanceBranchHead`

The `entitygraph.UpdateEntity` call must include the current `_rev` or an
expected-value check on `head_commit_id`. If `entitygraph.DataManager` exposes
a conditional update (e.g. `UpdateEntityIfMatch`), use it. Otherwise, wrap the
read-then-write in an ArangoDB transaction inside the backend implementation
and surface a version-conflict error.

---

## Branch

`feature/GIT-011_concurrency-ref-updates`
