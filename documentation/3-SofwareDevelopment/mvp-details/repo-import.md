# Repository Import (GIT-016)

Topics: Import · Clone · Async Job · Public HTTPS

---

## Sub-Tasks

| ID | Title | Depends On | Status |
|---|---|---|---|
| GIT-016a | `ImportJob` TypeDefinition + `git_importjobs` collection in `schema.go` | ~~GIT-001~~ ✅ | 📋 Not Started |
| GIT-016b | Types (`ImportRepoRequest`, `ImportJob`) + errors + `GitManager` interface additions | GIT-016a | 📋 Not Started |
| GIT-016c | Core implementation: background goroutine, go-git clone, all-branch entity walk, cancel map | GIT-016b | 📋 Not Started |
| GIT-016d | Proto additions (3 RPCs: `ImportRepo`, `GetImportStatus`, `CancelImport`) + `buf generate` | GIT-016b | 📋 Not Started |
| GIT-016e | gRPC server handlers + error mapping for all 3 RPCs | GIT-016c, GIT-016d | 📋 Not Started |
| GIT-016f | Unit tests — import manager, cancel, concurrency rejection | GIT-016e | 📋 Not Started |

---

## Overview

`ImportRepo` ingests an existing external Git repository (e.g.
`https://github.com/aosanya/CodeValdGit`) into CodeValdGit, representing it
fully in the entity graph. It is a **separate method** on `GitManager` — not
a variant of `InitRepo`.

The call returns immediately with a **Job ID**. Ingestion runs asynchronously
in the background. The caller polls `GetImportStatus(jobID)` to track
progress and detect completion or failure.

---

## Confirmed Decisions

| # | Decision | Detail |
|---|---|---|
| 1 | **Full history** | All commits, trees, and blobs are ingested — not a HEAD-only snapshot |
| 2 | **All branches** | Every remote branch is imported with its full commit history |
| 3 | **Separate method** | `ImportRepo` is its own `GitManager` method; `InitRepo` is unchanged |
| 4 | **Public HTTPS only** | No credentials for MVP — private repos are a future extension |
| 5 | **Async** | `ImportRepo` returns immediately; ingestion runs in a background goroutine |
| 6 | **Job ID tracking** | Returns a `jobID`; caller polls `GetImportStatus(jobID)` for status |

---

## Acceptance Criteria

- [ ] `GitManager` gains `ImportRepo(ctx, ImportRepoRequest) (ImportJob, error)`
- [ ] `GitManager` gains `GetImportStatus(ctx, jobID string) (ImportJob, error)`
- [ ] `ImportRepo` returns `ErrRepoAlreadyExists` if the agency already has a repository entity
- [ ] `ImportRepo` returns `ErrImportInProgress` if a job with status `pending` or `running` already exists for this agency
- [ ] Background goroutine: clones remote URL via `go-git` (`gogit.PlainCloneContext`) into a temp dir, then walks all branches and commits, writing entity graph entities
- [ ] All branches and their full commit histories are ingested
- [ ] `ImportJob.Status` transitions: `pending → running → completed | failed | cancelled`
- [ ] `CancelImport(ctx, jobID string) error` cancels a pending or running import
- [ ] On cancellation, the background goroutine's context is cancelled, temp dir cleaned up, status set to `cancelled`
- [ ] On failure, `ImportJob.ErrorMessage` contains a human-readable reason
- [ ] Temp clone directory is cleaned up on completion, failure, or cancellation
- [ ] `cross.git.{agencyID}.repo.imported` published on successful completion
- [ ] `cross.git.{agencyID}.repo.import.failed` published on failure
- [ ] `cross.git.{agencyID}.repo.import.cancelled` published on cancellation

---

## Interface Changes

### `git.go` additions

