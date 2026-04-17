# CodeValdGit — Architecture

## 1. Core Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Git engine | [go-git](https://github.com/go-git/go-git) pure-Go | No system `git` binary dependency; embeddable in Go services |
| Repo granularity | Multiple repos per Agency | An agency may own any number of named repositories; each is addressed by its entitygraph ID |
| Agent write policy | Always on a branch, never `main` | Prevents concurrent agent writes from corrupting shared history |
| Branch naming | `task/{task-id}` (convention) | Short-lived, traceable back to CodeValdWork task records |
| Merge strategy | Auto-merge on task completion | No human approval gate in v1; policy layer can extend later |
| Storage backend (v1 library) | Pluggable `Backend` interface wrapping `storage.Storer` | Filesystem and ArangoDB are both valid; caller injects the backend via `cmd/main.go` |
| Storage backend (v2 service) | `entitygraph.DataManager` from CodeValdSharedLib | Git domain objects (Repository, Branch, Blob, Commit) are stored as named entities in the graph; no go-git internal storage layer exposed |
| Worktree filesystem | Pluggable via `billy.Filesystem` interface | go-git separates object storage from the working tree; both are independently injectable (v1 only) |
| Service API | v2 flat `GitManager` interface (`git.go`) | Replaces the nested Backend/RepoManager/Repo hierarchy for the gRPC service; each instance is scoped to one agency at construction time |
| Cross-service events | `CrossPublisher` interface (optional) | Publishes Git lifecycle events to CodeValdCross after successful writes; `nil` = events skipped (useful in unit tests) |
| Service registration | `internal/registrar` heartbeat to Cross every 20 s | All HTTP routes and gRPC method bindings are declared at registration time; Cross needs zero recompile when new routes are added |

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

The single `repoManager` implementation in `internal/manager/` holds a `Backend` and delegates lifecycle calls to it. `NewRepoManager(b Backend)` is the sole constructor — `cmd/main.go` picks and constructs the backend.

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

> **v1 → v2 evolution**: The original plan was to implement go-git's
> `storage.Storer` interface directly in ArangoDB (four collections:
> `git_objects`, `git_refs`, `git_index`, `git_config`). This approach was
> superseded. See [architecture-arangodb.md](architecture-arangodb.md) for the
> full rationale.

**v2 (current)** uses `CodeValdSharedLib/entitygraph` as the storage layer.
Git domain objects are stored as named typed entities:

| Collection | Type | Contents |
|---|---|---|
| `git_entities` | Document | Mutable refs: Repository, Branch, Tag |
| `git_objects` | Document | Immutable content-addressed objects: Commit, Tree, Blob |
| `git_relationships` | **Edge** | All directed graph edges (`has_repository`, `has_branch`, `points_to`, `has_tree`, `has_blob`, `has_parent`) |
| `git_schemas_draft` | Document | Draft TypeDefinition schemas per agency |
| `git_schemas_published` | Document | Published schema snapshots (append-only) |

Named graph: `git_graph` (edge collection: `git_relationships`; vertex collections: `git_entities`, `git_objects`).

Key properties:
- **No staging area** — Blob entities are written directly; no `git_index` collection.
- **Branch refs are entities** — a Branch entity in `git_entities` carries a `head_commit_id` property; ref updates are entity property updates, not document replacements.
- **Immutable objects** — Commit, Tree, and Blob entities in `git_objects` are content-addressed and never mutated after creation.
- **Schema-driven** — `DefaultGitSchema()` in `schema.go` is seeded into the entity graph on startup via `entitygraph.SchemaManager`.

The `storage/arangodb` package in CodeValdGit is a **thin adapter** — it supplies CodeValdGit-specific collection names and graph names to the shared `entitygraph.NewArangoDataManager` from SharedLib.

### Package Layout

