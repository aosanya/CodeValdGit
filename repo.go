package codevaldgit

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-billy/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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

// WriteFile commits content to path on task/{taskID}.
// Implemented in MVP-GIT-004.
func (r *repo) WriteFile(_ context.Context, taskID, path, _, _, _ string) error {
	return fmt.Errorf("WriteFile %q %q: not yet implemented — see MVP-GIT-004", taskID, path)
}

// ReadFile returns the content of path at ref.
// Implemented in MVP-GIT-004.
func (r *repo) ReadFile(_ context.Context, ref, path string) (string, error) {
	return "", fmt.Errorf("ReadFile %q %q: not yet implemented — see MVP-GIT-004", ref, path)
}

// DeleteFile removes path from task/{taskID} as a new commit.
// Implemented in MVP-GIT-004.
func (r *repo) DeleteFile(_ context.Context, taskID, path, _, _ string) error {
	return fmt.Errorf("DeleteFile %q %q: not yet implemented — see MVP-GIT-004", taskID, path)
}

// ListDirectory returns the immediate children of path at ref.
// Implemented in MVP-GIT-004.
func (r *repo) ListDirectory(_ context.Context, ref, path string) ([]FileEntry, error) {
	return nil, fmt.Errorf("ListDirectory %q %q: not yet implemented — see MVP-GIT-004", ref, path)
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
