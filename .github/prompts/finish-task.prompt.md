---
agent: agent
---

# Complete and Merge Current Task

Follow the **mandatory completion process** for CodeValdGit tasks:

## Completion Process (MANDATORY)

1. **Validate code quality**
   ```bash
   go build ./...           # Must succeed — no compilation errors
   go test -v -race ./...   # Must pass — all tests green, no races
   go vet ./...             # Must show 0 issues
   golangci-lint run ./...  # Must pass
   ```

2. **Remove all debug logs before merge (MANDATORY)**
   - Remove all `fmt.Printf`, `fmt.Println` debug statements
   - Remove all `log.Printf` / `log.Println` debug statements (keep production error logging only)
   - After removal: `go vet ./...` catches unused variables/imports
   - After removal: verify `go build ./...` still succeeds

   ```bash
   # Check for leftover debug output
   grep -r "fmt.Printf\|fmt.Println" . --include="*.go"
   grep -r "log.Printf.*GIT-\|log.Println.*GIT-" . --include="*.go"
   ```

3. **Verify library contract compliance**
   - [ ] All new exported symbols have godoc comments
   - [ ] All new exported methods accept `context.Context` as first argument
   - [ ] No hardcoded storage backends in root package
   - [ ] Errors are typed (`ErrMergeConflict`, not raw strings)
   - [ ] No file exceeds 500 lines
   - [ ] No use of `git` binary (pure go-git only)
   - [ ] Write operations only via task branches, never directly to `main`

4. **Update documentation if architecture changed**
   - If the implementation deviated from `documentation/2-SoftwareDesignAndArchitecture/architecture.md`, update it to reflect the actual design
   - If a new design decision was made, add it to the decision table in `documentation/2-SoftwareDesignAndArchitecture/architecture.md`
   - If an open question in `documentation/1-SoftwareRequirements/requirements.md` was resolved, update it
   - Update task status in `documentation/3-SofwareDevelopment/mvp.md` (🔲 → ✅)
   - Update task status in `documentation/3-SofwareDevelopment/mvp-details/README.md`

5. **Merge to main**
   ```bash
   # Final validation
   go build ./...
   go test -v -race ./...
   go vet ./...

   # Commit implementation
   git add .
   git commit -m "GIT-XXX: Implement [description]

   - Key implementation detail 1
   - Key implementation detail 2
   - Removed all debug logs
   - All tests pass with -race
   "

   # Merge to main
   git checkout main
   git merge feature/GIT-XXX_description --no-ff -m "Merge GIT-XXX: [description]"
   git branch -d feature/GIT-XXX_description
   ```

## Success Criteria

- ✅ `go build ./...` succeeds
- ✅ `go test -race ./...` passes — all tests green, no data races
- ✅ `go vet ./...` shows 0 issues
- ✅ All debug logs removed
- ✅ Library contract compliance verified (godoc, context, no hardcoded storage)
- ✅ Documentation updated if architecture changed
- ✅ Merged to `main` and feature branch deleted
