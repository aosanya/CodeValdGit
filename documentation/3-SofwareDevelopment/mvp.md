# MVP - Minimum Viable Product Task Breakdown

## Task Overview
- **Objective**: Deliver a complete Go library that provides real Git semantics for artifact versioning in CodeValdCortex, replacing the custom `internal/git/` implementation
- **Success Criteria**: All 9 tasks (MVP-GIT-001 through MVP-GIT-009) implemented and tested; CodeValdCortex imports `github.com/aosanya/CodeValdGit`; legacy `internal/git/` deleted; ArangoDB Git collections (`git_objects`, `git_refs`, `repositories`) dropped
- **Dependencies**: Go module infrastructure, go-git v5, ArangoDB connectivity

## Platform Documentation
- **Requirements**: [requirements.md](../1-SoftwareRequirements/requirements.md) - Functional and non-functional requirements, scope, open questions
- **Architecture**: [architecture.md](../2-SoftwareDesignAndArchitecture/architecture.md) - Core design decisions, storage backends, branching model, proposed API

## Documentation Structure
- **High-Level Overview**: This file (`mvp.md`) provides task tables, priorities, dependencies, and brief descriptions
- **Detailed Specifications**: Each task with detailed requirements is documented in `mvp-details/{TASK_SPEC}.md`
- **Reference Pattern**: Tasks reference their detail files using the format `See: mvp-details/{spec}.md`

## Workflow Integration

### Task Management Process
1. **Task Assignment**: Pick tasks based on priority (P0 first) and dependencies
2. **Implementation**: Update "Status" column as work progresses (Not Started → In Progress → Testing → Complete)
3. **Completion Process** (MANDATORY):
   - Create detailed coding session document in `coding_sessions/` using format: `{TaskID}_{description}.md`
   - Add completed task to summary table in `mvp_done.md` with completion date
   - Remove completed task from this active `mvp.md` file
   - Update any dependent task references using notation: `~~MVP-GIT-XXX~~ ✅` (strikethrough + checkmark)
   - Merge feature branch to main
4. **Dependencies**: Ensure prerequisite tasks are completed before starting dependent work

### Dependency Notation Convention
- **Active dependencies**: `MVP-GIT-XXX` (plain text)
- **Completed dependencies**: `~~MVP-GIT-XXX~~ ✅` (strikethrough + checkmark)
- **Multiple dependencies**: Comma-separated, e.g., `MVP-GIT-001, ~~MVP-GIT-002~~ ✅`
- **None**: Use `None` for tasks with no prerequisites

### Branch Management (MANDATORY)
```bash
# Create feature branch
git checkout -b feature/MVP-GIT-XXX_description

# Work, build validation, test
# ... development work ...

# Merge when complete and tested
git checkout main
git merge feature/MVP-GIT-XXX_description --no-ff
git branch -d feature/MVP-GIT-XXX_description
```

---

## Status Legend
- ✅ **Completed** - Task done, merged to main (see `mvp_done.md`)
- 🚀 **In Progress** - Currently being worked on
- 📋 **Not Started** - Ready to begin (dependencies met)
- ⏸️ **Blocked** - Waiting on dependencies
- ⚠️ **Deprecated** - Superseded by other work

---

## P0: Foundation (CRITICAL)

*~~MVP-GIT-001~~ ✅ and ~~MVP-GIT-002~~ ✅ complete — see `mvp_done.md`.*

---

## P0: Core Operations (CRITICAL)

*~~MVP-GIT-003~~ ✅ and ~~MVP-GIT-004~~ ✅ complete — see `mvp_done.md`.*

---

## P0: Merge Workflow (CRITICAL)

*~~MVP-GIT-005~~ ✅ and ~~MVP-GIT-006~~ ✅ complete — see `mvp_done.md`.*

---

## P1: History & Diff (IMPORTANT)

*~~MVP-GIT-007~~ ✅ complete — see `mvp_done.md`.*

