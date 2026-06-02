---
status: 📋 Draft (2026-06-02)
owner: CodeValdGit
scope: git-operation failure events + field contracts for recovery pipelines
source: gap analysis of `/4-QA/agencies/utility-app-builder/09`
---

# Git Failure Modes

CodeValdGit handles the git operations the 09 pipeline drives: branch
create, file write, branch merge, branch delete. Each is a distinct
operation with its own failure surface, plus a cross-cutting issue —
**entity-graph vs. bare-ref divergence** — which is the root cause of
several open bugs.

This doc catalogues the failure events Git emits and the field contracts
that recovery pipelines (per
[FEAT-20260602-005](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-005_failure_pipelines_synthesized_success.md))
must satisfy when they synthesize Git success events.

The orchestration overview lives in
[CodeValdCross — pipeline-failure-handling](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/pipeline-failure-handling.md).

---

## Failure events CodeValdGit emits

| Event | When emitted | `payload` fields |
|---|---|---|
| `git.branch.failed` | A `git.branch.create` or `git.branch.delete` request returned an error. | `repository`, `name`, `operation` ∈ {`create`, `delete`}, `error`, `workflow_run_id` |
| `git.file.write_failed` | A `git.file.write` failed (auth, missing branch, storer error). | `repository`, `branch`, `path`, `error`, `workflow_run_id` |
| `git.conflict.detected` | A merge request failed because of a conflict. Distinct from a generic merge error. | `repository`, `branch`, `merge_into`, `conflict_paths`, `workflow_run_id` |
| `git.merge.failed` | A merge request failed for non-conflict reasons (auth, missing branch, 4xx). | `repository`, `branch`, `merge_into`, `error`, `workflow_run_id` |

Today the system only publishes some of these — `git.conflict.detected` is
referenced as an assertion in 09 docs but no producer is documented; the
merge endpoint returns errors but the calling function (`merge-flutter-branch`)
wraps them into `functions.job.failed`. Need to publish these at the
CodeValdGit layer too so failure pipelines can route on them directly.

---

## Field contracts for synthesized success events

### `git.branch.created`

Listened for by: nothing structural — but the AI's branch-creation todo
expects to observe its own `git.branch.created` to confirm the branch
exists before proceeding to file writes.

- **Must produce:** `repository`, `name`, `head_commit_id`, `from_branch`
  (must be `main` or another stable branch), `workflow_run_id`
- **May differ:** `created_at`

A recovery pipeline that synthesizes this **must** also have actually
created the branch in the bare git ref store (entity row alone is not
enough — `compile-flutter`'s `git clone` reads from smart-HTTP which serves
the bare repo). See BUG-09-020.

### `git.file.written`

Listened for by: today nothing reads this synchronously; downstream
consumers read the file tree at clone time. **Implicitly relied on** —
`work.todo.completed` should fire only after every `git.file.write` event
has a corresponding `git.file.written` and the file is durable on the bare
repo (BUG-09-020 phase 2).

- **Must produce:** `repository`, `branch`, `path`, `commit_id`,
  `workflow_run_id`
- **May differ:** `size_bytes`, `content_hash`

### `git.branch.merged`

Listened for by: closure SSE, audit. **Implicitly relied on** by the merge
binary's caller — `merge-flutter-branch` reads its own merge response and
expects the bare repo to be updated.

- **Must produce:** `repository`, `branch`, `merge_into = "main"`,
  `merge_commit_id`, `workflow_run_id`
- **May differ:** `merge_strategy`, `merged_files`

### `git.branch.deleted`

- **Must produce:** `repository`, `name`, `workflow_run_id`
- **May differ:** `deleted_at`

---

## GF-N — Git-specific failure modes (and which recovery pipeline owns them)

### GF-1 — `git.branch.create` rejected

**Trigger:** branch already exists with a different head; auth failure; repo
missing. Today: the receiver logs and emits a generic error response. No
typed failure event.

