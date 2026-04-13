# CodeValdGit — Architecture Review

> Reviewed: March 2026
> Addressed items link to the decision documents that resolved them.

---

## What Looks Strong

The architecture gets several important things right:

- **Per-agency repo isolation** matches the existing tenancy model and keeps blast radius small.
- **Branch-per-task** is the right baseline for concurrent agent work.
- **Backend injection** is clean — `Backend` handles lifecycle; shared repo logic stays storage-agnostic.
- **Git Smart HTTP** support is a smart move for standard tooling and future interoperability.
- **Short-lived task branches + merge on completion** gives a usable first workflow.

The design is coherent. The gaps are mostly around what happens when multiple agents, failures, retries, and scale show up.

---

## Gaps

### ~~1. No explicit concurrency model for refs~~ ✅ Addressed

> Resolved in [architecture-concurrency.md](../architecture-concurrency.md) — `RefLocker` interface, CAS on `head_commit_id`, per-agency merge serialisation. Implementation tracked as **GIT-011**.

<details>
<summary>Original finding</summary>

Two concurrent `MergeBranch` calls for the same agency race to advance the same default-branch HEAD pointer, causing a lost update. Without a clear concurrency contract you can get lost updates or ref corruption at the application level even if the underlying Git objects are valid.

Missing decisions:
- Is there one writer per repo at a time, or one writer per branch with serialised updates to shared refs?
- How are ref updates made atomic in both backends?
- What is the lock granularity: repo, branch, or ref?

For ArangoDB specifically: compare-and-swap on refs, optimistic revision checks, and transaction boundaries across `git_refs`, `git_index`, and object inserts are all required.

</details>

---

### ~~2. The rebase/cherry-pick merge plan is underspecified and risky~~ ✅ Addressed

> Resolved in [architecture-merge.md](../architecture-merge.md) — tree-diff squash merge, fork-point tracking on `Branch`, `ErrMergeConflict` conflict surface. Implementation tracked as **GIT-012**.

<details>
<summary>Original finding</summary>

The design said "if main has advanced → auto-rebase task branch onto main, then fast-forward merge" but left undefined: how to compute the exact commit range to replay, whether merges inside the task branch are allowed, how rename detection works, what conflict detection means for binaries vs text, what happens to authorship and timestamps on replayed commits, whether commit IDs are expected to change, and what happens if replay partially succeeds and then fails.

**Decision taken**: single-commit squash merge onto main; tree-diff apply from fork-point to task HEAD; no replay of intermediate commits; branch history preserved for audit.

</details>

---

### ~~3. No clear transaction boundaries~~ ✅ Addressed

> Resolved in [architecture-transactions.md](../architecture-transactions.md) — atomicity rules, `MergeRequest` with `IdempotencyKey`, retry-safety matrix. Implementation tracked as **GIT-013**.

<details>
<summary>Original finding</summary>

`MergeBranch` is a multi-step operation. A process crash between steps produces ambiguous state. The design had no documented recovery behaviour, no idempotency key, and no explicit rule about which step constitutes visibility of the merge.

**Decision taken**: object writes are speculative and idempotent; only `advanceBranchHead` makes the merge visible; `DeleteBranch` called only after `advanceBranchHead` succeeds; every merge attempt carries an `IdempotencyKey` for safe retry.

</details>

---

### 4. Worktree strategy is not coherent yet

The operational model for the worktree is still undefined. For the ArangoDB backend, `OpenStorer` returns storage + `memfs.New()` "or osfs for a durable worktree" — this is ambiguous.

**Questions to resolve:**
- Are repos bare (object-store only, mutations via plumbing)?
- Non-bare with working tree reconstructed on demand?
- Hybrid where some operations require temporary checkout?

**Recommendation**: treat repos as bare/object-store first; create an ephemeral scratch worktree only for operations that need checkout-like behaviour. Define: scratch worktree location, cleanup policy, max size, concurrency rules, crash recovery.

