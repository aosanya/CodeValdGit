# Branch Workflow

Topics: Branch-Per-Task · Fast-Forward Merge · Auto-Rebase & Conflict Resolution

---

## MVP-GIT-003 — Branch-Per-Task Workflow

### Overview
Implement `CreateBranch` and `DeleteBranch` on the `Repo` interface. Agents **must never commit directly to `main`**. Every write operation happens on a short-lived `task/{task-id}` branch. This task establishes that constraint and the branch lifecycle primitives.

### Branch Naming Convention

| Branch | Format | Example |
|---|---|---|
| Main trunk | `main` | `main` |
| Task branch | `task/{task-id}` | `task/task-abc-001` |

Task IDs come from CodeValdCortex's task records. The library does not validate or generate task IDs — callers provide them.

### Acceptance Criteria
- [ ] `CreateBranch(ctx, taskID)` creates `refs/heads/task/{taskID}` pointing at the current `HEAD` of `main`
- [ ] Returns `ErrBranchExists` if `task/{taskID}` already exists
- [ ] `DeleteBranch(ctx, taskID)` removes `refs/heads/task/{taskID}`
- [ ] Returns `ErrBranchNotFound` if the branch does not exist
- [ ] Deleting `main` is explicitly rejected with a descriptive error
- [ ] Both operations are safe to call concurrently (different task IDs)

### Implementation Notes

```go
// CreateBranch — go-git plumbing approach
func (r *repo) CreateBranch(ctx context.Context, taskID string) error {
    branchName := "task/" + taskID
    ref := plumbing.NewBranchReferenceName(branchName)

    // Resolve HEAD of main to get the starting SHA
    mainRef, err := r.gitRepo.Reference(plumbing.NewBranchReferenceName("main"), true)
    if err != nil {
        return fmt.Errorf("resolving main: %w", err)
    }

    // Check branch does not already exist
    if _, err := r.gitRepo.Reference(ref, false); err == nil {
        return ErrBranchExists
    }

    // Create the new branch reference
    newRef := plumbing.NewHashReference(ref, mainRef.Hash())
    return r.gitRepo.Storer.SetReference(newRef)
}

// DeleteBranch — remove the reference
func (r *repo) DeleteBranch(ctx context.Context, taskID string) error {
    if taskID == "" || taskID == "main" {
        return errors.New("cannot delete main branch")
    }
    ref := plumbing.NewBranchReferenceName("task/" + taskID)
    if _, err := r.gitRepo.Reference(ref, false); err != nil {
        return ErrBranchNotFound
    }
    return r.gitRepo.Storer.RemoveReference(ref)
}
```

### Branch Lifecycle (Full Picture)

```
Task starts:
  CreateBranch("task/{task-id}")  ← branch points at main HEAD

Agent writes files:
  WriteFile("task/{task-id}", ...)  ← commits accumulate on branch

Task completes:
  MergeBranch("task/{task-id}")     ← merges branch into main (see MVP-GIT-005/006)
  DeleteBranch("task/{task-id}")    ← branch ref removed
```

### Dependencies
- MVP-GIT-002 (repo must be openable)

### Tests

| Test | Approach |
|---|---|
| `TestCreateBranch_Success` | Branch ref exists after call; points at main HEAD |
| `TestCreateBranch_AlreadyExists` | Returns `ErrBranchExists` |
| `TestCreateBranch_EmptyTaskID` | Returns error |
| `TestDeleteBranch_Success` | Branch ref absent after call |
| `TestDeleteBranch_NotFound` | Returns `ErrBranchNotFound` |
| `TestDeleteBranch_Main` | Returns error; main is not deleted |
| `TestConcurrentCreateBranch` | 5 goroutines create different branches simultaneously |

---

## MVP-GIT-005 — Fast-Forward Merge

### Overview
Implement `MergeBranch` for the happy path: when `main` has not advanced since the task branch was created, a fast-forward merge is possible. The task branch `HEAD` simply becomes the new `main` `HEAD` — no new merge commit is created.

### Acceptance Criteria
- [ ] `MergeBranch(ctx, taskID)` fast-forwards `main` to `task/{taskID}` HEAD when possible
- [ ] `main` HEAD is updated to the task branch's latest commit SHA
- [ ] The operation is idempotent — merging an already-merged branch returns nil (no-op)
- [ ] Returns `ErrBranchNotFound` if `task/{taskID}` does not exist
- [ ] go-git's `FastForwardMerge` is used via `Worktree.Pull` or equivalent

### When Fast-Forward Is Possible

