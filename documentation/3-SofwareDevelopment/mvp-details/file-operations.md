# File Operations

Topics: File Write/Read/Delete · Directory Listing · Commit Attribution

---

## MVP-GIT-004 — File Operations & Commit Attribution

### Overview
Implement the core file operations on the `Repo` interface: `WriteFile`, `ReadFile`, `DeleteFile`, and `ListDirectory`. Every write is a Git commit on the task branch, attributed to the agent or user who triggered the operation. Reads work on any Git ref (branch name, tag, or commit SHA).

### Acceptance Criteria

**WriteFile**
- [ ] `WriteFile(ctx, taskID, path, content, author, message)` writes `content` to `path` on branch `task/{taskID}` as a new Git commit
- [ ] Returns `ErrBranchNotFound` if `task/{taskID}` does not exist
- [ ] Commit carries the provided `author` (agent ID) and `message`
- [ ] Commit timestamp is the current UTC time
- [ ] Subdirectories in `path` are created implicitly (no need to call `MkdirAll` separately)
- [ ] Writing the same content to the same path twice produces two commits (not deduplicated)
- [ ] `path` must be relative (no leading `/`); absolute paths are rejected

**ReadFile**
- [ ] `ReadFile(ctx, ref, path)` returns the file content at the given `ref`
- [ ] `ref` can be a branch name (`main`, `task/{taskID}`), a tag, or a full/short commit SHA
- [ ] Returns `ErrRefNotFound` if `ref` does not resolve
- [ ] Returns `ErrFileNotFound` if `path` does not exist at that ref
- [ ] Returns content as a plain `string`; binary files are returned as-is (base64 not applied)

**DeleteFile**
- [ ] `DeleteFile(ctx, taskID, path, author, message)` removes `path` on branch `task/{taskID}` as a new Git commit
- [ ] Returns `ErrBranchNotFound` if the branch does not exist
- [ ] Returns `ErrFileNotFound` if `path` does not exist on the branch
- [ ] Commit attribution follows the same rules as `WriteFile`

**ListDirectory**
- [ ] `ListDirectory(ctx, ref, path)` returns the immediate children of `path` at the given `ref`
- [ ] An empty `path` (`""` or `"/"`) lists the repo root
- [ ] Each `FileEntry` has `Name`, `Path`, `IsDir`, and `Size` populated
- [ ] Returns `ErrRefNotFound` for unknown refs
- [ ] Returns an empty slice (not an error) for an empty directory or a path that has no children
- [ ] Returns `ErrFileNotFound` if `path` does not exist at `ref` and is not the root

### Commit Attribution (FR-004)

Every commit created by this library **must** record:

| Field | Source | Example |
|---|---|---|
| Author name | `author` argument | `agent-task-abc-001` |
| Author email | Derived: `{author}@codevaldcortex.local` | `agent-task-abc-001@codevaldcortex.local` |
| Commit message | `message` argument | `feat: generate analysis report` |
| Timestamp | `time.Now().UTC()` | RFC3339 |
| Committer | Same as author for agent commits | |

The `author` string is the agent ID from CodeValdCortex. Callers should pass structured messages (e.g. `task/{task-id}: {description}`) for traceability.

### Implementation Notes

#### WriteFile

```go
func (r *repo) WriteFile(ctx context.Context,
    taskID, path, content, author, message string) error {

    if filepath.IsAbs(path) {
        return fmt.Errorf("path must be relative, got: %s", path)
    }

    w, err := r.gitRepo.Worktree()
    if err != nil {
        return err
    }

    // Checkout the task branch
    branchRef := plumbing.NewBranchReferenceName("task/" + taskID)
    if err := w.Checkout(&git.CheckoutOptions{Branch: branchRef}); err != nil {
        return ErrBranchNotFound
    }

    // Write file to working tree (creates parent dirs as needed)
    absPath := filepath.Join(r.workdir, filepath.FromSlash(path))
    if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
        return err
    }
    if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
        return err
    }

    // Stage and commit
    if _, err := w.Add(path); err != nil {
        return err
    }
    _, err = w.Commit(message, &git.CommitOptions{
        Author: &object.Signature{
            Name:  author,
            Email: author + "@codevaldcortex.local",
            When:  time.Now().UTC(),
        },
    })
    return err
}
```

#### ReadFile (any ref)

```go
func (r *repo) ReadFile(ctx context.Context, ref, path string) (string, error) {
    hash, err := r.resolveRef(ref)
    if err != nil {
        return "", ErrRefNotFound
    }

    commit, err := r.gitRepo.CommitObject(hash)
    if err != nil {
        return "", ErrRefNotFound
    }

    tree, err := commit.Tree()
    if err != nil {
        return "", err
    }

    file, err := tree.File(path)
    if err != nil {
        return "", ErrFileNotFound
    }

    return file.Contents()
}
```

#### resolveRef

```go
// resolveRef resolves a branch name, tag, or SHA to a commit hash.
func (r *repo) resolveRef(ref string) (plumbing.Hash, error) {
    // Try as a branch reference first
    if refObj, err := r.gitRepo.Reference(
        plumbing.NewBranchReferenceName(ref), true); err == nil {
        return refObj.Hash(), nil
    }

    // Try as a tag
    if refObj, err := r.gitRepo.Reference(
        plumbing.NewTagReferenceName(ref), true); err == nil {
        return refObj.Hash(), nil
    }

    // Try as a raw SHA (full or abbreviated)
    hash := plumbing.NewHash(ref)
    if hash != plumbing.ZeroHash {
        return hash, nil
    }

    return plumbing.ZeroHash, ErrRefNotFound
}
```

