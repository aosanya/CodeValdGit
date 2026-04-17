// Package codevaldgit provides Git-based artifact versioning for the
// CodeVald platform. Value types used by GitManager and its callers are
// defined here.
//
// These are the v2 entitygraph-aligned domain types. Each struct mirrors a
// TypeDefinition declared in [DefaultGitSchema] and is used as the Go
// representation when reading entities from the entitygraph DataManager.
//
// Storage mapping:
//   - Repository, Branch, Tag → "git_entities" collection (mutable refs)
//   - Commit, Tree, Blob       → "git_objects" collection (immutable, content-addressed)
//   - All edges                → "git_relationships" edge collection
package codevaldgit

// Agency is the root entity for an agency in CodeValdGit.
// Each agency may own one or more [Repository] entities linked via
// has_repository edges in the entity graph.
type Agency struct {
	// ID is the unique entitygraph identifier for this agency.
	ID string `json:"id"`

	// Name is the human-readable agency label.
	Name string `json:"name"`

	// Description is an optional free-text description of the agency.
	Description string `json:"description,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the agency was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the ISO 8601 timestamp when the agency was last modified.
	UpdatedAt string `json:"updated_at"`
}

// Repository is a versioned codebase owned by an [Agency].
// An agency can have multiple repositories; each is linked to its owning
// agency via a belongs_to_agency edge. Sub-resources (Branches, Tags,
// Commits) are separate entities linked via edges in the entity graph.
type Repository struct {
	// ID is the unique entitygraph identifier for this repository.
	ID string `json:"id"`

	// AgencyID is the entitygraph ID of the owning Agency, resolved from
	// the belongs_to_agency edge.
	AgencyID string `json:"agency_id"`

	// Name is the human-readable label, typically the agency ID used as the repo key.
	Name string `json:"name"`

	// Description is an optional free-text description of the repository.
	Description string `json:"description,omitempty"`

	// DefaultBranch is the name of the primary branch (e.g. "main").
	DefaultBranch string `json:"default_branch"`

	// CreatedAt is the ISO 8601 timestamp when the repository was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the ISO 8601 timestamp when the repository was last modified.
	UpdatedAt string `json:"updated_at"`
}

// Branch is a named ref pointing to a [Commit]. The target commit is linked
// via a points_to edge and is updated atomically on each push or merge.
// Branches are mutable; the task-branch workflow creates one Branch per task
// and deletes it after the task branch is merged.
type Branch struct {
	// ID is the unique entitygraph identifier for this branch.
	ID string `json:"id"`

	// RepositoryID is resolved from the belongs_to_repository edge.
	RepositoryID string `json:"repository_id"`

	// Name is the full ref name, e.g. "main" or "task/abc-001".
	Name string `json:"name"`

	// IsDefault is true for the repository's default branch (e.g. main).
	IsDefault bool `json:"is_default,omitempty"`

	// HeadCommitID is the entitygraph ID of the current HEAD Commit, resolved
	// from the points_to edge.
	HeadCommitID string `json:"head_commit_id,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the branch was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the ISO 8601 timestamp when the branch ref was last updated.
	UpdatedAt string `json:"updated_at"`
}

