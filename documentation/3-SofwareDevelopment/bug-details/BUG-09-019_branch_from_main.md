# BUG-09-019 — `git.branch.create` does not branch from main

**Status:** ✅ Fixed (2026-06-01)
**Severity:** High — feature branches inherit each other's commits, breaking isolation
**Owner:** CodeValdGit
**Source finding:** [`/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md`](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)

---

## Problem

When MVP-SF-001 → MVP-SF-002 → MVP-SF-003 ran sequentially, `git log --all --oneline --graph` against the resulting `shared-farms` clone showed all three feature branches sharing **one linear commit chain**. MVP-SF-002 and MVP-SF-003 were descendants of MVP-SF-001's tip rather than independent branches off `main`. MVP-SF-002 had no unique commits and shared a tip with MVP-SF-003.

## Investigation

The QA-side hypothesis was partly right, partly wrong:

- ❌ `git_impl_branch.go:CreateBranch` was **not** defaulting to storer HEAD. It already resolved an empty `FromBranchID` via `defaultBranch(repo)` (the entity flagged `is_default=true`).
- ✅ But `handleBranchCreate` in `internal/server/event_receiver.go` was **ignoring `payload.from_branch`** entirely. Even when the AI emitted `from_branch: "main"`, `CreateBranch` was called with `FromBranchID=""`. After MVP-SF-001 merged to main, the `defaultBranch` lookup returned main-with-MVP-SF-001-tip, so subsequent feature branches inherited those commits.
- ✅ The AI side was inconsistent — the decomposer rules in `agency.json` didn't mandate emitting `from_branch: "main"`.

## Resolution

Commit `d701744` ("fix: default commit author + honour from_branch on git.branch.create") landed both halves of the platform-side fix:

### Receiver — [`internal/server/event_receiver.go:handleBranchCreate`](../../../internal/server/event_receiver.go)

```go
var fromBranchID string
if p.FromBranch != "" {
    fromBranch, fbErr := s.mgr.GetBranchByName(ctx, repo.ID, p.FromBranch)
    if fbErr != nil {
        log.Printf("codevaldgit: handleBranchCreate: resolve from_branch %q: %v — falling back to repo default", p.FromBranch, fbErr)
    } else {
        fromBranchID = fromBranch.ID
    }
}
branch, err := s.mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
    RepositoryID: repo.ID,
    Name:         p.Name,
    FromBranchID: fromBranchID,
})
```

### Manager — [`git_impl_branch.go:CreateBranch`](../../../git_impl_branch.go)

No code change. Existing behaviour already correct:

```go
if req.FromBranchID != "" {
    sourceBranch, err = m.GetBranch(ctx, req.FromBranchID)
} else {
    sourceBranch, err = m.defaultBranch(ctx, repo.ID)
}
```

### Decomposer — `CodeValdImplementations/Agencies/utility-app-builder/agency.json`

Two belt-and-braces rules now force the AI to emit `from_branch: "main"`:

- `developer-assigned-handler` → `RULE BRANCH-TODO`: _"ALWAYS include from_branch: \"main\" so the branch is created off main."_
- `work-todo-handler` → `RULE BRANCH-CREATE`: _"The git.branch.create event MUST include \"from_branch\": \"main\"..."_

## Open follow-ups (non-blocking)

- No regression test covers the `from_branch` resolution branch in `handleBranchCreate`. Add one alongside the next change to that file.
- `handleFileWrite`'s auto-create fallback (when `git.file.write` arrives for a missing branch) doesn't pass `FromBranchID` and relies on `CreateBranch`'s default-branch fallback. Correct today; worth tightening if that fallback is ever revisited.
- The Work-2 verdict assertion (`git log --all --graph` ancestor check) hasn't been added to the /09 docs yet.
