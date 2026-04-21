// server_files.go — File operation gRPC handlers.
package server

import (
	"context"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

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
	log.Printf("[gRPC ReadFile] branch_id=%q branch_name=%q repository_name=%q path=%q",
		req.GetBranchId(), req.GetBranchName(), req.GetRepositoryName(), req.GetPath())
	branchID := req.GetBranchId()
	if req.GetBranchName() != "" {
		repoID, err := s.resolveRepoID(ctx, "", req.GetRepositoryName())
		if err != nil {
			log.Printf("[gRPC ReadFile] resolveRepoID error: %v", err)
			return nil, toGRPCError(err)
		}
		branchID, err = s.resolveBranchID(ctx, repoID, req.GetBranchName())
		if err != nil {
			log.Printf("[gRPC ReadFile] resolveBranchID error: %v", err)
			return nil, toGRPCError(err)
		}
		log.Printf("[gRPC ReadFile] resolved → branchID=%q", branchID)
	}
	log.Printf("[gRPC ReadFile] branchID=%s path=%q", branchID, req.GetPath())
	blob, err := s.mgr.ReadFile(ctx, branchID, req.GetPath())
	if err != nil {
		log.Printf("[gRPC ReadFile] error branchID=%s path=%q: %v", branchID, req.GetPath(), err)
		return nil, toGRPCError(err)
	}
	log.Printf("[gRPC ReadFile] OK branchID=%s path=%q blobID=%s encoding=%s contentLen=%d",
		branchID, req.GetPath(), blob.ID, blob.Encoding, len(blob.Content))
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
	log.Printf("[gRPC ListDirectory] branch_id=%q branch_name=%q repository_name=%q path=%q",
		req.GetBranchId(), req.GetBranchName(), req.GetRepositoryName(), req.GetPath())
	branchID := req.GetBranchId()
	if req.GetBranchName() != "" {
		repoID, err := s.resolveRepoID(ctx, "", req.GetRepositoryName())
		if err != nil {
			log.Printf("[gRPC ListDirectory] resolveRepoID error: %v", err)
			return nil, toGRPCError(err)
		}
		branchID, err = s.resolveBranchID(ctx, repoID, req.GetBranchName())
		if err != nil {
			log.Printf("[gRPC ListDirectory] resolveBranchID error: %v", err)
			return nil, toGRPCError(err)
		}
		log.Printf("[gRPC ListDirectory] resolved → branchID=%q", branchID)
	}
	entries, err := s.mgr.ListDirectory(ctx, branchID, req.GetPath())
	if err != nil {
		log.Printf("[gRPC ListDirectory] mgr.ListDirectory error branchID=%q path=%q: %v", branchID, req.GetPath(), err)
		return nil, toGRPCError(err)
	}
	out := make([]*pb.FileEntry, len(entries))
	for i, e := range entries {
		out[i] = fileEntryToProto(e)
	}
	return &pb.ListDirectoryResponse{Entries: out}, nil
}