**Post FEAT-005:** CodeValdGit publishes `git.branch.failed`. The
upstream caller (AI's branch-creation todo) detects via `ai.run.failed
{ reason: git_branch_failed }`. The AI run's `on_failure_pipeline`
(`impl-solving-problem`) decides whether to retry with a different branch
name or fail terminally.

### GF-2 — `git.file.write` race with task completion (BUG-09-020)

**Trigger:** `git.file.write` events are accepted async; the file appears
in the entity graph but not yet in the bare ref store when `compile-flutter`
clones. Compile sees a partial branch.

**Post FEAT-005:** does not directly help — this is a within-Git
synchronization bug, not an orchestration failure. The fix lives in
CodeValdGit's storer (Phase 1 of BUG-09-020's fix plan: flush before
emitting `git.file.written`).

However: the failure pipeline pattern adds a defensive read-side check in
`compile-flutter`. If the binary detects expected paths missing, it
publishes `functions.job.failed { reason: branch_partial }` instead of
`issues-found`. The `compile-solving-problem` recovery then waits T seconds
and retries — which catches the race without changing the storer.

### GF-3 — Merge conflict

**Trigger:** CodeValdGit detects two-way conflicting changes. Emits
`git.conflict.detected` (new event; not currently emitted) AND
`git.merge.failed`. The `merge-flutter-branch` function returns
`functions.job.failed`.

**Recovery:** `merge-solving-problem` runs an AI conflict-resolver agent
that reads the conflict markers, edits the files, commits, and retries the
merge internally. On success, synthesizes `functions.job.completed {
function_name: "merge-flutter-branch", status: "ok", ... }`.

Open question: should the conflict resolver run in the **failed branch**
(adding a resolution commit to the feature branch) or in a **resolution
branch** (a new branch off the merge-base)? The former is simpler and
matches typical PR-conflict workflows. Recommend the former.

### GF-4 — Merge endpoint 4xx (auth / missing branch)

**Trigger:** the merge endpoint returns 401/403/404. Today: the function
emits `functions.job.failed`. The `merge-failure-diagnostics` AI run
diagnoses but doesn't fix.

**Post FEAT-005:** same routing as GF-3 — `merge-solving-problem` branches
internally on the error type. For 4xx (auth), terminal-fail with a clear
Comm message. For missing branch (rare — branch was deleted between compile
and merge), the recovery can attempt to recreate it from the compile job's
recorded commit.

### GF-5 — `git.branch.delete` 404 (already gone)

**Trigger:** delete called on a non-existent branch. CodeValdGit returns
404; the delete function wraps to `functions.job.failed`.

**Fix:** CodeValdGit normalizes "delete of non-existent branch" to a
success response with `{ "already_absent": true }`. The delete function
treats this as `status="ok"`. No failure event fires; no recovery needed.
This is a correctness fix at the Git layer, not a recovery pattern.

### GF-6 — Bare-ref vs. entity-graph divergence (cross-cutting)

**Trigger:** any operation where the bare repo and entity graph drift
([BUG-09-019, BUG-09-020](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)).

**Post FEAT-005:** not directly addressed by the failure-pipeline pattern —
this is a write-boundary bug. The pattern does help by making mismatches
observable: if the failure pipeline reads the bare-repo state via `git
clone` and finds it inconsistent with the entity graph, it can publish a
typed `git.divergence.detected` event for ops visibility.

Tracked under BUG-09-020; not in scope here.

---

## What the recovery pipelines look like

The Git operations don't have their own top-level recovery pipelines —
they're driven by the consumer (AI todo for branch-create, function for
merge). The recovery pipelines that affect Git are owned by upstream
services:

- `impl-solving-problem` (CodeValdAI) — handles `git.branch.create` failure
  in the AI's branch-creation todo
- `merge-solving-problem` (CodeValdFunctions) — handles `git.merge.failed`
  and `git.conflict.detected`

CodeValdGit's responsibility under FEAT-005 is therefore narrower: publish
the typed failure events with enough payload context, and don't change the
shape of success events. Recovery semantics live upstream.

---

## Open follow-ups

- Publish `git.branch.failed`, `git.file.write_failed`, `git.conflict.detected`,
  `git.merge.failed` as typed events (today some of these are not emitted).
- Document the "already absent" idempotency normalization for delete.
- Add a `git.divergence.detected` event for ops observability — fired when
  CodeValdGit observes entity-graph vs. bare-repo inconsistency in a
  health-check sweep.
- Decision: should the conflict resolver run on the feature branch or a
  dedicated resolution branch? See GF-3.
- BUG-09-020 phase 1 (storer flush before `git.file.written`) is independent
  of this design; ship it on its own track.

---

## Related work

- [Cross — pipeline-failure-handling](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/pipeline-failure-handling.md)
- [Cross — FEAT-20260602-005 — failure pipelines via synthesized success events](../../../../CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/FEAT-20260602-005_failure_pipelines_synthesized_success.md)
- [Functions — function-job-failure-modes](../../../../CodeValdFunctions/documentation/3-SofwareDevelopment/mvp-details/function-job-failure-modes.md)
- [AI — ai-run-failure-modes](../../../../CodeValdAI/documentation/3-SofwareDevelopment/mvp-details/ai-run-failure-modes.md)
- [branch-workflow.md](branch-workflow.md)
- [critical-merge-strategy.md](critical-merge-strategy.md)
- [critical-concurrency.md](critical-concurrency.md)
- [BUG-09-019 — git.branch.create does not branch from main](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)
- [BUG-09-020 — task completes before all git.file.write events flush](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)
