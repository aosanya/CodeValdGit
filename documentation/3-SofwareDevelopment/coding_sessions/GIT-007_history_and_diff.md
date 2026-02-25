# GIT-007: History & Diff — UI Read Access

**Date**: 2026-02-25
**Branch**: `feature/GIT-007_history_and_diff`
**Task**: MVP-GIT-007 — History & Diff — UI Read Access
**FR**: FR-007 (History & Diff — UI Read Access)

---

## Summary

Replaced the `Log` and `Diff` stubs in `repo.go` with full, non-mutating implementations backed by go-git v5 plumbing. Both methods are safe for concurrent access, accept any ref (branch name, tag, or full SHA), and have no working-tree side effects.

---

## Files Changed

| File | Change |
|------|--------|
| `repo.go` | Replaced `Log` and `Diff` stubs with real implementations; fixed `CommitObject` error path in `Diff` to return `ErrRefNotFound` |
| `history_test.go` | New — 14 tests covering `Log` (6) and `Diff` (8) |

---

## Implementation

### `Log` (repo.go)

```
func (r *repo) Log(_ context.Context, ref, path string) ([]Commit, error)
```

1. Resolves `ref` via `resolveRef` — returns `ErrRefNotFound` for unknown refs
2. Builds `gogit.LogOptions{From: hash}`; when `path != ""` sets `opts.FileName = &path` to engage go-git's path-filter walk
3. Calls `r.git.Log(opts)` → iterator, iterates with `ForEach`
4. Maps each `*object.Commit` → `Commit{SHA, Author (committer name), Message, Timestamp.UTC()}`
5. Returns newest-first (go-git default); empty slice for valid ref + untouched path

### `Diff` (repo.go)

```
func (r *repo) Diff(_ context.Context, fromRef, toRef string) ([]FileDiff, error)
```

1. Resolves both refs; returns `ErrRefNotFound` for either bad ref (including when `CommitObject` fails on a hash derived from a non-hex string)
2. Same hash → returns `nil` immediately (identical tree)
3. `fromCommit.Tree()` and `toCommit.Tree()` → `*object.Tree`
4. `object.DiffTree(fromTree, toTree)` → `[]*object.Change`
5. For each change:
   - `ch.Action()` → `merkletrie.Action` (Insert / Modify / Delete)
   - Canonical path: `ch.To.Name`; for Delete: `ch.From.Name`
   - `ch.Patch()` → `*object.Patch`; iterates `FilePatches()`, skips binary files (`fp.IsBinary()` → `Patch: ""`)
   - Calls `diffOperation(action)` → `"add"` / `"modify"` / `"delete"`
6. Returns `[]FileDiff{Path, Operation, Patch}`

### `diffOperation` helper (repo.go)

Maps `merkletrie.Action` to the `FileDiff.Operation` string:
- `Insert` → `"add"`
- `Delete` → `"delete"`
- anything else → `"modify"`

---

## Tests — `history_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestLog_AllCommits` | 3 commits on main; `Log("main", "")` returns ≥3, newest-first |
| `TestLog_FilterByPath` | 3 commits touching different files; `Log("main", "file-a.md")` returns exactly 2 |
| `TestLog_PathNoHistory` | Valid ref, path never written → empty slice, no error |
| `TestLog_RefNotFound` | Unknown ref → `ErrRefNotFound` |
| `TestLog_AtSHA` | `Log("{sha}", "")` starts from that commit; descendants not included |
| `TestLog_CommitFields` | SHA is 40 chars, Author/Message/Timestamp all populated |
| `TestDiff_AddFile` | New file → `Operation=="add"`, `Patch` contains `"+hello"` |
| `TestDiff_ModifyFile` | Updated file → `Operation=="modify"`, Patch has `-original` and `+updated` |
| `TestDiff_DeleteFile` | Deleted file → `Operation=="delete"`, Patch contains `-bye` |
| `TestDiff_SameRef` | Same ref twice → empty slice, no error |
| `TestDiff_IdenticalTrees` | Same SHA twice → empty slice |
| `TestDiff_RefNotFound` | Either bad ref → `ErrRefNotFound` |
| `TestDiff_BranchVsMain` | Diff(main, task/branch) shows task file as `"add"` |
| `TestConcurrentLog` | 5 goroutines call `Log` simultaneously — no races or errors |

**Total tests**: 14 new (62 total across package)

---

## Key Design Notes

- **Binary detection**: `fp.IsBinary()` from go-git; Patch set to `""` for binary files
- **No working tree mutations**: `Diff` and `Log` are pure read operations via go-git plumbing
- **`resolveRef` edge case**: `plumbing.NewHash` accepts any string as hex; for strings with non-hex chars (e.g. `"bad-ref"`) it can return a non-zero hash — fixed by also catching the subsequent `CommitObject` failure and returning `ErrRefNotFound`
- **`LogOptions.FileName`**: Available in go-git v5.16.5 as `*string`; nil means no path filter

---

## Test Run

```
go test -v -race -count=1 ./...   # 62 PASS, 0 FAIL
go vet ./...                       # 0 issues
go build ./...                     # clean
```
