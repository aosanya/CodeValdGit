package codevaldgit_test

import (
	"context"
	"errors"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ---------------------------------------------------------------------------
// MergeBranch tests — MVP-GIT-005
// ---------------------------------------------------------------------------

// TestMergeBranch_FastForward verifies that MergeBranch advances main HEAD to
// the task branch tip when a fast-forward is possible (main has not advanced
// since the branch was created).
func TestMergeBranch_FastForward(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "merge-ff-001"

	// Create branch and write a file to it.
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "output/report.md", "# Report", "agent-1", "Add report"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Merge should succeed and be a fast-forward.
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// Verify the file is now visible from main.
	content, err := repo.ReadFile(ctx, "main", "output/report.md")
	if err != nil {
		t.Fatalf("ReadFile from main after merge: %v", err)
	}
	if content != "# Report" {
		t.Errorf("ReadFile: got %q, want %q", content, "# Report")
	}
}

// TestMergeBranch_AlreadyMerged verifies that calling MergeBranch a second
// time on a branch that is already in main is a no-op (returns nil).
func TestMergeBranch_AlreadyMerged(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "merge-idempotent-001"

	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "out.txt", "data", "agent-1", "Add file"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("first MergeBranch: %v", err)
	}

	// Second call must return nil (idempotent).
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("second MergeBranch (idempotent): %v", err)
	}
}

// TestMergeBranch_BranchNotFound verifies that MergeBranch returns
// ErrBranchNotFound when the task branch does not exist.
func TestMergeBranch_BranchNotFound(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	err := repo.MergeBranch(ctx, "nonexistent-task")
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Fatalf("MergeBranch(nonexistent): got %v, want ErrBranchNotFound", err)
	}
}

// TestMergeBranch_EmptyBranch verifies that merging a branch that has no
// commits beyond its creation point (same HEAD as main) is an idempotent
// no-op and returns nil.
func TestMergeBranch_EmptyBranch(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "merge-empty-001"

	// Branch created from main but no writes committed to it.
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Branch tip == main tip → should be treated as already-merged (nil).
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch on empty branch: %v", err)
	}
}
