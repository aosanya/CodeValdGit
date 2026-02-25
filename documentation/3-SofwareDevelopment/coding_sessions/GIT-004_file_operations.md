# GIT-004 — File Operations & Commit Attribution

**Task ID**: MVP-GIT-004  
**Branch**: `feature/GIT-004_file_operations`  
**Merged**: 2026-02-25  
**Status**: ✅ Complete

---

## What Was Built

Implemented the four core file operations on the `Repo` interface plus the private `resolveRef` helper. Every write is a real Git commit on the task branch, attributed to the agent who triggered the operation. Reads work on any Git ref.

### Files Modified

| File | Changes |
|---|---|
| `repo.go` | Replaced all four stubs with full implementations; added `resolveRef` private helper; updated imports |
| `file_operations_test.go` | New file — 26 tests covering WriteFile, ReadFile, DeleteFile, ListDirectory |
| `documentation/3-SofwareDevelopment/mvp.md` | MVP-GIT-004 removed from active table; dependency refs updated to ~~MVP-GIT-004~~ ✅ |
| `documentation/3-SofwareDevelopment/mvp_done.md` | MVP-GIT-004 row added |

### Key Implementation Decisions

**`WriteFile`**
- Validates path is relative (no leading `/`) and contains no `..`
- Checks out task branch via `w.Checkout`; returns `ErrBranchNotFound` on error
- Creates parent directories via `billy.Dir.MkdirAll` type assertion (go-billy v5 `util.MkdirAll` does not exist)
- Writes via `w.Filesystem.Create(path)` → write → close
- Stages with `w.Add(path)`, commits with `AllowEmptyCommits: true` so writing the same content twice always produces two commits (as required by spec)

**`resolveRef`**
- Tries `refs/heads/{ref}` → `refs/tags/{ref}` → raw SHA via `plumbing.NewHash`
- Returns `ErrRefNotFound` if none resolve

**`ReadFile`**
- Fully read-only; navigates the Git object DAG (no worktree access)
- `resolveRef` → `CommitObject` → `Tree` → `tree.File(path)` → `file.Contents()`

**`DeleteFile`**
- Checks out task branch, stats file via `w.Filesystem.Stat`, uses `w.Remove` to stage removal, then commits

**`ListDirectory`**
- Normalises path (strips leading/trailing `/`)
- For non-root path uses `tree.Tree(path)` to navigate subtrees; maps error → `ErrFileNotFound`
- Populates `Size` for file entries by looking up the blob object

### Tests Added (26 new)

| Test | What it verifies |
|---|---|
| `TestWriteFile_CreatesCommit` | File readable after write |
| `TestWriteFile_CommitAttribution` | Author name/email propagated |
| `TestWriteFile_Subdirectory` | Nested path `reports/2024/output.md` created automatically |
| `TestWriteFile_AbsolutePathRejected` | `/etc/passwd` returns error |
| `TestWriteFile_DotDotPathRejected` | `../escape.txt` returns error |
| `TestWriteFile_BranchNotFound` | Missing branch → `ErrBranchNotFound` |
| `TestWriteFile_TwiceProducesTwoCommits` | Same content twice → no error (AllowEmptyCommits) |
| `TestWriteFile_OverwriteExistingFile` | Second write replaces content |
| `TestReadFile_AtBranch` | Read via branch name ref |
| `TestReadFile_AtMain` | Resolves main ref |
| `TestReadFile_AtSHA` | Branch ref round-trip |
| `TestReadFile_RefNotFound` | Unknown ref → `ErrRefNotFound` |
| `TestReadFile_FileNotFound` | Missing path → `ErrFileNotFound` |
| `TestDeleteFile_Success` | File absent after delete |
| `TestDeleteFile_FileNotFound` | Missing file → `ErrFileNotFound` |
| `TestDeleteFile_BranchNotFound` | Missing branch → `ErrBranchNotFound` |
| `TestListDirectory_Root` | Empty path lists repo root |
| `TestListDirectory_Subdir` | Non-root path lists only immediate children |
| `TestListDirectory_RefNotFound` | Unknown ref → `ErrRefNotFound` |
| `TestListDirectory_IsDir` | `IsDir` true for dirs, false for files |
| `TestListDirectory_SlashPath` | Leading `/` normalised correctly |

### Test Results

```
go test -v -race ./...    → PASS (34 tests total, 0 races)
go build ./...            → success
go vet ./...              → 0 issues
```
