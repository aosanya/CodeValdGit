# CodeValdGit — Active Bug Backlog

## Overview

Bugs in scope for CodeValdGit. Mirrors the `mvp.md` / `mvp_done.md` / `mvp-details/` layout used for feature work.

- **Fixed bugs**: see [`bugs_done.md`](bugs_done.md)
- **Per-bug detail**: see [`bug-details/`](bug-details/)
- **Master cross-service queue**: [`../../../CodeValdCross/documentation/3-SofwareDevelopment/prioritization.md`](../../../CodeValdCross/documentation/3-SofwareDevelopment/prioritization.md)

## Workflow

### Completion Process (MANDATORY)
1. Implement and validate (`go build ./...`, `go vet ./...`, `go test -race ./...`)
2. Move the bug row from this file to `bugs_done.md`
3. Update the detail file's Status header to `✅ Fixed (YYYY-MM-DD)` and cite the commit / branch
4. Strike-through + ✅ the entry on the master prioritization.md
5. Merge feature branch to main

### Status Legend
- 📋 **Open** — not yet started or in triage
- 🚀 **In Progress** — actively being worked
- ⏸️ **Blocked** — waiting on a dependency
- ✅ **Fixed** — moved to `bugs_done.md` (do not list here)

---

## Active Bugs

| Bug ID | Title | Severity | Status | Phase Owner | Depends On |
|--------|-------|----------|--------|-------------|------------|
| [BUG-09-020](bug-details/BUG-09-020_filewrite_flush_race.md) | Task completes before all `git.file.write` events flush | High | 📋 Open | CodeValdGit (P1), CodeValdWork+AI (P2), CodeValdFunctions (P3) | — |

---

## BUG-09-020 — `git.file.write` flush race

**Status**: 📋 Open · **Severity**: High · **Estimated effort**: ~6h (Phase 1) + ~6h (Phase 2) + ~2h (Phase 3)

The bare-git ref store and the entity-graph commit happen in separate write boundaries. `git.file.written` is published when the entity row exists, but smart-HTTP may not yet serve the file. Multi-file tasks complete with a partial branch on disk; compile passes incorrectly. Source: QA finding /09 (2026-05-31), 5 written events vs 2 files on disk.

**Phase 1 — CodeValdGit (primary)**: fsync the bare-repo pack file in `git_impl_fileops.go:WriteFile` before publishing `git.file.written`.

**Phase 2 — CodeValdWork + CodeValdAI**: wait for write balance to drain before publishing `work.todo.completed`; add `EmittedWrites []string` to `AgentRun`.

**Phase 3 — CodeValdFunctions**: compile-flutter compares cloned tree against `git.file.written` event log; return `infrastructure-error` on mismatch.

See: [bug-details/BUG-09-020_filewrite_flush_race.md](bug-details/BUG-09-020_filewrite_flush_race.md)