#### DeleteFile

```go
func (r *repo) DeleteFile(ctx context.Context,
    taskID, path, author, message string) error {

    w, err := r.gitRepo.Worktree()
    if err != nil {
        return err
    }

    branchRef := plumbing.NewBranchReferenceName("task/" + taskID)
    if err := w.Checkout(&git.CheckoutOptions{Branch: branchRef}); err != nil {
        return ErrBranchNotFound
    }

    // Check file exists before attempting removal
    if _, err := w.Filesystem.Stat(path); os.IsNotExist(err) {
        return ErrFileNotFound
    }

    if _, err := w.Remove(path); err != nil {
        return err
    }

    _, err = w.Commit(message, &git.CommitOptions{
        Author: &object.Signature{
            Name:  author,
            Email: author + "@codevaldcortex.local",
            When:  time.Now().UTC(),
        },
    })
    return err
}
```

#### ListDirectory

```go
func (r *repo) ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error) {
    hash, err := r.resolveRef(ref)
    if err != nil {
        return nil, ErrRefNotFound
    }

    commit, err := r.gitRepo.CommitObject(hash)
    if err != nil {
        return nil, ErrRefNotFound
    }

    tree, err := commit.Tree()
    if err != nil {
        return nil, err
    }

    // Walk to the requested subtree
    if path != "" && path != "/" {
        entry, err := tree.FindEntry(path)
        if err != nil {
            return nil, ErrFileNotFound
        }
        if !entry.Mode.IsFile() {
            subtree, err := r.gitRepo.TreeObject(entry.Hash)
            if err != nil {
                return nil, err
            }
            tree = subtree
        }
    }

    var entries []FileEntry
    for _, e := range tree.Entries {
        entries = append(entries, FileEntry{
            Name:  e.Name,
            Path:  filepath.Join(path, e.Name),
            IsDir: !e.Mode.IsFile(),
        })
    }
    return entries, nil
}
```

### Supported File Types (FR-002)

The library stores **any file type** without restriction:

| Type | Examples | Behaviour |
|---|---|---|
| Text | `.go`, `.md`, `.yaml`, `.json`, `.txt` | Stored as-is; meaningful diffs |
| Binary | `.png`, `.pdf`, `.zip`, `.wasm` | Stored as Git blobs; diff shows binary change |
| Empty | Zero-byte files | Supported; stored as empty blob |

There is no file size limit enforced by this library. Large binary blobs will impact repo performance — document this as a known limitation.

### Concurrency Model

`ReadFile` and `ListDirectory` are **read-only and safe to call concurrently** — they only walk the object DAG and do not touch the worktree.

`WriteFile`, `DeleteFile`, and `CreateBranch`/`DeleteBranch` **mutate the worktree** and must not be called concurrently for the same `taskID`. The caller (CodeValdCortex) is responsible for serialising writes per task. Multiple tasks writing to *different* branches concurrently is safe because each branch is independent.

> **Note on worktree**: go-git's filesystem backend uses a single shared worktree directory. Concurrent checkouts to different branches on the same `Repo` instance will corrupt the worktree. If CodeValdCortex needs concurrent multi-task writes on the same repo, use a per-task `Repo` instance (call `OpenRepo` per task) or switch to the in-memory/ArangoDB backend.

### Dependencies
- MVP-GIT-003 (branch must exist before writing)
- MVP-GIT-002 (repo must be openable)

### Tests

| Test | Approach |
|---|---|
| `TestWriteFile_CreatesCommit` | Write file; verify commit exists on branch with correct content |
| `TestWriteFile_CommitAttribution` | Author name and email match expected format |
| `TestWriteFile_Subdirectory` | Write to `reports/2024/output.md`; parent dirs created automatically |
| `TestWriteFile_AbsolutePathRejected` | `/etc/passwd` returns error |
| `TestWriteFile_BranchNotFound` | Returns `ErrBranchNotFound` |
| `TestWriteFile_BinaryFile` | Write PNG bytes; read back identical bytes |
| `TestReadFile_AtMain` | Read file from `main` ref |
| `TestReadFile_AtBranch` | Read file from `task/{id}` branch ref |
| `TestReadFile_AtSHA` | Read file at a specific commit SHA |
| `TestReadFile_RefNotFound` | Returns `ErrRefNotFound` |
| `TestReadFile_FileNotFound` | Returns `ErrFileNotFound` |
| `TestDeleteFile_Success` | File absent after delete; commit recorded |
| `TestDeleteFile_FileNotFound` | Returns `ErrFileNotFound` |
| `TestDeleteFile_BranchNotFound` | Returns `ErrBranchNotFound` |
| `TestListDirectory_Root` | Empty path lists repo root entries |
| `TestListDirectory_Subdir` | Lists only immediate children |
| `TestListDirectory_Empty` | Returns empty slice for empty dir |
| `TestListDirectory_RefNotFound` | Returns `ErrRefNotFound` |
| `TestListDirectory_IsDir` | `IsDir` is true for subdirectories |

### Edge Cases & Constraints

- **Path with `..`**: Reject paths that contain `..` to prevent worktree escapes
- **Overwrite existing file**: `WriteFile` on an existing path replaces the content — this is normal and expected
- **Write then read same branch**: `ReadFile(ctx, "task/{id}", path)` after `WriteFile(ctx, taskID, path, ...)` must return the newly written content
- **Case-sensitive paths**: Linux filesystems are case-sensitive; `Report.md` and `report.md` are distinct files
- **Unicode paths**: UTF-8 filenames must be preserved exactly
