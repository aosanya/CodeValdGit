// server_tag.go — Tag management gRPC handlers.
package server

import (
	"context"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

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
