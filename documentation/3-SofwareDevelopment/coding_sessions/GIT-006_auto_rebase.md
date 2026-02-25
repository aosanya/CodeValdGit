# GIT-006: Auto-Rebase & Conflict Resolution

**Date**: 2026-02-25
**Branch**: `feature/GIT-006_auto_rebase`
**Task**: MVP-GIT-006 — Auto-Rebase & Conflict Resolution
**FR**: FR-006 (Merge Conflict Resolution — fallback rebase path)

---

## Summary

Replaced the `rebaseAndMerge` stub in `repo.go` with a full manual cherry-pick rebase implementation. Since go-git v5 has no native rebase, each commit on the task branch (since the common ancestor with main) is replayed onto the current main tip using pure plumbing — tree diffing, conflict detection, new tree construction, and new commit writing.

---

## Files Changed

| File | Change |
|------|--------|
| `repo.go` | Replaced `rebaseAndMerge` stub with full implementation; added `commitsSinceAncestor`, `cherryPick`, `treeToFileMap`, `buildTree`, `writeNewCommit`; added `repoFileEntry` type; added imports `sort`, `filemode`, `merkletrie` |
| `merge_test.go` | Added `advanceMain` helper + 6 new rebase/conflict tests |

---

## Implementation

### `rebaseAndMerge` (repo.go)

Orchestrates the rebase:
1. Calls `commitsSinceAncestor(taskTip, mainTip)` to collect task-only commits, oldest-first
2. Cherry-picks each commit in order; respects context cancellation
3. On conflict: returns `*ErrMergeConflict` immediately; task branch ref is **not mutated**
4. On success: updates task branch ref to rebased tip, then fast-forwards main

### `commitsSinceAncestor` (repo.go)

Walks `git.Log(from: tip)` (newest-first), collects commits until `ancestor` is found, then reverses the slice for oldest-first cherry-pick replay.

### `cherryPick` (repo.go)

Applies the diff from `src.Parent → src` onto `base`:

1. Gets `baseTree`, `srcParentTree`, `srcTree` from the storer
2. Calls `object.DiffTree(srcParent, src)` to enumerate changes
3. Flattens `baseTree` and `srcParentTree` to `map[string]repoFileEntry` via `treeToFileMap`
4. Applies each change with conflict detection:
   - **Insert**: conflict if base has the path with different content
   - **Modify**: conflict if base changed the file since srcParent (hash mismatch)
   - **Delete**: conflict if base changed the file since srcParent
5. If conflicts → returns `(ZeroHash, conflictPaths, nil)` without writing anything
6. If clean → calls `buildTree` then `writeNewCommit`, returns new commit hash

### `buildTree` (repo.go)

Recursively builds git tree objects from a flat `map[string]repoFileEntry`:
- Groups paths by first component; files at root level → `TreeEntry{Mode: file mode}`
- Subdirectory files → recursive `buildTree` call → `TreeEntry{Mode: filemode.Dir}`
- Sorts entries by name (git requirement)
- Encodes and stores via `r.git.Storer.SetEncodedObject`

### `writeNewCommit` (repo.go)

Writes a new `object.Commit` copying `src.Author` and `src.Message`, with `parent` as the sole parent and `treeHash` as the tree. Committer timestamp is `time.Now().UTC()`.

### Conflict detection rules

| Change type | Conflict condition |
|-------------|-------------------|
| Insert (new file) | Base already has that path with a **different** blob hash |
| Modify (edit file) | Base has a **different** blob hash than srcParent for that path |
| Delete (remove file) | Base has a **different** blob hash than srcParent for that path |

---

## Tests

| Test | Result |
|------|--------|
| `TestMergeBranch_NeedsRebase_Success` | Different files on task and main — rebase succeeds, both files on main ✅ |
| `TestMergeBranch_NeedsRebase_Conflict` | Same path modified by both — `*ErrMergeConflict` returned ✅ |
| `TestMergeBranch_ConflictLeavesTaskClean` | Task branch HEAD unchanged after conflict ✅ |
| `TestMergeBranch_MultipleTaskCommits` | 3 commits on task branch all rebased onto main ✅ |
| `TestMergeBranch_ConflictFiles` | `ConflictingFiles` contains exactly `["conflict.txt"]` ✅ |
| `TestMergeBranch_AgentRetry` | Conflict → new task branch from current main → fast-forward success ✅ |

Plus all 4 MVP-GIT-005 fast-forward tests still pass (48 total).

---

## Build Validation

```
go build ./...         → OK
go test -v -race ./... → 48 tests PASS (0 failures)
go vet ./...           → 0 issues
```

---

## Design Notes

- **No worktree mutation**: all cherry-pick operations use the go-git object storer directly (`storer.NewEncodedObject`, `storer.SetEncodedObject`). The working tree filesystem is never touched during rebase.
- **O(n·d) tree rebuild**: `buildTree` is O(n) files × O(d) depth — acceptable for MVP; noted in godoc.
- **Context cancellation**: the cherry-pick loop checks `ctx.Err()` between commits.
- **Binary file conflicts**: detected via hash mismatch (same rule as text files) — no diff heuristic needed.

---

## Pre-Checklist

- [x] FR-006 and architecture sections re-read
- [x] Feature branch created: `feature/GIT-006_auto_rebase`
- [x] Existing files checked — `ErrMergeConflict` already in `types.go`
- [x] Files modified: `repo.go` only (+ `merge_test.go`)
- [x] Todo list tracked and completed

---

## Next Task

**MVP-GIT-007** — History & Diff — UI Read Access (depends on ~~MVP-GIT-004~~ ✅)