```go
// ImportRepoRequest carries the parameters for an async repository import.
type ImportRepoRequest struct {
    // Name is the human-readable repository name stored on the Repository entity.
    Name string
    // Description is an optional description stored on the Repository entity.
    Description string
    // SourceURL is the public HTTPS URL of the remote Git repository to clone.
    // Private repositories are not supported in v1 — no credentials are accepted.
    // Example: "https://github.com/aosanya/CodeValdGit"
    SourceURL string
    // DefaultBranch is the name of the branch to mark as the repository default.
    // If empty, defaults to "main".
    DefaultBranch string
}

// ImportJob represents the state of an async repository import operation.
type ImportJob struct {
    // ID is the stable job identifier returned by ImportRepo.
    ID string
    // AgencyID scopes this job to the owning agency.
    AgencyID string
    // SourceURL is the remote URL being imported.
    SourceURL string
    // Status is one of: "pending", "running", "completed", "failed".
    Status string
    // ErrorMessage is populated when Status == "failed".
    ErrorMessage string
    // CreatedAt is the ISO 8601 timestamp at which ImportRepo was called.
    CreatedAt string
    // UpdatedAt is the ISO 8601 timestamp of the last status transition.
    UpdatedAt string
}

// ImportRepo begins an async import of a public Git repository into this
// agency's entity graph. It returns immediately with an ImportJob whose
// ID can be used to poll GetImportStatus.
//
// Returns [ErrRepoAlreadyExists] if a Repository entity already exists for
// this agency. Each agency supports exactly one repository.
ImportRepo(ctx context.Context, req ImportRepoRequest) (ImportJob, error)

// GetImportStatus returns the current state of an import job.
//
// Returns [ErrImportJobNotFound] if no job with the given ID exists for
// this agency.
GetImportStatus(ctx context.Context, jobID string) (ImportJob, error)

// CancelImport cancels a pending or running import job. The background
// goroutine's context is cancelled, any partial entity writes are left as-is,
// and the temp clone directory is removed.
//
// Returns [ErrImportJobNotFound] if the job does not exist.
// Returns [ErrImportJobNotCancellable] if the job has already reached a
// terminal state (completed, failed, or cancelled).
CancelImport(ctx context.Context, jobID string) error
```

### New errors (`errors.go`)

```go
// ErrImportJobNotFound is returned by GetImportStatus when the jobID does
// not correspond to any import job for this agency.
var ErrImportJobNotFound = errors.New("import job not found")

// ErrImportInProgress is returned by ImportRepo when an import job is
// already running for this agency (prevents duplicate concurrent imports).
var ErrImportInProgress = errors.New("import already in progress")

// ErrImportJobNotCancellable is returned by CancelImport when the job has
// already reached a terminal state (completed, failed, or cancelled).
var ErrImportJobNotCancellable = errors.New("import job is not cancellable")
```

---

## Ingestion Pipeline (Background Goroutine)

```
ImportRepo called
    │
    ├── 1. Check for existing job with status pending|running → return ErrImportInProgress if found
    ├── 2. Create ImportJob entity (status=pending), return job ID
    │
    └── goroutine starts:
            │
            ├── 3. Update status → running
            ├── 4. gogit.PlainCloneContext(ctx, tempDir, &CloneOptions{URL, NoCheckout: false})
            ├── 5. Iterate remote branches (repo.References())
            │       For each branch:
            │       ├── Upsert Branch entity (keyed by branch name)
            │       └── Walk commits (oldest → newest via LogOptions{Order: git.LogOrderCommitterTime})
            │               For each commit:
            │               ├── Upsert Commit entity (keyed by SHA — overwrites on retry)
            │               ├── Walk tree (commit.Tree())
            │               │       For each blob: Upsert Blob entity (keyed by SHA)
            │               │       Upsert Tree entity (keyed by SHA)
            │               └── Upsert relationships (has_commit, has_tree, has_blob, etc.)
            │
            │       Note: all writes use upsert semantics. If the SHA already
            │       exists from a prior partial import, the entity is overwritten
            │       with complete data. This guarantees consistency on retry
            │       without requiring cleanup of partial state.
            ├── 6. Create Repository entity with default branch set
            ├── 7. Publish cross.git.{agencyID}.repo.imported
            ├── 8. Update status → completed
            └── 9. os.RemoveAll(tempDir)

    On any error:
            ├── Update status → failed, set ErrorMessage
            ├── Publish cross.git.{agencyID}.repo.import.failed
            └── os.RemoveAll(tempDir)

    On CancelImport(jobID):
            ├── Look up cancel func for jobID in in-process cancel map
            ├── Call cancelFunc() → goroutine's ctx.Done() fires
            ├── Update status → cancelled
            ├── Publish cross.git.{agencyID}.repo.import.cancelled
            └── os.RemoveAll(tempDir) (goroutine cleans up on ctx cancellation)
```

### go-git Clone Options (public HTTPS)

```go
cloneOpts := &git.CloneOptions{
    URL:          req.SourceURL,
    // No Auth field — public repos only in v1
    // Fetch all remote branches:
    Tags:         git.AllTags,
    SingleBranch: false,
}
```

---

## Job Storage

Import jobs use a **dedicated `ImportJob` TypeDefinition** added to
`DefaultGitSchema()` in `schema.go`. Jobs are stored in the
`git_importjobs` collection, scoped to the agency.

### New TypeDefinition (schema.go)

