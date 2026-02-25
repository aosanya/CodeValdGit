// Package filesystem provides a filesystem-backed implementation of
// [codevaldgit.Backend]. Git repositories are stored as real .git directories
// on disk. DeleteRepo archives repos via os.Rename; PurgeRepo hard-deletes via
// os.RemoveAll.
//
// Obtain a Backend with [NewFilesystemBackend], then pass it to
// codevaldgit.NewRepoManager.
//
// Fully implemented in MVP-GIT-002.
package filesystem

import (
	"context"
	"errors"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// FilesystemConfig holds path settings for the filesystem backend.
type FilesystemConfig struct {
	// BasePath is the root directory for live repositories.
	// Each agency gets a subdirectory: {BasePath}/{agencyID}/
	// Must be an absolute path to an existing, writable directory.
	BasePath string

	// ArchivePath is the root directory for archived repositories.
	// [codevaldgit.RepoManager.DeleteRepo] moves repos here;
	// [codevaldgit.RepoManager.PurgeRepo] removes from here.
	// Must be an absolute path to a writable directory.
	ArchivePath string
}

// filesystemBackend implements [codevaldgit.Backend] using on-disk .git repos.
type filesystemBackend struct {
	cfg FilesystemConfig
}

// NewFilesystemBackend constructs a filesystem-backed [codevaldgit.Backend].
// Both BasePath and ArchivePath must be non-empty.
// Returns an error if either path is missing from cfg.
func NewFilesystemBackend(cfg FilesystemConfig) (codevaldgit.Backend, error) {
	if cfg.BasePath == "" {
		return nil, errors.New("NewFilesystemBackend: BasePath must not be empty")
	}
	if cfg.ArchivePath == "" {
		return nil, errors.New("NewFilesystemBackend: ArchivePath must not be empty")
	}
	return &filesystemBackend{cfg: cfg}, nil
}

// InitRepo creates a new .git directory at {BasePath}/{agencyID}/.
// Implemented in MVP-GIT-002.
func (b *filesystemBackend) InitRepo(_ context.Context, agencyID string) error {
	return errors.New("InitRepo: not yet implemented — see MVP-GIT-002")
}

// OpenStorer returns a filesystem storage.Storer and osfs working tree for agencyID.
// Implemented in MVP-GIT-002.
func (b *filesystemBackend) OpenStorer(_ context.Context, agencyID string) (storage.Storer, billy.Filesystem, error) {
	return nil, nil, errors.New("OpenStorer: not yet implemented — see MVP-GIT-002")
}

// DeleteRepo archives {BasePath}/{agencyID}/ to {ArchivePath}/{agencyID}/ via os.Rename.
// Implemented in MVP-GIT-002.
func (b *filesystemBackend) DeleteRepo(_ context.Context, agencyID string) error {
	return errors.New("DeleteRepo: not yet implemented — see MVP-GIT-002")
}

// PurgeRepo hard-deletes {ArchivePath}/{agencyID}/ via os.RemoveAll.
// Implemented in MVP-GIT-002.
func (b *filesystemBackend) PurgeRepo(_ context.Context, agencyID string) error {
	return errors.New("PurgeRepo: not yet implemented — see MVP-GIT-002")
}