// Tag is an immutable named ref pointing to a [Commit]. Once created, the
// target commit must never change. Lightweight tags record only a name and
// SHA; annotated tags also carry a message and tagger metadata.
type Tag struct {
	// ID is the unique entitygraph identifier for this tag.
	ID string `json:"id"`

	// RepositoryID is resolved from the belongs_to_repository edge.
	RepositoryID string `json:"repository_id"`

	// Name is the human-readable tag label, e.g. "v1.0.0".
	Name string `json:"name"`

	// SHA is the commit SHA this tag resolves to.
	SHA string `json:"sha"`

	// Message is the annotation message for annotated tags; empty for lightweight tags.
	Message string `json:"message,omitempty"`

	// TaggerName is the name of the person or agent who created the tag.
	TaggerName string `json:"tagger_name,omitempty"`

	// TaggerAt is the ISO 8601 timestamp at which the tag was created.
	TaggerAt string `json:"tagger_at,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the tag was persisted.
	CreatedAt string `json:"created_at"`
}

// Commit is an immutable git commit entity stored in the "git_objects"
// collection. It is content-addressed by [SHA] and never mutated after
// creation. The root [Tree] is linked via a has_tree edge; parent commits
// are linked via has_parent edges (0 for the initial commit, 1 for a normal
// commit, 2+ for merge commits).
type Commit struct {
	// ID is the unique entitygraph identifier for this commit entity.
	ID string `json:"id"`

	// RepositoryID is resolved from the belongs_to_repository edge.
	RepositoryID string `json:"repository_id"`

	// SHA is the full 40-character hex Git commit hash.
	SHA string `json:"sha"`

	// Message is the commit message as stored in Git.
	Message string `json:"message"`

	// AuthorName is the name or agent ID of the commit author.
	AuthorName string `json:"author_name,omitempty"`

	// AuthorEmail is the author email address recorded in the Git commit.
	AuthorEmail string `json:"author_email,omitempty"`

	// AuthorAt is the ISO 8601 author timestamp.
	AuthorAt string `json:"author_at,omitempty"`

	// CommitterName is the name of the person or service that committed the tree.
	CommitterName string `json:"committer_name,omitempty"`

	// CommitterEmail is the committer email address.
	CommitterEmail string `json:"committer_email,omitempty"`

	// CommittedAt is the ISO 8601 committer timestamp.
	CommittedAt string `json:"committed_at,omitempty"`

	// TreeID is the entitygraph ID of the root Tree, resolved from the has_tree edge.
	TreeID string `json:"tree_id,omitempty"`

	// ParentIDs are the entitygraph IDs of parent Commits, resolved from has_parent edges.
	ParentIDs []string `json:"parent_ids,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the commit entity was persisted.
	CreatedAt string `json:"created_at"`
}

