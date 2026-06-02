// git_rollback_test.go covers FEAT-20260602-004 (Git leg) manager behaviour:
// RollbackByWorkflowRun must hard-delete every non-default Branch tagged with
// the given workflow_run_id, flip every matching MergeRequest to
// "rolled_back", emit one git.merge.rolled_back per MR transition, and emit a
// single git.workflow_run.rolled_back summary event regardless of work done.
package codevaldgit_test

import (
	"context"
	"errors"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// rollbackTestHelpers — small inline helpers so the rollback tests don't have
// to grow the shared test fixture in git_manager_test.go.

// countByTopic returns how many recorded events have the given topic.
func countByTopic(events []publishedEvent, topic string) int {
	n := 0
	for _, e := range events {
		if e.topic == topic {
			n++
		}
	}
	return n
}

// fakeUpdate is a tiny convenience wrapper that constructs the entitygraph
// UpdateEntityRequest used by tests to side-load state into the fakeDataManager.
func fakeUpdate(props map[string]any) entitygraph.UpdateEntityRequest {
	return entitygraph.UpdateEntityRequest{Properties: props}
}

// TestGitManager_RollbackByWorkflowRun_DeletesBranchesAndFlipsMRs verifies the
// happy path: branches and MRs from the target run are unwound, branches and
// MRs from a different run are untouched, the default branch is preserved, and
// the summary + per-MR events fire with the right payloads.
func TestGitManager_RollbackByWorkflowRun_DeletesBranchesAndFlipsMRs(t *testing.T) {
	const targetRun = "wfr_rollback_target"
	const otherRun = "wfr_rollback_other"
	mgr, _, pub := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	// Two branches in the target run; one branch in another run; one with no run.
	target1, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/target-1", WorkflowRunID: targetRun,
	})
	if err != nil {
		t.Fatalf("CreateBranch target-1: %v", err)
	}
	target2, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/target-2", WorkflowRunID: targetRun,
	})
	if err != nil {
		t.Fatalf("CreateBranch target-2: %v", err)
	}
	otherBranch, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/other", WorkflowRunID: otherRun,
	})
	if err != nil {
		t.Fatalf("CreateBranch other: %v", err)
	}
	if _, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/no-run",
	}); err != nil {
		t.Fatalf("CreateBranch no-run: %v", err)
	}

	// One MR per branch in the target run; one MR in the other run.
	target1MR, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID: repo.ID, Title: "target-1", SourceBranchID: target1.ID, WorkflowRunID: targetRun,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest target-1: %v", err)
	}
	target2MR, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID: repo.ID, Title: "target-2", SourceBranchID: target2.ID, WorkflowRunID: targetRun,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest target-2: %v", err)
	}
	otherMR, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID: repo.ID, Title: "other", SourceBranchID: otherBranch.ID, WorkflowRunID: otherRun,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest other: %v", err)
	}

	// Drain creation-time events so the assertions below only see rollback events.
	_ = pub.published() // drain — count assertions use total counts below

	result, err := mgr.RollbackByWorkflowRun(ctx, targetRun)
	if err != nil {
		t.Fatalf("RollbackByWorkflowRun: %v", err)
	}
	if result.WorkflowRunID != targetRun {
		t.Errorf("result.WorkflowRunID = %q, want %q", result.WorkflowRunID, targetRun)
	}
	if result.BranchesDeleted != 2 {
		t.Errorf("result.BranchesDeleted = %d, want 2", result.BranchesDeleted)
	}
	if result.MergeRequestsRolledBack != 2 {
		t.Errorf("result.MergeRequestsRolledBack = %d, want 2", result.MergeRequestsRolledBack)
	}
	if result.DefaultBranchesSkipped != 0 {
		t.Errorf("result.DefaultBranchesSkipped = %d, want 0", result.DefaultBranchesSkipped)
	}

	// Target branches are gone; other branches survive.
	if _, err := mgr.GetBranch(ctx, target1.ID); !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("GetBranch(target1) = %v, want ErrBranchNotFound", err)
	}
	if _, err := mgr.GetBranch(ctx, target2.ID); !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("GetBranch(target2) = %v, want ErrBranchNotFound", err)
	}
	if _, err := mgr.GetBranch(ctx, otherBranch.ID); err != nil {
		t.Errorf("GetBranch(other) unexpectedly failed: %v", err)
	}

	// Target MRs are now "rolled_back"; the other MR is untouched.
	got1, err := mgr.GetMergeRequest(ctx, target1MR.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest target-1: %v", err)
	}
	if got1.Status != codevaldgit.MergeRequestStatusRolledBack {
		t.Errorf("MR target-1 status = %q, want %q", got1.Status, codevaldgit.MergeRequestStatusRolledBack)
	}
	got2, err := mgr.GetMergeRequest(ctx, target2MR.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest target-2: %v", err)
	}
	if got2.Status != codevaldgit.MergeRequestStatusRolledBack {
		t.Errorf("MR target-2 status = %q, want %q", got2.Status, codevaldgit.MergeRequestStatusRolledBack)
	}
	gotOther, err := mgr.GetMergeRequest(ctx, otherMR.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest other: %v", err)
	}
	if gotOther.Status != codevaldgit.MergeRequestStatusOpen {
		t.Errorf("other MR status = %q, want %q (untouched)", gotOther.Status, codevaldgit.MergeRequestStatusOpen)
	}

	// Exactly two git.merge.rolled_back events + one git.workflow_run.rolled_back.
	events := pub.published()
	mrEvents := countByTopic(events, codevaldgit.TopicMergeRolledBack)
	summary := countByTopic(events, codevaldgit.TopicWorkflowRunRolledBack)
	if mrEvents != 2 {
		t.Errorf("git.merge.rolled_back count = %d, want 2 (events: %v)", mrEvents, events)
	}
	if summary != 1 {
		t.Errorf("git.workflow_run.rolled_back count = %d, want 1 (events: %v)", summary, events)
	}
}

