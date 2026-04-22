// server_history.go — Commit log and diff gRPC handlers.
package server

import (
	"context"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// ── History ───────────────────────────────────────────────────────────────────

// Log implements pb.GitServiceServer.
func (s *Server) Log(ctx context.Context, req *pb.LogRequest) (*pb.LogResponse, error) {
	log.Printf("[gRPC Log] branch_id=%q branch_name=%q repository_name=%q path=%q limit=%d",
		req.GetBranchId(), req.GetBranchName(), req.GetRepositoryName(), req.GetPath(), req.GetLimit())
	branchID := req.GetBranchId()
	if req.GetBranchName() != "" {
		repoID, err := s.resolveRepoID(ctx, "", req.GetRepositoryName())
		if err != nil {
			log.Printf("[gRPC Log] resolveRepoID error: %v", err)
			return nil, toGRPCError(err)
		}
		branchID, err = s.resolveBranchID(ctx, repoID, req.GetBranchName())
		if err != nil {
			log.Printf("[gRPC Log] resolveBranchID error: %v", err)
			return nil, toGRPCError(err)
		}
		log.Printf("[gRPC Log] resolved → branchID=%q", branchID)
	}
	entries, err := s.mgr.Log(ctx, branchID, codevaldgit.LogFilter{
		Path:  req.GetPath(),
		Limit: int(req.GetLimit()),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.CommitEntry, len(entries))
	for i, e := range entries {
		out[i] = commitEntryToProto(e)
	}
	return &pb.LogResponse{Commits: out}, nil
}

// Diff implements pb.GitServiceServer.
func (s *Server) Diff(ctx context.Context, req *pb.DiffRequest) (*pb.DiffResponse, error) {
	diffs, err := s.mgr.Diff(ctx, req.GetFromRef(), req.GetToRef())
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.FileDiff, len(diffs))
	for i, d := range diffs {
		out[i] = fileDiffToProto(d)
	}
	return &pb.DiffResponse{Diffs: out}, nil
}
