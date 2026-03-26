# CodeValdGit — Architecture

## 1. Core Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Git engine | [go-git](https://github.com/go-git/go-git) pure-Go | No system `git` binary dependency; embeddable in Go services |
| Repo granularity | 1 repo per Agency | Mirrors CodeValdCortex's database-per-agency isolation |
| Agent write policy | Always on a branch, never `main` | Prevents concurrent agent writes from corrupting shared history |
| Branch naming | `task/{task-id}` | Short-lived, traceable back to CodeValdCortex task records |
| Merge strategy | Auto-merge on task completion | No human approval gate for now; policy layer can extend this later |
| Storage backend | Pluggable via `storage.Storer` interface | go-git's open/closed design; caller injects the storer — filesystem and ArangoDB are both valid implementations |
| Worktree filesystem | Pluggable via `billy.Filesystem` interface | go-git separates object storage from the working tree; both are independently injectable |

---

## 2. Storage Backends

### go-git Pluggable Interfaces

go-git separates storage into two injectable interfaces:

| Interface | Package | Purpose |
|---|---|---|
| `storage.Storer` | `github.com/go-git/go-git/v5/storage` | Git objects, refs, index, config |
| `billy.Filesystem` | `github.com/go-git/go-billy/v5` | Working tree (checked-out files) |

### CodeValdGit `Backend` Interface

CodeValdGit adds a thin `Backend` interface on top of `storage.Storer`. It captures the operations that differ per storage type — repo lifecycle (init, archive/flag, purge) and storer construction — while the shared `Repo` implementation (branches, files, history) sits in `internal/repo/` and is backend-agnostic.

```go
// Backend abstracts storage-specific repo lifecycle.
// Implemented by storage/filesystem and storage/arangodb.
type Backend interface {
    // InitRepo provisions a new store for agencyID.
    InitRepo(ctx context.Context, agencyID string) error
    // OpenStorer returns a go-git storage.Storer and billy.Filesystem for agencyID.
    OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error)
    // DeleteRepo archives or flags the repo as deleted (behaviour is backend-specific).
    DeleteRepo(ctx context.Context, agencyID string) error
    // PurgeRepo permanently removes all storage for agencyID.
    PurgeRepo(ctx context.Context, agencyID string) error
}
```

The single `repoManager` implementation in `internal/manager/` holds a `Backend` and delegates lifecycle calls to it. `NewRepoManager(b Backend)` is the sole constructor — the caller (CodeValdCortex) picks and constructs the backend.

### Filesystem Backend (`storage/filesystem/`)

```
{base_path}/
└── {agency-id}/          ← One real .git repo per Agency
    └── .git/
```

| Operation | Implementation |
|---|---|
| `InitRepo` | `git.PlainInit` on disk; empty commit on `main` |
| `DeleteRepo` | `os.Rename` to `{archive_path}/{agency-id}/` (non-destructive) |
| `PurgeRepo` | `os.RemoveAll` of archive directory |
| `OpenStorer` | `filesystem.NewStorage` + `osfs.New` |

Simple, portable, works on any mounted volume (local disk, PVC, NFS).

### ArangoDB Backend (`storage/arangodb/`)

| Operation | Implementation |
|---|---|
| `InitRepo` | Insert seed documents into `git_objects`, `git_refs`, `git_config`, `git_index` |
| `DeleteRepo` | Set `deleted: true` flag on all agency documents (non-destructive; auditable) |
| `PurgeRepo` | Delete all documents where `agencyID == target` from all four collections |
| `OpenStorer` | `arango.NewStorage(db, agencyID)` + `memfs.New()` (or `osfs` for a durable worktree) |

The working tree (`billy.Filesystem`) remains on a local or in-memory filesystem — only the Git object store moves to ArangoDB. This mirrors the existing database-per-agency model in CodeValdCortex and means repos survive container restarts without a mounted volume.

| Collection | Contents |
|---|---|
| `git_objects` | Encoded Git objects (blobs, trees, commits, tags) keyed by SHA |
| `git_refs` | Branch and tag references |
| `git_index` | Staging area index |
| `git_config` | Per-repo Git config |

> **Selection**: The caller (CodeValdCortex) constructs the desired `Backend` implementation and passes it to `NewRepoManager`. CodeValdGit's core logic is backend-agnostic.

### Package Layout