// TestGitManager_RollbackByWorkflowRun_PreservesMergedSHA verifies that
// MergeRequests previously marked "merged" keep their merged_commit_sha after
// being flipped to "rolled_back" — the value is audit-state, not lifecycle.
func TestGitManager_RollbackByWorkflowRun_PreservesMergedSHA(t *testing.T) {
	const runID = "wfr_rollback_merged"
	mgr, fdm, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	source, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/already-merged", WorkflowRunID: runID,
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	mr, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID: repo.ID, Title: "Merged MR", SourceBranchID: source.ID, WorkflowRunID: runID,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}

	// Directly transition the entity to "merged" with a recorded SHA — we skip
	// MergeBranch to avoid touching the head-commit plumbing, which isn't the
	// behaviour under test here.
	if _, err := fdm.UpdateEntity(ctx, testAgencyID, mr.ID, fakeUpdate(map[string]any{
		"status":            codevaldgit.MergeRequestStatusMerged,
		"merged_commit_sha": "deadbeefdeadbeefdeadbeef",
	})); err != nil {
		t.Fatalf("fake merge transition: %v", err)
	}

	if _, err := mgr.RollbackByWorkflowRun(ctx, runID); err != nil {
		t.Fatalf("RollbackByWorkflowRun: %v", err)
	}

	got, err := mgr.GetMergeRequest(ctx, mr.ID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	if got.Status != codevaldgit.MergeRequestStatusRolledBack {
		t.Errorf("status = %q, want %q", got.Status, codevaldgit.MergeRequestStatusRolledBack)
	}
	if got.MergedCommitSHA != "deadbeefdeadbeefdeadbeef" {
		t.Errorf("merged_commit_sha = %q, want preserved", got.MergedCommitSHA)
	}
}

