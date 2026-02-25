---
agent: agent
---

# Debug a CodeValdGit Issue

## How to Use This Prompt

When you encounter a bug in CodeValdGit, describe the failing behaviour and use the guidelines below to add targeted debug logging, isolate the cause, and clean up before merging.

## Common Failure Scenarios

### Scenario 1: `MergeBranch` Returns Unexpected Error
**Symptom**: `ErrMergeConflict` returned even when files don't conflict
**Cause**: Rebase cherry-pick order wrong, or worktree not reset after failed attempt
**Check**: Inspect `internal/rebase/` — ensure worktree is cleaned on error path

### Scenario 2: `WriteFile` Has No Effect
**Symptom**: File written, no error, but `ReadFile` returns old content
**Cause**: Commit not flushed to `storage.Storer`, or wrong branch ref resolved
**Check**: Confirm `Worktree.Commit()` is called and `storage.Storer` is not in-memory-only

### Scenario 3: `OpenRepo` Returns `ErrNotFound`
**Symptom**: Repo exists on disk but `OpenRepo` fails
**Cause**: `base_path` misconfigured, or `.git` dir missing for filesystem backend
**Check**: Print `filepath.Join(basePath, agencyID)` and verify directory exists

### Scenario 4: Context Cancellation Not Respected
**Symptom**: Operation hangs after caller cancels context
**Cause**: Missing `ctx.Err()` check in rebase cherry-pick loop
**Check**: Add `ctx.Err()` check at top of each loop iteration

### Scenario 5: ArangoDB Backend Objects Not Persisted
**Symptom**: Commits appear to succeed but are lost on restart
**Cause**: ArangoDB `storage.Storer` not flushing; check `SetEncodedObject` implementation
**Check**: Add log in `SetEncodedObject` to confirm it is being called

## Debug Print Guidelines

### Prefix Format
All debug prints MUST be prefixed with: `[TASK-ID]`

### Go
```go
log.Printf("[GIT-XXX] Function called: %s with args: %+v", functionName, args)
log.Printf("[GIT-XXX] State before: %+v", state)
log.Printf("[GIT-XXX] Error in operation: %v", err)
```
### Strategic Placement

Add debug prints at:

1. **Function Entry Points**
   - Log function name and key parameters
   - Example: `log.Printf("[GIT-XXX] CreateBranch called: taskID=%s", taskID)`

2. **State Changes**
   - Before and after critical state modifications
   - Example: `log.Printf("[GIT-XXX] Cherry-pick %d/%d: commit=%s", i, total, hash)`

3. **Conditional Branches**
   - Log which branch is taken and why
   - Example: `log.Printf("[GIT-XXX] Fast-forward possible=%v", canFF)`

4. **Loop Iterations** (for rebase cherry-pick loop)
   - Log iteration count and key variables
   - Example: `log.Printf("[GIT-XXX] Processing commit %d/%d: %s", i, total, hash)`

5. **Error Handling**
   - Log errors with context before returning
   - Example: `log.Printf("[GIT-XXX] MergeBranch failed: %v", err)`

6. **Return Statements** (for complex functions)
   - Log what is being returned
   - Example: `log.Printf("[GIT-XXX] MergeBranch result: conflict files=%v", files)`

### What NOT to Debug

Avoid adding debug prints to:
- Simple getters/setters
- Trivial utility functions
- Hot paths called thousands of times per operation
- Already well-instrumented code

### Debug Print Structure

Use descriptive messages that answer:
1. **WHERE**: Which function/block is executing
2. **WHAT**: What operation is happening
3. **VALUES**: Relevant variable values
4. **CONTEXT**: Why this matters (optional)

**Good Example:**
```go
log.Printf("[GIT-XXX] MergeBranch: taskID=%s canFF=%v commitCount=%d",
    taskID, canFF, len(commits))
```

**Bad Example:**
```go
fmt.Println("here") // No context, no task ID, not helpful
```

### Cleanup Instructions

Always add a comment above debug blocks:
```go
// TODO: Remove debug log for GIT-XXX after issue is resolved
log.Printf("[GIT-XXX] Debug info here")
```

## Execution Steps

1. **Identify Task ID** from branch or context
2. **Analyse the failing code path** in `repo.go`, `manager.go`, or `internal/rebase/`
3. **Select strategic points** where debug prints will be most valuable
4. **Add debug prints** with proper format and task ID prefix
5. **Run tests** and filter output: `go test ./... 2>&1 | grep GIT-XXX`
6. **Explain placement** briefly — why each print was added

## Output Format

After adding debug prints, provide:

```markdown
### Debug Prints Added for [TASK-ID]

**File**: `path/to/file.go`

**Locations**:
1. Line XX: Function entry — logs parameters
2. Line YY: State change — logs before/after values
3. Line ZZ: Conditional check — logs decision logic

**Usage**: Run `go test -v ./... 2>&1 | grep GIT-XXX` to see output.
```

## Example Request Handling

**User**: "Add debug prints to track why MergeBranch is returning ErrMergeConflict unexpectedly"

**Your Response**:
1. Check git branch → Extract task ID (e.g., GIT-006)
2. Trace `MergeBranch` → `internal/rebase/` → cherry-pick loop
3. Add prints at:
   - `MergeBranch` entry: log `taskID`, `main` HEAD SHA
   - Each cherry-pick iteration: log commit SHA and result
   - Conflict detection: log conflicting paths
4. Use format: `log.Printf("[GIT-006] ...")`
5. Explain what each print reveals

## Remember

- **Always** use task ID prefix
- **Be strategic** - don't over-instrument
- **Be descriptive** - logs should tell a story
- **Be consistent** - use same format throughout
- **Be removable** - add TODO comments for cleanup
