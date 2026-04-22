# Repository Import v2 — Lazy Branch Import (GIT-023)

Topics: Import · Lazy Fetch · On-Demand Branches · Performance

---

## Problem Statement

The v1 `ImportRepo` implementation (`git_impl_import.go`) is **too slow** for
any non-trivial repository. Root causes:

| Bottleneck | Cause |
|---|---|
| Full packfile download | `PlainCloneContext` fetches every object for every branch before any entity is written |
| Full commit history walk per branch | Every commit reachable from every branch tip is walked |
| Full tree walk per commit | Every file in the working tree at every commit is materialised as an entity |
| Redundant commit/tree/blob inserts | Branches share history — the same commits are re-visited multiple times (ArangoDB `AlreadyExists` round-trip per duplicate) |
| Full blob content stored | Every version of every file is stored as an entity property — both slow and storage-heavy |
| No batching | Each commit, tree, and blob is a separate ArangoDB round-trip |

**Result**: A repository with 1 000 commits and 500 files generates up to
500 000 entity-creation attempts, each a separate ArangoDB call. Import time
scales as O(commits × files_per_commit).

---

## Redesign: Two-Phase Lazy Import

### Phase 1 — Quick Import (seconds, not minutes)

**Goal**: Accept the import request, fetch only the minimal metadata needed to
list branches, and return control to the caller. The job reaches `completed` in
seconds.

**Steps** (all inside the background goroutine in `runImport`):

1. **Bare shallow clone** — `go-git` `PlainCloneContext` with:
   ```go
   gogit.CloneOptions{
       URL:          req.SourceURL,
       Bare:         true,
       Depth:        1,                // only tip commit per branch
       SingleBranch: false,            // still fetch all branch refs
       Tags:         gogit.NoTags,     // skip tags — fetched lazily on request
   }
   ```
   This downloads **one commit per branch** rather than the full history.

2. **List remote refs** — iterate `repo.References()` over the cloned bare
   repo. For every ref that looks like a branch (`refs/heads/*` or
   `refs/remotes/origin/*`), extract the branch name and tip SHA.

