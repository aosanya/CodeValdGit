// server_mergerequests.go — MergeRequest gRPC handlers.
//
// All business logic lives in [codevaldgit.GitManager]. This file is wiring
// only: it resolves human-readable IDs to entity IDs, delegates to the
// manager, and maps domain objects to proto.
package server

import (
	"context"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// CreateMergeRequest implements pb.GitServiceServer.
func (s *Server) CreateMergeRequest(ctx context.Context, req *pb.CreateMergeRequestRequest) (*pb.MergeRequest, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}

	sourceID := req.GetSourceBranchId()
	if sourceID == "" && req.GetSourceBranchName() != "" {
		b, err := s.mgr.GetBranchByName(ctx, repoID, req.GetSourceBranchName())
		if err != nil {
			return nil, toGRPCError(err)
		}
		sourceID = b.ID
	}

	targetID := req.GetTargetBranchId()
	if targetID == "" && req.GetTargetBranchName() != "" {
		b, err := s.mgr.GetBranchByName(ctx, repoID, req.GetTargetBranchName())
		if err != nil {
			return nil, toGRPCError(err)
		}
		targetID = b.ID
	}

	mr, err := s.mgr.CreateMergeRequest(ctx, codevaldgit.CreateMergeRequestRequest{
		RepositoryID:   repoID,
		Title:          req.GetTitle(),
		Description:    req.GetDescription(),
		SourceBranchID: sourceID,
		TargetBranchID: targetID,
		AuthorName:     req.GetAuthorName(),
		WorkflowRunID:  req.GetWorkflowRunId(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return mergeRequestToProto(mr), nil
}

// GetMergeRequest implements pb.GitServiceServer.
func (s *Server) GetMergeRequest(ctx context.Context, req *pb.GetMergeRequestRequest) (*pb.MergeRequest, error) {
	mr, err := s.mgr.GetMergeRequest(ctx, req.GetMergeRequestId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return mergeRequestToProto(mr), nil
}

// ListMergeRequests implements pb.GitServiceServer.
// Filters by repository, status, and/or workflow_run_id (FEAT-20260602-001).
// Empty filters disable each constraint.
func (s *Server) ListMergeRequests(ctx context.Context, req *pb.ListMergeRequestsRequest) (*pb.ListMergeRequestsResponse, error) {
	var repoID string
	if req.GetRepositoryId() != "" || req.GetRepositoryName() != "" {
		resolved, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
		if err != nil {
			return nil, toGRPCError(err)
		}
		repoID = resolved
	}
	mrs, err := s.mgr.ListMergeRequests(ctx, codevaldgit.MergeRequestFilter{
		RepositoryID:  repoID,
		Status:        req.GetStatus(),
		WorkflowRunID: req.GetWorkflowRunId(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.MergeRequest, len(mrs))
	for i, mr := range mrs {
		out[i] = mergeRequestToProto(mr)
	}
	return &pb.ListMergeRequestsResponse{MergeRequests: out}, nil
}

// CompleteMergeRequest implements pb.GitServiceServer.
func (s *Server) CompleteMergeRequest(ctx context.Context, req *pb.CompleteMergeRequestRequest) (*pb.MergeRequest, error) {
	mr, err := s.mgr.CompleteMergeRequest(ctx, req.GetMergeRequestId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return mergeRequestToProto(mr), nil
}

// CloseMergeRequest implements pb.GitServiceServer.
func (s *Server) CloseMergeRequest(ctx context.Context, req *pb.CloseMergeRequestRequest) (*pb.MergeRequest, error) {
	mr, err := s.mgr.CloseMergeRequest(ctx, req.GetMergeRequestId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return mergeRequestToProto(mr), nil
}
