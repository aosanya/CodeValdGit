# MVP - Minimum Viable Product Task Breakdown

## Task Overview
- **Objective**: Deliver CodeValdGit as a standalone gRPC microservice that provides real Git semantics for artifact versioning in CodeValdCortex, replacing the custom `internal/git/` implementation. CodeValdCortex communicates via generated gRPC client stubs rather than importing CodeValdGit as a Go module.
- **Success Criteria**: All 10 tasks (MVP-GIT-001 through MVP-GIT-010) implemented and tested; CodeValdGit deployed as a standalone gRPC microservice; legacy `internal/git/` deleted from CodeValdCortex by a separate integration effort
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

*~~MVP-GIT-008~~ ✅ complete — see `mvp_done.md`.*

---

## P1: gRPC Microservice Integration (IMPORTANT)

*CodeValdGit runs as a standalone gRPC microservice; CodeValdCortex communicates via generated client stubs instead of importing the library as a Go module dependency.*

| Task ID | Title | Description | Status | Priority | Effort | Skills | Dependencies | Details |
|---------|-------|-------------|--------|----------|--------|--------|--------------|------|
| MVP-GIT-009 | gRPC Service Proto & Codegen | Define `proto/codevaldgit/v1/service.proto` with all RPCs mapping the `RepoManager`/`Repo` interfaces. Define `MergeConflictInfo` detail message. Set up `buf` toolchain. Generate Go server + client stubs under `gen/go/codevaldgit/v1/`. Add `make proto` target. | 📋 Not Started | P1 | Medium | Go, Protobuf, gRPC, buf | ~~MVP-GIT-001~~ ✅ through ~~MVP-GIT-008~~ ✅ | [grpc-service.md](mvp-details/grpc-service.md) |
| MVP-GIT-010 | gRPC Server Implementation | Add `cmd/server/main.go` to start a gRPC listener. Implement `internal/grpcserver/server.go` wrapping `RepoManager`/`Repo`. Map all Go errors to gRPC status codes; pack `MergeConflictInfo` on conflict. Register `grpc.health.v1` health service. Graceful shutdown. `Dockerfile.server` for standalone deployment. | 📋 Not Started | P1 | Medium | Go, gRPC, Docker | MVP-GIT-009 | [grpc-service.md](mvp-details/grpc-service.md) |

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
- **Storage & Integration**: ~~MVP-GIT-008~~ ✅ complete
- **gRPC Microservice Integration**: 2 tasks remaining (MVP-GIT-009, MVP-GIT-010)

**Total P1**: 4 tasks (2 complete, 2 remaining)

**Grand Total Active Tasks**: 10 tasks (8 complete, 2 remaining)

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

**Phase 3 — gRPC Microservice Integration:**
9. MVP-GIT-009 — gRPC Service Proto & Codegen (after MVP-GIT-001–008 ✅)
10. MVP-GIT-010 — gRPC Server Implementation (after MVP-GIT-009)

---

## Done

_(Move tasks here as they complete — see `mvp_done.md`)_
