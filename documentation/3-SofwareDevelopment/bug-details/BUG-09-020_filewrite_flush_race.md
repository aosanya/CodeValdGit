# BUG-09-020 — Task completes before all `git.file.write` events flush

**Status:** 🟢 All three phases fixed (Phase 1 + Phase 2 + Phase 3 — 2026-06-01)
**Severity:** High — committed branch is missing files the AI declared; compile passes incorrectly against an incomplete tree
**Owner:** CodeValdGit (Phase 1 ✅ done); CodeValdWork + CodeValdAI (Phase 2 ✅ done); CodeValdFunctions (Phase 3 ✅ done)
**Estimated effort:** Phase 1 ~6h ✅ done, Phase 2 ~6h ✅ done, Phase 3 ~2h ✅ done
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

### Phase 2 — CodeValdWork + CodeValdAI: make task completion wait for all writes (~6h, done 2026-06-01)

4. ✅ CodeValdAI now publishes the ordered list of paths it emitted via `git.file.write` on the `ai.task.completed` payload. Implementation:
   - `EmittedWrites []string` added to [`AgentRun`](../../../../CodeValdAI/models.go) (persisted as a typed array property in the AgentRun schema, see [`schema.go`](../../../../CodeValdAI/schema.go)).
   - `EmittedWrites []string` added to [`TaskCompletedPayload`](../../../../CodeValdAI/events.go).
   - [`dispatchActions`](../../../../CodeValdAI/execute.go) now returns `(hasSubtasks, emittedWrites)` and `ExecuteRunStreaming` persists `emitted_writes` on the run entity before publishing `ai.task.completed`.
5. ✅ CodeValdWork's dispatcher now gates `work.todo.completed` on receipt of every matching `git.file.written` event. Implementation:
   - New [`writeTracker`](../../../../CodeValdWork/internal/server/event_writes.go) keyed by `run_id`; records expected paths and fires a deferred callback when every path has been confirmed.
   - [`handleAITaskStatus`](../../../../CodeValdWork/internal/server/event_dispatcher.go) defers its Todo status transition (and the `work.todo.completed` publish that flows from it) through the tracker whenever an `ai.task.completed` payload carries non-empty `EmittedWrites`.
   - `git.file.written` is consumed via a new [`handleFileWritten`](../../../../CodeValdWork/internal/server/event_writes.go) handler and added to the default `WORK_SUBSCRIBE_TOPICS` list in [`internal/config/config.go`](../../../../CodeValdWork/internal/config/config.go).
   - 30 s gate timeout (`writeGateTimeout`) fires the deferred callback anyway and logs a warning so a lost `git.file.written` cannot stall the pipeline.
   - Pre-arrival buffer covers the rare case where a `git.file.written` reaches the dispatcher before the `ai.task.completed` it answers.
   - Unit tests: [`TestWriteTracker_FiresAfterAllPathsConfirmed`](../../../../CodeValdWork/internal/server/event_writes_test.go), `TestWriteTracker_FiresOnTimeout`, `TestWriteTracker_PreArrivalBuffer`, `TestWriteTracker_OnlyFiresOnce` — pass under `-race`.

### Phase 3 — CodeValdFunctions: defensive read-side check (~2h, done 2026-06-01)

6. ✅ [`compile-flutter`](../../../../CodeValdFunctions/functions/compile-flutter) now cross-checks the cloned tree against the `git.file.written` event log immediately after `git clone`:
   - `pubsub_expected_paths()` queries `GET /pubsub/{agencyId}/events?topic=git.file.written&after_timestamp=<task.createdAt>` (falls back to `now - 1h` if the task lookup didn't yield `createdAt`) and collects the set of paths CodeValdGit reports as written on the analysed branch.
   - `missing_from_tree()` returns the subset of those paths absent from `tmpdir`.
   - On missing paths: sleep 2 s, re-clone, re-check. If still missing, emit `status=infrastructure-error` with the missing paths in the result body so the merge guard treats this as a pipeline failure rather than a code defect.
   - Defensive only: any pubsub query failure (network, gateway absent) logs a warning and skips the check rather than erroring — Phase 1's per-agency write lock is the primary correctness guarantee; Phase 3 is purely a backstop.

## Verification

- ✅ Unit-level (Phase 1): [`TestBUG09020_ConcurrentWriteFilesAllLand`](../../../git_concurrency_test.go) passes with `-race`.
- ✅ Unit-level (Phase 2): writeTracker tests in [`event_writes_test.go`](../../../../CodeValdWork/internal/server/event_writes_test.go) pass with `-race`.
- ⏳ End-to-end: re-run the MVP-SF-003 reproducer to confirm `count(git.file.written) == count(git.file.write) == count(files on branch)`. Add as a /09-work-03 verdict line.
- ⏳ End-to-end Phase 3: with the per-agency lock removed (simulate the race by reverting `WriteFile`'s mutex temporarily), confirm `compile-flutter` returns `status=infrastructure-error` rather than `issues-found` when the clone is short of declared files.

## Dependencies on other gaps

- Phase 2 step 4 is much easier after BUG-09-023 (proto3 replace-all) is fixed — the dispatcher needs to PATCH the todo with `expected_writes` mid-flight without zeroing other fields.
- Once fixed, BUG-09-021 (imports for files never written) becomes easier to catch because compile-flutter sees the complete branch.
