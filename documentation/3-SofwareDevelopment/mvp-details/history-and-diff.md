# History & Diff

Topics: Commit Log · File History · Diff Between Refs · UI Read Access

---

## MVP-GIT-007 — History & Diff (UI Read Access)

### Overview
Implement the read-only history and diff operations on the `Repo` interface: `Log` (commit history for a file or path) and `Diff` (per-file changes between two refs). These provide the CodeValdCortex UI with a file browser that can navigate any point in history, view file change logs, and compare branches.

All operations in this task are **non-mutating and safe to call concurrently**.

### Acceptance Criteria

**Log**
- [ ] `Log(ctx, ref, path)` returns commits that touched `path`, ordered newest-first
- [ ] `ref` can be a branch name, tag, or commit SHA
- [ ] Returns `ErrRefNotFound` for unknown refs
- [ ] Each `Commit` in the result has `SHA`, `Author`, `Message`, and `Timestamp` populated
- [ ] An empty `path` (`""`) returns all commits reachable from `ref` (full repo log)
- [ ] A non-existent `path` with a valid `ref` returns an empty slice (not an error)
- [ ] Pagination is not required for MVP — return all matching commits

**Diff**
- [ ] `Diff(ctx, fromRef, toRef)` returns per-file changes between the two refs
- [ ] Each `FileDiff` has `Path`, `Operation` (`"add"` | `"modify"` | `"delete"`), and `Patch` (unified diff text)
- [ ] Returns `ErrRefNotFound` if either ref is unresolvable
- [ ] Binary files: `Operation` is set correctly but `Patch` is `""` (no text diff for binaries)
- [ ] Identical trees: returns empty slice (no error)
- [ ] `fromRef` and `toRef` may be the same — returns empty slice

### Log Implementation

```go
func (r *repo) Log(ctx context.Context, ref, path string) ([]Commit, error) {
    hash, err := r.resolveRef(ref)
    if err != nil {
        return nil, ErrRefNotFound
    }

    opts := &git.LogOptions{From: hash}
    if path != "" {
        opts.PathFilter = func(p string) bool {
            return p == path || strings.HasPrefix(p, path+"/")
        }
        opts.FileName = &path  // go-git v5.12+ supports FileName filter
    }

    iter, err := r.gitRepo.Log(opts)
    if err != nil {
        return nil, err
    }
    defer iter.Close()

    var commits []Commit
    err = iter.ForEach(func(c *object.Commit) error {
        commits = append(commits, Commit{
            SHA:       c.Hash.String(),
            Author:    c.Author.Name,
            Message:   c.Message,
            Timestamp: c.Author.When,
        })
        return nil
    })
    return commits, err
}
```

> **go-git note**: The `FileName` field in `LogOptions` was added in go-git v5.12.0. For earlier versions, filtering by path requires manually walking the iterator and comparing each commit's tree diff against the previous commit.

#### Path Filtering (fallback for pre-v5.12 go-git)

If `FileName` filter is unavailable, implement manually:

```go
// filterByPath returns true if commit c modified the given path
// compared to its first parent.
func commitTouchedPath(r *git.Repository, c *object.Commit, path string) (bool, error) {
    if c.NumParents() == 0 {
        // Root commit — check if path exists in tree
        tree, _ := c.Tree()
        _, err := tree.FindEntry(path)
        return err == nil, nil
    }

    parent, err := c.Parent(0)
    if err != nil {
        return false, err
    }

    changes, err := parent.Tree(); ...
    // Walk changes and check for path
}
```

### Diff Implementation

```go
func (r *repo) Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error) {
    fromHash, err := r.resolveRef(fromRef)
    if err != nil {
        return nil, ErrRefNotFound
    }
    toHash, err := r.resolveRef(toRef)
    if err != nil {
        return nil, ErrRefNotFound
    }

    // Same ref → no diff
    if fromHash == toHash {
        return nil, nil
    }

    fromCommit, err := r.gitRepo.CommitObject(fromHash)
    if err != nil {
        return nil, ErrRefNotFound
    }
    toCommit, err := r.gitRepo.CommitObject(toHash)
    if err != nil {
        return nil, ErrRefNotFound
    }

    fromTree, err := fromCommit.Tree()
    if err != nil {
        return nil, err
    }
    toTree, err := toCommit.Tree()
    if err != nil {
        return nil, err
    }

    changes, err := fromTree.Diff(toTree)
    if err != nil {
        return nil, err
    }

    var diffs []FileDiff
    for _, change := range changes {
        patch, err := change.Patch()
        if err != nil {
            return nil, err
        }

        op := operationFromAction(change.Action())
        path := changePath(change)

        patchText := ""
        if !isBinaryPatch(patch) {
            patchText = patch.String()
        }

        diffs = append(diffs, FileDiff{
            Path:      path,
            Operation: op,
            Patch:     patchText,
        })
    }
    return diffs, nil
}

func operationFromAction(a merkletrie.Action) string {
    switch a {
    case merkletrie.Insert:
        return "add"
    case merkletrie.Delete:
        return "delete"
    default:
        return "modify"
    }
}
```