---

## P1: Storage & Integration (IMPORTANT)

*ArangoDB backend for container-restart persistence and final wiring into CodeValdCortex.*

| Task ID | Title | Description | Status | Priority | Effort | Skills | Dependencies | Details |
|---------|-------|-------------|--------|----------|--------|--------|--------------|---------|
| MVP-GIT-008 | ArangoDB Storage Backend | Implement custom `storage.Storer` backed by ArangoDB: collections `git_objects` (blobs/trees/commits keyed by SHA), `git_refs` (branch & tag refs), `git_index` (staging area), `git_config` (per-repo config). Partitioned by `agencyID`. Working tree remains on local/in-memory `billy.Filesystem`. Repos survive container restarts without a PVC | 📋 Not Started | P1 | High | Go, go-git, ArangoDB | ~~MVP-GIT-001~~ ✅, ~~MVP-GIT-002~~ ✅ | [storage-backends.md](mvp-details/storage-backends.md) |
| MVP-GIT-009 | CodeValdCortex Integration | Add `github.com/aosanya/CodeValdGit` as Go module dependency in CodeValdCortex. Wire `RepoManager` into Agency and Task service constructors. Delete `internal/git/` packages (ops, storage, fileindex, models). Drop legacy ArangoDB Git collections (`git_objects`, `git_refs`, `repositories`). Full integration test suite passing | 📋 Not Started | P1 | Medium | Go, Backend Dev, Integration Testing | ~~MVP-GIT-006~~ ✅, ~~MVP-GIT-007~~ ✅, MVP-GIT-008 | [integration.md](mvp-details/integration.md) |

---

## Bugs and Issues

### Active Bugs

_(None)_

### Resolved Bugs

_(None yet)_

---

## Deprecated / Removed Tasks

_(None)_

---

## Task Summary by Priority

### P0 (Blocking — Must Complete First)
- **Foundation**: ~~2 tasks (MVP-GIT-001, MVP-GIT-002)~~ ✅ both complete
- **Core Operations**: ~~2 tasks (MVP-GIT-003, MVP-GIT-004)~~ ✅ both complete
- **Merge Workflow**: ~~2 tasks (MVP-GIT-005, MVP-GIT-006)~~ ✅ both complete

**Total P0**: 6 tasks ✅ ALL COMPLETE

### P1 (Important — Core Library Features)
- **History & Diff**: ~~1 task (MVP-GIT-007)~~ ✅ complete
- **Storage & Integration**: ~~MVP-GIT-008~~ ✅ complete, 1 task remaining (MVP-GIT-009)

**Total P1**: 3 tasks (2 complete, 1 remaining)

**Grand Total Active Tasks**: 9 tasks (8 complete, 1 remaining)

---

**Note**: This document contains only active and pending tasks. All completed tasks are moved to `mvp_done.md` to maintain a clean, actionable backlog.

Follow this sequence:

**Phase 1 — Foundation & Core:**
1. ~~MVP-GIT-001~~ ✅ — Library Scaffolding
2. ~~MVP-GIT-002~~ ✅ — Filesystem Repo Lifecycle
3. ~~MVP-GIT-003~~ ✅ — Branch-Per-Task Workflow
4. ~~MVP-GIT-004~~ ✅ — File Operations & Commit Attribution
5. ~~MVP-GIT-005~~ ✅ — Fast-Forward Merge
6. ~~MVP-GIT-006~~ ✅ — Auto-Rebase & Conflict Resolution

**Phase 2 — Read Access & Persistence:**
7. ~~MVP-GIT-007~~ ✅ — History & Diff
8. ~~MVP-GIT-008~~ ✅ — ArangoDB Storage Backend

**Phase 3 — Integration:**
9. MVP-GIT-009 — CodeValdCortex Integration (after MVP-GIT-006, MVP-GIT-007, MVP-GIT-008)

---

## Done

_(Move tasks here as they complete — see `mvp_done.md`)_
