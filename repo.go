package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"
)

// repo is the concrete backend-agnostic implementation of [Repo].
// It wraps a go-git repository constructed from a [Backend]-supplied
// storage.Storer and billy.Filesystem. The same implementation is used
// regardless of whether the filesystem or ArangoDB backend is active.
//
// Branch and file operations are implemented in MVP-GIT-003 and MVP-GIT-004.
// Merge is implemented in MVP-GIT-005 and MVP-GIT-006.
// History and diff are implemented in MVP-GIT-007.
type repo struct {
	git *gogit.Repository
}

// newRepo opens a go-git repository from the given storer and working tree,
// returning a [Repo] ready for use. Called by repoManager.OpenRepo.
func newRepo(storer storage.Storer, wt billy.Filesystem) (Repo, error) {
	r, err := gogit.Open(storer, wt)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	return &repo{git: r}, nil
}

// taskBranchName returns the full branch name for a task ID.
func taskBranchName(taskID string) string {
	return "task/" + taskID
}

// CreateBranch creates refs/heads/task/{taskID} pointing at the current HEAD
// of main. Returns [ErrBranchExists] if the branch already exists.
// Returns an error if taskID is empty or main cannot be resolved.
func (r *repo) CreateBranch(_ context.Context, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("CreateBranch: taskID must not be empty")
	}

	mainRefName := plumbing.NewBranchReferenceName("main")
	mainRef, err := r.git.Reference(mainRefName, true)
	if err != nil {
		return fmt.Errorf("CreateBranch: resolve main: %w", err)
	}

	branchRefName := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if _, err := r.git.Reference(branchRefName, false); err == nil {
		// Reference already exists.
		return ErrBranchExists
	}

	newRef := plumbing.NewHashReference(branchRefName, mainRef.Hash())
	if err := r.git.Storer.SetReference(newRef); err != nil {
		return fmt.Errorf("CreateBranch %q: set reference: %w", taskID, err)
	}
	return nil
}

// MergeBranch merges task/{taskID} into main.
// Implemented in MVP-GIT-005 and MVP-GIT-006.
func (r *repo) MergeBranch(_ context.Context, taskID string) error {
	return fmt.Errorf("MergeBranch %q: not yet implemented — see MVP-GIT-005", taskID)
}

// DeleteBranch removes refs/heads/task/{taskID}.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns an error if taskID is empty or equals "main" (protected).
func (r *repo) DeleteBranch(_ context.Context, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("DeleteBranch: taskID must not be empty")
	}
	if taskID == "main" {
		return fmt.Errorf("DeleteBranch: cannot delete the main branch")
	}

	branchRefName := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if _, err := r.git.Reference(branchRefName, false); err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return ErrBranchNotFound
		}
		return fmt.Errorf("DeleteBranch %q: lookup: %w", taskID, err)
	}

	if err := r.git.Storer.RemoveReference(branchRefName); err != nil {
		return fmt.Errorf("DeleteBranch %q: remove reference: %w", taskID, err)
	}
	return nil
}

