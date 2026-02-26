# CodeValdCortex Integration

> ⚠️ **SUPERSEDED** — The original Go-module wiring approach described in this file has been replaced by the **gRPC Microservice Integration** design.
> See [grpc-service.md](grpc-service.md) for the active specifications covering:
> - **MVP-GIT-009**: gRPC Service Proto & Codegen
> - **MVP-GIT-010**: gRPC Server Implementation
>
> This file is retained as historical context for the original design decisions.

---

## MVP-GIT-009 — CodeValdCortex Integration

### Overview
Wire CodeValdGit into CodeValdCortex as a drop-in replacement for the custom `internal/git/` package. This task covers: adding CodeValdGit as a dependency, updating the CodeValdCortex lifecycle hooks (agency create/delete, task start/complete), deleting the replaced packages, and dropping the ArangoDB Git collections.

This is the final MVP task. All earlier tasks must be complete and tested before starting integration.

### Acceptance Criteria
- [ ] `go.mod` in CodeValdCortex references `github.com/aosanya/CodeValdGit`
- [ ] `internal/git/` is fully deleted from CodeValdCortex
- [ ] All compilation errors from the deletion are resolved
- [ ] `git_objects`, `git_refs`, `repositories` ArangoDB collections are no longer used (collections may be dropped via a migration script)
- [ ] All five CodeValdCortex lifecycle events call the correct CodeValdGit operations (see mapping table below)
- [ ] Existing integration tests for agent file output pass
- [ ] No regressions in CodeValdCortex's test suite

### Lifecycle Hook Mapping

| CodeValdCortex Event | Location | CodeValdGit Call |
|---|---|---|
| Agency created | `internal/agency/service.go` — `CreateAgency` | `RepoManager.InitRepo(agencyID)` |
| Agency deleted | `internal/agency/service.go` — `DeleteAgency` | `RepoManager.DeleteRepo(agencyID)` |
| Task started | `internal/task/service.go` — `StartTask` | `Repo.CreateBranch(taskID)` |
| Agent writes output | `internal/agent/` — file write handler | `Repo.WriteFile(taskID, path, content, agentID, message)` |
| Task completed | `internal/task/service.go` — `CompleteTask` | `Repo.MergeBranch(taskID)` → `Repo.DeleteBranch(taskID)` |

### Files to Delete in CodeValdCortex

After integration is verified, remove these files entirely:

```
internal/git/ops/operations.go         ← custom SHA-1 object engine
internal/git/storage/repository.go     ← ArangoDB Git object storage
internal/git/fileindex/service.go      ← ArangoDB file index service
internal/git/fileindex/repository.go   ← ArangoDB file index repository
internal/git/models/                   ← custom GitObject, GitTree, GitCommit models
```

> After deletion, run `go build ./...` to surface all import sites that need updating.

### Packages to Update in CodeValdCortex

| Package | Change Required |
|---|---|
| `internal/agency/` | Call `RepoManager.InitRepo` on create; `RepoManager.DeleteRepo` on delete |
| `internal/task/` | Call `Repo.CreateBranch` on task start; `Repo.MergeBranch` + `Repo.DeleteBranch` on complete |
| `internal/agent/` | Replace custom file write calls with `Repo.WriteFile` |
| `internal/handlers/` | Replace `internal/git` read calls with `Repo.ReadFile`, `Repo.ListDirectory`, `Repo.Log`, `Repo.Diff` |
| `cmd/main.go` | Inject `RepoManager` into service constructors |

### Dependency Injection Pattern

CodeValdCortex uses constructor injection. Add `codevaldgit.RepoManager` to the service layer:

```go
// In internal/agency/service.go
type Service struct {
    db          Database
    repoManager codevaldgit.RepoManager  // ← inject
    // ...
}

func NewService(db Database, rm codevaldgit.RepoManager) *Service {
    return &Service{db: db, repoManager: rm}
}

func (s *Service) CreateAgency(ctx context.Context, agency Agency) error {
    if err := s.db.InsertAgency(ctx, agency); err != nil {
        return err
    }
    // Initialise Git repo for the new agency
    return s.repoManager.InitRepo(ctx, agency.ID)
}

func (s *Service) DeleteAgency(ctx context.Context, agencyID string) error {
    if err := s.db.DeleteAgency(ctx, agencyID); err != nil {
        return err
    }
    // Archive the Git repo (not hard-deleted)
    return s.repoManager.DeleteRepo(ctx, agencyID)
}
```

### Task Service Integration

```go
// In internal/task/service.go
func (s *Service) StartTask(ctx context.Context, task Task) error {
    if err := s.db.InsertTask(ctx, task); err != nil {
        return err
    }
    repo, err := s.repoManager.OpenRepo(ctx, task.AgencyID)
    if err != nil {
        return fmt.Errorf("opening repo for agency %s: %w", task.AgencyID, err)
    }
    return repo.CreateBranch(ctx, task.ID)
}

func (s *Service) CompleteTask(ctx context.Context, taskID, agencyID string) error {
    repo, err := s.repoManager.OpenRepo(ctx, agencyID)
    if err != nil {
        return err
    }
    if err := repo.MergeBranch(ctx, taskID); err != nil {
        var conflictErr *codevaldgit.ErrMergeConflict
        if errors.As(err, &conflictErr) {
            // Route conflict back to agent
            return s.routeConflictToAgent(ctx, conflictErr)
        }
        return err
    }
    return repo.DeleteBranch(ctx, taskID)
}
```

