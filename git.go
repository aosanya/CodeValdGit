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
// Concrete implementations are provided in internal/manager/. Storage is
// injected via [entitygraph.DataManager] so the manager is backend-agnostic.
package codevaldgit

import (
	"context"
	"fmt"

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

	// InitRepo creates the single Repository entity for this agency.
	// Returns [ErrRepoAlreadyExists] if a repository entity already exists.
	// Publishes "cross.git.{agencyID}.repo.created" after a successful write.
	InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error)

	// GetRepository retrieves the single Repository entity for this agency.
	// Returns [ErrRepoNotInitialised] if no repository has been created yet.
	GetRepository(ctx context.Context) (Repository, error)

	// DeleteRepo marks the repository entity as archived (soft delete).
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	DeleteRepo(ctx context.Context) error

	// PurgeRepo permanently removes all repository data for this agency.
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	PurgeRepo(ctx context.Context) error

	// ── Branch Management ─────────────────────────────────────────────────────

	// CreateBranch creates a new Branch entity from the specified source.
	// If req.FromBranchID is empty, the repository default branch is used.
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	// Returns [ErrBranchExists] if a branch with the given name already exists.
	CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error)

	// GetBranch retrieves a Branch entity by its entitygraph ID.
	// Returns [ErrBranchNotFound] if no branch with that ID exists.
	GetBranch(ctx context.Context, branchID string) (Branch, error)

	// ListBranches returns all Branch entities for this agency's repository.
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	ListBranches(ctx context.Context) ([]Branch, error)

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

	// ListTags returns all Tag entities for this agency's repository.
	// Returns [ErrRepoNotInitialised] if no repository entity exists.
	ListTags(ctx context.Context) ([]Tag, error)

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

// ── Repository Lifecycle stubs ────────────────────────────────────────────────

// InitRepo is a stub; full implementation added in GIT-005.
func (m *gitManager) InitRepo(_ context.Context, _ CreateRepoRequest) (Repository, error) {
	return Repository{}, fmt.Errorf("not implemented")
}

// GetRepository is a stub; full implementation added in GIT-005.
func (m *gitManager) GetRepository(_ context.Context) (Repository, error) {
	return Repository{}, fmt.Errorf("not implemented")
}

// DeleteRepo is a stub; full implementation added in GIT-005.
func (m *gitManager) DeleteRepo(_ context.Context) error {
	return fmt.Errorf("not implemented")
}

// PurgeRepo is a stub; full implementation added in GIT-005.
func (m *gitManager) PurgeRepo(_ context.Context) error {
	return fmt.Errorf("not implemented")
}

// ── Branch Management stubs ───────────────────────────────────────────────────

// CreateBranch is a stub; full implementation added in GIT-005.
func (m *gitManager) CreateBranch(_ context.Context, _ CreateBranchRequest) (Branch, error) {
	return Branch{}, fmt.Errorf("not implemented")
}

// GetBranch is a stub; full implementation added in GIT-005.
func (m *gitManager) GetBranch(_ context.Context, _ string) (Branch, error) {
	return Branch{}, fmt.Errorf("not implemented")
}

// ListBranches is a stub; full implementation added in GIT-005.
func (m *gitManager) ListBranches(_ context.Context) ([]Branch, error) {
	return nil, fmt.Errorf("not implemented")
}

// DeleteBranch is a stub; full implementation added in GIT-005.
func (m *gitManager) DeleteBranch(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

// MergeBranch is a stub; full implementation added in GIT-005.
func (m *gitManager) MergeBranch(_ context.Context, _ string) (Branch, error) {
	return Branch{}, fmt.Errorf("not implemented")
}

// ── Tag Management stubs ──────────────────────────────────────────────────────

// CreateTag is a stub; full implementation added in GIT-005.
func (m *gitManager) CreateTag(_ context.Context, _ CreateTagRequest) (Tag, error) {
	return Tag{}, fmt.Errorf("not implemented")
}

// GetTag is a stub; full implementation added in GIT-005.
func (m *gitManager) GetTag(_ context.Context, _ string) (Tag, error) {
	return Tag{}, fmt.Errorf("not implemented")
}

// ListTags is a stub; full implementation added in GIT-005.
func (m *gitManager) ListTags(_ context.Context) ([]Tag, error) {
	return nil, fmt.Errorf("not implemented")
}

// DeleteTag is a stub; full implementation added in GIT-005.
func (m *gitManager) DeleteTag(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

// ── File Operation stubs ──────────────────────────────────────────────────────

// WriteFile is a stub; full implementation added in GIT-005.
func (m *gitManager) WriteFile(_ context.Context, _ WriteFileRequest) (Commit, error) {
	return Commit{}, fmt.Errorf("not implemented")
}

// ReadFile is a stub; full implementation added in GIT-005.
func (m *gitManager) ReadFile(_ context.Context, _, _ string) (Blob, error) {
	return Blob{}, fmt.Errorf("not implemented")
}

// DeleteFile is a stub; full implementation added in GIT-005.
func (m *gitManager) DeleteFile(_ context.Context, _ DeleteFileRequest) (Commit, error) {
	return Commit{}, fmt.Errorf("not implemented")
}

// ListDirectory is a stub; full implementation added in GIT-005.
func (m *gitManager) ListDirectory(_ context.Context, _, _ string) ([]FileEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

// ── History stubs ─────────────────────────────────────────────────────────────

// Log is a stub; full implementation added in GIT-005.
func (m *gitManager) Log(_ context.Context, _ string, _ LogFilter) ([]CommitEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

// Diff is a stub; full implementation added in GIT-005.
func (m *gitManager) Diff(_ context.Context, _, _ string) ([]FileDiff, error) {
	return nil, fmt.Errorf("not implemented")
}
