# BUG-09-020 — Task completes before all `git.file.write` events flush

**Status:** 🟢 Phase 1 fixed (2026-06-01) — Phases 2 + 3 remain open as belt-and-braces
**Severity:** High — committed branch is missing files the AI declared; compile passes incorrectly against an incomplete tree
**Owner:** CodeValdGit (Phase 1 ✅ done); CodeValdWork + CodeValdAI (Phase 2 — dispatcher counter); CodeValdFunctions (Phase 3 — compile-flutter defensive check)
**Estimated effort:** Phase 1 ~6h ✅ done, Phase 2 ~6h, Phase 3 ~2h
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

## Root cause (corrected 2026-06-01)

The original write-up assumed an asynchronous bare-repo pack-file flush. That premise was wrong: the production server wires [`gitarangodb.NewArangoStorerBackend`](../../../storage/arangodb/) as the only storer — there is no filesystem bare repo, no pack file, no asynchronous flush. Every git object is an ArangoDB entity written synchronously inside [`WriteFile`](../../../git_impl_fileops.go).

The actual race is in CodeValdGit itself:

1. AI emits N `git.file.write` events for the same branch.
2. [`event_receiver.go:NotifyEvent`](../../../internal/server/event_receiver.go) fans out each as `go s.handleFileWrite(...)` — fully parallel goroutines, no per-branch serialisation.
3. Each goroutine calls [`WriteFile`](../../../git_impl_fileops.go), which:
   - reads `branch.HeadCommitID` (all N goroutines see the *same* parent)
   - builds a tree = parent's files + its one new file
   - creates a Commit pointing at that shared parent (sibling commits, not a chain)
   - calls `advanceBranchHead(branchID, newCommitID, "")` — empty `expectedHeadCommitID`, **no CAS guard**
4. The N unsynchronised `advanceBranchHead` calls race; the branch tip ends up at whichever commit ran last. The other N-1 commits exist as orphan objects but the branch ref doesn't reference them.
5. `git.file.written` is published from every goroutine regardless — so the event count is correct but the branch contents are not.
6. compile-flutter clones via smart-HTTP and sees only whatever the last-writer commit captured.

This matches the evidence exactly: 5 writes → 2 files on branch (two writes happened to chain by luck of the scheduler; three were overwritten).

## Concrete fix plan

### Phase 1 — CodeValdGit: serialise concurrent WriteFile calls (~1h, done 2026-06-01)

1. ✅ Wrap [`WriteFile`](../../../git_impl_fileops.go) in the existing per-agency [`RefLocker.WithMergeLock`](../../../git.go) (same primitive already used by `MergeBranch`). Body extracted to `writeFileLocked`. Concurrent goroutines now serialise on the same per-agency mutex, so each write sees the previous write's commit as its parent and chains correctly.
2. ✅ Concurrency regression: [`TestBUG09020_ConcurrentWriteFilesAllLand`](../../../git_concurrency_test.go) fires 10 concurrent `WriteFile` calls on the same branch and asserts all 10 files are reachable from the branch HEAD via `ReadFile`. Passes with the lock; without the lock, would fail with only the last-writer's file present.
3. Trade-off: per-agency lock means writes to different branches within the same agency now serialise. Per CLAUDE.md "one running instance = one agency", contention is bounded by the AI's per-agency write rate. Finer-grained per-branch locking can be added later if measured contention demands it.

### Phase 2 — CodeValdWork + CodeValdAI: make task completion wait for all writes (~6h)

4. In `CodeValdWork/internal/server/event_dispatcher.go` (the dispatcher that turns `ai.task.completed` into `work.todo.completed`), add a "pending write balance" counter per todo run:
   - Increment when a `git.file.write` is observed with the todo's run_id
   - Decrement when matching `git.file.written` arrives
   - Hold the `work.todo.completed` publish until the counter reaches 0 (30s timeout that publishes anyway + logs a warning if exceeded)
5. The `ai.task.completed` payload must carry the list of write IDs the todo emitted. Add `EmittedWrites []string` to `CodeValdAI/models.go` AgentRun and populate it from the action parser.

### Phase 3 — CodeValdFunctions: defensive read-side check (~2h, cheap belt-and-braces)

6. In `CodeValdFunctions/functions/compile-flutter`, after `git clone`, call `GET /pubsub/{agencyId}/events?topic=git.file.written&after_timestamp=<branch_create_time>` and compare paths to what's in tmpdir. Missing path → retry the clone once after 2s; if still missing, return `status=infrastructure-error` instead of `issues-found`.

## Verification

- ✅ Unit-level: [`TestBUG09020_ConcurrentWriteFilesAllLand`](../../../git_concurrency_test.go) passes with `-race`.
- ⏳ End-to-end: re-run the MVP-SF-003 reproducer to confirm `count(git.file.written) == count(git.file.write) == count(files on branch)`. Add as a /09-work-03 verdict line.

## Dependencies on other gaps

- Phase 2 step 4 is much easier after BUG-09-023 (proto3 replace-all) is fixed — the dispatcher needs to PATCH the todo with `expected_writes` mid-flight without zeroing other fields.
- Once fixed, BUG-09-021 (imports for files never written) becomes easier to catch because compile-flutter sees the complete branch.
