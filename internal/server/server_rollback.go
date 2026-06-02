// server_rollback.go — Workflow-run rollback gRPC handler.
//
// FEAT-20260602-004 (Git leg). The handler is wiring only — all logic lives
// in [codevaldgit.GitManager.RollbackByWorkflowRun].
package server

import (
	"context"

	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// RollbackByWorkflowRun implements pb.GitServiceServer.
func (s *Server) RollbackByWorkflowRun(ctx context.Context, req *pb.RollbackByWorkflowRunRequest) (*pb.RollbackByWorkflowRunResponse, error) {
	result, err := s.mgr.RollbackByWorkflowRun(ctx, req.GetWorkflowRunId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.RollbackByWorkflowRunResponse{
		WorkflowRunId:           result.WorkflowRunID,
		BranchesDeleted:         int32(result.BranchesDeleted),
		MergeRequestsRolledBack: int32(result.MergeRequestsRolledBack),
		DefaultBranchesSkipped:  int32(result.DefaultBranchesSkipped),
	}, nil
}
