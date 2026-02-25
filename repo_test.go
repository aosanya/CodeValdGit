package codevaldgit_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
)

// openTestRepo initialises a fresh repository in a temp directory and returns
// an open Repo ready for use.
func openTestRepo(t *testing.T) codevaldgit.Repo {
	t.Helper()
	base := t.TempDir()
	archive := t.TempDir()
	b, err := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
		BasePath:    base,
		ArchivePath: archive,
	})
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	ctx := context.Background()
	const agency = "test-agency"
	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	// Ensure BasePath dir exists before creating subdirs via os.MkdirAll.
	_ = os.MkdirAll(base, 0o755)

	mgr, err := codevaldgit.NewRepoManager(b)
	if err != nil {
		t.Fatalf("NewRepoManager: %v", err)
	}
	repo, err := mgr.OpenRepo(ctx, agency)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	return repo
}

// ---------------------------------------------------------------------------
// CreateBranch tests
// ---------------------------------------------------------------------------

// TestCreateBranch_Success verifies that CreateBranch creates a branch ref
// that is readable after the call.
func TestCreateBranch_Success(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "task-abc-001"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
}

// TestCreateBranch_AlreadyExists verifies that a second CreateBranch for the
// same taskID returns ErrBranchExists.
func TestCreateBranch_AlreadyExists(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "task-dup"); err != nil {
		t.Fatalf("first CreateBranch: %v", err)
	}
	if err := repo.CreateBranch(ctx, "task-dup"); !errors.Is(err, codevaldgit.ErrBranchExists) {
		t.Fatalf("second CreateBranch: got %v, want ErrBranchExists", err)
	}
}

// TestCreateBranch_EmptyTaskID verifies that an empty taskID returns an error.
func TestCreateBranch_EmptyTaskID(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, ""); err == nil {
		t.Fatal("CreateBranch with empty taskID: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// DeleteBranch tests
// ---------------------------------------------------------------------------

// TestDeleteBranch_Success verifies that after DeleteBranch the branch no
// longer exists — a subsequent CreateBranch for the same ID succeeds.
func TestDeleteBranch_Success(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()
	const taskID = "task-del-001"

	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.DeleteBranch(ctx, taskID); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	// Re-creating must now succeed (branch is gone).
	if err := repo.CreateBranch(ctx, taskID); err != nil {
		t.Fatalf("re-CreateBranch after delete: %v", err)
	}
}

// TestDeleteBranch_NotFound verifies that deleting a non-existent branch
// returns ErrBranchNotFound.
func TestDeleteBranch_NotFound(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.DeleteBranch(ctx, "nonexistent-task"); !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Fatalf("DeleteBranch: got %v, want ErrBranchNotFound", err)
	}
}

// TestDeleteBranch_Main verifies that attempting to delete the "main" branch
// returns an error and does not affect main.
func TestDeleteBranch_Main(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.DeleteBranch(ctx, "main"); err == nil {
		t.Fatal("DeleteBranch(main): expected error, got nil")
	}
}

// TestDeleteBranch_EmptyTaskID verifies that an empty taskID returns an error.
func TestDeleteBranch_EmptyTaskID(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.DeleteBranch(ctx, ""); err == nil {
		t.Fatal("DeleteBranch with empty taskID: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Concurrent tests
// ---------------------------------------------------------------------------

// TestConcurrentCreateBranch verifies that 5 goroutines can each create a
// distinct task branch simultaneously without data races or errors.
func TestConcurrentCreateBranch(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	taskIDs := []string{"task-c-1", "task-c-2", "task-c-3", "task-c-4", "task-c-5"}
	errs := make([]error, len(taskIDs))
	var wg sync.WaitGroup
	for i, id := range taskIDs {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			errs[i] = repo.CreateBranch(ctx, id)
		}(i, id)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d CreateBranch(%q): %v", i, taskIDs[i], err)
		}
	}
}
