package codevaldgit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// advanceMain creates a temporary task branch, commits a file onto it, merges
// it into main (fast-forward), then deletes the helper branch. Used by
// rebase tests to simulate main advancing after a task branch was created.
func advanceMain(t *testing.T, repo codevaldgit.Repo, helperID, path, content string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateBranch(ctx, helperID); err != nil {
		t.Fatalf("advanceMain CreateBranch(%q): %v", helperID, err)
	}
	if err := repo.WriteFile(ctx, helperID, path, content, "system", "advance main: "+path); err != nil {
		t.Fatalf("advanceMain WriteFile(%q): %v", path, err)
	}
	if err := repo.MergeBranch(ctx, helperID); err != nil {
		t.Fatalf("advanceMain MergeBranch(%q): %v", helperID, err)
	}
	if err := repo.DeleteBranch(ctx, helperID); err != nil {
		t.Fatalf("advanceMain DeleteBranch(%q): %v", helperID, err)
	}
}

// ---------------------------------------------------------------------------
// MergeBranch tests — MVP-GIT-005 (fast-forward)
// ---------------------------------------------------------------------------

// TestMergeBranch_FastForward verifies that MergeBranch advances main HEAD to
// the task branch tip when a fast-forward is possible (main has not advanced
// since the branch was created).
func TestMergeBranch_FastForward(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "merge-ff-001"

	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "output/report.md", "# Report", "agent-1", "Add report"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// File must now be visible from main.
	content, err := repo.ReadFile(ctx, "main", "output/report.md")
	if err != nil {
		t.Fatalf("ReadFile from main after merge: %v", err)
	}
	if content != "# Report" {
		t.Errorf("ReadFile: got %q, want %q", content, "# Report")
	}
}

// TestMergeBranch_AlreadyMerged verifies that calling MergeBranch a second
// time on a branch already in main is a no-op (returns nil).
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

// TestMergeBranch_EmptyBranch verifies that merging a branch with no commits
// beyond its creation point (same HEAD as main) is an idempotent no-op.
func TestMergeBranch_EmptyBranch(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "merge-empty-001"

	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch on empty branch: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MergeBranch tests — MVP-GIT-006 (auto-rebase)
// ---------------------------------------------------------------------------

// TestMergeBranch_NeedsRebase_Success verifies that when main has advanced
// with a different file, the task branch is rebased cleanly and both files
// are visible on main after the merge.
func TestMergeBranch_NeedsRebase_Success(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "rebase-ok-001"

	// Create task branch and write fileA.
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "task.md", "task output", "agent-1", "Add task output"); err != nil {
		t.Fatalf("WriteFile taskA: %v", err)
	}

	// Advance main with a different file (no content conflict).
	advanceMain(t, repo, "setup-rebase-ok", "config.yaml", "key: value")

	// Rebase should succeed — different files, no conflict.
	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch (needs rebase): %v", err)
	}

	// Both files must be visible from main.
	if _, err := repo.ReadFile(ctx, "main", "task.md"); err != nil {
		t.Fatalf("ReadFile task.md from main: %v", err)
	}
	if _, err := repo.ReadFile(ctx, "main", "config.yaml"); err != nil {
		t.Fatalf("ReadFile config.yaml from main: %v", err)
	}
}

// TestMergeBranch_NeedsRebase_Conflict verifies that when both the task branch
// and main independently modify the same file, MergeBranch returns
// *ErrMergeConflict with the conflicting path listed.
func TestMergeBranch_NeedsRebase_Conflict(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "rebase-conflict-001"

	// Task branch writes "shared.txt".
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "shared.txt", "task version", "agent-1", "Write shared"); err != nil {
		t.Fatalf("WriteFile task: %v", err)
	}

	// Main independently writes the SAME file with different content.
	advanceMain(t, repo, "setup-conflict", "shared.txt", "main version")

	// Merge must return *ErrMergeConflict.
	err := repo.MergeBranch(ctx, taskID)
	var conflictErr *codevaldgit.ErrMergeConflict
	if !errors.As(err, &conflictErr) {
		t.Fatalf("MergeBranch: got %v, want *ErrMergeConflict", err)
	}
}

// TestMergeBranch_ConflictLeavesTaskClean verifies that after a conflict the
// task branch ref is still at its original HEAD (the branch is not mutated).
func TestMergeBranch_ConflictLeavesTaskClean(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "conflict-clean-001"

	// Task branch writes a unique sentinel file and the shared file.
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "shared.txt", "task version", "agent-1", "Write shared"); err != nil {
		t.Fatalf("WriteFile task: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "proof.txt", "sentinel", "agent-1", "Write proof"); err != nil {
		t.Fatalf("WriteFile proof: %v", err)
	}

	// Main independently writes shared.txt.
	advanceMain(t, repo, "setup-clean", "shared.txt", "main version")

	// Merge returns conflict.
	if err := repo.MergeBranch(ctx, taskID); err == nil {
		t.Fatal("expected *ErrMergeConflict, got nil")
	}

	// Task branch must still have both original commits intact.
	// Verify by reading proof.txt from the task branch ref "task/{taskID}".
	content, err := repo.ReadFile(ctx, "task/"+taskID, "proof.txt")
	if err != nil {
		t.Fatalf("ReadFile proof.txt after conflict: %v", err)
	}
	if content != "sentinel" {
		t.Errorf("proof.txt: got %q, want %q — task branch was mutated", content, "sentinel")
	}
}