3. **Write entity graph** — create one `Repository` entity + one `Branch`
   entity per discovered ref. Branch entity carries:
   - `name` (string)
   - `head_commit_sha` (string — tip SHA from the ref)
   - `status` = `"stub"` — a new sentinel value signalling the branch content
     has not yet been fetched (see [Branch Status](#branch-status))
   - `source_url` (string — for future re-fetch)

4. **Persist bare clone path** on the `Repository` entity (`bare_clone_path`
   property) so `FetchBranch` can reuse the local clone without re-downloading.

5. **Auto-fetch the default branch** — immediately after all stub entities are
   written, `runImport` looks up the stub `Branch` entity whose `name` matches
   `req.DefaultBranch` and calls `FetchBranch` for it. This starts a background
   goroutine (the same one used by the on-demand fetch path) that deepens the
   bare clone and materialises the full commit history, tip-commit tree, and
   blob metadata for that branch. The import job itself transitions to
   `completed` without waiting for the fetch goroutine — the UI can poll
   `GetFetchBranchStatus` on the returned `FetchBranchJob` ID.

   If the default branch stub is not found (e.g. the name was wrong or the
   remote has no matching ref) the auto-fetch is skipped with a log warning;
   the import still completes successfully.

6. **Mark job `completed`** and publish `cross.git.{agencyID}.repo.imported`.

**Result**: Import finishes in seconds. The UI immediately shows all branch
names. The default branch content is already being populated in the background
without any user action required.

---

### Phase 2 — On-Demand Branch Fetch

**Goal**: When the user navigates to a branch, fetch and materialise its full
content.

**New `GitManager` method**:

```go
// FetchBranch fetches the full commit history, trees, and blobs for a
// previously imported branch stub and materialises them in the entity graph.
// It is idempotent — calling it again on an already-fetched branch is a no-op.
// Returns ErrBranchAlreadyFetched if the branch status is already "fetched".
FetchBranch(ctx context.Context, req FetchBranchRequest) (FetchBranchJob, error)
```

```go
// FetchBranchRequest carries the parameters for an on-demand branch fetch.
type FetchBranchRequest struct {
    AgencyID   string
    RepoID     string
    BranchName string
}

// FetchBranchJob represents the state of an async branch fetch operation.
type FetchBranchJob struct {
    ID         string
    AgencyID   string
    RepoID     string
    BranchName string
    // Status: "pending" | "running" | "completed" | "failed"
    Status       string
    ErrorMessage string
    CreatedAt    string
    UpdatedAt    string
}
```

**Steps** (inside background goroutine):

1. **Check branch status** — return `ErrBranchAlreadyFetched` if status ≠ `"stub"`.
2. **Transition branch status** → `"fetching"`.
3. **Deepen clone** — call `go-git` `FetchContext` with the specific refspec
   (`refs/heads/<branchName>:refs/heads/<branchName>`) and `Depth: 0`
   (unshallow) using the existing bare clone at `bare_clone_path`.
   If `bare_clone_path` no longer exists, re-clone bare shallow, then unshallow
   this branch only.
4. **Walk commit history** — `walkBranchCommits` with a `seenSHAs map[string]bool`
   passed through the call chain so shared commits across branches are skipped
   after the first fetch. The seen-set is stored as a property on the
   `Repository` entity (`fetched_commit_shas`, a string array) so it survives
   server restarts.
5. **Walk trees for tip commit only** (default) — only the HEAD commit's tree
   is materialised as entities. Historical trees are intentionally omitted to
   keep storage proportional to the number of fetched branches × unique files.
6. **Store blob metadata, not content** — `upsertBlob` writes SHA, path, name,
   extension, and size. Content is fetched lazily by `ReadFile` (see Phase 3).
7. **Transition branch status** → `"fetched"`.
8. **Publish** `cross.git.{agencyID}.branch.fetched`.

---

### Phase 3 — Lazy Blob Content (ReadFile)

**Goal**: Serve file content on demand without storing it in every blob entity.

**Change to `ReadFile`**:

1. Look up the `Blob` entity for the requested `(branchID, path)`.
2. If `Blob.content` is present (already cached in the entity), return it immediately.
3. If `Blob.content` is absent, open the **backend storer** for the repository
   (`m.backend.OpenStorer(ctx, agencyID, repoName)`) and read the blob object by
   SHA (`repo.BlobObject(plumbing.NewHash(sha))`). This works for both pushed
   repositories (objects in ArangoDB/filesystem storer) and imported repositories
   (objects in the same storer after `IndexPushedBranch` runs). Write the content
   back to the entity (`UpdateEntity` — cache it for future reads), then return it.
4. If the blob object cannot be resolved in the storer, return
   `ErrBlobContentUnavailable`.

> **Note — why not a bare clone?**
>
> The original GIT-023e design stored blob objects in a bare clone on disk
> (`bare_clone_path` on the Repository entity) and read from it via
> `gogit.PlainOpen`. This was architecturally wrong: repositories created
> by `git push` (via `git-receive-pack`) store their objects in the
> **backend storer** (ArangoDB or filesystem), not in a bare clone. There
> is no bare clone for pushed repositories. Reading from the storer via
> `m.backend.OpenStorer` is the correct and universal approach — it works
> for both import-origin and push-origin repositories without any
> conditional branching. The `bare_clone_path` property on `Repository`
> entities is now unused for content hydration and can be ignored.

---

## Branch Status

The `Branch` entity gains a `status` property with the following state machine:

```
stub  ──FetchBranch──►  fetching  ──success──►  fetched
                                  ──failure──►  fetch_failed
```

| Status | Meaning | UI |
|---|---|---|
| `stub` | Branch name + tip SHA known; no files/commits stored | Show "Load Branch" button |
| `fetching` | Background fetch in progress | Show progress indicator |
| `fetched` | Full content materialised | Normal file browser |
| `fetch_failed` | Fetch error; `error_message` on entity | Show retry button |

---

## Sub-Tasks

| ID | Title | Depends On | Status |
|---|---|---|---|
| GIT-023a | Add `status` property (`stub`/`fetching`/`fetched`/`fetch_failed`) to `Branch` TypeDefinition in `schema.go`; add `FetchBranchJob` TypeDefinition to schema; add `git_fetchjobs` collection | ~~GIT-001~~ ✅ | 📋 Not Started |
| GIT-023b | Add `FetchBranchRequest`, `FetchBranchJob` types to `models.go`; add `ErrBranchAlreadyFetched`, `ErrBlobContentUnavailable` to `errors.go`; add `FetchBranch` to `GitManager` interface in `git.go` | GIT-023a | 📋 Not Started |
| GIT-023c | Refactor `runImport` — replace full `PlainClone` + all-branch-walk with bare shallow clone + `ls-refs` branch listing + stub entity writes; update `walkBranchCommits` to accept a `seenSHAs` set | GIT-023b | 📋 Not Started |
| GIT-023i | Auto-fetch default branch during import — after stub entities are written, `runImport` calls `FetchBranch` for the default branch so it is populated without user interaction | GIT-023c, GIT-023d | ✅ Done |
| GIT-023d | Implement `FetchBranch` — background goroutine; deepen clone or re-clone; walk commits (tip-tree only, seen-SHA dedupe); store blob metadata only; transition branch status | GIT-023b, GIT-023c | 📋 Not Started |
| GIT-023e | Update `ReadFile` — lazy blob content: check `Blob.content` → read from bare clone → cache back to entity → `ErrBlobContentUnavailable` fallback | GIT-023d | 📋 Not Started |
| GIT-023f | Proto additions — `FetchBranch` RPC + `GetFetchBranchStatus` RPC; `buf generate` | GIT-023b | 📋 Not Started |
| GIT-023g | gRPC server handlers for `FetchBranch` and `GetFetchBranchStatus` + HTTP route registration | GIT-023d, GIT-023f | 📋 Not Started |
| GIT-023h | Unit tests — stub import completes in <5 s (mocked remote); `FetchBranch` idempotency; `ReadFile` lazy content cache; `ErrBranchAlreadyFetched` rejection | GIT-023g | 📋 Not Started |

---

## Interface Changes Summary

### `git.go` additions

```go
// FetchBranch materialises the full content of a previously imported branch stub.
// Returns immediately with a FetchBranchJob; the actual fetch runs in a background goroutine.
// Returns ErrBranchAlreadyFetched if the branch status is already "fetched" or "fetching".
FetchBranch(ctx context.Context, req FetchBranchRequest) (FetchBranchJob, error)

// GetFetchBranchStatus returns the current state of an async FetchBranch job.
GetFetchBranchStatus(ctx context.Context, agencyID, jobID string) (FetchBranchJob, error)
```

### `models.go` additions

```go
// FetchBranchRequest carries the parameters for an on-demand branch content fetch.
type FetchBranchRequest struct {
    AgencyID   string
    RepoID     string
    BranchName string
}

// FetchBranchJob represents the state of an async on-demand branch fetch operation.
type FetchBranchJob struct {
    ID           string
    AgencyID     string
    RepoID       string
    BranchName   string
    Status       string // "pending" | "running" | "completed" | "failed"
    ErrorMessage string
    CreatedAt    string
    UpdatedAt    string
}
```

### `errors.go` additions

```go
// ErrBranchAlreadyFetched is returned by FetchBranch when the branch status
// is already "fetched" or "fetching" — the content is either available or
// a fetch is already in progress.
var ErrBranchAlreadyFetched = errors.New("branch already fetched")

// ErrBlobContentUnavailable is returned by ReadFile when the blob entity exists
// but its content has not been cached and the bare clone is no longer on disk.
// The caller should trigger a FetchBranch to restore the local clone.
var ErrBlobContentUnavailable = errors.New("blob content unavailable — trigger FetchBranch to restore")
```

---

## HTTP Routes

Registered in `internal/registrar/routes.go`:

| Method | Pattern | Capability | gRPC Method |
|---|---|---|---|
| `POST` | `/git/{agencyId}/repositories/{repoId}/branches/{branchName}/fetch` | `fetch_branch` | `GitService/FetchBranch` |
| `GET` | `/git/{agencyId}/fetch-jobs/{jobId}` | `get_fetch_branch_status` | `GitService/GetFetchBranchStatus` |

---

## Performance Impact

| Metric | v1 (full import) | v2 (lazy) |
|---|---|---|
| Import completion time (medium repo, ~1 000 commits, ~500 files) | 5–30 minutes | **< 10 seconds** |
| ArangoDB writes on import | ~500 000 | **~1 + branch_count** |
| Branch content available | All at once (after full import) | **On demand — seconds per branch** |
| Blob content stored | Every version of every file | **Only files that are read** |
| Network traffic on import | Full packfile (all branches, all history) | **Shallow packfile (~1 commit per branch)** |

---

## Acceptance Criteria

- [ ] `GitManager` gains `FetchBranch` and `GetFetchBranchStatus` methods
- [ ] `ImportRepo` completes in < 10 seconds for any public GitHub repo regardless of size
- [ ] Imported `Branch` entities have `status = "stub"` until `FetchBranch` is called
- [ ] `FetchBranch` returns `ErrBranchAlreadyFetched` if status is `"fetched"` or `"fetching"`
- [ ] `FetchBranch` transitions branch through `fetching → fetched` (or `fetch_failed`)
- [ ] `ReadFile` caches blob content lazily; returns `ErrBlobContentUnavailable` when bare clone is absent
- [ ] Default branch is automatically fetched (commits + tip-tree + blob metadata) during import — no user action required
- [ ] If the default branch stub is not found the import still completes (auto-fetch is best-effort)
- [ ] `cross.git.{agencyID}.repo.imported` published after Phase 1 import (stub branches)
- [ ] `cross.git.{agencyID}.branch.fetched` published after Phase 2 fetch completes
- [ ] Seen-SHA deduplication: shared commits between branches are not re-walked
- [ ] Temp dir (for re-clone) cleaned up on completion, failure, or cancellation
- [ ] `go test -race ./...` passes
