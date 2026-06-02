// git_workflowrun_test.go covers FEAT-20260602-001 manager behaviour:
// workflow_run_id persistence on Branch, the MergeRequest lifecycle, and the
// associated git.merge.* event publishing.
package codevaldgit_test

import (
	"context"
	"errors"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// TestGitManager_CreateBranch_PersistsWorkflowRunID asserts that the run ID
// is persisted on the branch entity and round-trips through GetBranch.
func TestGitManager_CreateBranch_PersistsWorkflowRunID(t *testing.T) {
	const runID = "wfr_branch_001"
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	created, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID:  repo.ID,
		Name:          "feature/run-001",
		WorkflowRunID: runID,
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if created.WorkflowRunID != runID {
		t.Errorf("created.WorkflowRunID = %q, want %q", created.WorkflowRunID, runID)
	}

	got, err := mgr.GetBranch(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetBranch: %v", err)
	}
	if got.WorkflowRunID != runID {
		t.Errorf("GetBranch.WorkflowRunID = %q, want %q", got.WorkflowRunID, runID)
	}
}

// TestGitManager_ListBranchesFiltered_ByWorkflowRunID asserts that branches
// are filtered to only those carrying the requested run ID.
func TestGitManager_ListBranchesFiltered_ByWorkflowRunID(t *testing.T) {
	const runA = "wfr_filter_A"
	const runB = "wfr_filter_B"
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	for _, b := range []struct {
		name  string
		runID string
	}{
		{"feature/a-1", runA},
		{"feature/a-2", runA},
		{"feature/b-1", runB},
		{"feature/no-run", ""},
	} {
		if _, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
			RepositoryID:  repo.ID,
			Name:          b.name,
			WorkflowRunID: b.runID,
		}); err != nil {
			t.Fatalf("CreateBranch(%s): %v", b.name, err)
		}
	}

	got, err := mgr.ListBranchesFiltered(ctx, repo.ID, codevaldgit.BranchFilter{WorkflowRunID: runA})
	if err != nil {
		t.Fatalf("ListBranchesFiltered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(filtered) = %d, want 2 — got %v", len(got), branchNames(got))
	}
	for _, b := range got {
		if b.WorkflowRunID != runA {
			t.Errorf("filtered branch %q has WorkflowRunID = %q, want %q", b.Name, b.WorkflowRunID, runA)
		}
	}

	// Empty filter returns all branches (default + 4 created above = 5).
	all, err := mgr.ListBranchesFiltered(ctx, repo.ID, codevaldgit.BranchFilter{})
	if err != nil {
		t.Fatalf("ListBranchesFiltered (no filter): %v", err)
	}
	if len(all) != 5 {
		t.Errorf("len(all) = %d, want 5", len(all))
	}
}

// TestGitManager_CreateMergeRequest_PublishesAndPersists covers the open path:
// the MR is created in "open" status, carries the run ID, and a
// git.merge.requested event is published.
func TestGitManager_CreateMergeRequest_PublishesAndPersists(t *testing.T) {
	const runID = "wfr_mr_001"
	mgr, _, pub := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	source, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID:  repo.ID,
		Name:          "feature/mr-source",
		WorkflowRunID: runID,
	})
	if err != nil {
		t.Fatalf("CreateBranch source: %v", err)
	}

	mr, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID:   repo.ID,
		Title:          "Add widget",
		SourceBranchID: source.ID,
		WorkflowRunID:  runID,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}
	if mr.Status != codevaldgit.MergeRequestStatusOpen {
		t.Errorf("mr.Status = %q, want %q", mr.Status, codevaldgit.MergeRequestStatusOpen)
	}
	if mr.WorkflowRunID != runID {
		t.Errorf("mr.WorkflowRunID = %q, want %q", mr.WorkflowRunID, runID)
	}
	if mr.SourceBranchID != source.ID {
		t.Errorf("mr.SourceBranchID = %q, want %q", mr.SourceBranchID, source.ID)
	}

	// Round-trip via GetMergeRequest.
	got, err := mgr.GetMergeRequest(ctx, mr.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if got.Title != "Add widget" || got.WorkflowRunID != runID {
		t.Errorf("GetMergeRequest = %+v, want title=Add widget run_id=%s", got, runID)
	}

	if !publishedHasTopic(pub.published(), codevaldgit.TopicMergeRequested) {
		t.Errorf("expected %q published, got %v", codevaldgit.TopicMergeRequested, pub.published())
	}
}

// TestGitManager_ListMergeRequests_FilterByWorkflowRunID asserts the run-id
// filter selects only MRs from the matching run.
func TestGitManager_ListMergeRequests_FilterByWorkflowRunID(t *testing.T) {
	const runA = "wfr_listmr_A"
	const runB = "wfr_listmr_B"
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	sourceA, _ := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID,
		Name:         "feature/list-A",
	})
	sourceB, _ := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID,
		Name:         "feature/list-B",
	})

	if _, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID:   repo.ID,
		Title:          "A",
		SourceBranchID: sourceA.ID,
		WorkflowRunID:  runA,
	}); err != nil {
		t.Fatalf("CreateMergeRequest A: %v", err)
	}
	if _, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID:   repo.ID,
		Title:          "B",
		SourceBranchID: sourceB.ID,
		WorkflowRunID:  runB,
	}); err != nil {
		t.Fatalf("CreateMergeRequest B: %v", err)
	}

	got, err := mgr.ListMergeRequests(ctx, codevaldgit.MergeRequestFilter{WorkflowRunID: runA})
	if err != nil {
		t.Fatalf("ListMergeRequests: %v", err)
	}
	if len(got) != 1 || got[0].WorkflowRunID != runA {
		t.Errorf("filtered MRs = %+v, want one MR with run %q", got, runA)
	}
}

// TestGitManager_CloseMergeRequest_TransitionsToClosed asserts that an open MR
// can be closed without merging.
func TestGitManager_CloseMergeRequest_TransitionsToClosed(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)
	source, _ := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID,
		Name:         "feature/close-me",
	})
	mr, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID:   repo.ID,
		Title:          "Close test",
		SourceBranchID: source.ID,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}

	closed, err := mgr.CloseMergeRequest(ctx, mr.ID)
	if err != nil {
		t.Fatalf("CloseMergeRequest: %v", err)
	}
	if closed.Status != codevaldgit.MergeRequestStatusClosed {
		t.Errorf("closed.Status = %q, want %q", closed.Status, codevaldgit.MergeRequestStatusClosed)
	}

	// Closing a non-open MR returns ErrMergeRequestNotOpen.
	if _, err := mgr.CloseMergeRequest(ctx, mr.ID); !errors.Is(err, codevaldgit.ErrMergeRequestNotOpen) {
		t.Errorf("second CloseMergeRequest: got %v, want ErrMergeRequestNotOpen", err)
	}
}

// publishedHasTopic returns true when at least one event with the given topic
// appears in events.
func publishedHasTopic(events []publishedEvent, topic string) bool {
	for _, e := range events {
		if e.topic == topic {
			return true
		}
	}
	return false
}

// branchNames extracts the Name field from a slice of branches for error reporting.
func branchNames(bs []codevaldgit.Branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}
