package server_test

import (
"context"
"testing"

codevaldgit "github.com/aosanya/CodeValdGit"
pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
"google.golang.org/grpc/codes"
)

func TestServer_CreateKeyword_Success(t *testing.T) {
want := codevaldgit.Keyword{ID: "kw-1", Name: "authentication"}
client := newTestServer(t, &fakeGitManager{
createKeyword: func(_ context.Context, _ codevaldgit.CreateKeywordRequest) (codevaldgit.Keyword, error) {
return want, nil
},
})
resp, err := client.CreateKeyword(context.Background(), &pb.CreateKeywordRequest{Name: "authentication"})
if err != nil {
t.Fatalf("CreateKeyword: %v", err)
}
if resp.GetId() != "kw-1" || resp.GetName() != "authentication" {
t.Errorf("got id=%q name=%q, want kw-1/authentication", resp.GetId(), resp.GetName())
}
}

func TestServer_CreateKeyword_AlreadyExists(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
createKeyword: func(_ context.Context, _ codevaldgit.CreateKeywordRequest) (codevaldgit.Keyword, error) {
return codevaldgit.Keyword{}, codevaldgit.ErrKeywordAlreadyExists
},
})
_, err := client.CreateKeyword(context.Background(), &pb.CreateKeywordRequest{Name: "dup"})
if grpcCode(err) != codes.AlreadyExists {
t.Errorf("code = %v, want AlreadyExists", grpcCode(err))
}
}

func TestServer_GetKeyword_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
getKeyword: func(_ context.Context, kwID string) (codevaldgit.Keyword, error) {
return codevaldgit.Keyword{ID: kwID, Name: "grpc"}, nil
},
})
resp, err := client.GetKeyword(context.Background(), &pb.GetKeywordRequest{KeywordId: "kw-99"})
if err != nil {
t.Fatalf("GetKeyword: %v", err)
}
if resp.GetId() != "kw-99" {
t.Errorf("id = %q, want kw-99", resp.GetId())
}
}

func TestServer_GetKeyword_NotFound(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
getKeyword: func(_ context.Context, _ string) (codevaldgit.Keyword, error) {
return codevaldgit.Keyword{}, codevaldgit.ErrKeywordNotFound
},
})
_, err := client.GetKeyword(context.Background(), &pb.GetKeywordRequest{KeywordId: "missing"})
if grpcCode(err) != codes.NotFound {
t.Errorf("code = %v, want NotFound", grpcCode(err))
}
}

func TestServer_ListKeywords_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
listKeywords: func(_ context.Context, _ codevaldgit.KeywordFilter) ([]codevaldgit.Keyword, error) {
return []codevaldgit.Keyword{{ID: "kw-1", Name: "go"}, {ID: "kw-2", Name: "grpc"}}, nil
},
})
resp, err := client.ListKeywords(context.Background(), &pb.ListKeywordsRequest{})
if err != nil {
t.Fatalf("ListKeywords: %v", err)
}
if len(resp.GetKeywords()) != 2 {
t.Errorf("len = %d, want 2", len(resp.GetKeywords()))
}
}

func TestServer_GetKeywordTree_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
getKeywordTree: func(_ context.Context, kwID string) ([]codevaldgit.KeywordTreeNode, error) {
return []codevaldgit.KeywordTreeNode{
{
Keyword:  codevaldgit.Keyword{ID: kwID, Name: "root"},
Children: []codevaldgit.KeywordTreeNode{{Keyword: codevaldgit.Keyword{ID: "child-1", Name: "child"}}},
},
}, nil
},
})
resp, err := client.GetKeywordTree(context.Background(), &pb.GetKeywordTreeRequest{KeywordId: "kw-root"})
if err != nil {
t.Fatalf("GetKeywordTree: %v", err)
}
if len(resp.GetNodes()) != 1 || len(resp.GetNodes()[0].GetChildren()) != 1 {
t.Errorf("unexpected tree shape: nodes=%d children=%d", len(resp.GetNodes()), len(resp.GetNodes()[0].GetChildren()))
}
}

func TestServer_UpdateKeyword_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
updateKeyword: func(_ context.Context, kwID string, req codevaldgit.UpdateKeywordRequest) (codevaldgit.Keyword, error) {
return codevaldgit.Keyword{ID: kwID, Name: req.Name}, nil
},
})
resp, err := client.UpdateKeyword(context.Background(), &pb.UpdateKeywordRequest{KeywordId: "kw-5", Name: "updated"})
if err != nil {
t.Fatalf("UpdateKeyword: %v", err)
}
if resp.GetName() != "updated" {
t.Errorf("name = %q, want updated", resp.GetName())
}
}