### Merge Conflict Routing

When `CompleteTask` receives `*ErrMergeConflict`, CodeValdCortex must:

1. Mark the task as `conflict` state (new task state — add if not present)
2. Send an event to the responsible agent with the conflicting file list
3. Wait for the agent to resolve conflicts via `WriteFile`
4. Agent signals resolution → `CompleteTask` retried

```go
func (s *Service) routeConflictToAgent(ctx context.Context, err *codevaldgit.ErrMergeConflict) error {
    return s.events.Publish(ctx, events.MergeConflict{
        TaskID:           err.TaskID,
        ConflictingFiles: err.ConflictingFiles,
    })
}
```

### ArangoDB Collection Migration

After integration is complete, drop the legacy Git collections:

```javascript
// ArangoDB Foxx or shell script
db._drop("git_objects");
db._drop("git_refs");
db._drop("repositories");
// Note: git_index and git_config may not exist in the legacy schema
```

Create a migration script at `scripts/migrate-to-codevaldgit.sh`:

```bash
#!/bin/bash
# Drops legacy ArangoDB Git collections after CodeValdGit integration.
# Run ONLY after verifying CodeValdGit is working in production.
set -e

ARANGO_URL="${ARANGO_URL:-http://localhost:8529}"
DB="${ARANGO_DB:-cortex}"

for collection in git_objects git_refs repositories; do
    echo "Dropping $collection from $DB..."
    curl -s -X DELETE "${ARANGO_URL}/_db/${DB}/_api/collection/${collection}" \
        -u "${ARANGO_USER}:${ARANGO_PASSWORD}" | jq .
done
echo "Done."
```

### Wiring in cmd/main.go

```go
// In cmd/main.go — add after existing service construction

// Choose backend based on config
var repoManager codevaldgit.RepoManager
switch cfg.Git.Backend {
case "arangodb":
    repoManager, err = codevaldgit.NewRepoManager(codevaldgit.RepoManagerConfig{
        Backend: codevaldgit.BackendArangoDB,
        ArangoDB: &codevaldgit.ArangoConfig{
            Database: arangoDB, // existing ArangoDB connection
        },
    })
default: // "filesystem"
    repoManager, err = codevaldgit.NewRepoManager(codevaldgit.RepoManagerConfig{
        Backend:     codevaldgit.BackendFilesystem,
        BasePath:    cfg.Git.BasePath,
        ArchivePath: cfg.Git.ArchivePath,
    })
}
if err != nil {
    log.Fatalf("initialising git repo manager: %v", err)
}

// Inject into services
agencyService := agency.NewService(agencyDB, repoManager)
taskService   := task.NewService(taskDB, repoManager)
```

### config.yaml Changes

```yaml
# Add to CodeValdCortex config.yaml
git:
  backend: filesystem       # "filesystem" | "arangodb"
  base_path: /data/repos    # for filesystem backend
  archive_path: /data/archive
  # For arangodb backend — uses existing arangodb connection
```

### Integration Test Plan

| Test | Coverage |
|---|---|
| `TestAgencyCreate_InitialisesRepo` | After `CreateAgency`, `OpenRepo(agencyID)` succeeds |
| `TestAgencyDelete_ArchivesRepo` | After `DeleteAgency`, archive path contains repo |
| `TestTaskStart_CreatesBranch` | After `StartTask`, branch `task/{id}` exists |
| `TestAgentWriteFile_CommitsToTaskBranch` | File appears on task branch, not main |
| `TestTaskComplete_MergesAndDeletesBranch` | After `CompleteTask`, file on main; task branch deleted |
| `TestTaskComplete_ConflictRouted` | Conflict event published with file list |
| `TestEndToEnd_AgencyLifecycle` | Full lifecycle: create agency → start task → write → complete → delete |

### Dependencies

All of:
- MVP-GIT-001 through MVP-GIT-007 (complete library)
- MVP-GIT-008 (ArangoDB backend — if using ArangoDB backend in production)

### Rollout Plan

1. **Feature flag**: Gate CodeValdGit behind `GIT_BACKEND=codevaldgit` env var initially
2. **Parallel run** (optional): Run both `internal/git/` and CodeValdGit for a short period; compare outputs
3. **Cut over**: Set `GIT_BACKEND=codevaldgit` in production
4. **Cleanup**: Delete `internal/git/` packages and drop legacy collections
5. **Remove feature flag**: Clean up the conditional after validation

### Known Risks

| Risk | Mitigation |
|---|---|
| Data in legacy `git_objects` not migrated | No migration needed — ArangoDB Git collections are standalone versioning; existing artifact history in ArangoDB is superseded by CodeValdGit from integration date |
| Concurrent writes from multiple CodeValdCortex replicas | Each `Repo` call is per-task; task branches are isolated; main is only updated at task completion — low collision risk for MVP |
| go-git rebase edge cases | Covered by MVP-GIT-006 tests; `ErrMergeConflict` provides a safe fallback path |
| Volume provisioning for filesystem backend | Document that `/data/repos` must be a persistent volume in Kubernetes deployments |
