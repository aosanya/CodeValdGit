// server_workflowrun_test.go covers the FEAT-20260602-001 wiring:
// workflow_run_id propagation through CreateBranch, ListBranches filter, and
// the new MergeRequest RPCs.
package server_test

import (
	"context"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"google.golang.org/grpc/codes"
)

// TestServer_CreateBranch_ForwardsWorkflowRunID asserts that the gRPC handler
// passes workflow_run_id through to the manager and surfaces it in the
// returned Branch proto.
func TestServer_CreateBranch_ForwardsWorkflowRunID(t *testing.T) {
	const runID = "wfr_test_001"
	var seen string
	client := newTestServer(t, &fakeGitManager{
		createBranch: func(_ context.Context, req codevaldgit.CreateBranchRequest) (codevaldgit.Branch, error) {
			seen = req.WorkflowRunID
			return codevaldgit.Branch{ID: "br-1", Name: req.Name, WorkflowRunID: req.WorkflowRunID}, nil
		},
	})
	resp, err := client.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Name:          "task/feature",
		WorkflowRunId: runID,
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if seen != runID {
		t.Errorf("manager saw workflow_run_id=%q, want %q", seen, runID)
	}
	if resp.GetWorkflowRunId() != runID {
		t.Errorf("resp.workflow_run_id=%q, want %q", resp.GetWorkflowRunId(), runID)
	}
}

// TestServer_ListBranches_FilterByWorkflowRunID asserts that the handler
// invokes ListBranchesFiltered with the run ID and returns only the matching
// branches.
func TestServer_ListBranches_FilterByWorkflowRunID(t *testing.T) {
	const runID = "wfr_test_002"
	client := newTestServer(t, &fakeGitManager{
		getRepository: func(_ context.Context, repoID string) (codevaldgit.Repository, error) {
			return codevaldgit.Repository{ID: repoID, Name: repoID}, nil
		},
		listBranchesFiltered: func(_ context.Context, _ string, filter codevaldgit.BranchFilter) ([]codevaldgit.Branch, error) {
			if filter.WorkflowRunID != runID {
				t.Fatalf("filter.WorkflowRunID = %q, want %q", filter.WorkflowRunID, runID)
			}
			return []codevaldgit.Branch{
				{ID: "br-1", Name: "feature/a", WorkflowRunID: runID},
			}, nil
		},
	})
	resp, err := client.ListBranches(context.Background(), &pb.ListBranchesRequest{
		RepositoryId:  "repo-1",
		WorkflowRunId: runID,
	})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(resp.GetBranches()) != 1 || resp.GetBranches()[0].GetWorkflowRunId() != runID {
		t.Errorf("ListBranches returned %v, want one branch with workflow_run_id=%q", resp.GetBranches(), runID)
	}
}

// TestServer_CreateMergeRequest_ForwardsWorkflowRunID asserts that opening an
// MR carries the workflow_run_id and that the returned proto exposes it.
func TestServer_CreateMergeRequest_ForwardsWorkflowRunID(t *testing.T) {
	const runID = "wfr_test_003"
	var seen codevaldgit.CreateMergeRequestRequest
	client := newTestServer(t, &fakeGitManager{
		getRepository: func(_ context.Context, repoID string) (codevaldgit.Repository, error) {
			return codevaldgit.Repository{ID: repoID, Name: repoID}, nil
		},
		createMergeRequest: func(_ context.Context, req codevaldgit.CreateMergeRequestRequest) (codevaldgit.MergeRequest, error) {
			seen = req
			return codevaldgit.MergeRequest{
				ID:             "mr-1",
				RepositoryID:   req.RepositoryID,
				Title:          req.Title,
				SourceBranchID: req.SourceBranchID,
				TargetBranchID: req.TargetBranchID,
				Status:         codevaldgit.MergeRequestStatusOpen,
				WorkflowRunID:  req.WorkflowRunID,
			}, nil
		},
	})
	resp, err := client.CreateMergeRequest(context.Background(), &pb.CreateMergeRequestRequest{
		RepositoryId:   "repo-1",
		Title:          "Add widget",
		SourceBranchId: "br-source",
		TargetBranchId: "br-target",
		WorkflowRunId:  runID,
	})
	if err != nil {
		t.Fatalf("CreateMergeRequest: %v", err)
	}
	if seen.WorkflowRunID != runID {
		t.Errorf("manager saw workflow_run_id=%q, want %q", seen.WorkflowRunID, runID)
	}
	if resp.GetWorkflowRunId() != runID {
		t.Errorf("resp.workflow_run_id=%q, want %q", resp.GetWorkflowRunId(), runID)
	}
	if resp.GetStatus() != codevaldgit.MergeRequestStatusOpen {
		t.Errorf("resp.status=%q, want %q", resp.GetStatus(), codevaldgit.MergeRequestStatusOpen)
	}
}