```
github.com/aosanya/CodeValdGit/
├── codevaldgit.go          # RepoManager + Repo + Backend interfaces
├── types.go                # FileEntry, Commit, FileDiff, AuthorInfo, ErrMergeConflict
├── errors.go               # Sentinel errors (ErrRepoNotFound, ErrBranchNotFound, etc.)
├── config.go               # NewRepoManager constructor
├── internal/
│   ├── manager/            # Concrete repoManager — implements RepoManager, delegates to Backend
│   ├── repo/               # Shared Repo implementation — used by both storage backends
│   └── gitutil/            # Shared go-git helper utilities
└── storage/
    ├── filesystem/         # NewFilesystemBackend() — implements Backend (filesystem lifecycle)
    └── arangodb/           # NewArangoBackend()    — implements Backend (ArangoDB lifecycle)
```

---

## 3. Repository Identity

Naming convention: the Agency ID is the repository key in both backends.
- Filesystem: `{base_path}/{agency-id}/.git`
- ArangoDB: documents in `git_objects` etc. carry an `agency_id` field as the partition key (mirrors the existing database-per-agency isolation).

---

## 4. Branching Model

```
main
 │
 ├── task/task-abc-001     ← Agent A works here
 │     commits...
 │     └── auto-merged → main on task completion
 │
 └── task/task-xyz-002     ← Agent B works here (concurrent, isolated)
       commits...
       └── auto-merged → main on task completion
```

### Branch Lifecycle
1. **Task starts** → `CreateBranch("task/{task-id}", from: "main")`
2. **Agent writes files** → `Commit(branch: "task/{task-id}", files, author, message)`
3. **Task completes** → `MergeBranch("task/{task-id}", into: "main")`
   - If fast-forward is possible → merge directly
   - If `main` has advanced → **auto-rebase** task branch onto `main`, then fast-forward merge
   - If rebase conflicts → return `ErrMergeConflict{Files: [...]}` to caller; branch left clean for retry
4. **Branch deleted** → `DeleteBranch("task/{task-id}")`

> **Implementation note**: go-git only supports `FastForwardMerge`. The rebase step must be implemented by cherry-picking commits from the task branch onto the latest `main` using go-git's plumbing layer (`object.Commit`, `Worktree.Commit`).

---

## 5. Proposed Library API (Draft)

```go
// Backend abstracts storage-specific repo lifecycle.
// Implemented by storage/filesystem and storage/arangodb.
// The caller constructs the desired backend and passes it to NewRepoManager.
type Backend interface {
    InitRepo(ctx context.Context, agencyID string) error
    OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error)
    DeleteRepo(ctx context.Context, agencyID string) error
    PurgeRepo(ctx context.Context, agencyID string) error
}

// NewRepoManager constructs the shared RepoManager backed by the given Backend.
// Use storage/filesystem.NewFilesystemBackend or storage/arangodb.NewArangoBackend
// to obtain a Backend, then pass it here.
func NewRepoManager(b Backend) RepoManager

// RepoManager is the top-level entry point for managing per-agency Git repositories.
// Obtain via NewRepoManager. One instance is typically shared process-wide.
type RepoManager interface {
    InitRepo(ctx context.Context, agencyID string) error                   // delegates to Backend.InitRepo
    OpenRepo(ctx context.Context, agencyID string) (Repo, error)
    DeleteRepo(ctx context.Context, agencyID string) error                 // delegates to Backend.DeleteRepo
    PurgeRepo(ctx context.Context, agencyID string) error                  // delegates to Backend.PurgeRepo
}

// Repo represents a single agency's Git repository. Obtained via RepoManager.OpenRepo.
// Backed by internal/repo — backend-agnostic; works over any storage.Storer.
type Repo interface {
    // Branch operations
    CreateBranch(ctx context.Context, taskID string) error
    MergeBranch(ctx context.Context, taskID string) error
    DeleteBranch(ctx context.Context, taskID string) error

    // File operations (always on a task branch)
    WriteFile(ctx context.Context, taskID, path, content, author, message string) error
    ReadFile(ctx context.Context, ref, path string) (string, error)
    DeleteFile(ctx context.Context, taskID, path, author, message string) error
    ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)

    // History
    Log(ctx context.Context, ref, path string) ([]Commit, error)
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
```

---

## 6. Integration with CodeValdCortex

CodeValdCortex will call CodeValdGit at these lifecycle points:

