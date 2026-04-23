// server_branch.go — Branch management gRPC handlers.
package server

import (
	"context"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// ── Branch Management ─────────────────────────────────────────────────────────

// CreateBranch implements pb.GitServiceServer.
func (s *Server) CreateBranch(ctx context.Context, req *pb.CreateBranchRequest) (*pb.Branch, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	branch, err := s.mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repoID,
		Name:         req.GetName(),
		FromBranchID: req.GetFromBranchId(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return branchToProto(branch), nil
}

// GetBranch implements pb.GitServiceServer.
// It accepts either a branch entity ID or a branch name. When the value does
// not resolve as an ID, resolveBranchByIDOrName is used to look up by name
// within the repository identified by repository_name.
func (s *Server) GetBranch(ctx context.Context, req *pb.GetBranchRequest) (*pb.Branch, error) {
	branch, err := s.mgr.GetBranch(ctx, req.GetBranchId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return branchToProto(branch), nil
}

// ListBranches implements pb.GitServiceServer.
func (s *Server) ListBranches(ctx context.Context, req *pb.ListBranchesRequest) (*pb.ListBranchesResponse, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	branches, err := s.mgr.ListBranches(ctx, repoID)
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.Branch, len(branches))
	for i, b := range branches {
		out[i] = branchToProto(b)
	}
	return &pb.ListBranchesResponse{Branches: out}, nil
}

// DeleteBranch implements pb.GitServiceServer.
func (s *Server) DeleteBranch(ctx context.Context, req *pb.DeleteBranchRequest) (*pb.DeleteBranchResponse, error) {
	if err := s.mgr.DeleteBranch(ctx, req.GetBranchId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.DeleteBranchResponse{}, nil
}

// MergeBranch implements pb.GitServiceServer.
func (s *Server) MergeBranch(ctx context.Context, req *pb.MergeBranchRequest) (*pb.Branch, error) {
	branch, err := s.mgr.MergeBranch(ctx, req.GetBranchId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return branchToProto(branch), nil
}

// ── On-Demand Branch Fetch ────────────────────────────────────────────────────

// FetchBranch implements pb.GitServiceServer. It triggers an async on-demand
// fetch for a stub branch identified by repo and branch name. The method
// resolves branch_name → branch entity ID via resolveBranchID before
// delegating to [codevaldgit.GitManager.FetchBranch].
func (s *Server) FetchBranch(ctx context.Context, req *pb.FetchBranchRequest) (*pb.FetchBranchJob, error) {
	// The REST route binds {repoName} to repo_id, so the field may carry either
	// a repository name or a UUID — resolveRepoID handles both.
	repoID, err := s.resolveRepoID(ctx, "", req.GetRepoId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	branchID, err := s.resolveBranchID(ctx, repoID, req.GetBranchName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	job, err := s.mgr.FetchBranch(ctx, codevaldgit.FetchBranchRequest{
		RepoID:   repoID,
		BranchID: branchID,
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return fetchBranchJobToProto(job), nil
}

// GetFetchBranchStatus implements pb.GitServiceServer. It returns the current
// state of a branch fetch job identified by job_id.
func (s *Server) GetFetchBranchStatus(ctx context.Context, req *pb.GetFetchBranchStatusRequest) (*pb.FetchBranchJob, error) {
	job, err := s.mgr.GetFetchBranchStatus(ctx, req.GetJobId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return fetchBranchJobToProto(job), nil
}
