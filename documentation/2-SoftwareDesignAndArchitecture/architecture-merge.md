# CodeValdGit — Merge Strategy

> Source: `review/review.md` (March 2026 design review)
> Status: Defined — implementation tracked in `mvp.md` (GIT-012)

---

## Problem

The current `MergeBranch` implementation advances the default-branch HEAD
pointer directly to the task branch HEAD. This:

1. Does not detect whether the task branch diverged from the current default
   branch since it was created.
2. Silently overwrites any commits that landed on the default branch while the
   task was running.

The legacy go-git `repo.go` had cherry-pick rebase, but replaying each commit
individually is fragile, changes commit IDs, and is difficult to make
crash-safe.

---

## Decision: Tree-Diff Squash Merge

```
1. Read fork_point_commit_id from the Branch entity (set by CreateBranch).
2. Read current default-branch HEAD commit.
3. If fork-point == default HEAD → fast-forward: advance pointer, done.
4. If fork-point != default HEAD → diverged:
   a. Compute tree diff: fork-point tree → task HEAD tree  (agent's net changes)
   b. Apply diff to current default HEAD tree
   c. If clean → create one squash Commit entity on the default branch
   d. If conflicts → return ErrMergeConflict{Files: [...]}
```

---

## Cherry-Pick Rebase vs Squash Merge

| Property | Cherry-pick Rebase | Squash Merge |
|---|---|---|
| Commit IDs change | Yes — author/tooling confusion | No — single new commit |
| Intermediate commits replayed | Yes — noisy, multi-step | No — net-change only |
| Conflict surface | Per-commit | Per-file tree diff |
| Partial failure recovery | Hard — loop state | Simple — apply is atomic |
| Task branch history preserved | Yes | Yes — branch retained for audit |

---

## Fork-Point Tracking

`CreateBranch` must record the default-branch HEAD at creation time as
`fork_point_commit_id` on the `Branch` entity. `MergeBranch` reads this to
determine divergence.

Add to `Branch` in `models.go`:

```go
// ForkPointCommitID is the default-branch HEAD commit ID at the time this
// branch was created. Used by MergeBranch to detect divergence and compute
// the correct tree diff base.
ForkPointCommitID string `json:"fork_point_commit_id,omitempty"`
```

`CreateBranch` in `git_impl_repo.go` must populate this field when creating
a task branch:

```go
Properties: map[string]any{
    "name":                  req.Name,
    "is_default":            false,
    "head_commit_id":        sourceBranch.HeadCommitID,
    "fork_point_commit_id":  sourceBranch.HeadCommitID,  // ← new
    "created_at":            now,
    "updated_at":            now,
},
```

---

## Conflict Surface

`ErrMergeConflict` in `types.go` is already defined. For squash merge, the
`Files` field carries the paths where the agent's tree diff could not be
applied cleanly to the current default-branch tree.

```go
// ErrMergeConflict is returned by MergeBranch when the tree-diff apply
// cannot complete cleanly. The branch is left untouched; the caller is
// responsible for routing the conflict back to the agent.
type ErrMergeConflict struct {
    Files []string // conflicting file paths
}
```

---

## Branch History Retention

The task branch entity is **not** deleted by `MergeBranch`. The caller
(`DeleteBranch`) removes it after the merge succeeds. The squash commit on the
default branch stores `source_branch_id` as metadata for audit.

See [architecture-concurrency.md](architecture-concurrency.md) for the
serialisation wrapper around this operation, and
[architecture-transactions.md](architecture-transactions.md) for crash-safety
rules.
