# Problem Definition

## The Problem

[CodeValdCortex](../../../CodeValdCortex/README.md) is an enterprise multi-agent AI orchestration platform. AI agents inside CodeValdCortex produce **artifacts** — code files, Markdown reports, YAML configs, and any other file type — as the outputs of tasks.

Before CodeValdGit, CodeValdCortex managed these artifacts using a **custom hand-rolled Git engine** in `internal/git/` backed by ArangoDB:

| Component | Problem |
|---|---|
| `internal/git/ops/` | Custom SHA-1 object engine — reinvented Git from scratch, bug-prone |
| `internal/git/storage/` | ArangoDB `git_objects` / `git_refs` collections — tightly coupled to ArangoDB |
| `internal/git/fileindex/` | ArangoDB-backed file index — separate system that could drift from Git state |
| `internal/git/models/` | Custom `GitObject`, `GitTree`, `GitCommit` structs — incompatible with standard tooling |

**Consequences:**
- Hard to maintain — any Git edge case required custom fixes
- No standard merge strategy — concurrent agent writes had no safe reconciliation
- No history browsing — UI couldn't navigate file history or compare branches
- No portability — artifacts were locked inside ArangoDB, unreadable by standard Git tools

---

## The Solution

Replace `internal/git/` with **CodeValdGit** — a proper Go library backed by [go-git](https://github.com/go-git/go-git):

- **Real Git semantics** — branches, commits, merges, diffs — all via go-git's pure-Go engine
- **Task isolation** — every agent task works on its own `task/{task-id}` branch
- **Auto-rebase** — when `main` has advanced, the library rebases the task branch automatically
- **Pluggable storage** — filesystem (default) or ArangoDB, injected by the caller
- **Portable artifacts** — every repo is a valid `.git` directory, readable by any Git client

---

## Scope of Replacement

| Replaced | Replacement |
|---|---|
| `internal/git/ops/` | go-git object engine |
| `internal/git/storage/` | `storage.Storer` (filesystem or ArangoDB) |
| `internal/git/fileindex/` | go-git tree walking |
| `internal/git/models/` | go-git plumbing types |
| ArangoDB `git_objects`, `git_refs`, `repositories` | Dropped entirely |
