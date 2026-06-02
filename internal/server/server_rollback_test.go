// server_rollback_test.go covers FEAT-20260602-004 (Git leg) gRPC wiring:
// RollbackByWorkflowRun must forward workflow_run_id, echo the manager's
// counters, and map ErrWorkflowRunIDRequired to InvalidArgument.
package server_test

import (
	"context"
	"errors"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestServer_RollbackByWorkflowRun_ForwardsArgsAndCounters asserts the handler
// passes workflow_run_id to the manager and echoes the counters back through
// the response message.
func TestServer_RollbackByWorkflowRun_ForwardsArgsAndCounters(t *testing.T) {
	const runID = "wfr_server_rollback_001"
	var seen string
	client := newTestServer(t, &fakeGitManager{
		rollbackByWorkflowRun: func(_ context.Context, workflowRunID string) (codevaldgit.RollbackResult, error) {
			seen = workflowRunID
			return codevaldgit.RollbackResult{
				WorkflowRunID:           workflowRunID,
				BranchesDeleted:         3,
				MergeRequestsRolledBack: 2,
				DefaultBranchesSkipped:  1,
			}, nil
		},
	})

	resp, err := client.RollbackByWorkflowRun(context.Background(), &pb.RollbackByWorkflowRunRequest{
		WorkflowRunId: runID,
	})
	if err != nil {
		t.Fatalf("RollbackByWorkflowRun: %v", err)
	}
	if seen != runID {
		t.Errorf("manager saw workflow_run_id=%q, want %q", seen, runID)
	}
	if resp.GetWorkflowRunId() != runID {
		t.Errorf("resp.workflow_run_id=%q, want %q", resp.GetWorkflowRunId(), runID)
	}
	if resp.GetBranchesDeleted() != 3 {
		t.Errorf("branches_deleted=%d, want 3", resp.GetBranchesDeleted())
	}
	if resp.GetMergeRequestsRolledBack() != 2 {
		t.Errorf("merge_requests_rolled_back=%d, want 2", resp.GetMergeRequestsRolledBack())
	}
	if resp.GetDefaultBranchesSkipped() != 1 {
		t.Errorf("default_branches_skipped=%d, want 1", resp.GetDefaultBranchesSkipped())
	}
}

// TestServer_RollbackByWorkflowRun_EmptyRunIDIsInvalidArgument asserts the
// ErrWorkflowRunIDRequired sentinel surfaces as gRPC InvalidArgument so the
// CodeValdWork coordinator can distinguish "bad request" from "Git failed".
func TestServer_RollbackByWorkflowRun_EmptyRunIDIsInvalidArgument(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		rollbackByWorkflowRun: func(_ context.Context, workflowRunID string) (codevaldgit.RollbackResult, error) {
			if workflowRunID == "" {
				return codevaldgit.RollbackResult{}, codevaldgit.ErrWorkflowRunIDRequired
			}
			return codevaldgit.RollbackResult{WorkflowRunID: workflowRunID}, nil
		},
	})

	_, err := client.RollbackByWorkflowRun(context.Background(), &pb.RollbackByWorkflowRunRequest{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument; err=%v", st.Code(), err)
	}
}

// TestServer_RollbackByWorkflowRun_ManagerInternalErrorMapsToInternal asserts
// that an unexpected manager error reaches the client as gRPC Internal.
func TestServer_RollbackByWorkflowRun_ManagerInternalErrorMapsToInternal(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		rollbackByWorkflowRun: func(_ context.Context, _ string) (codevaldgit.RollbackResult, error) {
			return codevaldgit.RollbackResult{}, errors.New("graph went sideways")
		},
	})

	_, err := client.RollbackByWorkflowRun(context.Background(), &pb.RollbackByWorkflowRunRequest{
		WorkflowRunId: "wfr_internal_check",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal; err=%v", st.Code(), err)
	}
}