// TestGitManager_RollbackByWorkflowRun_NoOpWhenRunProducedNothing verifies
// that a run with no Git artifacts produces a zero-counter result and still
// fires the summary event (so the cross-service coordinator can mark the Git
// leg complete).
func TestGitManager_RollbackByWorkflowRun_NoOpWhenRunProducedNothing(t *testing.T) {
	mgr, _, pub := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	_ = pub.published() // drain — count assertions use total counts below

	result, err := mgr.RollbackByWorkflowRun(ctx, "wfr_no_artifacts")
	if err != nil {
		t.Fatalf("RollbackByWorkflowRun: %v", err)
	}
	if result.BranchesDeleted != 0 || result.MergeRequestsRolledBack != 0 {
		t.Errorf("expected zero counters, got %+v", result)
	}
	if countByTopic(pub.published(), codevaldgit.TopicWorkflowRunRolledBack) != 1 {
		t.Errorf("expected summary event even on no-op; got %v", pub.published())
	}
	if countByTopic(pub.published(), codevaldgit.TopicMergeRolledBack) != 0 {
		t.Errorf("expected no git.merge.rolled_back on no-op; got %v", pub.published())
	}
}

// TestGitManager_RollbackByWorkflowRun_IsIdempotent verifies that a second
// invocation on the same run reports zero work because every MR is already
// "rolled_back" and every branch is already gone.
func TestGitManager_RollbackByWorkflowRun_IsIdempotent(t *testing.T) {
	const runID = "wfr_rollback_idempotent"
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	source, _ := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repo.ID, Name: "feature/once", WorkflowRunID: runID,
	})
	if _, err := mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID: repo.ID, Title: "Once", SourceBranchID: source.ID, WorkflowRunID: runID,
	}); err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}

	first, err := mgr.RollbackByWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("first RollbackByWorkflowRun: %v", err)
	}
	if first.BranchesDeleted != 1 || first.MergeRequestsRolledBack != 1 {
		t.Errorf("first result = %+v, want 1 branch + 1 MR", first)
	}

	second, err := mgr.RollbackByWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("second RollbackByWorkflowRun: %v", err)
	}
	if second.BranchesDeleted != 0 || second.MergeRequestsRolledBack != 0 {
		t.Errorf("second result = %+v, want zero work on idempotent re-run", second)
	}
}

// TestGitManager_RollbackByWorkflowRun_RejectsEmptyRunID guards against the
// "match every artifact" footgun: an empty run-id must be rejected with the
// dedicated sentinel so the gRPC layer can map it to InvalidArgument.
func TestGitManager_RollbackByWorkflowRun_RejectsEmptyRunID(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)

	_, err := mgr.RollbackByWorkflowRun(ctx, "")
	if !errors.Is(err, codevaldgit.ErrWorkflowRunIDRequired) {
		t.Errorf("err = %v, want ErrWorkflowRunIDRequired", err)
	}
}

// TestGitManager_RollbackByWorkflowRun_PreservesDefaultBranch verifies that
// a default branch tagged with the rollback run-id (an unexpected but
// possible state) is preserved and surfaced via DefaultBranchesSkipped.
func TestGitManager_RollbackByWorkflowRun_PreservesDefaultBranch(t *testing.T) {
	const runID = "wfr_rollback_default"
	mgr, fdm, _ := newTestManager(t)
	ctx := context.Background()
	repo := mustInitRepo(t, mgr)

	defaultBranch := mustDefaultBranch(t, mgr, repo.ID)

	// Backfill the run-id onto the default branch (simulates a misconfigured
	// orchestrator); the rollback must skip it without deleting the only
	// repository ref.
	if _, err := fdm.UpdateEntity(ctx, testAgencyID, defaultBranch.ID, fakeUpdate(map[string]any{
		"workflow_run_id": runID,
	})); err != nil {
		t.Fatalf("backfill default branch run_id: %v", err)
	}

	result, err := mgr.RollbackByWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("RollbackByWorkflowRun: %v", err)
	}
	if result.DefaultBranchesSkipped != 1 {
		t.Errorf("DefaultBranchesSkipped = %d, want 1", result.DefaultBranchesSkipped)
	}
	if result.BranchesDeleted != 0 {
		t.Errorf("BranchesDeleted = %d, want 0", result.BranchesDeleted)
	}
	if _, err := mgr.GetBranch(ctx, defaultBranch.ID); err != nil {
		t.Errorf("default branch deleted unexpectedly: %v", err)
	}
}
