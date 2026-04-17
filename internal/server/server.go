// Package server implements the GitService gRPC handler.
// It wraps a [codevaldgit.GitManager] and translates between proto messages
// and domain types. No business logic lives here — all calls delegate to
// the injected GitManager.
package server

import (
	"context"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// Server implements pb.GitServiceServer by wrapping a codevaldgit.GitManager.
// Construct via New; register with grpc.Server using
// pb.RegisterGitServiceServer.
type Server struct {
	pb.UnimplementedGitServiceServer
	mgr codevaldgit.GitManager
}

// New constructs a Server backed by the given GitManager.
func New(mgr codevaldgit.GitManager) *Server {
	return &Server{mgr: mgr}
}

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
func (s *Server) GetRepositoryByName(ctx context.Context, req *pb.GetRepositoryByNameRequest) (*pb.Repository, error) {
	repo, err := s.mgr.GetRepositoryByName(ctx, req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return repoToProto(repo), nil
}

// resolveRepoID resolves a repository entity ID from either a direct ID or a
// human-readable name. When repoName is non-empty it takes precedence over
// repoID, allowing URL-based name lookups to work transparently.
func (s *Server) resolveRepoID(ctx context.Context, repoID, repoName string) (string, error) {
	if repoName != "" {
		repo, err := s.mgr.GetRepositoryByName(ctx, repoName)
		if err != nil {
			return "", err
		}
		return repo.ID, nil
	}
	return repoID, nil
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
	log.Printf("[ListBranches] repository_id=%q", repoID)
	branches, err := s.mgr.ListBranches(ctx, repoID)
	if err != nil {
		log.Printf("[ListBranches] error for repository_id=%q: %v", repoID, err)
		return nil, toGRPCError(err)
	}
	log.Printf("[ListBranches] repository_id=%q — found %d branch(es)", repoID, len(branches))
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

// ── Tag Management ────────────────────────────────────────────────────────────

// CreateTag implements pb.GitServiceServer.
func (s *Server) CreateTag(ctx context.Context, req *pb.CreateTagRequest) (*pb.Tag, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	tag, err := s.mgr.CreateTag(ctx, codevaldgit.CreateTagRequest{
		RepositoryID: repoID,
		Name:         req.GetName(),
		CommitID:     req.GetCommitId(),
		Message:      req.GetMessage(),
		TaggerName:   req.GetTaggerName(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return tagToProto(tag), nil
}

// GetTag implements pb.GitServiceServer.
func (s *Server) GetTag(ctx context.Context, req *pb.GetTagRequest) (*pb.Tag, error) {
	tag, err := s.mgr.GetTag(ctx, req.GetTagId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return tagToProto(tag), nil
}

// ListTags implements pb.GitServiceServer.
func (s *Server) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	repoID, err := s.resolveRepoID(ctx, req.GetRepositoryId(), req.GetRepositoryName())
	if err != nil {
		return nil, toGRPCError(err)
	}
	tags, err := s.mgr.ListTags(ctx, repoID)
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.Tag, len(tags))
	for i, t := range tags {
		out[i] = tagToProto(t)
	}
	return &pb.ListTagsResponse{Tags: out}, nil
}

// DeleteTag implements pb.GitServiceServer.
func (s *Server) DeleteTag(ctx context.Context, req *pb.DeleteTagRequest) (*pb.DeleteTagResponse, error) {
	if err := s.mgr.DeleteTag(ctx, req.GetTagId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.DeleteTagResponse{}, nil
}

// ── File Operations ───────────────────────────────────────────────────────────

// WriteFile implements pb.GitServiceServer.
func (s *Server) WriteFile(ctx context.Context, req *pb.WriteFileRequest) (*pb.Commit, error) {
	commit, err := s.mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID:    req.GetBranchId(),
		Path:        req.GetPath(),
		Content:     req.GetContent(),
		Encoding:    req.GetEncoding(),
		AuthorName:  req.GetAuthorName(),
		AuthorEmail: req.GetAuthorEmail(),
		Message:     req.GetMessage(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return commitToProto(commit), nil
}

// ReadFile implements pb.GitServiceServer.
func (s *Server) ReadFile(ctx context.Context, req *pb.ReadFileRequest) (*pb.Blob, error) {
	blob, err := s.mgr.ReadFile(ctx, req.GetBranchId(), req.GetPath())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return blobToProto(blob), nil
}

// DeleteFile implements pb.GitServiceServer.
func (s *Server) DeleteFile(ctx context.Context, req *pb.DeleteFileRequest) (*pb.Commit, error) {
	commit, err := s.mgr.DeleteFile(ctx, codevaldgit.DeleteFileRequest{
		BranchID:    req.GetBranchId(),
		Path:        req.GetPath(),
		AuthorName:  req.GetAuthorName(),
		AuthorEmail: req.GetAuthorEmail(),
		Message:     req.GetMessage(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return commitToProto(commit), nil
}

// ListDirectory implements pb.GitServiceServer.
func (s *Server) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	entries, err := s.mgr.ListDirectory(ctx, req.GetBranchId(), req.GetPath())
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.FileEntry, len(entries))
	for i, e := range entries {
		out[i] = fileEntryToProto(e)
	}
	return &pb.ListDirectoryResponse{Entries: out}, nil
}

// ── History ───────────────────────────────────────────────────────────────────

// Log implements pb.GitServiceServer.
func (s *Server) Log(ctx context.Context, req *pb.LogRequest) (*pb.LogResponse, error) {
	entries, err := s.mgr.Log(ctx, req.GetBranchId(), codevaldgit.LogFilter{
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

// ── Async Repository Import ───────────────────────────────────────────────────

// ImportRepo implements pb.GitServiceServer. It starts an asynchronous clone
// of a remote Git repository into the agency's storage backend and returns a
// job ID that the caller can poll via GetImportStatus.
func (s *Server) ImportRepo(ctx context.Context, req *pb.ImportRepoRequest) (*pb.ImportRepoResponse, error) {
	job, err := s.mgr.ImportRepo(ctx, codevaldgit.ImportRepoRequest{
		Name:          req.GetName(),
		Description:   req.GetDescription(),
		SourceURL:     req.GetSourceUrl(),
		DefaultBranch: req.GetDefaultBranch(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.ImportRepoResponse{JobId: job.ID}, nil
}

// GetImportStatus implements pb.GitServiceServer. It returns the current state
// of an import job identified by job_id.
func (s *Server) GetImportStatus(ctx context.Context, req *pb.GetImportStatusRequest) (*pb.ImportJobResponse, error) {
	job, err := s.mgr.GetImportStatus(ctx, req.GetJobId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return importJobToProto(job), nil
}

// CancelImport implements pb.GitServiceServer. It requests cancellation of a
// running import job; the job must be in a cancellable state.
func (s *Server) CancelImport(ctx context.Context, req *pb.CancelImportRequest) (*pb.CancelImportResponse, error) {
	if err := s.mgr.CancelImport(ctx, req.GetJobId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.CancelImportResponse{}, nil
}

// importJobToProto converts a domain ImportJob to its proto representation.
func importJobToProto(j codevaldgit.ImportJob) *pb.ImportJobResponse {
	return &pb.ImportJobResponse{
		JobId:        j.ID,
		AgencyId:     j.AgencyID,
		SourceUrl:    j.SourceURL,
		Status:       j.Status,
		ErrorMessage: j.ErrorMessage,
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
}
