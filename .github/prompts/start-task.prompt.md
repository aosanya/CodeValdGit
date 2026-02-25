---
agent: agent
---

# Start New Task

Follow the **mandatory task startup process** for CodeValdGit tasks:

## Task Startup Process (MANDATORY)

1. **Select the next task**
   - Check `documentation/3-SofwareDevelopment/mvp.md` for the task list and current status
   - Check `documentation/3-SofwareDevelopment/mvp-details/` for detailed specs per topic
   - Check `documentation/1-SoftwareRequirements/requirements.md` for unimplemented functional requirements (FR-001–FR-008)
   - Prefer foundational tasks (e.g., `RepoManager`, core `Repo` interface) before dependent ones

2. **Read the specification**
   - Re-read the relevant FR(s) in `documentation/1-SoftwareRequirements/requirements.md`
   - Re-read the corresponding section in `documentation/2-SoftwareDesignAndArchitecture/architecture.md`
   - Read the task spec in `documentation/3-SofwareDevelopment/mvp-details/{topic-file}.md`
   - Understand how the task fits into the two-interface design (`RepoManager`, `Repo`)
   - Note any go-git constraints (e.g., FR-006: manual rebase required — no native rebase in go-git v5)

3. **Create feature branch from `main`**
   ```bash
   cd /workspaces/CodeValdGit
   git checkout main
   git pull origin main
   git checkout -b feature/GIT-XXX_description
   ```
   Branch naming: `feature/GIT-XXX_description` (lowercase with underscores)

4. **Read project guidelines**
   - Review `.github/instructions/rules.instructions.md`
   - Key rules: interface-first, context propagation, no hardcoded storage, godoc on all exports

5. **Create a todo list**
   - Break the task into actionable steps
   - Use the manage_todo_list tool to track progress
   - Mark items in-progress and completed as you go

## Pre-Implementation Checklist

Before starting:
- [ ] Relevant FRs and architecture sections re-read
- [ ] Feature branch created: `feature/GIT-XXX_description`
- [ ] Existing files checked — no duplicate types in `models.go` or `errors.go`
- [ ] Understood which file(s) to modify (`manager.go`, `repo.go`, `errors.go`, `models.go`, `storage/arangodb/`, `internal/rebase/`)
- [ ] Todo list created for this task

## Development Standards

- **No hardcoded storage** — inject via `storage.Storer` and `billy.Filesystem`
- **Every exported symbol** must have a godoc comment
- **Every exported method** takes `context.Context` as the first argument
- **Write operations** must go through task branches (`task/{task-id}`), never directly to `main`
- **Errors** must be typed (`ErrMergeConflict`, `ErrNotFound`, etc.) — not raw `errors.New` strings for public errors

## Git Workflow

```bash
# Create feature branch
git checkout -b feature/GIT-XXX_description

# Regular commits during development
git add .
git commit -m "GIT-XXX: Descriptive message"

# Build validation before merge
go build ./...           # must succeed
go test -v -race ./...   # must pass
go vet ./...             # must show 0 issues
golangci-lint run ./...  # must pass

# Merge when complete
git checkout main
git merge feature/GIT-XXX_description --no-ff
git branch -d feature/GIT-XXX_description
```

## Success Criteria

- ✅ Relevant FR(s) and architecture doc reviewed
- ✅ Feature branch created from `main`
- ✅ Todo list created with implementation steps
- ✅ Ready to implement following library design rules
