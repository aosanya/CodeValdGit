// git_concurrency_test.go — GIT-011 unit tests for concurrent MergeBranch
// behaviour and the RefLocker contract.
package codevaldgit_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// TestGIT011_ConcurrentMerges verifies that two goroutines merging different
// task branches concurrently both succeed with no lost update.
//
// The in-process [mutexLocker] serialises the two advance-head calls so that
// the second merge sees the result of the first and still succeeds.
func TestGIT011_ConcurrentMerges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, repoID, _ := newTestManagerAndSeedRepo(t)

	// Create two independent task branches, each with one file.
	branchAID := createTaskBranch(t, mgr, repoID, "concurrent-a")
	branchBID := createTaskBranch(t, mgr, repoID, "concurrent-b")
	writeTestFile(t, mgr, branchAID, "a.txt", "branch-a content")
	writeTestFile(t, mgr, branchBID, "b.txt", "branch-b content")

	// Merge both branches concurrently.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{branchAID, branchBID} {
		wg.Add(1)
		go func(idx int, branchID string) {
			defer wg.Done()
			_, errs[idx] = mgr.MergeBranch(ctx, branchID)
		}(i, id)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d MergeBranch failed: %v", i, err)
		}
	}
}

// TestBUG09020_ConcurrentWriteFilesAllLand verifies the BUG-09-020 fix: when N
// goroutines fire WriteFile against the same branch in parallel, the per-agency
// RefLocker serialises them so that each commit chains onto the previous one
// and every file ends up reachable from the branch HEAD. Without the lock,
// each goroutine would read the same parent HEAD, build a sibling commit, and
// the unsynchronised advanceBranchHead would leave the branch tip pointing at
// only the last-writer's commit — losing the other N-1 files.
func TestBUG09020_ConcurrentWriteFilesAllLand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, repoID, _ := newTestManagerAndSeedRepo(t)
	branchID := createTaskBranch(t, mgr, repoID, "bug09020-concurrent-writes")

	const n = 10
	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("dir/file_%02d.txt", i)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
				BranchID:   branchID,
				Path:       paths[idx],
				Content:    fmt.Sprintf("content-%02d", idx),
				AuthorName: "test-author",
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("WriteFile %d failed: %v", i, err)
		}
	}

	// All N files must be reachable from the branch HEAD. This is the
	// equivalent of `git ls-tree -r <branch>` from the bug reproducer.
	for _, p := range paths {
		blob, err := mgr.ReadFile(ctx, branchID, p)
		if err != nil {
			t.Errorf("ReadFile %q after concurrent writes: %v", p, err)
			continue
		}
		if blob.Path != p {
			t.Errorf("ReadFile %q returned blob with Path=%q", p, blob.Path)
		}
	}
}

// TestGIT011_MergeLockRespectsContextCancellation verifies that WithMergeLock
// returns ctx.Err() when the context is cancelled before fn runs.
func TestGIT011_MergeLockRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mgr, _, repoID, _ := newTestManagerAndSeedRepo(t)
	branchID := createTaskBranch(t, mgr, repoID, "ctx-cancel-001")
	writeTestFile(t, mgr, branchID, "file.txt", "content")

	_, err := mgr.MergeBranch(ctx, branchID)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