// TestMergeBranch_MultipleTaskCommits verifies that a rebase with 3 commits on
// the task branch all land on main after a successful auto-rebase.
func TestMergeBranch_MultipleTaskCommits(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "multi-commit-001"

	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	for i, file := range []string{"f1.txt", "f2.txt", "f3.txt"} {
		msg := fmt.Sprintf("Commit %d", i+1)
		if err := repo.WriteFile(ctx, taskID, file, file, "agent-1", msg); err != nil {
			t.Fatalf("WriteFile %q: %v", file, err)
		}
	}

	// Advance main with an unrelated file.
	advanceMain(t, repo, "setup-multi", "unrelated.txt", "unrelated")

	if err := repo.MergeBranch(ctx, taskID); err != nil {
		t.Fatalf("MergeBranch (multi-commit rebase): %v", err)
	}

	// All 3 task files plus the unrelated file must be on main.
	for _, file := range []string{"f1.txt", "f2.txt", "f3.txt", "unrelated.txt"} {
		if _, err := repo.ReadFile(ctx, "main", file); err != nil {
			t.Errorf("ReadFile %q from main after rebase: %v", file, err)
		}
	}
}

// TestMergeBranch_ConflictFiles verifies that ConflictingFiles contains
// exactly the paths that triggered conflicts and no others.
func TestMergeBranch_ConflictFiles(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "conflict-files-001"

	// Task writes two files: one that will conflict and one that won't.
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "conflict.txt", "task", "agent-1", "conflict file"); err != nil {
		t.Fatalf("WriteFile conflict.txt: %v", err)
	}
	if err := repo.WriteFile(ctx, taskID, "safe.txt", "no conflict", "agent-1", "safe file"); err != nil {
		t.Fatalf("WriteFile safe.txt: %v", err)
	}

	// Main independently writes conflict.txt only.
	advanceMain(t, repo, "setup-files", "conflict.txt", "main version")

	err := repo.MergeBranch(ctx, taskID)
	var conflictErr *codevaldgit.ErrMergeConflict
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected *ErrMergeConflict, got %v", err)
	}

	if len(conflictErr.ConflictingFiles) != 1 || conflictErr.ConflictingFiles[0] != "conflict.txt" {
		t.Errorf("ConflictingFiles: got %v, want [conflict.txt]", conflictErr.ConflictingFiles)
	}
}

// TestMergeBranch_AgentRetry demonstrates the full agent retry workflow:
// after MergeBranch returns *ErrMergeConflict, the agent creates a new task
// branch from the current main, writes the resolved content, and the merge
// succeeds via fast-forward (main has not changed between conflict and retry).
func TestMergeBranch_AgentRetry(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	// taskA is created from the initial main and writes shared.txt.
	if err := repo.CreateBranch(ctx, "retry-taskA"); err != nil {
		t.Fatalf("CreateBranch taskA: %v", err)
	}
	if err := repo.WriteFile(ctx, "retry-taskA", "shared.txt", "task-A version", "agent-1", "taskA writes shared"); err != nil {
		t.Fatalf("WriteFile taskA: %v", err)
	}

	// Main advances: independently writes shared.txt with different content.
	advanceMain(t, repo, "retry-setup", "shared.txt", "main version")

	// First attempt: conflict detected; task branch left clean.
	err := repo.MergeBranch(ctx, "retry-taskA")
	var conflictErr *codevaldgit.ErrMergeConflict
	if !errors.As(err, &conflictErr) {
		t.Fatalf("first MergeBranch: got %v, want *ErrMergeConflict", err)
	}
	if conflictErr.TaskID != "retry-taskA" {
		t.Errorf("ErrMergeConflict.TaskID: got %q, want %q", conflictErr.TaskID, "retry-taskA")
	}

	// Agent retry: create a NEW task branch from the CURRENT main, write the
	// resolved content, and merge. Fast-forward succeeds.
	if err := repo.CreateBranch(ctx, "retry-taskB"); err != nil {
		t.Fatalf("CreateBranch taskB (retry): %v", err)
	}
	if err := repo.WriteFile(ctx, "retry-taskB", "shared.txt", "resolved version", "agent-1", "resolve conflict"); err != nil {
		t.Fatalf("WriteFile taskB: %v", err)
	}
	if err := repo.MergeBranch(ctx, "retry-taskB"); err != nil {
		t.Fatalf("MergeBranch taskB (retry): %v", err)
	}

	// Resolved content must be on main.
	content, err := repo.ReadFile(ctx, "main", "shared.txt")
	if err != nil {
		t.Fatalf("ReadFile shared.txt after retry: %v", err)
	}
	if content != "resolved version" {
		t.Errorf("shared.txt: got %q, want %q", content, "resolved version")
	}
}