---

### 5. Missing packfile / garbage collection / storage growth strategy

Git object stores grow indefinitely unless managed. For the entitygraph/ArangoDB backend:
- Many tiny documents for blobs/trees/commits can be inefficient.
- Duplicate object insert contention under concurrent writes.
- No packfile-like optimisation unless explicitly designed for.

No lifecycle exists for GC, pruning unreachable objects, compaction, or retention after branch deletion.

**Recommendation**: define storage maintenance — unreachable objects retained for N days after branch deletion; periodic mark-and-sweep; ArangoDB backend may need a custom compaction strategy.

---

### ~~6. The ArangoDB backend is operationally underdesigned~~ ✅ Addressed

> Resolved in [architecture-arangodb.md](../architecture-arangodb.md) — v1/v2 design evolution, per-sub-gap analysis (index semantics closed by v2, CAS via GIT-011, object deduplication, Smart HTTP limitation, query load profile), and filesystem-first production guidance.
> Implementation tracked as **GIT-014**.

<details>
<summary>Original finding</summary>

On paper "store Git objects and refs in ArangoDB" is elegant. In practice:

| Sub-gap | Detail |
|---|---|
| **Index semantics** | `git_index` is mutable state. One index per repo is dangerous with concurrent task branches. |
| **Ref consistency** | Branch refs must update atomically with expected old SHA checks (partially resolved by GIT-011 CAS). |
| **Object deduplication** | Object identity should be `(agency_id, sha)`, not just `sha`. Unique constraints needed for concurrent identical writes. |
| **Query/load profile** | Git workloads are bursty, small-read-heavy. ArangoDB round-trips per tree walk need measurement. |
| **Smart HTTP performance** | Serving pack protocol from a document DB may be significantly slower than from disk. |

**Recommendation**: filesystem backend as production default first; ArangoDB backend treated as experimental until benchmarked with realistic repo sizes and concurrent load.

</details>

---

### 7. Missing authorisation and policy layer for Smart HTTP

Smart HTTP exposes clone/fetch/push. Standard Git clients can bypass higher-level task APIs unless the transport is restricted.

**Questions to resolve:**
- Can clients push directly to `main`?
- Can clients create arbitrary branches?
- Is Git HTTP read-only for humans, write-only for service accounts?
- How is agency-level auth enforced from the URL path?

**Recommendation**: add a policy layer — deny push to `main`, allow push only to `task/{task-id}` branches if the principal owns that task, deny force-push, scope read access to agency. Without this, branch safety exists only in the gRPC API, not in the Git transport.

---

### 8. No model for branch lifecycle failures and abandoned tasks

No defined behaviour for:
- Agent crashes and never completes.
- Merge conflicts unresolved for days.
- Task is cancelled.
- Branch is stale because `main` moved significantly.
- Task retries create multiple branches for the same task ID.

**Recommendation**: define branch states outside Git (in CodeValdWork): `active`, `merge_pending`, `conflicted`, `merged`, `abandoned`. Define retention: auto-clean abandoned branches after review window; prevent duplicate live branches for the same task unless explicitly resumed. Git alone must not be the task-state source of truth.

---

### 9. API is too file-centric for multi-file agent output

`WriteFile` commits one file at a time — this produces noisy history and makes multi-file changes non-atomic.

**Missing methods:**
- `GetRef(ref)` / `BranchExists(taskID)`
- `MergeStatus(taskID)` / `ConflictFiles(...)`
- `Stat(ref, path)` / `MoveFile` rename semantics
- Batch/atomic multi-file commit

**Recommendation**: add `CommitFiles(taskID, changes[], author, message)` where `changes` supports write/delete/mkdir/move. This is far more realistic for agent output.

---

### 10. No explicit handling of binary files, large files, or repo size limits

Agents may generate images, PDFs, archives, generated bundles, large JSON artifacts. Git stores them but performance degrades quickly.