```go
{
    Name:              "ImportJob",
    StorageCollection: "git_importjobs",
    Properties: []entitygraph.PropertyDefinition{
        {Name: "agency_id",     Type: "string", Required: true},
        {Name: "source_url",    Type: "string", Required: true},
        {Name: "status",        Type: "string", Required: true}, // pending|running|completed|failed|cancelled
        {Name: "error_message", Type: "string"},
        {Name: "created_at",    Type: "string", Required: true},
        {Name: "updated_at",    Type: "string", Required: true},
    },
},
```

The `git_importjobs` collection is created alongside the other `git_*`
collections during `DefaultGitSchema()` bootstrapping — no manual ArangoDB
setup required.

---

## Proto Changes (`service.proto`)

Two new RPCs on `GitService`:

```protobuf
// ImportRepo begins an async import of a public Git repository.
// Returns immediately with a job ID; poll GetImportStatus for progress.
// Error: ALREADY_EXISTS if a repository already exists for the agency.
// Error: FAILED_PRECONDITION if an import is already in progress.
rpc ImportRepo(ImportRepoRequest) returns (ImportRepoResponse);

// GetImportStatus returns the current state of an import job.
// Error: NOT_FOUND if no job with the given ID exists.
rpc GetImportStatus(GetImportStatusRequest) returns (ImportJobResponse);

// CancelImport cancels a pending or running import job.
// Error: NOT_FOUND if no job with the given ID exists.
// Error: FAILED_PRECONDITION if the job is already in a terminal state.
rpc CancelImport(CancelImportRequest) returns (CancelImportResponse);

// ── Messages ──────────────────────────────────────────────────────────────

message ImportRepoRequest {
  string agency_id      = 1;
  string name           = 2;
  string description    = 3;
  string source_url     = 4;
  string default_branch = 5;
}

message ImportRepoResponse {
  string job_id = 1;
}

message GetImportStatusRequest {
  string agency_id = 1;
  string job_id    = 2;
}

message CancelImportRequest {
  string agency_id = 1;
  string job_id    = 2;
}
message CancelImportResponse {}

message ImportJobResponse {
  string job_id       = 1;
  string agency_id    = 2;
  string source_url   = 3;
  string status       = 4; // "pending" | "running" | "completed" | "failed" | "cancelled"
  string error_message = 5;
  string created_at   = 6;
  string updated_at   = 7;
}
```

### gRPC Error Code Mapping (additions)

| Go Error | gRPC `codes` |
|---|---|
| `ErrImportJobNotFound` | `codes.NotFound` |
| `ErrImportInProgress` | `codes.FailedPrecondition` |
| `ErrImportJobNotCancellable` | `codes.FailedPrecondition` |
| `ErrRepoAlreadyExists` (on ImportRepo) | `codes.AlreadyExists` |

---

## Pub/Sub Events

| Topic | Trigger | Payload |
|---|---|---|
| `cross.git.{agencyID}.repo.imported` | Import completed successfully | `{ "job_id": "...", "repository_id": "..." }` |
| `cross.git.{agencyID}.repo.import.failed` | Import failed | `{ "job_id": "...", "error": "..." }` |
| `cross.git.{agencyID}.repo.import.cancelled` | Import cancelled by caller | `{ "job_id": "..." }` |

---

## Open Questions (from research session)

| # | Question | Status |
|---|---|---|
| Q7 | Should `cancelled` be a valid job status? Is a `CancelImport` RPC needed? | ✅ Yes — `cancelled` state + `CancelImport` RPC + `cross.git.{agencyID}.repo.import.cancelled` event |
| Q8 | Job entity storage: reuse `GitInternalState` or add `ImportJob` TypeDefinition to schema? | ✅ New `ImportJob` TypeDefinition in `schema.go`, stored in `git_importjobs` collection |
| Q9 | Deduplication: if blobs/commits with the same SHA already exist (e.g. prior import), upsert or skip? | ✅ Upsert — always overwrite; guarantees complete entity data on retry |
| Q10 | Concurrency: should concurrent `ImportRepo` calls on the same agency return `ErrImportInProgress` or queue? | ✅ Reject — return `ErrImportInProgress` immediately if a `pending` or `running` job exists |

---

## Dependencies

| Depends On | Reason |
|---|---|
| ~~GIT-001~~ ✅ | Schema — `Repository`, `Branch`, `Commit`, `Tree`, `Blob`, `GitInternalState` TypeDefinitions |
| ~~GIT-002~~ ✅ | `GitManager` interface — `ImportRepo` and `GetImportStatus` are new methods |
| ~~GIT-004~~ ✅ | ArangoDB entitygraph backend — entity writes during ingestion |
| ~~GIT-005~~ ✅ | Concrete `gitManager` — `ImportRepo` is implemented here |
| ~~GIT-006~~ ✅ | gRPC server — new RPC handlers for `ImportRepo` and `GetImportStatus` |
