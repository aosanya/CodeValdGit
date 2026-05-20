# GIT-016 — Delete-Only Push via Smart HTTP

**Date**: 2026-05-19
**Status**: ✅ Complete
**Build**: `go build ./...` ✅

---

## Problem

`git push origin --delete <branch>` returned HTTP 500 against the CodeValdGit Smart HTTP server.

Server log:

```
[receive-pack][utility-app-builder/shared-farms] ReceivePack error (type=*packfile.Error): empty packfile
```

### Root Cause

The git Smart HTTP protocol allows a receive-pack request that contains only deletion commands to omit the packfile entirely — there are no new objects to transfer. go-git's `session.ReceivePack` unconditionally attempts to decode a packfile from the request body. When the body ends after the flush packet (no packfile present), it returns `*packfile.Error: empty packfile`, which `receivePack` forwarded as HTTP 500.

---

## Fix

**File**: `internal/server/githttp.go`

After decoding the pkt-line commands in `receivePack`, check whether every command is a deletion (`cmd.New == plumbing.ZeroHash`). If so, handle them directly via `storer.RemoveReference` and bypass `sess.ReceivePack` entirely.

Two functions added:

- `isDeleteOnly(req)` — returns true when all commands have `New == zero hash`.
- `receivePackDeletes(w, agencyID, repoName, req)` — opens the storer, calls `RemoveReference` for each command, and encodes a `packp.ReportStatus` with `UnpackStatus = "ok"` and per-ref `CommandStatus` entries.

The `GitHTTPHandler` struct gained a `backend codevaldgit.Backend` field (already available at construction time) so `receivePackDeletes` can call `backend.OpenStorer` without a round-trip through the go-git transport layer.

Mixed pushes (some creates/updates alongside deletes) are unaffected: `isDeleteOnly` returns false and the existing `sess.ReceivePack` path handles them normally, since those requests do carry a packfile.

---

## Storer Behaviour for Stub Branches

Several branches in the `shared-farms` repo were created via the gRPC `CreateBranch` API and never received a push — their `Branch.sha` is empty in ArangoDB. `IterReferences` (used to advertise refs to the client) applies a fallback that advertises the default branch SHA for these stubs. `RemoveReference` handles the absent-sha case gracefully: it looks up the Branch entity by name and calls `dm.DeleteEntity`, succeeding regardless of whether a sha was stored.

---

## Verification

After rebuilding and restarting the container:

```
$ git push origin --delete \
    feature/UTIL-015-dark-mode-ui \
    feature/UTIL-015-preferences-layer \
    feature/UTIL-T01-test-branch \
    feature/UTIL008-dark-mode-toggle-settings \
    feature/UTIL011-dark-mode \
    feature/util-001-app-version-widget

To http://.../utility-app-builder/shared-farms
 - [deleted]         feature/UTIL-015-dark-mode-ui
 - [deleted]         feature/UTIL-015-preferences-layer
 - [deleted]         feature/UTIL-T01-test-branch
 - [deleted]         feature/UTIL008-dark-mode-toggle-settings
 - [deleted]         feature/UTIL011-dark-mode
 - [deleted]         feature/util-001-app-version-widget
```

```
$ git branch -a
* main
  remotes/origin/HEAD -> origin/main
  remotes/origin/main
```
