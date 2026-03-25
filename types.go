package codevaldgit

import (
	"fmt"
	"time"
)

// FileEntry is a single item returned by [Repo.ListDirectory].
type FileEntry struct {
	// Name is the base name of the file or directory (no path prefix).
	Name string

	// Path is the full path from the repository root.
	Path string

	// IsDir is true when the entry is a directory (Git tree node).
	IsDir bool

	// Size is the byte size of the file. Zero for directories.
	Size int64
}

// CommitEntry is a summary of a single Git commit returned by [Repo.Log].
// This is the v1 log-view type used by the Repo interface; the entitygraph
// entity for a stored Commit object is defined in models.go.
type CommitEntry struct {
	// SHA is the full 40-character hex commit hash.
	SHA string

	// Author is the name or ID of the person or agent who authored the commit.
	Author string

	// Message is the commit message as stored in Git.
	Message string

	// Timestamp is the author timestamp of the commit in UTC.
	Timestamp time.Time
}

// FileDiff describes the changes to one file between two refs, as returned
// by [Repo.Diff].
type FileDiff struct {
	// Path is the file path relative to the repository root.
	// For renames, this is the destination path.
	Path string

	// Operation describes the type of change: "add", "modify", or "delete".
	Operation string

	// Patch is the unified diff text for the file. Empty for binary files.
	Patch string
}

// AuthorInfo carries the author name and email for write operations.
// Passed into implementations that need to record a Git author signature.
type AuthorInfo struct {
	// Name is the human-readable name or agent ID.
	Name string

	// Email is the author email address recorded in the Git commit.
	Email string
}

// ErrMergeConflict is returned by [Repo.MergeBranch] when the auto-rebase
// encounters a content conflict that cannot be resolved automatically.
// The task branch is left in a clean state (rebase aborted) so the agent
// can resolve the conflicts and retry.
type ErrMergeConflict struct {
	// TaskID is the task branch suffix (the value passed to MergeBranch).
	TaskID string

	// ConflictingFiles lists the repository-relative paths of the files
	// that produced conflicts during the rebase.
	ConflictingFiles []string
}

// Error implements the error interface.
func (e *ErrMergeConflict) Error() string {
	return fmt.Sprintf("merge conflict on task branch %q: conflicting files %v", e.TaskID, e.ConflictingFiles)
}