// Tree is an immutable git tree entity stored in the "git_objects" collection.
// A tree represents a directory listing at a specific point in time.
// The root tree of a commit is linked via a has_tree edge on the [Commit];
// nested subdirectory trees are linked via has_subtree edges on the parent tree.
type Tree struct {
	// ID is the unique entitygraph identifier for this tree entity.
	ID string `json:"id"`

	// SHA is the full 40-character hex Git tree hash.
	SHA string `json:"sha"`

	// Path is the directory path within the commit tree hierarchy.
	// An empty string ("") denotes the root tree of a commit.
	Path string `json:"path,omitempty"`

	// CommitID is the entitygraph ID of the owning Commit, resolved from the
	// belongs_to_commit edge. Only set when this tree is the root (Path == "").
	CommitID string `json:"commit_id,omitempty"`

	// BlobIDs are the entitygraph IDs of direct [Blob] children, resolved from has_blob edges.
	BlobIDs []string `json:"blob_ids,omitempty"`

	// SubtreeIDs are the entitygraph IDs of nested [Tree] children, resolved from has_subtree edges.
	SubtreeIDs []string `json:"subtree_ids,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the tree entity was persisted.
	CreatedAt string `json:"created_at"`
}

// Blob is an immutable git blob entity stored in the "git_objects" collection.
// Blobs are content-addressed by [SHA] and represent individual file contents.
// Text file content is stored as-is; binary file content is base64-encoded
// and [Encoding] is set to "base64".
type Blob struct {
	// ID is the unique entitygraph identifier for this blob entity.
	ID string `json:"id"`

	// SHA is the full 40-character hex Git blob hash.
	SHA string `json:"sha"`

	// Path is the file path relative to the repository root,
	// e.g. "src/handlers/server.go".
	Path string `json:"path"`

	// Name is the base file name including extension, e.g. "Test.txt".
	Name string `json:"name,omitempty"`

	// Extension is the file extension without the leading dot, e.g. "txt".
	// Empty for files with no extension or dotfiles.
	Extension string `json:"extension,omitempty"`

	// Size is the byte size of the file content.
	Size int64 `json:"size,omitempty"`

	// Encoding is "utf-8" for text files or "base64" for binary files.
	Encoding string `json:"encoding,omitempty"`

	// Content holds the raw file content. Base64-encoded when Encoding == "base64".
	Content string `json:"content,omitempty"`

	// TreeID is the entitygraph ID of the owning [Tree], resolved from the
	// belongs_to_tree edge.
	TreeID string `json:"tree_id,omitempty"`

	// CreatedAt is the ISO 8601 timestamp when the blob entity was persisted.
	CreatedAt string `json:"created_at"`
}

// ── Request / filter types ────────────────────────────────────────────────────
//
// These value types are used as arguments to [GitManager] methods.
// All fields are plain scalars; no pointers — use zero values to indicate
// omission where noted in the field comments.

// CreateRepoRequest carries the parameters for [GitManager.InitRepo].
type CreateRepoRequest struct {
	// Name is the human-readable label for the repository, typically the
	// agency ID used as the repo key. Required.
	Name string `json:"name"`

	// Description is an optional free-text description of the repository.
	Description string `json:"description,omitempty"`

	// DefaultBranch is the name of the primary branch to create (e.g. "main").
	// Defaults to "main" when empty.
	DefaultBranch string `json:"default_branch,omitempty"`
}

// CreateBranchRequest carries the parameters for [GitManager.CreateBranch].
type CreateBranchRequest struct {
	// RepositoryID is the entitygraph ID of the [Repository] that will own this
	// branch. Required.
	RepositoryID string `json:"repository_id"`

	// Name is the full branch name (e.g. "task/abc-001"). Required.
	Name string `json:"name"`

	// FromBranchID is the entitygraph ID of the source branch from which the
	// new branch is created. When empty, the repository's default branch is used.
	FromBranchID string `json:"from_branch_id,omitempty"`
}

// CreateTagRequest carries the parameters for [GitManager.CreateTag].
type CreateTagRequest struct {
	// RepositoryID is the entitygraph ID of the [Repository] that will own this
	// tag. Required.
	RepositoryID string `json:"repository_id"`

	// Name is the human-readable tag label (e.g. "v1.0.0"). Required.
	Name string `json:"name"`

	// CommitID is the entitygraph ID of the [Commit] this tag points to. Required.
	CommitID string `json:"commit_id"`

	// Message is the annotation message for annotated tags. Empty for
	// lightweight tags.
	Message string `json:"message,omitempty"`

	// TaggerName is the name of the person or agent creating the tag.
	TaggerName string `json:"tagger_name,omitempty"`
}

// WriteFileRequest carries the parameters for [GitManager.WriteFile].
type WriteFileRequest struct {
	// BranchID is the entitygraph ID of the target [Branch]. Required.
	BranchID string `json:"branch_id"`

	// Path is the file path relative to the repository root (e.g.
	// "output/report.md"). Required.
	Path string `json:"path"`

	// Content is the full file content to commit.
	// Binary content must be base64-encoded and Encoding set to "base64".
	Content string `json:"content"`

	// Encoding is "utf-8" (default) or "base64" for binary content.
	Encoding string `json:"encoding,omitempty"`

	// AuthorName is the name or agent ID of the commit author.
	AuthorName string `json:"author_name,omitempty"`

	// AuthorEmail is the email address recorded in the Git commit.
	AuthorEmail string `json:"author_email,omitempty"`

	// Message is the commit message. Defaults to "Update {path}" when empty.
	Message string `json:"message,omitempty"`
}

// DeleteFileRequest carries the parameters for [GitManager.DeleteFile].
type DeleteFileRequest struct {
	// BranchID is the entitygraph ID of the target [Branch]. Required.
	BranchID string `json:"branch_id"`

	// Path is the file path relative to the repository root. Required.
	Path string `json:"path"`

	// AuthorName is the name or agent ID recorded in the deletion commit.
	AuthorName string `json:"author_name,omitempty"`

	// AuthorEmail is the email address recorded in the deletion commit.
	AuthorEmail string `json:"author_email,omitempty"`

	// Message is the commit message. Defaults to "Delete {path}" when empty.
	Message string `json:"message,omitempty"`
}

// LogFilter constrains the result set returned by [GitManager.Log].
// All fields are optional; zero values mean "no constraint".
type LogFilter struct {
	// Path restricts the log to commits that modified the file at this path.
	// Empty means return the full branch history.
	Path string `json:"path,omitempty"`

	// Limit caps the number of commits returned. 0 means no limit.
	Limit int `json:"limit,omitempty"`
}