```
github.com/aosanya/CodeValdGit/
│
│  ── v2 gRPC service API (current) ─────────────────────────────────────────
├── git.go                  # GitManager interface + CrossPublisher + NewGitManager
├── git_impl_repo.go        # GitManager: repository lifecycle, branches, tags
├── git_impl_fileops.go     # GitManager: file writes, reads, history, diff
├── schema.go               # DefaultGitSchema() — entity types seeded on startup
├── models.go               # Domain model structs (Repository, Branch, Blob, Commit, …)
├── types.go                # Request/response types (CreateRepoRequest, WriteFileRequest, …)
├── errors.go               # Sentinel errors (ErrRepoNotInitialised, ErrBranchNotFound, …)
│
│  ── v1 library API (retained for filesystem backend / library consumers) ───
├── codevaldgit.go          # Backend + RepoManager + Repo interfaces
├── manager.go              # Concrete repoManager wrapping Backend
├── repo.go                 # Concrete Repo implementation over storage.Storer
│
├── internal/
│   ├── server/             # gRPC GitService handler — wraps GitManager (v2)
│   │   ├── server.go       # Handler delegation + toGRPCError
│   │   └── githttp.go      # Git Smart HTTP handler (cmux HTTP/1.1 path)
│   ├── registrar/          # Cross heartbeat — Register RPC every 20 s
│   └── config/             # Config struct + env loader
│
└── storage/
    └── arangodb/           # Thin adapter: provides collection names to entitygraph
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

## 5. Current Service API — v2 GitManager

`GitManager` is the single interface used by the gRPC server (`internal/server/server.go`).
Each instance is scoped to **one agency** — the `agencyID` is fixed at construction time
via `NewGitManager` and is not passed per-call. All implementations must be safe for
concurrent use.

```go
// GitManager is the primary interface for Git repository management.
// gRPC handlers hold this interface — never the concrete type.
// Each instance is scoped to a single agency (agencyID fixed at construction).
type GitManager interface {

    // ── Repository Lifecycle ──────────────────────────────────────────────

    // InitRepo creates a new Repository entity with the given name.
    // Returns ErrRepoAlreadyExists if a repository with the same name exists.
    // Publishes "cross.git.{agencyID}.repo.created" after a successful write.
    InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error)

    // ListRepositories returns all Repository entities owned by this agency.
    ListRepositories(ctx context.Context) ([]Repository, error)

    // GetRepository retrieves a Repository entity by its entitygraph ID.
    // Returns ErrRepoNotInitialised if no repository with that ID exists.
    GetRepository(ctx context.Context, repoID string) (Repository, error)

    // DeleteRepo soft-deletes the specified repository entity.
    // Returns ErrRepoNotInitialised if no repository with that ID exists.
    DeleteRepo(ctx context.Context, repoID string) error

    // PurgeRepo permanently removes the specified repository entity.
    // Returns ErrRepoNotInitialised if no repository with that ID exists.
    PurgeRepo(ctx context.Context, repoID string) error

    // ── Branch Management ─────────────────────────────────────────────────

    // CreateBranch creates a new Branch entity from the specified source.
    // If req.FromBranchID is empty, the repository default branch is used.
    CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error)

    GetBranch(ctx context.Context, branchID string) (Branch, error)
    ListBranches(ctx context.Context) ([]Branch, error)
    DeleteBranch(ctx context.Context, branchID string) error

    // MergeBranch merges the given branch into the repository's default branch.
    // Returns ErrMergeConflict with conflicting paths if auto-rebase fails.
    MergeBranch(ctx context.Context, branchID string) (Branch, error)

    // ── Tag Management ────────────────────────────────────────────────────

    CreateTag(ctx context.Context, req CreateTagRequest) (Tag, error)
    GetTag(ctx context.Context, tagID string) (Tag, error)
    ListTags(ctx context.Context) ([]Tag, error)
    DeleteTag(ctx context.Context, tagID string) error

    // ── File Operations ───────────────────────────────────────────────────

    // WriteFile commits a single file to the specified branch.
    // Creates Commit, Tree, and Blob entities in the entity graph.
    WriteFile(ctx context.Context, req WriteFileRequest) (Commit, error)

    // ReadFile retrieves the Blob entity for a file at the branch's current HEAD.
    ReadFile(ctx context.Context, branchID, path string) (Blob, error)

    // DeleteFile removes a file from the specified branch via a deletion commit.
    DeleteFile(ctx context.Context, req DeleteFileRequest) (Commit, error)

    ListDirectory(ctx context.Context, branchID, path string) ([]FileEntry, error)

    // ── History ───────────────────────────────────────────────────────────

    // Log returns the commit history for the branch, newest to oldest.
    Log(ctx context.Context, branchID string, filter LogFilter) ([]CommitEntry, error)

    // Diff returns per-file change summaries between two refs (branch IDs or commit SHAs).
    Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}

// NewGitManager constructs a GitManager backed by the given DataManager and SchemaManager.
// agencyID is the single agency scoped to this database instance.
// pub may be nil — cross-service events are skipped when no publisher is set.
func NewGitManager(
    dm entitygraph.DataManager,
    sm GitSchemaManager,
    pub CrossPublisher,
    agencyID string,
) GitManager
```

### v1 Library API (Backend / RepoManager / Repo)

The v1 three-interface hierarchy (`codevaldgit.go`, `manager.go`, `repo.go`) remains
in the codebase for the filesystem backend and any library consumers that embed
CodeValdGit directly. It is **not** used by the gRPC server — the server delegates
to `GitManager` (v2) exclusively.

See [architecture-arangodb.md](architecture-arangodb.md) for the full v1 → v2 evolution rationale.

---

## 6. Integration via CodeValdCross

External callers (CodeValdHi, CodeValdAI, and future consumers) never call
CodeValdGit directly. All traffic flows through the CodeValdCross HTTP
management proxy:

```
Caller → POST /{agencyId}/repositories   (HTTP, Cross port 8080)
  → dynamicProxy matches RouteInfo
    → GrpcMethod = "/codevaldgit.v1.GitService/InitRepo"
    → ConnForAgency("codevaldgit", agencyId) → *grpc.ClientConn
    → fetches InitRepoRequest descriptor via gRPC server reflection
    → injects path-param agencyId → grpc.Invoke
      → 200 OK (JSON)