Fast-forward is possible when `main` HEAD is an ancestor of `task/{taskID}` HEAD:

```
main:          A → B
task/abc:      A → B → C → D

After merge:   main → D   (no merge commit; main just moves forward)
```

Fast-forward is **not** possible when `main` has new commits the task branch doesn't have (see MVP-GIT-006).

### Implementation Notes

```go
// MergeBranch — attempts fast-forward first
func (r *repo) MergeBranch(ctx context.Context, taskID string) error {
    branchName := "task/" + taskID

    taskRef, err := r.gitRepo.Reference(
        plumbing.NewBranchReferenceName(branchName), true)
    if err != nil {
        return ErrBranchNotFound
    }

    mainRef, err := r.gitRepo.Reference(
        plumbing.NewBranchReferenceName("main"), true)
    if err != nil {
        return fmt.Errorf("resolving main: %w", err)
    }

    // Check if already up-to-date (idempotent)
    if mainRef.Hash() == taskRef.Hash() {
        return nil
    }

    // Check if fast-forward is possible: main HEAD must be ancestor of task HEAD
    if ok, err := r.isAncestor(mainRef.Hash(), taskRef.Hash()); err != nil {
        return err
    } else if ok {
        // Fast-forward: move main HEAD to task branch HEAD
        newMain := plumbing.NewHashReference(
            plumbing.NewBranchReferenceName("main"), taskRef.Hash())
        return r.gitRepo.Storer.SetReference(newMain)
    }

    // Main has advanced — fall through to auto-rebase (MVP-GIT-006)
    return r.rebaseAndMerge(ctx, taskID, taskRef, mainRef)
}
```

### Ancestor Check

```go
// isAncestor returns true if candidateAncestor is reachable walking backwards from tip.
func (r *repo) isAncestor(candidateAncestor, tip plumbing.Hash) (bool, error) {
    iter, err := r.gitRepo.Log(&git.LogOptions{From: tip})
    if err != nil {
        return false, err
    }
    defer iter.Close()
    for {
        c, err := iter.Next()
        if err == io.EOF {
            return false, nil
        }
        if err != nil {
            return false, err
        }
        if c.Hash == candidateAncestor {
            return true, nil
        }
    }
}
```

### Dependencies
- MVP-GIT-003 (branches must exist before merging)
- MVP-GIT-004 (need at least one commit on branch to test meaningfully)

### Tests

| Test | Approach |
|---|---|
| `TestMergeBranch_FastForward` | Create branch, write file, merge; verify main HEAD == task HEAD |
| `TestMergeBranch_AlreadyMerged` | Merge twice; second call returns nil |
| `TestMergeBranch_BranchNotFound` | Returns `ErrBranchNotFound` |
| `TestMergeBranch_EmptyBranch` | Branch exists but has no extra commits — fast-forward is no-op |

---

## MVP-GIT-006 — Auto-Rebase & Conflict Resolution

### Overview
Handle the case where `main` has advanced since the task branch was created (fast-forward not possible). The library must **automatically rebase** the task branch commits onto the current `main`, then fast-forward merge. If the rebase encounters a content conflict, return a structured `ErrMergeConflict` and leave the task branch in a clean state so the agent can retry.

### Why Manual Rebase is Required

> **go-git constraint**: `Repository.Merge()` only supports `FastForwardMerge` strategy (added v5.12.0). Three-way merges and rebase are **not natively supported** in go-git. The rebase must be implemented manually by cherry-picking commits from the task branch onto `main`.

### Acceptance Criteria
- [ ] When fast-forward is not possible, auto-rebase is attempted automatically (no caller change required)
- [ ] Auto-rebase cherry-picks each task branch commit onto the current `main` in order
- [ ] On rebase success, fast-forward merge proceeds and `main` is updated
- [ ] On rebase conflict: return `*ErrMergeConflict{TaskID, ConflictingFiles}`; task branch is left in its **original pre-rebase state** (clean for retry)
- [ ] Rebase does not mutate `main` — all work happens on a temporary in-memory worktree or rebase branch
- [ ] `ConflictingFiles` in the error contains at minimum the file paths involved

### Rebase Strategy

```
Before rebase:
  main:    A → B → C           ← main has advanced (new commit C)
  task:    A → B → D → E       ← task branch has commits D and E

Rebase steps:
  1. Cherry-pick D onto C → D'
  2. Cherry-pick E onto D' → E'

After successful rebase:
  main:    A → B → C → D' → E'  (task commits replayed on top of new main)
```

### Implementation Approach

Since go-git lacks native rebase, implement cherry-pick manually:

```go
// rebaseAndMerge rebases taskBranch commits onto main, then fast-forwards.
func (r *repo) rebaseAndMerge(ctx context.Context, taskID string,
    taskRef, mainRef *plumbing.Reference) error {

    // 1. Collect commits on task branch that are NOT on main
    //    Walk from task HEAD back to the common ancestor
    taskCommits, err := r.commitsSinceAncestor(taskRef.Hash(), mainRef.Hash())
    if err != nil {
        return err
    }

    // 2. Cherry-pick each commit onto main using a temporary worktree
    currentBase := mainRef.Hash()
    for _, commit := range taskCommits {
        newHash, conflictFiles, err := r.cherryPick(ctx, currentBase, commit)
        if err != nil {
            return err
        }
        if len(conflictFiles) > 0 {
            // Task branch is untouched (we worked on a temp worktree)
            return &ErrMergeConflict{
                TaskID:           taskID,
                ConflictingFiles: conflictFiles,
            }
        }
        currentBase = newHash
    }

    // 3. All cherry-picks succeeded — update task branch ref to rebased tip
    rebasedRef := plumbing.NewHashReference(
        plumbing.NewBranchReferenceName("task/"+taskID), currentBase)
    if err := r.gitRepo.Storer.SetReference(rebasedRef); err != nil {
        return err
    }

    // 4. Fast-forward main to rebased task HEAD
    newMain := plumbing.NewHashReference(
        plumbing.NewBranchReferenceName("main"), currentBase)
    return r.gitRepo.Storer.SetReference(newMain)
}
```

### Cherry-Pick Detail

A cherry-pick of commit `C` onto base `B` produces a new commit `C'`:
1. Get the tree diff between `C.Parent` and `C` (what changed)
2. Apply that diff to the tree of `B`
3. If any file in the diff conflicts with `B`'s content → conflict
4. Write the merged tree as a new commit with `B` as parent

Conflict detection: a conflict exists when a file is modified in both `C`'s diff and `B`'s history since the common ancestor.

### Error Handling

```go
// ErrMergeConflict returned when auto-rebase finds a content conflict.
type ErrMergeConflict struct {
    TaskID           string
    ConflictingFiles []string
}

func (e *ErrMergeConflict) Error() string {
    return fmt.Sprintf("merge conflict on task/%s: conflicting files: %v",
        e.TaskID, e.ConflictingFiles)
}

// Callers check:
if err := repo.MergeBranch(ctx, taskID); err != nil {
    var conflictErr *ErrMergeConflict
    if errors.As(err, &conflictErr) {
        // Route conflict back to agent for resolution
        // conflictErr.ConflictingFiles lists what needs resolution
    }
    return err
}
```

### Agent Retry Flow (after conflict)

1. `MergeBranch` returns `*ErrMergeConflict` with file list
2. CodeValdCortex routes conflict details back to the responsible agent
3. Agent calls `WriteFile` to resolve each conflicting file on the task branch
4. Agent requests task completion again → `MergeBranch` retried
5. If `main` has not changed: fast-forward succeeds
6. If `main` has advanced again: auto-rebase attempted again

### Dependencies
- MVP-GIT-005 (fast-forward merge; this is the fallback path)
- MVP-GIT-004 (need multi-commit branches to test rebase)

### Tests

| Test | Approach |
|---|---|
| `TestMergeBranch_NeedsRebase_Success` | Write to main after branching; task commits rebase cleanly |
| `TestMergeBranch_NeedsRebase_Conflict` | Both main and task modify same file; `*ErrMergeConflict` returned |
| `TestMergeBranch_ConflictLeavesTaskClean` | After conflict, task branch is still at original HEAD |
| `TestMergeBranch_MultipleTaskCommits` | Rebase with 3+ commits on task branch |
| `TestMergeBranch_ConflictFiles` | `ConflictingFiles` contains exactly the conflicted paths |
| `TestMergeBranch_AgentRetry` | Conflict → agent resolves → successful merge on retry |

### Edge Cases & Constraints

- **Task branch with no new commits**: If all task commits are already in `main`'s history, this should be treated as already-merged (idempotent no-op)
- **Empty cherry-pick**: If a commit introduces no file changes (e.g., empty commit), apply as-is without conflict
- **Binary files**: Detect binary content and mark as conflict automatically (no merge heuristic for binaries)
- **Large number of commits**: Cherry-pick is O(n) in commit count — document this limitation; no optimisation required for MVP
- **Interrupted rebase**: All work on temporary structures; original task branch ref is only updated after full success