// WriteFile commits content to path on branch task/{taskID} as a new Git commit
// attributed to author. The branch must already exist — call [Repo.CreateBranch] first.
// path must be relative (no leading "/") and must not contain "..".
// Subdirectories are created automatically. Returns [ErrBranchNotFound] if the
// task branch does not exist.
func (r *repo) WriteFile(_ context.Context, taskID, path, content, author, message string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("WriteFile: path must be relative, got: %s", path)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("WriteFile: path must not contain '..', got: %s", path)
	}

	w, err := r.git.Worktree()
	if err != nil {
		return fmt.Errorf("WriteFile: get worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if err := w.Checkout(&gogit.CheckoutOptions{Branch: branchRef}); err != nil {
		return ErrBranchNotFound
	}

	// Create parent directories if the path is nested.
	if dir := filepath.Dir(path); dir != "." {
		if dirFS, ok := w.Filesystem.(billy.Dir); ok {
			if err := dirFS.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("WriteFile: mkdir %q: %w", dir, err)
			}
		}
	}

	// Write the file content via the billy filesystem.
	f, err := w.Filesystem.Create(path)
	if err != nil {
		return fmt.Errorf("WriteFile: create %q: %w", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return fmt.Errorf("WriteFile: write %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("WriteFile: close %q: %w", path, err)
	}

	if _, err := w.Add(path); err != nil {
		return fmt.Errorf("WriteFile: stage %q: %w", path, err)
	}

	_, err = w.Commit(message, &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  author,
			Email: author + "@codevaldcortex.local",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return fmt.Errorf("WriteFile: commit: %w", err)
	}
	return nil
}

// ReadFile returns the content of path at the given ref.
// ref may be a branch name, tag name, or full commit SHA.
// Returns [ErrRefNotFound] if the ref cannot be resolved, or
// [ErrFileNotFound] if the path does not exist at that ref.
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) ReadFile(_ context.Context, ref, path string) (string, error) {
	hash, err := r.resolveRef(ref)
	if err != nil {
		return "", ErrRefNotFound
	}

	commit, err := r.git.CommitObject(hash)
	if err != nil {
		return "", ErrRefNotFound
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("ReadFile: get tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return "", ErrFileNotFound
	}

	return file.Contents()
}

// DeleteFile removes path from branch task/{taskID} as a new Git commit.
// Returns [ErrBranchNotFound] if the branch does not exist, or
// [ErrFileNotFound] if path does not exist on the branch.
func (r *repo) DeleteFile(_ context.Context, taskID, path, author, message string) error {
	w, err := r.git.Worktree()
	if err != nil {
		return fmt.Errorf("DeleteFile: get worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if err := w.Checkout(&gogit.CheckoutOptions{Branch: branchRef}); err != nil {
		return ErrBranchNotFound
	}

	if _, err := w.Filesystem.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ErrFileNotFound
		}
		return fmt.Errorf("DeleteFile: stat %q: %w", path, err)
	}

	if _, err := w.Remove(path); err != nil {
		return fmt.Errorf("DeleteFile: remove %q: %w", path, err)
	}

	_, err = w.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  author,
			Email: author + "@codevaldcortex.local",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return fmt.Errorf("DeleteFile: commit: %w", err)
	}
	return nil
}

// ListDirectory returns the immediate children of path at the given ref.
// An empty path ("") or "/" lists the repository root.
// Each [FileEntry] has Name, Path, IsDir, and Size populated.
// Returns [ErrRefNotFound] for unknown refs, [ErrFileNotFound] if path does
// not exist at ref (and is not the root), or an empty slice for empty dirs.
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) ListDirectory(_ context.Context, ref, path string) ([]FileEntry, error) {
	hash, err := r.resolveRef(ref)
	if err != nil {
		return nil, ErrRefNotFound
	}

	commit, err := r.git.CommitObject(hash)
	if err != nil {
		return nil, ErrRefNotFound
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("ListDirectory: get tree: %w", err)
	}

	// Normalise path: strip leading/trailing slashes.
	path = strings.Trim(path, "/")

	if path != "" {
		sub, err := tree.Tree(path)
		if err != nil {
			return nil, ErrFileNotFound
		}
		tree = sub
	}

	var entries []FileEntry
	for _, e := range tree.Entries {
		var size int64
		if e.Mode.IsFile() {
			if blob, berr := r.git.BlobObject(e.Hash); berr == nil {
				size = blob.Size
			}
		}
		entries = append(entries, FileEntry{
			Name:  e.Name,
			Path:  filepath.Join(path, e.Name),
			IsDir: !e.Mode.IsFile(),
			Size:  size,
		})
	}
	return entries, nil
}

// Log returns commits touching path at ref, newest first.
// Implemented in MVP-GIT-007.
func (r *repo) Log(_ context.Context, ref, path string) ([]Commit, error) {
	return nil, fmt.Errorf("Log %q %q: not yet implemented — see MVP-GIT-007", ref, path)
}

// Diff returns per-file changes between fromRef and toRef.
// Implemented in MVP-GIT-007.
func (r *repo) Diff(_ context.Context, fromRef, toRef string) ([]FileDiff, error) {
	return nil, fmt.Errorf("Diff %q %q: not yet implemented — see MVP-GIT-007", fromRef, toRef)
}

// resolveRef resolves a branch name, tag name, or commit SHA to a plumbing.Hash.
// It tries, in order: branch ref → tag ref → raw SHA (full or abbreviated).
// Returns [ErrRefNotFound] if none of those resolve.
func (r *repo) resolveRef(ref string) (plumbing.Hash, error) {
	// Try as a branch reference (refs/heads/{ref}).
	if refObj, err := r.git.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		return refObj.Hash(), nil
	}

	// Try as a tag reference (refs/tags/{ref}).
	if refObj, err := r.git.Reference(plumbing.NewTagReferenceName(ref), true); err == nil {
		return refObj.Hash(), nil
	}

	// Try as a raw commit SHA (full 40-char or abbreviated).
	hash := plumbing.NewHash(ref)
	if hash != plumbing.ZeroHash {
		return hash, nil
	}

	return plumbing.ZeroHash, ErrRefNotFound
}
