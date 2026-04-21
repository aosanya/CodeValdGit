// server_import.go — Async repository import gRPC handlers.
package server

import (
	"context"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

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
		JobId:         j.ID,
		AgencyId:      j.AgencyID,
		Name:          j.Name,
		SourceUrl:     j.SourceURL,
		DefaultBranch: j.DefaultBranch,
		Status:        j.Status,
		ErrorMessage:  j.ErrorMessage,
		ProgressSteps: j.ProgressSteps,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
	}
}