**Decisions needed:**
- Max file size per commit.
- Max repo size per agency.
- Whether LFS-like handling is needed later.
- Whether certain paths/extensions are denied.
- Whether generated artifacts belong in Git or external blob storage.

This matters much more once Smart HTTP is exposed.

---

### 11. Observability and auditability are missing

Merges are automated. You need to be able to answer: who wrote this commit, which task merged it, what base commit it started from, why a merge failed, how long it took, which files conflicted.

**Recommendation**: record structured metadata for every mutation — `agencyID`, `taskID`, actor, old ref, new ref, operation type, merge attempt ID, duration, result, conflict files. Emit metrics: repo open latency, commit latency, merge latency, clone/fetch/push latency, ArangoDB round-trips, conflict rate.

---

### 12. Delete and purge semantics need tightening

`DeleteRepo` archives; `PurgeRepo` is permanent. Open questions:
- Can a deleted repo be reopened?
- Does Smart HTTP return `404` or `410` for deleted repos?
- What if purge happens while a clone/fetch/merge is in flight?
- Is delete idempotent?
- Can purge be blocked by a retention policy?

**Recommendation**: define explicit lifecycle states (`active`, `deleted/archived`, `purged`) and the behaviour of all API and HTTP calls in each state.

---

### 13. Smart HTTP routing and repo naming need hardening

Using `agencyID` directly in the URL path requires defining:
- Allowed character set.
- URL escaping / normalisation / canonicalisation rules.
- Case sensitivity.
- Protection against path ambiguity (especially on filesystem backend).

**Recommendation**: introduce a canonical repo key encoding rather than using raw agency IDs directly on disk paths.

---

### 14. Disaster recovery / backup strategy is missing

CodeValdGit becomes a system of record for generated files. Open questions:
- Backup unit: per-agency repo, per-backend, or snapshots?
- How to restore a single agency.
- How to verify object/ref consistency after restore.
- For ArangoDB: restore collections globally or per agency?
- For filesystem: are archive dirs included?

---

### 15. Smart HTTP plus gRPC creates two write paths

Two mutation planes exist unless one is restricted. Raw `git push` via Smart HTTP can bypass gRPC task branch naming rules entirely, creating histories the repo layer does not expect.

**Recommendation for V1**: make Smart HTTP read-only (clone and fetch only; push disabled) until policy and invariants are proven.

---

### 16. Missing compatibility assumptions about go-git

go-git advanced behaviours can differ from native Git expectations around edge cases.

**Recommended test matrix:**
- Branch create/delete
- Concurrent commits
- Merge conflict detection
- Clone/fetch/push interoperability with stock Git CLI
- Binary file diffs, rename scenarios, non-ASCII file paths
- Large repos

Do not assume spec-level support means production-level equivalence.

---

## V1 Recommendations

| # | Recommendation | Status |
|---|---|---|
| 1 | Make filesystem the primary backend; keep ArangoDB experimental | Open — gap 6 |
| 2 | Make Smart HTTP read-only | Open — gap 15 |
| ~~3~~ | ~~Serialise merges to main per agency~~ | ✅ GIT-011 |
| ~~4~~ | ~~Replace rebase replay with squash merge~~ | ✅ GIT-012 |
| 5 | Add batch commit API (`CommitFiles`) | Open — gap 9 |
| 6 | Treat worktrees as ephemeral scratch space | Open — gap 4 |
| 7 | Define lifecycle/state model outside Git (in CodeValdWork) | Open — gap 8 |

---

## Summary

The three most critical correctness gaps — concurrency, merge strategy, and transaction boundaries — are now defined in [architecture-concurrency.md](../architecture-concurrency.md), [architecture-merge.md](../architecture-merge.md), and [architecture-transactions.md](../architecture-transactions.md) respectively, with implementation tasks GIT-011 through GIT-013.

The remaining gaps (4–16) are structural and operational concerns for subsequent iterations.