// TestServer_ListMergeRequests_FilterByWorkflowRunID asserts that filter
// propagation works for MRs.
func TestServer_ListMergeRequests_FilterByWorkflowRunID(t *testing.T) {
	const runID = "wfr_test_004"
	client := newTestServer(t, &fakeGitManager{
		listMergeRequests: func(_ context.Context, filter codevaldgit.MergeRequestFilter) ([]codevaldgit.MergeRequest, error) {
			if filter.WorkflowRunID != runID {
				t.Fatalf("filter.WorkflowRunID = %q, want %q", filter.WorkflowRunID, runID)
			}
			if filter.Status != codevaldgit.MergeRequestStatusOpen {
				t.Fatalf("filter.Status = %q, want %q", filter.Status, codevaldgit.MergeRequestStatusOpen)
			}
			return []codevaldgit.MergeRequest{
				{ID: "mr-1", Title: "T", Status: codevaldgit.MergeRequestStatusOpen, WorkflowRunID: runID},
			}, nil
		},
	})
	resp, err := client.ListMergeRequests(context.Background(), &pb.ListMergeRequestsRequest{
		WorkflowRunId: runID,
		Status:        codevaldgit.MergeRequestStatusOpen,
	})
	if err != nil {
		t.Fatalf("ListMergeRequests: %v", err)
	}
	if len(resp.GetMergeRequests()) != 1 {
		t.Fatalf("ListMergeRequests returned %d, want 1", len(resp.GetMergeRequests()))
	}
	got := resp.GetMergeRequests()[0]
	if got.GetWorkflowRunId() != runID {
		t.Errorf("got workflow_run_id=%q, want %q", got.GetWorkflowRunId(), runID)
	}
}

// TestServer_CompleteMergeRequest_NotOpen asserts the FailedPrecondition
// mapping for [codevaldgit.ErrMergeRequestNotOpen].
func TestServer_CompleteMergeRequest_NotOpen(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		completeMergeRequest: func(_ context.Context, _ string) (codevaldgit.MergeRequest, error) {
			return codevaldgit.MergeRequest{}, codevaldgit.ErrMergeRequestNotOpen
		},
	})
	_, err := client.CompleteMergeRequest(context.Background(), &pb.CompleteMergeRequestRequest{
		MergeRequestId: "mr-1",
	})
	if grpcCode(err) != codes.FailedPrecondition {
		t.Errorf("got code %v, want FailedPrecondition", grpcCode(err))
	}
}

// TestServer_GetMergeRequest_NotFound asserts NotFound mapping.
func TestServer_GetMergeRequest_NotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		getMergeRequest: func(_ context.Context, _ string) (codevaldgit.MergeRequest, error) {
			return codevaldgit.MergeRequest{}, codevaldgit.ErrMergeRequestNotFound
		},
	})
	_, err := client.GetMergeRequest(context.Background(), &pb.GetMergeRequestRequest{
		MergeRequestId: "unknown",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}