func TestServer_UpdateKeyword_NotFound(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
updateKeyword: func(_ context.Context, _ string, _ codevaldgit.UpdateKeywordRequest) (codevaldgit.Keyword, error) {
return codevaldgit.Keyword{}, codevaldgit.ErrKeywordNotFound
},
})
_, err := client.UpdateKeyword(context.Background(), &pb.UpdateKeywordRequest{KeywordId: "nope", Name: "x"})
if grpcCode(err) != codes.NotFound {
t.Errorf("code = %v, want NotFound", grpcCode(err))
}
}

func TestServer_DeleteKeyword_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{})
if _, err := client.DeleteKeyword(context.Background(), &pb.DeleteKeywordRequest{KeywordId: "kw-del"}); err != nil {
t.Fatalf("DeleteKeyword: %v", err)
}
}

func TestServer_CreateEdge_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
createEdge: func(_ context.Context, req codevaldgit.CreateEdgeRequest) error {
if req.RelationshipName != "tagged_with" {
return codevaldgit.ErrInvalidRelationship
}
return nil
},
})
_, err := client.CreateEdge(context.Background(), &pb.CreateEdgeRequest{
BranchId: "branch-1", FromEntityId: "blob-1", RelationshipName: "tagged_with", ToEntityId: "kw-1",
})
if err != nil {
t.Fatalf("CreateEdge: %v", err)
}
}

func TestServer_CreateEdge_InvalidRelationship(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
createEdge: func(_ context.Context, _ codevaldgit.CreateEdgeRequest) error {
return codevaldgit.ErrInvalidRelationship
},
})
_, err := client.CreateEdge(context.Background(), &pb.CreateEdgeRequest{RelationshipName: "bad"})
if grpcCode(err) != codes.InvalidArgument {
t.Errorf("code = %v, want InvalidArgument", grpcCode(err))
}
}

func TestServer_DeleteEdge_NotFound(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
deleteEdge: func(_ context.Context, _ codevaldgit.DeleteEdgeRequest) error {
return codevaldgit.ErrEdgeNotFound
},
})
_, err := client.DeleteEdge(context.Background(), &pb.DeleteEdgeRequest{
BranchId: "b", FromEntityId: "e1", RelationshipName: "tagged_with", ToEntityId: "kw",
})
if grpcCode(err) != codes.NotFound {
t.Errorf("code = %v, want NotFound", grpcCode(err))
}
}

func TestServer_GetNeighborhood_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
getNeighborhood: func(_ context.Context, _, entityID string, _ int) (codevaldgit.GraphResult, error) {
return codevaldgit.GraphResult{
Nodes: []codevaldgit.GraphNode{{ID: entityID, TypeID: "Blob"}},
Edges: []codevaldgit.GraphEdge{{ID: "e-1", Name: "tagged_with", FromID: entityID, ToID: "kw-1"}},
}, nil
},
})
resp, err := client.GetNeighborhood(context.Background(), &pb.GetNeighborhoodRequest{
BranchId: "branch-1", EntityId: "blob-42", Depth: 1,
})
if err != nil {
t.Fatalf("GetNeighborhood: %v", err)
}
if len(resp.GetNodes()) != 1 || len(resp.GetEdges()) != 1 {
t.Errorf("nodes=%d edges=%d, want 1/1", len(resp.GetNodes()), len(resp.GetEdges()))
}
}

func TestServer_SearchByKeywords_Success(t *testing.T) {
client := newTestServer(t, &fakeGitManager{
searchByKeywords: func(_ context.Context, req codevaldgit.SearchByKeywordsRequest) (codevaldgit.GraphResult, error) {
if req.MatchMode == codevaldgit.KeywordMatchModeAND {
return codevaldgit.GraphResult{Nodes: []codevaldgit.GraphNode{{ID: "blob-1", TypeID: "Blob"}}}, nil
}
return codevaldgit.GraphResult{}, nil
},
})
resp, err := client.SearchByKeywords(context.Background(), &pb.SearchByKeywordsRequest{
BranchId: "branch-1", Keywords: []string{"kw-1", "kw-2"}, MatchMode: "AND", Cascade: true,
})
if err != nil {
t.Fatalf("SearchByKeywords: %v", err)
}
if len(resp.GetNodes()) != 1 {
t.Errorf("nodes = %d, want 1", len(resp.GetNodes()))
}
}
