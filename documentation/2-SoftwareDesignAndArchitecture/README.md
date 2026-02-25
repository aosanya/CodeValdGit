# 2 — Software Design & Architecture

## Overview

This section captures the **how** — design decisions, data models, component architecture, and technical constraints for CodeValdGit.

---

## Index

| Document | Description |
|---|---|
| [architecture.md](architecture.md) | Core design decisions, storage backends, branching model, draft API interfaces, CodeValdCortex integration table |

---

## Key Design Decisions at a Glance

| Decision | Choice | Rationale |
|---|---|---|
| Git engine | go-git (pure Go) | No system `git` binary dependency; embeddable in Go services |
| Repo granularity | 1 repo per Agency | Mirrors CodeValdCortex's database-per-agency isolation |
| Agent write policy | Always on a branch, never `main` | Prevents concurrent agent writes from corrupting shared history |
| Branch naming | `task/{task-id}` | Short-lived, traceable back to CodeValdCortex task records |
| Merge strategy | Auto-merge on task completion | No human approval gate for now; policy layer can extend later |
| Storage backend | Pluggable via `storage.Storer` | Caller injects the storer — filesystem and ArangoDB are both valid |
| Worktree filesystem | Pluggable via `billy.Filesystem` | Both storage and worktree are independently injectable |
| Merge conflict handling | Manual cherry-pick rebase | go-git v5 only supports FastForwardMerge natively |

---

## Component Architecture

```
github.com/aosanya/CodeValdGit    ← root package (library entry point)
├── manager.go                    # RepoManager interface + filesystem implementation
├── repo.go                       # Repo interface + implementation
├── errors.go                     # Exported error types (ErrMergeConflict, etc.)
├── models.go                     # FileEntry, Commit, FileDiff value types
├── storage/
│   └── arangodb/                 # ArangoDB storage.Storer implementation
│       ├── objects.go            # Git object storage (blobs, trees, commits, tags)
│       ├── refs.go               # Branch and tag references
│       ├── index.go              # Staging area index
│       └── config.go             # Per-repo Git config
└── internal/
    └── rebase/                   # Manual cherry-pick rebase (go-git plumbing layer)
```
