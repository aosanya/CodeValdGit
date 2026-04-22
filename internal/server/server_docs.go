// Package server — this file implements gRPC handlers for the documentation
// layer (GIT-021b): keyword CRUD, branch-scoped edge CRUD, and graph queries.
// All handlers delegate to the injected [codevaldgit.GitManager].
package server

import (
	"context"
	"encoding/json"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// ── Keyword CRUD ──────────────────────────────────────────────────────────────

// CreateKeyword implements pb.GitServiceServer.
func (s *Server) CreateKeyword(ctx context.Context, req *pb.CreateKeywordRequest) (*pb.Keyword, error) {
	kw, err := s.mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Scope:       req.GetScope(),
		ParentID:    req.GetParentId(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return keywordToProto(kw), nil
}

// GetKeyword implements pb.GitServiceServer.
func (s *Server) GetKeyword(ctx context.Context, req *pb.GetKeywordRequest) (*pb.Keyword, error) {
	kw, err := s.mgr.GetKeyword(ctx, req.GetKeywordId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return keywordToProto(kw), nil
}

// ListKeywords implements pb.GitServiceServer.
func (s *Server) ListKeywords(ctx context.Context, req *pb.ListKeywordsRequest) (*pb.ListKeywordsResponse, error) {
	keywords, err := s.mgr.ListKeywords(ctx, codevaldgit.KeywordFilter{
		Scope:    req.GetScope(),
		ParentID: req.GetParentId(),
		Limit:    int(req.GetLimit()),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.Keyword, len(keywords))
	for i, kw := range keywords {
		out[i] = keywordToProto(kw)
	}
	return &pb.ListKeywordsResponse{Keywords: out}, nil
}

// GetKeywordTree implements pb.GitServiceServer.
func (s *Server) GetKeywordTree(ctx context.Context, req *pb.GetKeywordTreeRequest) (*pb.GetKeywordTreeResponse, error) {
	nodes, err := s.mgr.GetKeywordTree(ctx, req.GetKeywordId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	out := make([]*pb.KeywordTreeNode, len(nodes))
	for i, n := range nodes {
		out[i] = keywordTreeNodeToProto(n)
	}
	return &pb.GetKeywordTreeResponse{Nodes: out}, nil
}

// UpdateKeyword implements pb.GitServiceServer.
func (s *Server) UpdateKeyword(ctx context.Context, req *pb.UpdateKeywordRequest) (*pb.Keyword, error) {
	kw, err := s.mgr.UpdateKeyword(ctx, req.GetKeywordId(), codevaldgit.UpdateKeywordRequest{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Scope:       req.GetScope(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return keywordToProto(kw), nil
}

// DeleteKeyword implements pb.GitServiceServer.
func (s *Server) DeleteKeyword(ctx context.Context, req *pb.DeleteKeywordRequest) (*pb.DeleteKeywordResponse, error) {
	if err := s.mgr.DeleteKeyword(ctx, req.GetKeywordId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.DeleteKeywordResponse{}, nil
}

// ── Branch-Scoped Edge CRUD ───────────────────────────────────────────────────

// CreateEdge implements pb.GitServiceServer.
func (s *Server) CreateEdge(ctx context.Context, req *pb.CreateEdgeRequest) (*pb.CreateEdgeResponse, error) {
	if err := s.mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         req.GetBranchId(),
		FromEntityID:     req.GetFromEntityId(),
		RelationshipName: req.GetRelationshipName(),
		ToEntityID:       req.GetToEntityId(),
	}); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.CreateEdgeResponse{}, nil
}

// DeleteEdge implements pb.GitServiceServer.
func (s *Server) DeleteEdge(ctx context.Context, req *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	if err := s.mgr.DeleteEdge(ctx, codevaldgit.DeleteEdgeRequest{
		BranchID:         req.GetBranchId(),
		FromEntityID:     req.GetFromEntityId(),
		RelationshipName: req.GetRelationshipName(),
		ToEntityID:       req.GetToEntityId(),
	}); err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.DeleteEdgeResponse{}, nil
}

// ── Graph Queries ─────────────────────────────────────────────────────────────

// GetNeighborhood implements pb.GitServiceServer.
func (s *Server) GetNeighborhood(ctx context.Context, req *pb.GetNeighborhoodRequest) (*pb.GraphResult, error) {
	result, err := s.mgr.GetNeighborhood(ctx, req.GetBranchId(), req.GetEntityId(), int(req.GetDepth()))
	if err != nil {
		return nil, toGRPCError(err)
	}
	return graphResultToProto(result), nil
}

// SearchByKeywords implements pb.GitServiceServer.
func (s *Server) SearchByKeywords(ctx context.Context, req *pb.SearchByKeywordsRequest) (*pb.GraphResult, error) {
	result, err := s.mgr.SearchByKeywords(ctx, codevaldgit.SearchByKeywordsRequest{
		BranchID:  req.GetBranchId(),
		Keywords:  req.GetKeywords(),
		MatchMode: codevaldgit.KeywordMatchMode(req.GetMatchMode()),
		Cascade:   req.GetCascade(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return graphResultToProto(result), nil
}

// QueryGraph implements pb.GitServiceServer.
func (s *Server) QueryGraph(ctx context.Context, req *pb.QueryGraphRequest) (*pb.GraphResult, error) {
	result, err := s.mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID:      req.GetBranchId(),
		Limit:         int(req.GetLimit()),
		SortBy:        req.GetSortBy(),
		Signals:       req.GetSignals(),
		KeywordIDs:    req.GetKeywordIds(),
		FileTypes:     req.GetFileTypes(),
		Folders:       req.GetFolders(),
		Relationships: req.GetRelationships(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return graphResultToProto(result), nil
}

// ── Mappers ───────────────────────────────────────────────────────────────────

// keywordToProto converts a domain Keyword to its proto representation.
func keywordToProto(kw codevaldgit.Keyword) *pb.Keyword {
	return &pb.Keyword{
		Id:          kw.ID,
		Name:        kw.Name,
		Description: kw.Description,
		Scope:       kw.Scope,
		ParentId:    kw.ParentID,
		ChildIds:    kw.ChildIDs,
		CreatedAt:   kw.CreatedAt,
		UpdatedAt:   kw.UpdatedAt,
	}
}

// keywordTreeNodeToProto recursively converts a domain KeywordTreeNode to proto.
func keywordTreeNodeToProto(n codevaldgit.KeywordTreeNode) *pb.KeywordTreeNode {
	children := make([]*pb.KeywordTreeNode, len(n.Children))
	for i, c := range n.Children {
		children[i] = keywordTreeNodeToProto(c)
	}
	return &pb.KeywordTreeNode{
		Keyword:  keywordToProto(n.Keyword),
		Children: children,
	}
}

// graphResultToProto converts a domain GraphResult to its proto representation.
// GraphNode properties are JSON-encoded to fit the string field in the proto.
func graphResultToProto(r codevaldgit.GraphResult) *pb.GraphResult {
	nodes := make([]*pb.GraphNode, len(r.Nodes))
	for i, n := range r.Nodes {
		props, _ := json.Marshal(n.Properties)
		nodes[i] = &pb.GraphNode{
			Id:         n.ID,
			TypeId:     n.TypeID,
			Properties: string(props),
		}
	}
	edges := make([]*pb.GraphEdge, len(r.Edges))
	for i, e := range r.Edges {
		edges[i] = &pb.GraphEdge{
			Id:     e.ID,
			Name:   e.Name,
			FromId: e.FromID,
			ToId:   e.ToID,
		}
	}
	return &pb.GraphResult{Nodes: nodes, Edges: edges}
}
