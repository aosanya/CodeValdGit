# CodeValdGit — Concurrency and Atomic Ref Updates

> Source: `review/review.md` (March 2026 design review)
> Status: Defined — implementation tracked in `mvp.md` (GIT-011)

---

## Problem

`MergeBranch` in `git_impl_repo.go` explicitly defers concurrency to the
caller ("callers are responsible for coordinating concurrent writes at the
application layer"). Without enforcement, two concurrent `MergeBranch` calls
for the same agency race to advance the same default-branch HEAD pointer,
causing a **lost update** — the second writer silently overwrites the first.

---

## Decision

| Rule | Scope |
|---|---|
| Object writes (Commit, Tree, Blob entities) are append-only and idempotent — safe to race | Global |
| Branch HEAD updates use compare-and-swap (CAS) — update only if current value matches expected | Per-branch |
| Default-branch merges are serialised per agency — one active merge per repository at a time | Per-agency |
| Branch deletion requires expected-HEAD validation — reject if HEAD has changed since read | Per-branch |

---

## `RefLocker` Interface

Add to `git.go`:

```go
// RefLocker serialises mutations to the default branch within an agency.
// The V1 default implementation is an in-process sync.Mutex keyed by agencyID.
// A distributed lock (ArangoDB document transactions, Redis SETNX) can be
// substituted without changing the interface.
type RefLocker interface {
    // WithMergeLock acquires an exclusive lock for agencyID, executes fn,
    // then releases the lock. Returns ctx.Err() if the context is cancelled
    // while waiting.
    WithMergeLock(ctx context.Context, agencyID string, fn func() error) error
}
```

`gitManager` accepts a `RefLocker` via `NewGitManager`. `MergeBranch` wraps
the entire HEAD-advance operation inside `RefLocker.WithMergeLock`.

---

## CAS in `advanceBranchHead`

The helper must pass the **expected** current `head_commit_id` to
`entitygraph.UpdateEntity` and return `ErrMergeConcurrencyConflict` if the
document has been modified since the read. In ArangoDB this maps to `_rev`
optimistic locking.

Add to `errors.go`:

```go
// ErrMergeConcurrencyConflict is returned by MergeBranch when the default
// branch HEAD was modified between the read and the compare-and-swap write.
// The caller should retry after re-reading the current branch state.
var ErrMergeConcurrencyConflict = errors.New("merge concurrency conflict: branch HEAD changed during merge")
```

---

## V1 Scope

| Item | V1 choice |
|---|---|
| `RefLocker` implementation | In-process `sync.Mutex` keyed by `agencyID` — correct for single-instance deployment |
| CAS | Expected `head_commit_id` passed through `advanceBranchHead`; `_rev` check in ArangoDB |
| Distributed lock | Documented as future extension; interface does not change when added |

---

## Sequence: Serialised Merge

```
caller → WithMergeLock(agencyID)
           │
           ├── read default-branch HEAD  (inside lock)
           ├── compute squash commit
           ├── write speculative objects (idempotent, outside visibility)
           └── advanceBranchHead(expectedHead=readValue)  ← CAS
                    ├── success → unlock, return updated Branch
                    └── conflict → unlock, return ErrMergeConcurrencyConflict
```

See [architecture-merge.md](architecture-merge.md) for the squash merge
strategy that executes inside the lock, and
[architecture-transactions.md](architecture-transactions.md) for crash-safety
rules around the same operation.
