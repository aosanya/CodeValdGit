// Package codevaldgit provides Git-based artifact versioning for CodeValdCortex.
//
// AI agents inside CodeValdCortex produce artifacts (code, Markdown, configs,
// reports, and any other file type). This library manages the storage,
// versioning, and lifecycle of those artifacts using real Git semantics via
// go-git — no system git binary is required.
//
// # Three-Interface Design
//
// The library exposes three top-level interfaces:
//
//   - [Backend] — storage-specific repo lifecycle (init, open storer, archive, purge).
//     Implemented by storage/filesystem and storage/arangodb.
//     The caller (CodeValdCortex) constructs the desired Backend and passes it
//     to [NewRepoManager].
//
//   - [RepoManager] — top-level lifecycle management, backend-agnostic.
//     One RepoManager is shared process-wide; it delegates to the Backend for
//     storage operations and constructs [Repo] instances via [internal/repo].
//
//   - [Repo] — per-agency repository operations (branches, files, history, diffs).
//     Obtained via [RepoManager.OpenRepo]. Backed by internal/repo — the same
//     implementation regardless of which Backend is in use.
//
// # Branch-Per-Task Workflow
//
// Agents must never commit directly to main. Every write goes through a task
// branch (task/{task-id}). MergeBranch auto-rebases and fast-forward merges
// the task branch back to main on task completion.
//
// # Storage Backends
//
// Two Backend implementations are provided out of the box:
//   - storage/filesystem — stores .git dirs on disk; DeleteRepo archives via os.Rename.
//   - storage/arangodb  — stores Git objects in ArangoDB; survives container restarts.
package codevaldgit

import (
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/storage"
)

// Backend abstracts storage-specific repo lifecycle operations.
// Implemented by storage/filesystem and storage/arangodb.
// Construct the desired Backend using NewFilesystemBackend or NewArangoBackend,
// then pass it to [NewRepoManager].
type Backend interface {
	// InitRepo provisions a new Git store for the given agencyID and repoName.
	// Returns [ErrRepoAlreadyExists] if a store already exists for that pair.
	InitRepo(ctx context.Context, agencyID, repoName string) error

	// OpenStorer returns a go-git storage.Storer and billy.Filesystem for the
	// named repository within agencyID.
	// Called internally by [RepoManager.OpenRepo] to construct a Repo.
	// Returns [ErrRepoNotFound] if no store exists for (agencyID, repoName).
	OpenStorer(ctx context.Context, agencyID, repoName string) (storage.Storer, billy.Filesystem, error)

	// DeleteRepo archives or flags the agency repo as deleted (behaviour is backend-specific).
	// Filesystem: os.Rename to ArchivePath (non-destructive; repo remains valid).
	// ArangoDB: sets a deleted flag on all agency documents (auditable).
	// Returns [ErrRepoNotFound] if no live store exists for agencyID.
	DeleteRepo(ctx context.Context, agencyID string) error

	// PurgeRepo permanently removes all storage for agencyID.
	// Filesystem: os.RemoveAll of the archive directory.
	// ArangoDB: deletes all documents where agencyID matches.
	// Returns [ErrRepoNotFound] if the target does not exist.
	PurgeRepo(ctx context.Context, agencyID string) error
}

// RepoManager is the top-level entry point for creating and managing
// per-agency Git repositories. Obtain an instance via [NewRepoManager].
// One RepoManager is typically shared process-wide.
type RepoManager interface {
	// InitRepo creates a new empty Git repository for the given agency and
	// repository name. Delegates to [Backend.InitRepo].
	// Returns [ErrRepoAlreadyExists] if a repo already exists for (agencyID, repoName).
	InitRepo(ctx context.Context, agencyID, repoName string) error

	// OpenRepo opens an existing repository by agency ID and repository name,
	// returning a backend-agnostic [Repo]. Delegates to [Backend.OpenStorer].
	// Returns [ErrRepoNotFound] if no store exists for (agencyID, repoName).
	OpenRepo(ctx context.Context, agencyID, repoName string) (Repo, error)

	// DeleteRepo delegates to [Backend.DeleteRepo].
	// For the filesystem backend this archives the repo to ArchivePath.
	// For the ArangoDB backend this sets a deleted flag on agency documents.
	// Returns [ErrRepoNotFound] if the live repo does not exist.
	DeleteRepo(ctx context.Context, agencyID string) error

	// PurgeRepo delegates to [Backend.PurgeRepo] and permanently removes all
	// storage for the agency. This is irreversible.
	// Returns [ErrRepoNotFound] if the target does not exist.
	PurgeRepo(ctx context.Context, agencyID string) error
}

// Repo represents a single agency's Git repository. Obtained via
// [RepoManager.OpenRepo]. All write operations require a taskID that
// identifies the task branch (task/{taskID}). Read operations accept any ref:
// branch name, tag name, or full commit SHA.
type Repo interface {
	// CreateBranch creates refs/heads/task/{taskID} from the current main HEAD.
	// Returns [ErrBranchExists] if the branch already exists.
	CreateBranch(ctx context.Context, taskID string) error

	// MergeBranch merges task/{taskID} into main.
	// Attempts a fast-forward merge first. If main has advanced since the branch
	// was created, auto-rebases the task branch onto current main and then
	// fast-forward merges. On a content conflict, returns *[ErrMergeConflict]
	// with the conflicting file paths; the branch is left in a clean state.
	// Returns [ErrBranchNotFound] if the task branch does not exist.
	MergeBranch(ctx context.Context, taskID string) error

	// DeleteBranch removes refs/heads/task/{taskID}.
	// Returns [ErrBranchNotFound] if the branch does not exist.
	DeleteBranch(ctx context.Context, taskID string) error

	// WriteFile commits content to path on task/{taskID}. The branch must already
	// exist — call [Repo.CreateBranch] first. author is the agent ID or username
	// recorded in the commit. message is the human-readable commit message;
	// the task ID is also embedded by the implementation.
	// Returns [ErrBranchNotFound] if the task branch does not exist.
	WriteFile(ctx context.Context, taskID, path, content, author, message string) error

	// ReadFile returns the content of path at the given ref.
	// ref may be a branch name, tag name, or full commit SHA.
	// Returns [ErrRefNotFound] if the ref cannot be resolved, or
	// [ErrFileNotFound] if the path does not exist at that ref.
	ReadFile(ctx context.Context, ref, path string) (string, error)

	// DeleteFile removes path from task/{taskID} as a new commit.
	// The branch must already exist. Returns [ErrBranchNotFound] or
	// [ErrFileNotFound] as appropriate.
	DeleteFile(ctx context.Context, taskID, path, author, message string) error

	// ListDirectory returns the immediate children of path at the given ref.
	// ref may be a branch name, tag name, or full commit SHA.
	// Returns [ErrRefNotFound] if the ref cannot be resolved.
	// An empty path ("") lists the repository root.
	ListDirectory(ctx context.Context, ref, path string) ([]FileEntry, error)

	// Log returns the ordered list of commits that touched path, newest first.
	// ref constrains the walk to the history reachable from that ref.
	// An empty path ("") returns the full commit history for the ref.
	Log(ctx context.Context, ref, path string) ([]CommitEntry, error)

	// Diff returns per-file changes between fromRef and toRef.
	// Both refs may be branch names, tag names, or full commit SHAs.
	// Returns [ErrRefNotFound] if either ref cannot be resolved.
	// Safe to call concurrently.
	Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)
}
