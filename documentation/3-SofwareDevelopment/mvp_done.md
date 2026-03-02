# MVP Done — Completed Tasks

Completed tasks are removed from `mvp.md` and recorded here with their completion date.

| Task ID | Title | Completion Date | Branch | Coding Session |
|---------|-------|-----------------|--------|----------------|
| MVP-GIT-001 | Library Scaffolding | 2026-02-25 | `feature/GIT-001_library_scaffolding` | [GIT-001_library_scaffolding.md](coding_sessions/GIT-001_library_scaffolding.md) |
| MVP-GIT-002 | Filesystem Repo Lifecycle | 2026-02-25 | `feature/GIT-002_filesystem_repo_lifecycle` | [GIT-002_filesystem_repo_lifecycle.md](coding_sessions/GIT-002_filesystem_repo_lifecycle.md) |
| MVP-GIT-003 | Branch-Per-Task Workflow | 2026-02-25 | `feature/GIT-003_branch_per_task_workflow` | [GIT-003_branch_per_task_workflow.md](coding_sessions/GIT-003_branch_per_task_workflow.md) |
| MVP-GIT-004 | File Operations & Commit Attribution | 2026-02-25 | `feature/GIT-004_file_operations` | [GIT-004_file_operations.md](coding_sessions/GIT-004_file_operations.md) |
| MVP-GIT-005 | Fast-Forward Merge | 2026-02-25 | `feature/GIT-005_fast_forward_merge` | [GIT-005_fast_forward_merge.md](coding_sessions/GIT-005_fast_forward_merge.md) |
| MVP-GIT-006 | Auto-Rebase & Conflict Resolution | 2026-02-25 | `feature/GIT-006_auto_rebase` | [GIT-006_auto_rebase.md](coding_sessions/GIT-006_auto_rebase.md) |
| MVP-GIT-007 | History & Diff — UI Read Access | 2026-02-25 | `feature/GIT-007_history_and_diff` | [GIT-007_history_and_diff.md](coding_sessions/GIT-007_history_and_diff.md) |
| MVP-GIT-008 | ArangoDB Storage Backend | 2026-02-25 | `feature/GIT-008_arangodb_storage` | [GIT-008_arangodb_storage.md](coding_sessions/GIT-008_arangodb_storage.md) |
| MVP-GIT-009 | gRPC Service Proto & Codegen | 2026-02-26 | `feature/GIT-009_grpc_proto_codegen` | — |
| MVP-GIT-010 | gRPC Server Implementation | 2026-02-26 | `feature/GIT-010_grpc_server_implementation` | — |
| MVP-GIT-011 | Service-Driven Route Registration | 2026-02-27 | — | Routes declared in `internal/registrar/registrar.go` with `GrpcMethod` + `PathBindings`; Cross dynamic proxy handles forwarding |
| MVP-GIT-012 | Migrate shared infrastructure to CodeValdSharedLib | 2026-03-02 | `feature/MVP-GIT-012_sharedlib_migration` | Replaced local registrar/serverutil/arangoutil with SharedLib packages; removed local `gen/go/codevaldcross/`; all imports updated to `github.com/aosanya/CodeValdSharedLib` |
