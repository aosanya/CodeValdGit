// git.go defines the v2 flat [GitManager] interface for CodeValdGit.
//
// The v2 design replaces the nested Backend/RepoManager/Repo hierarchy with a
// single Agency/AI-aligned interface. Each [GitManager] instance is scoped to
// one agency; the agencyID is fixed at construction time via [NewGitManager].
//
// All domain operations — repository lifecycle, branches, tags, file writes,
// and history — are methods on [GitManager]. Callers (typically a gRPC server
// handler) hold the interface, never the concrete type.
//
// The concrete [gitManager] implementation lives in git_impl_repo.go
// (repository lifecycle, branch management, tag management) and
// git_impl_fileops.go (file operations, commit history, diff).
// Storage is injected via [entitygraph.DataManager] so the manager is
// backend-agnostic.
package codevaldgit

import (
	"context"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// GitSchemaManager is a type alias for [entitygraph.SchemaManager].
// Used in cmd/main.go to seed [DefaultGitSchema] on startup via SetSchema.
type GitSchemaManager = entitygraph.SchemaManager

// CrossPublisher publishes Git lifecycle events to CodeValdCross.
// Implementations must be safe for concurrent use. A nil CrossPublisher is
// valid — publish calls are silently skipped.
type CrossPublisher interface {
	// Publish delivers an event for the given topic and agencyID to
	// CodeValdCross. Errors are non-fatal: implementations should log and
	// return nil for best-effort delivery.
	Publish(ctx context.Context, topic string, agencyID string) error
}

// GitManager is the primary interface for Git repository management.
// gRPC handlers hold this interface — never the concrete type.
//
// Each GitManager instance is scoped to a single agency. The agencyID is
// fixed at construction time via [NewGitManager].
//
// Implementations must be safe for concurrent use.
type GitManager interface {
	// ── Repository Lifecycle ──────────────────────────────────────────────────

	// InitRepo creates a new Repository entity for this agency.
	// Returns [ErrRepoAlreadyExists] if a repository with the same name already exists.
	// Publishes "cross.git.{agencyID}.repo.created" after a successful write.
	InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error)

	// ListRepositories returns all Repository entities for this agency.
	ListRepositories(ctx context.Context) ([]Repository, error)

	// GetRepository retrieves a Repository entity by its ID.
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	GetRepository(ctx context.Context, repoID string) (Repository, error)

	// GetRepositoryByName retrieves a Repository entity by its human-readable
	// name. Returns [ErrRepoNotInitialised] if no repository with that name
	// exists for this agency.
	GetRepositoryByName(ctx context.Context, repoName string) (Repository, error)

	// DeleteRepo marks the specified repository entity as archived (soft delete).
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	DeleteRepo(ctx context.Context, repoID string) error

	// PurgeRepo permanently removes all data for the specified repository.
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	PurgeRepo(ctx context.Context, repoID string) error

	// ── Branch Management ─────────────────────────────────────────────────────

	// CreateBranch creates a new Branch entity from the specified source.
	// If req.FromBranchID is empty, the repository default branch is used.
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	// Returns [ErrBranchExists] if a branch with the given name already exists.
	CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error)

	// GetBranch retrieves a Branch entity by its entitygraph ID.
	// Returns [ErrBranchNotFound] if no branch with that ID exists.
	GetBranch(ctx context.Context, branchID string) (Branch, error)

	// ListBranches returns all Branch entities for the specified repository.
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	ListBranches(ctx context.Context, repoID string) ([]Branch, error)

	// DeleteBranch removes a Branch entity.
	// Returns [ErrBranchNotFound] if no branch with that ID exists.
	// Returns an error if branchID refers to the repository's default branch.
	DeleteBranch(ctx context.Context, branchID string) error

	// MergeBranch merges the given branch into the repository's default branch.
	// Returns the updated default Branch on success.
	// Returns [ErrMergeConflict] with conflicting paths if a rebase conflict
	// cannot be auto-resolved.
	// Returns [ErrBranchNotFound] if no branch with that ID exists.
	MergeBranch(ctx context.Context, branchID string) (Branch, error)

	// ── Tag Management ────────────────────────────────────────────────────────

	// CreateTag creates an immutable Tag entity pointing to the specified commit.
	// Returns [ErrTagAlreadyExists] if a tag with the given name already exists.
	// Returns [ErrBranchNotFound] if req.CommitID does not resolve to a Commit
	// entity.
	CreateTag(ctx context.Context, req CreateTagRequest) (Tag, error)

	// GetTag retrieves a Tag entity by its entitygraph ID.
	// Returns [ErrTagNotFound] if no tag with that ID exists.
	GetTag(ctx context.Context, tagID string) (Tag, error)

	// ListTags returns all Tag entities for the specified repository.
	// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
	ListTags(ctx context.Context, repoID string) ([]Tag, error)

	// DeleteTag removes a Tag entity.
	// Returns [ErrTagNotFound] if no tag with that ID exists.
	DeleteTag(ctx context.Context, tagID string) error

	// ── File Operations ───────────────────────────────────────────────────────

	// WriteFile commits a single file to the specified branch, creating
	// Commit, Tree, and Blob entities in the entity graph.
	// Returns [ErrBranchNotFound] if the branch does not exist.
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	WriteFile(ctx context.Context, req WriteFileRequest) (Commit, error)

	// ReadFile retrieves the Blob entity for a file at the branch's current HEAD.
	// Returns [ErrBranchNotFound] if the branch does not exist.
	// Returns [ErrFileNotFound] if the path does not exist on the branch.
	ReadFile(ctx context.Context, branchID, path string) (Blob, error)

	// DeleteFile removes a file from the specified branch by creating a deletion
	// commit. Returns [ErrBranchNotFound] if the branch does not exist.
	// Returns [ErrFileNotFound] if the path does not exist on the branch.
	DeleteFile(ctx context.Context, req DeleteFileRequest) (Commit, error)

	// ListDirectory returns the immediate children (files and sub-directories)
	// at the given path on the branch.
	// Returns [ErrBranchNotFound] if the branch does not exist.
	// Returns [ErrFileNotFound] if the path does not exist on the branch.
	ListDirectory(ctx context.Context, branchID, path string) ([]FileEntry, error)

	// ── History ───────────────────────────────────────────────────────────────

	// Log returns the commit history for the branch, optionally filtered to a
	// specific file path via filter.Path. Results are ordered newest to oldest.
	// Returns [ErrBranchNotFound] if the branch does not exist.
	Log(ctx context.Context, branchID string, filter LogFilter) ([]CommitEntry, error)

	// Diff returns per-file change summaries between two refs.
	// fromRef and toRef may be branch IDs or commit SHAs.
	// Returns [ErrRefNotFound] if either ref cannot be resolved.
	Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error)

	// ── Repository Import ───────────────────────────────────────────────

	// ImportRepo begins an async import of a public Git repository into this
	// agency's entity graph. It returns immediately with an ImportJob whose
	// ID can be used to poll GetImportStatus.
	//
	// Returns [ErrRepoAlreadyExists] if a Repository entity with the same name
	// already exists for this agency.
	// Returns [ErrImportInProgress] if a job with status "pending" or "running"
	// already exists for this agency.
	ImportRepo(ctx context.Context, req ImportRepoRequest) (ImportJob, error)

	// GetImportStatus returns the current state of an import job.
	// Returns [ErrImportJobNotFound] if no job with the given ID exists for
	// this agency.
	GetImportStatus(ctx context.Context, jobID string) (ImportJob, error)

	// CancelImport cancels a pending or running import job. The background
	// goroutine's context is cancelled and the temp clone directory is removed.
	// Returns [ErrImportJobNotFound] if the job does not exist.
	// Returns [ErrImportJobNotCancellable] if the job has already reached a
	// terminal state (completed, failed, or cancelled).
	CancelImport(ctx context.Context, jobID string) error
}

// gitManager is the concrete implementation of [GitManager].
// It wraps [entitygraph.DataManager] to expose Git-specific convenience
// methods over the entity graph.
// Method bodies are stubs — implementations are added in GIT-005/GIT-006.
type gitManager struct {
	dm        entitygraph.DataManager // graph CRUD — injected by cmd/main.go
	sm        GitSchemaManager        // schema versioning — injected by cmd/main.go
	publisher CrossPublisher          // optional; nil = skip event publishing
	agencyID  string                  // the single agency ID for this database
}

// NewGitManager constructs a [GitManager] backed by the given
// [entitygraph.DataManager] and [GitSchemaManager].
// agencyID is the single agency scoped to this database instance.
// pub may be nil — cross-service events are skipped when no publisher is set.
func NewGitManager(
	dm entitygraph.DataManager,
	sm GitSchemaManager,
	pub CrossPublisher,
	agencyID string,
) GitManager {
	return &gitManager{
		dm:        dm,
		sm:        sm,
		publisher: pub,
		agencyID:  agencyID,
	}
}
