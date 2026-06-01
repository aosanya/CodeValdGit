# BUG-09-022 — Commits have empty author identity

**Status:** ✅ Fixed (2026-06-01)
**Severity:** Low — git history doesn't attribute authorship; future tooling that filters by author breaks
**Owner:** CodeValdGit
**Source finding:** [`/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md`](../../../../CodeValdCross/documentation/4-QA/agencies/utility-app-builder/bugs/09-mvp-sf-pipeline-findings.md)

---

## Problem

```
$ git log main --format='%an <%ae>' | head -3
 <>
 <>
 <>
```

Every commit landed with empty author name and email.

## Root cause

[`git_impl_fileops.go:WriteFile`](../../../git_impl_fileops.go) constructed `object.Commit` with `req.AuthorName` / `req.AuthorEmail` directly. When the `git.file.write` event payload didn't carry actor info (the common case for automated AI commits), both fields were empty strings and go-git wrote them through to the commit object unchanged.

## Resolution

Commit `d701744` ("fix: default commit author + honour from_branch on git.branch.create") falls back to a `codevald-bot <bot@codevald.local>` identity when both `AuthorName` and `AuthorEmail` are unset on the request.

## Open follow-ups (non-blocking)

- The fallback is a single hard-coded identity. A future enhancement could thread an `actor` field through `FileWritePayload` so the commit author reflects which AI agent produced the change.
- No regression test on the author-defaulting path. Add one alongside the next change to `git_impl_fileops.go`.
