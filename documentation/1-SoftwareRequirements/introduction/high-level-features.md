# High-Level Features

## Feature Overview

CodeValdGit provides the following top-level capabilities to CodeValdCortex:

---

### 1. Repository Lifecycle Management
- **Create** a Git repository for an Agency on demand
- **Open** an existing repository by Agency ID
- **Archive** a repository when an Agency is deleted (non-destructive; repo moved to archive path)
- **Purge** an archived repository permanently (operator-explicit hard delete)

### 2. Branch-Per-Task Workflow
- Every agent write happens on a **dedicated task branch** (`task/{task-id}`)
- Agents are **never allowed to commit directly to `main`**
- Task branches are created from the current `main` HEAD
- Branches are automatically deleted after a successful merge

### 3. File Operations
- **Write** any file type (text or binary) to a task branch as a Git commit
- **Read** file content at any ref (branch, tag, or commit SHA)
- **Delete** a file from a task branch as a Git commit
- **List** directory contents at any ref — enables historical file browsing

### 4. Merge & Conflict Resolution
- **Fast-forward merge** when `main` has not advanced since the branch was created
- **Auto-rebase** the task branch onto the latest `main` when fast-forward is not possible (manual cherry-pick via go-git plumbing — go-git v5 has no native rebase)
- **Structured conflict error** returned to the caller when rebase encounters content conflicts; task branch left clean for agent retry

### 5. History & Diff (UI Read Access)
- **Commit log** for a file or path — ordered newest-first
- **Diff** between any two refs — per-file changes with unified diff text
- All history operations are read-only and safe for concurrent access

### 6. Pluggable Storage Backends
- **Filesystem** (default) — one real `.git` directory per Agency under a configurable base path
- **ArangoDB** — custom `storage.Storer` implementation; object DAG stored in collections; survives container restarts without a mounted volume

---

## What CodeValdGit Does NOT Do

| Out of Scope | Reason |
|---|---|
| Remote Git hosting (GitHub / GitLab push/pull) | Local repos only for MVP |
| Authentication / access control | Handled by CodeValdCortex's policy layer |
| Pull request UI | Merge is programmatic, not UI-driven |
| HTTP API | This is a Go library, not a service |
