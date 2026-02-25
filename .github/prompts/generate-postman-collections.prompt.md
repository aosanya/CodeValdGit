---
agent: agent
---

# Research go-git API Usage

This prompt guides research into the go-git library to resolve specific implementation questions for CodeValdGit.

## Objective

Find accurate, version-specific go-git API usage for a given operation. CodeValdGit uses **go-git v5** (`github.com/go-git/go-git/v5`).

## Steps

### 1. Identify the Operation

State clearly what you need to accomplish:
- e.g., "Create a branch from the current HEAD of `main`"
- e.g., "Cherry-pick a commit onto a different branch"
- e.g., "Implement a custom `storage.Storer` backed by ArangoDB"

### 2. Check go-git Documentation

**Primary sources (in order)**:
1. [go-git GoDoc](https://pkg.go.dev/github.com/go-git/go-git/v5) — authoritative API reference
2. [go-git examples](https://github.com/go-git/go-git/tree/master/_examples) — runnable code samples
3. [go-git CHANGELOG](https://github.com/go-git/go-git/blob/master/CHANGELOG) — version-specific additions

**Key packages**:
| Package | Purpose |
|---|---|
| `github.com/go-git/go-git/v5` | Top-level `Repository`, `Worktree` |
| `github.com/go-git/go-git/v5/plumbing` | `Hash`, `ReferenceName`, `Reference` |
| `github.com/go-git/go-git/v5/plumbing/object` | `Commit`, `Tree`, `Blob` |
| `github.com/go-git/go-git/v5/storage` | `Storer` interface |
| `github.com/go-git/go-git/v5/storage/filesystem` | Filesystem `Storer` |
| `github.com/go-git/go-git/v5/storage/memory` | In-memory `Storer` (for tests) |
| `github.com/go-git/go-billy/v5` | `Filesystem` interface (worktree) |
| `github.com/go-git/go-billy/v5/memfs` | In-memory filesystem (for tests) |
| `github.com/go-git/go-billy/v5/osfs` | OS filesystem |

### 3. Known go-git v5 Constraints

| Operation | Status | Notes |
|---|---|---|
| Fast-forward merge | ✅ Supported | `MergeOptions{Strategy: FastForwardMerge}` (added v5.12.0) |
| Three-way merge | ❌ Not supported | Must implement manually or refuse |
| Rebase | ❌ Not supported | Must implement via cherry-pick loop using plumbing layer |
| Cherry-pick | 🔧 Plumbing only | Use `object.Commit`, `Worktree.Commit` manually |
| Shallow clone | ✅ Supported | `CloneOptions{Depth: N}` |

### 4. Rebase Implementation Pattern

Since go-git has no native rebase, the manual approach for `MergeBranch`:

```go
// 1. Walk task branch commits back to merge base with main
// 2. Reset task branch tip to current main HEAD
// 3. Cherry-pick each commit in order:
for _, commit := range commitsToRebase {
    // Apply commit's tree diff to worktree
    // Call worktree.Commit() with original author/message/time
}
// 4. Fast-forward merge task branch into main
```

### 5. Testing Pattern

Use in-memory backends for fast, hermetic unit tests:

```go
import (
    "github.com/go-git/go-git/v5/storage/memory"
    "github.com/go-git/go-billy/v5/memfs"
)

storer := memory.NewStorage()
fs := memfs.New()
repo, err := git.Init(storer, fs)
```

## Output Format

For each research question, provide:

```markdown
### [Operation Name]

**go-git version**: v5.x.x (or "all v5")
**Package**: `github.com/go-git/go-git/v5/...`

**Code example**:
```go
// Minimal working example
```

**Caveats**: Any known limitations or version requirements
**Source**: Link to GoDoc or go-git example file
```
