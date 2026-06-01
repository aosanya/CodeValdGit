# BUG-09-020 — Task completes before all `git.file.write` events flush

**Status:** 📋 Open
**Severity:** High — committed branch is missing files the AI declared; compile passes incorrectly against an incomplete tree
**Owner:** CodeValdGit (Phase 1 — primary fix); CodeValdWork + CodeValdAI (Phase 2 — dispatcher counter); CodeValdFunctions (Phase 3 — compile-flutter defensive check)
**Estimated effort:** Phase 1 ~6h, Phase 2 ~6h, Phase 3 ~2h
**Source finding:** [`/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md`](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)

---

## Reproducer

1. Assign a multi-file task (e.g. MVP-SF-003 Farm Dashboard) to developer-01.
2. Wait until `task.status == TASK_STATUS_COMPLETED`.
3. Compare:
   - `curl ${BASE}/pubsub/${AGENCY}/events?topic=git.file.write&after_timestamp=${ASSIGN_TIME} | jq '.events | length'`
   - Same with `topic=git.file.written`
   - Files actually on the branch: `git clone ... && git ls-tree -r origin/<branch> --name-only | wc -l`
4. Expected: all three counts equal. Observed: file count on branch < written count.

## Evidence (2026-05-31)

**MVP-SF-003:**
- 5 × `git.file.write` events (active_crop, upcoming_task, weather_data, shared_resource, dashboard_notifier)
- 5 × `git.file.written` events
- Task → COMPLETED at 20:53:42
- Branch on disk: **only 2 files** (`shared_resource.dart`, `main.dart`)

**MVP-SF-001:**
- 8 × `git.file.written` events; 6 files on disk

## Root cause

`git.file.written` is published by [`internal/server/event_receiver.go:handleFileWrite`](../../../internal/server/event_receiver.go) **after** the entity-graph commit, but the entity-graph write and the bare-git ref store are not synchronised within the same write boundary:

1. AI emits `git.file.write` → handled by CodeValdGit
2. CodeValdGit creates Blob + Tree + Commit entities + emits `git.file.written` ✓
3. The go-git storer also writes to the bare-repo pack files **asynchronously** — this races
4. `work.todo.completed` fires (CodeValdAI publishes when its run terminates, independent of step 3)
5. compile-flutter clones via smart-HTTP, reads from step 3's bare repo, sees only what flushed in time

`git.file.written` is **not** an acknowledgement that smart-HTTP will serve the file. It only confirms the entity row exists.

## Concrete fix plan

### Phase 1 — CodeValdGit: make `git.file.written` actually mean "durable" (~6h, preferred)

1. In [`git_impl_fileops.go:WriteFile`](../../../git_impl_fileops.go), after `advanceBranchHead` succeeds, fsync the underlying bare-repo pack file (the `BareRepoStorer` we wrap exposes the file handle; go-git's `storage.Storer` has no generic Flush).
2. Only publish `git.file.written` after the bare-repo flush completes.
3. Verification: re-run the reproducer; counts must match.

### Phase 2 — CodeValdWork + CodeValdAI: make task completion wait for all writes (~6h)

4. In `CodeValdWork/internal/server/event_dispatcher.go` (the dispatcher that turns `ai.task.completed` into `work.todo.completed`), add a "pending write balance" counter per todo run:
   - Increment when a `git.file.write` is observed with the todo's run_id
   - Decrement when matching `git.file.written` arrives
   - Hold the `work.todo.completed` publish until the counter reaches 0 (30s timeout that publishes anyway + logs a warning if exceeded)
5. The `ai.task.completed` payload must carry the list of write IDs the todo emitted. Add `EmittedWrites []string` to `CodeValdAI/models.go` AgentRun and populate it from the action parser.

### Phase 3 — CodeValdFunctions: defensive read-side check (~2h, cheap belt-and-braces)

6. In `CodeValdFunctions/functions/compile-flutter`, after `git clone`, call `GET /pubsub/{agencyId}/events?topic=git.file.written&after_timestamp=<branch_create_time>` and compare paths to what's in tmpdir. Missing path → retry the clone once after 2s; if still missing, return `status=infrastructure-error` instead of `issues-found`.

## Verification once fixed

- Reproducer above: write count == written count == file count on branch
- Add /09-work-03 verdict line: `count(git.file.written) == count(git.file.write) == count(files on branch)`

## Dependencies on other gaps

- Phase 2 step 4 is much easier after BUG-09-023 (proto3 replace-all) is fixed — the dispatcher needs to PATCH the todo with `expected_writes` mid-flight without zeroing other fields.
- Once fixed, BUG-09-021 (imports for files never written) becomes easier to catch because compile-flutter sees the complete branch.