| CodeValdCortex Event | CodeValdGit Call |
|---|---|
| Agency created | `RepoManager.InitRepo(agencyID)` |
| Task started | `Repo.CreateBranch(taskID)` |
| Agent writes output | `Repo.WriteFile(taskID, path, content, ...)` |
| Task completed | `Repo.MergeBranch(taskID)` → `Repo.DeleteBranch(taskID)` |
| Agency deleted | `RepoManager.DeleteRepo(agencyID)` |
| UI file browser | `Repo.ListDirectory("main", path)` |
| UI file view | `Repo.ReadFile("main", path)` |
| UI history view | `Repo.Log("main", path)` |

---

## 7. CodeValdSharedLib Dependency

CodeValdGit imports `github.com/aosanya/CodeValdSharedLib` for:

| SharedLib package | Replaces |
|---|---|
| `registrar` | `internal/registrar/registrar.go` (identical struct; service-specific metadata passed as constructor args) |
| `serverutil` | `envOrDefault`, `parseDuration` helpers and gRPC server setup block in `cmd/server/main.go` |
| `arangoutil` | ArangoDB `driverhttp.NewConnection` / auth / database bootstrap in `storage/arangodb/arangodb.go` |
| `gen/go/codevaldcross/v1` | Local copy of generated Cross stubs in `gen/go/codevaldcross/v1/` and `cmd/cross.go` |

> **Principle**: Any infrastructure code used by more than one service lives in
> SharedLib. CodeValdGit retains only domain logic, domain errors, gRPC
> handlers, and storage collection schemas.

See task MVP-GIT-012 in [mvp.md](../3-SofwareDevelopment/mvp.md) for migration scope.

---

## 8. What Gets Removed from CodeValdCortex

Once CodeValdGit is integrated, the following will be deleted:

- `internal/git/ops/operations.go` — custom SHA-1 blob/tree/commit engine
- `internal/git/storage/repository.go` — ArangoDB Git object storage
- `internal/git/fileindex/service.go` — ArangoDB file index service
- `internal/git/fileindex/repository.go` — ArangoDB file index repository
- `internal/git/models/` — custom Git object models
- ArangoDB collections: `git_objects`, `git_refs`, `repositories`

---

## 9. Git Smart HTTP Transport Libraries