```

CodeValdGit declares all its HTTP routes in its `RegisterRequest` heartbeat.
Adding a new endpoint requires **zero changes to CodeValdCross**.

### GitService gRPC endpoints (declared in `RegisterRequest.routes`)

| HTTP Method | HTTP Path | gRPC Method | Notes |
|---|---|---|---|
| `POST` | `/{agencyId}/repositories` | `InitRepo` | Creates Repository entity |
| `GET` | `/{agencyId}/repositories` | `GetRepository` | Returns the single agency repo |
| `DELETE` | `/{agencyId}/repositories` | `DeleteRepo` | Soft-archives the repo |
| `POST` | `/{agencyId}/branches` | `CreateBranch` | Creates a Branch entity |
| `GET` | `/{agencyId}/branches` | `ListBranches` | Lists all Branch entities |
| `GET` | `/{agencyId}/branches/{branchId}` | `GetBranch` | Returns one Branch |
| `DELETE` | `/{agencyId}/branches/{branchId}` | `DeleteBranch` | Removes Branch entity |
| `POST` | `/{agencyId}/branches/{branchId}/merge` | `MergeBranch` | Merges into default branch |
| `POST` | `/{agencyId}/branches/{branchId}/files` | `WriteFile` | Commits a file to the branch |
| `GET` | `/{agencyId}/branches/{branchId}/files/{path}` | `ReadFile` | Reads Blob at HEAD |
| `GET` | `/{agencyId}/branches/{branchId}/tree` | `ListDirectory` | Lists directory entries |
| `GET` | `/{agencyId}/branches/{branchId}/log` | `Log` | Commit history |
| `GET` | `/{agencyId}/diff` | `Diff` | File diff between two refs |

### Pub/sub events published by CodeValdGit

After each successful mutating operation CodeValdGit publishes a typed event
to CodeValdCross via its `CrossPublisher`:

| Event | Topic | Trigger |
|---|---|---|
| Repository created | `cross.git.{agencyID}.repo.created` | `InitRepo` |
| Branch created | `cross.git.{agencyID}.branch.created` | `CreateBranch` |
| File committed | `cross.git.{agencyID}.file.committed` | `WriteFile` |
| Branch merged | `cross.git.{agencyID}.branch.merged` | `MergeBranch` |

---

## 7. CodeValdSharedLib Dependency

CodeValdGit imports `github.com/aosanya/CodeValdSharedLib` for:

| SharedLib package | What CodeValdGit uses it for |
|---|---|
| `entitygraph` | `DataManager` and `SchemaManager` interfaces — the v2 storage layer; `git_entities`, `git_objects`, and `git_relationships` (edge) are managed through these interfaces |
| `registrar` | Generic Cross heartbeat registrar — sends `Register` RPC to Cross every 20 s; all service-specific metadata (service name, topics, routes) is passed as constructor args |
| `serverutil` | `NewGRPCServer` (enables gRPC reflection for Cross proxy), `RunWithGracefulShutdown`, `EnvOrDefault`, `ParseDurationString` |
| `arangoutil` | `Connect(ctx, Config)` — ArangoDB connection bootstrap in `storage/arangodb` |
| `gen/go/codevaldcross/v1` | Generated Go stubs for the Cross `OrchestratorService` used by the registrar heartbeat |
| `types` | `PathBinding`, `RouteInfo`, `ServiceRegistration` — shared with Cross; used when constructing the `RegisterRequest` routes slice |

> **Principle**: Any infrastructure code used by more than one service lives in
> SharedLib. CodeValdGit retains only its domain logic (`GitManager`), domain
> errors (`errors.go`), gRPC handlers (`internal/server/`), and storage
> schema (`schema.go`).

---

## 8. Integration Test Gate

CodeValdGit is the authoritative Git service for the platform. The integration
test suite (CROSS-IT-001 through CROSS-IT-004) validates the full call path:

```
CodeValdHi / CodeValdAI → Cross HTTP proxy → GitService gRPC → ArangoDB
```

See `CodeValdCross/documentation/3-SofwareDevelopment/mvp-details/integration-test-git.md`
for the full test specification.

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

---

## 10. Production Safety Design Decisions

Three correctness gaps — concurrency, merge strategy, and transaction
boundaries — are documented in separate domain files.

| Topic | File | Task |
|---|---|---|
| Concurrency and atomic ref updates — `RefLocker`, CAS on `head_commit_id`, per-agency merge serialisation | [architecture-concurrency.md](architecture-concurrency.md) | GIT-011 |
| Merge strategy: squash merge, fork-point tracking, conflict surface | [architecture-merge.md](architecture-merge.md) | GIT-012 |
| Transaction boundaries and idempotency — atomicity rules, `MergeRequest`, retry-safety matrix | [architecture-transactions.md](architecture-transactions.md) | GIT-013 |

| ArangoDB backend design — v1/v2 evolution, object deduplication, Smart HTTP limitation, production gate | [architecture-arangodb.md](architecture-arangodb.md) | GIT-014 |