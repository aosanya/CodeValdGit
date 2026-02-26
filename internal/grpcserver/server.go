package grpcserver

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// Server implements pb.RepoServiceServer by wrapping a codevaldgit.RepoManager.
// Construct via New; register with grpc.Server using
// pb.RegisterRepoServiceServer.
type Server struct {
	pb.UnimplementedRepoServiceServer
	mgr codevaldgit.RepoManager
}

// New constructs a Server backed by the given RepoManager.
func New(mgr codevaldgit.RepoManager) *Server {
	return &Server{mgr: mgr}
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

// InitRepo implements pb.RepoServiceServer.
func (s *Server) InitRepo(ctx context.Context, req *pb.InitRepoRequest) (*pb.InitRepoResponse, error) {
	if err := s.mgr.InitRepo(ctx, req.AgencyId); err != nil {
		return nil, mapError(err)
	}
	return &pb.InitRepoResponse{}, nil
}

// DeleteRepo implements pb.RepoServiceServer.
func (s *Server) DeleteRepo(ctx context.Context, req *pb.DeleteRepoRequest) (*pb.DeleteRepoResponse, error) {
	if err := s.mgr.DeleteRepo(ctx, req.AgencyId); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteRepoResponse{}, nil
}

// PurgeRepo implements pb.RepoServiceServer.
func (s *Server) PurgeRepo(ctx context.Context, req *pb.PurgeRepoRequest) (*pb.PurgeRepoResponse, error) {
	if err := s.mgr.PurgeRepo(ctx, req.AgencyId); err != nil {
		return nil, mapError(err)
	}
	return &pb.PurgeRepoResponse{}, nil
}

// ── Branch Operations ─────────────────────────────────────────────────────────

// CreateBranch implements pb.RepoServiceServer.
func (s *Server) CreateBranch(ctx context.Context, req *pb.CreateBranchRequest) (*pb.CreateBranchResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := repo.CreateBranch(ctx, req.TaskId); err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateBranchResponse{}, nil
}

// MergeBranch implements pb.RepoServiceServer.
// On a content conflict it returns codes.Aborted with a MergeConflictInfo detail.
func (s *Server) MergeBranch(ctx context.Context, req *pb.MergeBranchRequest) (*pb.MergeBranchResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := repo.MergeBranch(ctx, req.TaskId); err != nil {
		return nil, mapError(err)
	}
	return &pb.MergeBranchResponse{}, nil
}

// DeleteBranch implements pb.RepoServiceServer.
func (s *Server) DeleteBranch(ctx context.Context, req *pb.DeleteBranchRequest) (*pb.DeleteBranchResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := repo.DeleteBranch(ctx, req.TaskId); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteBranchResponse{}, nil
}

// ── File Operations ───────────────────────────────────────────────────────────

// WriteFile implements pb.RepoServiceServer.
func (s *Server) WriteFile(ctx context.Context, req *pb.WriteFileRequest) (*pb.WriteFileResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := repo.WriteFile(ctx, req.TaskId, req.Path, req.Content, req.Author, req.Message); err != nil {
		return nil, mapError(err)
	}
	return &pb.WriteFileResponse{}, nil
}

// ReadFile implements pb.RepoServiceServer.
func (s *Server) ReadFile(ctx context.Context, req *pb.ReadFileRequest) (*pb.ReadFileResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	content, err := repo.ReadFile(ctx, req.Ref, req.Path)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.ReadFileResponse{Content: content}, nil
}

// DeleteFile implements pb.RepoServiceServer.
func (s *Server) DeleteFile(ctx context.Context, req *pb.DeleteFileRequest) (*pb.DeleteFileResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	if err := repo.DeleteFile(ctx, req.TaskId, req.Path, req.Author, req.Message); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteFileResponse{}, nil
}

// ListDirectory implements pb.RepoServiceServer.
func (s *Server) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	entries, err := repo.ListDirectory(ctx, req.Ref, req.Path)
	if err != nil {
		return nil, mapError(err)
	}

	pbEntries := make([]*pb.FileEntry, len(entries))
	for i, e := range entries {
		pbEntries[i] = &pb.FileEntry{
			Name:  e.Name,
			Path:  e.Path,
			IsDir: e.IsDir,
			Size:  e.Size,
		}
	}
	return &pb.ListDirectoryResponse{Entries: pbEntries}, nil
}

// ── History ───────────────────────────────────────────────────────────────────

// Log implements pb.RepoServiceServer.
func (s *Server) Log(ctx context.Context, req *pb.LogRequest) (*pb.LogResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	commits, err := repo.Log(ctx, req.Ref, req.Path)
	if err != nil {
		return nil, mapError(err)
	}

	pbCommits := make([]*pb.CommitInfo, len(commits))
	for i, c := range commits {
		pbCommits[i] = &pb.CommitInfo{
			Sha:       c.SHA,
			Author:    c.Author,
			Message:   c.Message,
			Timestamp: timestamppb.New(c.Timestamp),
		}
	}
	return &pb.LogResponse{Commits: pbCommits}, nil
}

// Diff implements pb.RepoServiceServer.
func (s *Server) Diff(ctx context.Context, req *pb.DiffRequest) (*pb.DiffResponse, error) {
	repo, err := s.mgr.OpenRepo(ctx, req.AgencyId)
	if err != nil {
		return nil, mapError(err)
	}
	diffs, err := repo.Diff(ctx, req.FromRef, req.ToRef)
	if err != nil {
		return nil, mapError(err)
	}

	pbDiffs := make([]*pb.FileDiff, len(diffs))
	for i, d := range diffs {
		pbDiffs[i] = &pb.FileDiff{
			Path:      d.Path,
			Operation: d.Operation,
			Patch:     d.Patch,
		}
	}
	return &pb.DiffResponse{Diffs: pbDiffs}, nil
}