CodeValdGit serves the [Git Smart HTTP protocol](https://git-scm.com/docs/http-protocol)
alongside its gRPC service so that standard `git clone`, `git fetch`, and `git push`
clients can interact with agency repositories directly.
This section documents every go-git sub-package and the `cmux` multiplexer used
to implement that capability.

---

### 9.1 `plumbing/transport` — Core Transport Interfaces

**Import path**: `github.com/go-git/go-git/v5/plumbing/transport`

This package defines the language-neutral contracts that all go-git transport
implementations (HTTP, SSH, file, git://) must satisfy. GIT-007 uses these
interfaces as the bridge between the HTTP handler and the go-git server engine.

#### Key types

| Type | Purpose |
|---|---|
| `Transport` | Factory that creates upload-pack and receive-pack sessions for a given endpoint |
| `UploadPackSession` | Handles `git fetch` / `git clone` — advertise refs and stream a pack file to the client |
| `ReceivePackSession` | Handles `git push` — advertise refs and accept a pack file from the client |
| `Endpoint` | Parsed Git URL; the `Path` field (e.g. `"/agency-42"`) is used as the repository key |
| `AuthMethod` | Optional authentication credential (pass `nil` for unauthenticated access) |

#### Interface signatures

```go
// Transport is implemented by plumbing/transport/server.NewServer().
type Transport interface {
    NewUploadPackSession(*Endpoint, AuthMethod) (UploadPackSession, error)
    NewReceivePackSession(*Endpoint, AuthMethod) (ReceivePackSession, error)
}

// UploadPackSession — used for git-fetch / git-clone.
type UploadPackSession interface {
    AdvertisedReferencesContext(context.Context) (*packp.AdvRefs, error)
    UploadPack(context.Context, *packp.UploadPackRequest) (*packp.UploadPackResponse, error)
    io.Closer
}

// ReceivePackSession — used for git-push.
type ReceivePackSession interface {
    AdvertisedReferencesContext(context.Context) (*packp.AdvRefs, error)
    ReceivePack(context.Context, *packp.ReferenceUpdateRequest) (*packp.ReportStatus, error)
    io.Closer
}
```

#### Service-name constants

```go
const (
    UploadPackServiceName  = "git-upload-pack"   // fetch / clone
    ReceivePackServiceName = "git-receive-pack"  // push
)
```

These constants appear verbatim in HTTP query strings (`?service=git-upload-pack`) and
Content-Type headers, so they are used throughout the Smart HTTP handler rather than
raw string literals.

---

### 9.2 `plumbing/transport/server` — Server-Side Transport Engine

**Import path**: `github.com/go-git/go-git/v5/plumbing/transport/server`

This package turns a `Loader` (a bridge to the actual git storage) into a
`transport.Transport` that an HTTP handler can call.  It is the only go-git package
that implements the **server** side of the wire protocol; the `plumbing/transport/http`
package is client-only.

#### `Loader` interface

```go
// Loader resolves a transport.Endpoint to a go-git storage.Storer.
// Return transport.ErrRepositoryNotFound when the repo does not exist.
type Loader interface {
    Load(ep *transport.Endpoint) (storer.Storer, error)
}
```

CodeValdGit provides a custom `backendLoader` that maps `ep.Path` → agencyID and
calls `Backend.OpenStorer(ctx, agencyID)`:

```go
type backendLoader struct{ b codevaldgit.Backend }

func (l *backendLoader) Load(ep *transport.Endpoint) (storer.Storer, error) {
    agencyID := strings.Trim(ep.Path, "/")
    sto, _, err := l.b.OpenStorer(context.Background(), agencyID)
    if err != nil {
        return nil, transport.ErrRepositoryNotFound
    }
    return sto, nil
}
```

#### Built-in loader variants

| Constructor | Behaviour |
|---|---|
| `NewFilesystemLoader(base billy.Filesystem)` | Resolves `ep.Path` as a sub-path under `base`; best for single-backend setups |
| `MapLoader` (`map[string]storer.Storer`) | Directly maps endpoint string → storer; useful for tests |

CodeValdGit uses the custom `backendLoader` so that the filesystem `Backend` handles
path resolution consistently with the rest of the codebase.

#### `NewServer`

```go
func NewServer(loader Loader) transport.Transport
```

Wraps a `Loader` into a `transport.Transport`. The returned value is stateless and
safe to share across goroutines. One instance is constructed at startup and reused
for every inbound HTTP request.

---

### 9.3 `plumbing/protocol/packp` — Pack Protocol Messages

**Import path**: `github.com/go-git/go-git/v5/plumbing/protocol/packp`

`packp` contains the structs and codecs for every message that the Git pack
protocol exchanges during a clone, fetch, or push. The Smart HTTP handler reads
from and writes to these types.

#### Types used in GIT-007

| Type | Direction | Used in |
|---|---|---|
| `AdvRefs` | server → client | Both `info/refs` endpoints; carries the list of refs + capabilities |
| `UploadPackRequest` | client → server | `POST /{agencyID}/git-upload-pack` request body |
| `UploadPackResponse` | server → client | `POST /{agencyID}/git-upload-pack` response body (contains the pack file) |
| `ReferenceUpdateRequest` | client → server | `POST /{agencyID}/git-receive-pack` request body |
| `ReportStatus` | server → client | `POST /{agencyID}/git-receive-pack` response body (per-ref status) |

#### `AdvRefs.Prefix` — Smart HTTP service advertisement

The Smart HTTP protocol requires a pkt-line service announcement before the
reference list in `info/refs` responses.  `AdvRefs.Prefix` is `[][]byte` — each
entry is either a raw line payload or the sentinel `pktline.Flush`.  Setting the
prefix before calling `AdvRefs.Encode(w)` instructs the encoder to emit the
service header automatically:

```go
// Set the Smart HTTP service header.
// The encoder writes "NNNN# service=git-upload-pack\n" + "0000" before the refs.
advRefs.Prefix = [][]byte{
    []byte("# service=" + transport.UploadPackServiceName),
    pktline.Flush,
}
```

`pktline.Flush` is the sentinel (`[]byte(nil)` / length-zero slice) that the
encoder translates to the pkt-line flush packet `0000`.

#### Encode / Decode pattern

Every type follows the same Encode/Decode pattern:

```go
// Decode from an io.Reader (request body or server response).
req := packp.NewUploadPackRequest()
if err := req.Decode(r.Body); err != nil { ... }

// Encode to an io.Writer (response writer).
if err := resp.Encode(w); err != nil { ... }
```

---

### 9.4 `plumbing/format/pktline` — Packet-Line Framing

**Import path**: `github.com/go-git/go-git/v5/plumbing/format/pktline`

The Git wire protocol frames all data as *pkt-lines*: a 4-hex-digit length prefix
(including the 4 bytes of the length itself) followed by the payload.  The flush
packet `0000` signals the end of a block.

#### Key exports

| Symbol | Purpose |
|---|---|
| `Flush` (`[]byte`) | Sentinel used in `AdvRefs.Prefix` to emit a flush packet `0000` |
| `NewEncoder(w io.Writer)` | Writes pkt-line framed data to `w` |
| `NewScanner(r io.Reader)` | Reads and splits pkt-line framed data from `r` |
| `Encoder.Encodef(format, args...)` | Printf-style pkt-line write |
| `Encoder.Flush()` | Write the flush packet `0000` |

In GIT-007, `pktline` is used indirectly through `packp.AdvRefs.Prefix` — the
handler does **not** call `pktline.NewEncoder` directly; `packp` encodes all
framing internally.

---

### 9.5 `github.com/soheilhy/cmux` — gRPC + HTTP on One Port

**Import path**: `github.com/soheilhy/cmux`

cmux is a Go library that inspects the first bytes of each incoming TCP connection
and dispatches it to a matching `net.Listener` — allowing gRPC (HTTP/2 with a
specific content-type) and plain HTTP/1.1 (Git Smart HTTP) to share a **single
listen port**.

#### Why one port

Kubernetes services, firewall rules, and load-balancer health probes are all
simpler when a service exposes a single port.  cmux eliminates the need for a
second port or a separate sidecar proxy.

#### Matching rules used in GIT-009

```go
m := cmux.New(lis)                                    // wrap the TCP listener

// gRPC connections carry "application/grpc" in the HTTP/2 Content-Type header.
grpcL := m.MatchWithWriters(
    cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"),
)

// Everything else is treated as Git Smart HTTP (HTTP/1.1).
httpL := m.Match(cmux.Any())

go grpcServer.Serve(grpcL)
go http.Serve(httpL, gitHTTPHandler)
go m.Serve()  // starts the dispatcher loop
```

#### cmux and gRPC

gRPC uses HTTP/2 with TLS or cleartext H2C.  The matcher
`cmux.HTTP2MatchHeaderFieldSendSettings` inspects the HTTP/2 `SETTINGS` frame
(which gRPC clients always send first) and the `Content-Type: application/grpc`
header together, making the match reliable even for cleartext H2C connections.

---

### 9.6 Smart HTTP Endpoint Reference

The `GitHTTPHandler` (`internal/server/githttp.go`) registers four routes.  The
agencyID is the first path segment; all route matching is done by hand in `ServeHTTP`.

| Method | Path pattern | Service | Content-Type (response) |
|---|---|---|---|
| `GET` | `/{agencyID}/info/refs?service=git-upload-pack` | Upload-pack advertisement | `application/x-git-upload-pack-advertisement` |
| `GET` | `/{agencyID}/info/refs?service=git-receive-pack` | Receive-pack advertisement | `application/x-git-receive-pack-advertisement` |
| `POST` | `/{agencyID}/git-upload-pack` | Pack transfer (clone/fetch) | `application/x-git-upload-pack-result` |
| `POST` | `/{agencyID}/git-receive-pack` | Pack transfer (push) | `application/x-git-receive-pack-result` |

All responses include `Cache-Control: no-cache`.

#### `info/refs` response body format

```
<pkt-line "# service=git-upload-pack\n">
<flush-pkt "0000">
<AdvRefs encoded as pkt-lines>
```

`packp.AdvRefs.Encode` emits all of the above in one call once `AdvRefs.Prefix`
is populated as described in §9.3.

---

### 9.7 Library Version Summary

| Library | Version | Role |
|---|---|---|
| `github.com/go-git/go-git/v5` | v5.16.5 | Git engine — all operations |
| `github.com/go-git/go-git/v5/plumbing/transport` | (bundled) | Transport interfaces (`UploadPackSession`, `ReceivePackSession`) |
| `github.com/go-git/go-git/v5/plumbing/transport/server` | (bundled) | Server-side transport engine (`Loader`, `NewServer`) |
| `github.com/go-git/go-git/v5/plumbing/protocol/packp` | (bundled) | Pack protocol message types (`AdvRefs`, `UploadPackRequest`, etc.) |
| `github.com/go-git/go-git/v5/plumbing/format/pktline` | (bundled) | Pkt-line framing (used via `packp`, not directly) |
| `github.com/go-git/go-billy/v5` | v5.8.0 | Working-tree filesystem abstraction |
| `github.com/soheilhy/cmux` | TBD (added in GIT-009) | TCP multiplexer — gRPC + HTTP on one port |
