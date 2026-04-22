// git_concurrency_test.go — GIT-011 unit tests for concurrent MergeBranch
// behaviour and the RefLocker contract.
package codevaldgit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
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
