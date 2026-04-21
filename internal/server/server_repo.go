// server_repo.go — Repository lifecycle gRPC handlers.
package server

import (
	"context"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// ── Repository Lifecycle ──────────────────────────────────────────────────────

// InitRepo implements pb.GitServiceServer.
func (s *Server) InitRepo(ctx context.Context, req *pb.InitRepoRequest) (*pb.Repository, error) {
	repo, err := s.mgr.InitRepo(ctx, codevaldgit.CreateRepoRequest{
		Name:          req.GetName(),
		Description:   req.GetDescription(),
		DefaultBranch: req.GetDefaultBranch(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return repoToProto(repo), nil
}

// GetRepository implements pb.GitServiceServer.
func (s *Server) GetRepository(ctx context.Context, req *pb.GetRepositoryRequest) (*pb.Repository, error) {
	repo, err := s.mgr.GetRepository(ctx, req.GetRepositoryId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return repoToProto(repo), nil
}

// GetRepositoryByName implements pb.GitServiceServer.
// It tries a name-based lookup first. If that fails it falls back to an
// ID-based lookup so that callers still using UUIDs (pre-GITFE-013) continue
// to work.
func (s *Server) GetRepositoryByName(ctx context.Context, req *pb.GetRepositoryByNameRequest) (*pb.Repository, error) {
	repo, err := s.mgr.GetRepositoryByName(ctx, req.GetRepositoryName())
	if err != nil {
		// Fallback: the value might be a UUID from a pre-migration caller.
		repo, err = s.mgr.GetRepository(ctx, req.GetRepositoryName())
		if err != nil {
			return nil, toGRPCError(err)
		}
	}
	return repoToProto(repo), nil
}

// ListRepositories implements pb.GitServiceServer.
func (s *Server) ListRepositories(ctx context.Context, _ *pb.ListRepositoriesRequest) (*pb.ListRepositoriesResponse, error) {
	repos, err := s.mgr.ListRepositories(ctx)
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.Repository, len(repos))
	for i, r := range repos {
		out[i] = repoToProto(r)
	}
	return &pb.ListRepositoriesResponse{Repositories: out}, nil
}

// DeleteRepo implements pb.GitServiceServer.
func (s *Server) DeleteRepo(ctx context.Context, req *pb.DeleteRepoRequest) (*pb.DeleteRepoResponse, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	if err := s.mgr.DeleteRepo(ctx, repoID); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.DeleteRepoResponse{}, nil
}

// PurgeRepo implements pb.GitServiceServer.
func (s *Server) PurgeRepo(ctx context.Context, req *pb.PurgeRepoRequest) (*pb.PurgeRepoResponse, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	if err := s.mgr.PurgeRepo(ctx, repoID); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.PurgeRepoResponse{}, nil
}
