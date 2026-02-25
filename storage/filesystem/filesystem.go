// Package filesystem provides a filesystem-backed implementation of
// [codevaldgit.Backend]. Git repositories are stored as real .git directories
// on disk. DeleteRepo archives repos via os.Rename (with a copy+delete fallback
// for cross-device moves); PurgeRepo hard-deletes via os.RemoveAll.
//
// Obtain a Backend with [NewFilesystemBackend], then pass it to
// [codevaldgit.NewRepoManager].
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	gogitfs "github.com/go-git/go-git/v5/storage/filesystem"

	gogitstorage "github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// FilesystemConfig holds path settings for the filesystem backend.
type FilesystemConfig struct {
	// BasePath is the root directory for live repositories.
	// Each agency gets a subdirectory: {BasePath}/{agencyID}/
	// Must be an absolute path to a writable directory.
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

// InitRepo creates a new Git repository at {BasePath}/{agencyID}/ with an
// initial empty commit on the main branch.
// Returns [codevaldgit.ErrRepoAlreadyExists] if a repo already exists there.
func (b *filesystemBackend) InitRepo(_ context.Context, agencyID string) error {
	if err := validateAgencyID(agencyID); err != nil {
		return err
	}
	repoPath := filepath.Join(b.cfg.BasePath, agencyID)
	if _, err := os.Stat(repoPath); err == nil {
		return codevaldgit.ErrRepoAlreadyExists
	}

	r, err := gogit.PlainInit(repoPath, false)
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryAlreadyExists) {
			return codevaldgit.ErrRepoAlreadyExists
		}
		return fmt.Errorf("InitRepo %s: PlainInit: %w", agencyID, err)
	}

	// go-git defaults HEAD to refs/heads/master; point it at refs/heads/main.
	mainRef := plumbing.NewBranchReferenceName("main")
	if err := r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)); err != nil {
		return fmt.Errorf("InitRepo %s: set HEAD to main: %w", agencyID, err)
	}

	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("InitRepo %s: get worktree: %w", agencyID, err)
	}

	// Create the initial empty commit so branch refs can be resolved.
	_, err = wt.Commit("init", &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  "system",
			Email: "system@codevaldgit",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s: initial commit: %w", agencyID, err)
	}
	return nil
}

// OpenStorer returns a filesystem [gogitstorage.Storer] and osfs working tree
// for the given agencyID.
// Returns [codevaldgit.ErrRepoNotFound] if no live repository exists for agencyID.
func (b *filesystemBackend) OpenStorer(_ context.Context, agencyID string) (gogitstorage.Storer, billy.Filesystem, error) {
	if err := validateAgencyID(agencyID); err != nil {
		return nil, nil, err
	}
	repoPath := filepath.Join(b.cfg.BasePath, agencyID)
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return nil, nil, codevaldgit.ErrRepoNotFound
	}
	dotGit := osfs.New(filepath.Join(repoPath, ".git"))
	storer := gogitfs.NewStorage(dotGit, cache.NewObjectLRUDefault())
	wt := osfs.New(repoPath)
	return storer, wt, nil
}

// DeleteRepo archives {BasePath}/{agencyID}/ to {ArchivePath}/{agencyID}/ using
// os.Rename. If the rename fails (e.g. cross-device EXDEV), it falls back to a
// full directory copy followed by os.RemoveAll of the source.
// If {ArchivePath}/{agencyID}/ already exists, a Unix-timestamp suffix is
// appended to the destination to avoid collision.
// Returns [codevaldgit.ErrRepoNotFound] if no live repository exists for agencyID.
func (b *filesystemBackend) DeleteRepo(_ context.Context, agencyID string) error {
	if err := validateAgencyID(agencyID); err != nil {
		return err
	}
	src := filepath.Join(b.cfg.BasePath, agencyID)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return codevaldgit.ErrRepoNotFound
	}

	// Ensure the archive root exists.
	if err := os.MkdirAll(b.cfg.ArchivePath, 0o755); err != nil {
		return fmt.Errorf("DeleteRepo %s: ensure archive path: %w", agencyID, err)
	}

	dst := filepath.Join(b.cfg.ArchivePath, agencyID)
	// Avoid collisions: append Unix timestamp if the archive already exists.
	if _, err := os.Stat(dst); err == nil {
		dst = filepath.Join(b.cfg.ArchivePath, fmt.Sprintf("%s-%d", agencyID, time.Now().Unix()))
	}

	if err := os.Rename(src, dst); err != nil {
		// Fall back to directory copy + remove (handles cross-device moves).
		if err2 := copyDir(src, dst); err2 != nil {
			return fmt.Errorf("DeleteRepo %s: copy to archive: %w", agencyID, err2)
		}
		if err2 := os.RemoveAll(src); err2 != nil {
			return fmt.Errorf("DeleteRepo %s: remove source after copy: %w", agencyID, err2)
		}
	}
	return nil
}

// PurgeRepo permanently removes {ArchivePath}/{agencyID}/ via os.RemoveAll.
// Returns [codevaldgit.ErrRepoNotFound] if no archived repository exists for agencyID.
func (b *filesystemBackend) PurgeRepo(_ context.Context, agencyID string) error {
	if err := validateAgencyID(agencyID); err != nil {
		return err
	}
	path := filepath.Join(b.cfg.ArchivePath, agencyID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return codevaldgit.ErrRepoNotFound
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("PurgeRepo %s: %w", agencyID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// validateAgencyID rejects empty, path-traversal, and null-byte agency IDs
// to prevent escaping BasePath or ArchivePath.
func validateAgencyID(agencyID string) error {
	if agencyID == "" {
		return fmt.Errorf("agencyID must not be empty")
	}
	if strings.ContainsRune(agencyID, '/') {
		return fmt.Errorf("agencyID %q: contains '/'", agencyID)
	}
	if strings.ContainsRune(agencyID, 0) {
		return fmt.Errorf("agencyID %q: contains null byte", agencyID)
	}
	if strings.Contains(agencyID, "..") {
		return fmt.Errorf("agencyID %q: contains '..'", agencyID)
	}
	return nil
}

// copyDir recursively copies the directory tree rooted at src to dst.
// Used as a fallback when os.Rename fails (e.g. EXDEV cross-device error).
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile copies a single file from src to dst, preserving the file mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck

	_, err = io.Copy(out, in)
	return err
}