### UI Integration Points

The CodeValdCortex UI uses these operations at the following access points:

| UI Feature | Operation | Parameters |
|---|---|---|
| File browser (current) | `ListDirectory` | `ref="main"`, `path=""` |
| File browser (historical) | `ListDirectory` | `ref="{commit-sha}"`, `path=""` |
| View file content | `ReadFile` | `ref="main"`, `path="{file}"` |
| View file at historical commit | `ReadFile` | `ref="{sha}"`, `path="{file}"` |
| File commit history | `Log` | `ref="main"`, `path="{file}"` |
| Full repo history | `Log` | `ref="main"`, `path=""` |
| Branch vs main diff | `Diff` | `fromRef="main"`, `toRef="task/{id}"` |
| Compare two commits | `Diff` | `fromRef="{sha-a}"`, `toRef="{sha-b}"` |

### Commit Model

```go
// Commit is a summary of a single Git commit returned by Log.
type Commit struct {
    SHA       string    // Full 40-char SHA
    Author    string    // Author name (agent ID or human user)
    Message   string    // Full commit message
    Timestamp time.Time // Author timestamp (UTC)
}
```

Consumers needing the short SHA can truncate to 7 characters: `commit.SHA[:7]`.

### FileDiff Model

```go
// FileDiff describes changes to one file between two refs.
type FileDiff struct {
    Path      string // Relative file path
    Operation string // "add" | "modify" | "delete"
    Patch     string // Unified diff text; empty for binary files
}
```

`Patch` follows standard unified diff format:
```
--- a/reports/output.md
+++ b/reports/output.md
@@ -1,3 +1,4 @@
 # Report
-Old content
+Updated content
+Additional line
```

### Performance Considerations

- `Log` with `path` filtering walks the entire commit history — O(n) in commit count. For MVP this is acceptable; paginate if repos grow large.
- `Diff` between two distant refs computes a full tree diff — O(files changed). For MVP this is acceptable.
- Both operations are read-only; they can be called concurrently without locks.
- Result sets are returned fully in memory. For MVP, no streaming is required.

### Dependencies
- MVP-GIT-004 (file operations and commits must exist to have history)
- MVP-GIT-003 (branch refs needed for `Diff` between `main` and `task/...`)

### Tests

| Test | Approach |
|---|---|
| `TestLog_AllCommits` | Create 3 commits; `Log("main", "")` returns 3 in newest-first order |
| `TestLog_FilterByPath` | 3 commits touching different files; `Log("main", "file-a.md")` returns only commits that changed `file-a.md` |
| `TestLog_PathNoHistory` | Valid ref, path never touched → empty slice |
| `TestLog_RefNotFound` | Returns `ErrRefNotFound` |
| `TestLog_AtSHA` | `Log("{sha}", "")` starts from that commit |
| `TestDiff_AddFile` | `Operation == "add"`, `Patch` contains `+` lines |
| `TestDiff_ModifyFile` | `Operation == "modify"`, `Patch` contains `-` and `+` lines |
| `TestDiff_DeleteFile` | `Operation == "delete"`, `Patch` contains `-` lines |
| `TestDiff_BinaryFile` | `Patch == ""`, `Operation` correct |
| `TestDiff_SameRef` | Returns empty slice |
| `TestDiff_IdenticalTrees` | Returns empty slice |
| `TestDiff_RefNotFound` | Returns `ErrRefNotFound` for either bad ref |
| `TestDiff_BranchVsMain` | Diff `main` vs `task/{id}` shows task-branch changes |
| `TestConcurrentLog` | 5 goroutines call `Log` concurrently — no race condition |

### Edge Cases & Constraints

- **Initial commit (no parent)**: `Log` must handle the root commit gracefully — it has no parents, so diff-based path filtering falls back to checking tree presence
- **Merge commits**: Not generated by this library (auto-rebase produces linear history), but if encountered, `Log` should still traverse them
- **Very long commit messages**: Returned as-is — no truncation in this library
- **Timezone**: All timestamps stored as UTC in Git objects; returned as `time.Time` in UTC
- **Deleted files in diff**: When a file is deleted, `Path` should be the file's original path (from `fromRef`)
