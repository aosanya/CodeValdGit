// Package registrar — docs_routes.go
// Knowledge-graph (keyword / edge / neighbourhood) HTTP routes for CodeValdGit
// via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// docsRoutes returns routes for keyword, edge, and graph operations.
func docsRoutes() []types.RouteInfo {
	kwid := types.PathBinding{URLParam: "keywordId", Field: "keyword_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/keywords",
			Capability:   "create_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/CreateKeyword",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "get_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetKeyword",
			PathBindings: []types.PathBinding{kwid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/keywords",
			Capability:   "list_keywords",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListKeywords",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}/tree",
			Capability:   "get_keyword_tree",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetKeywordTree",
			PathBindings: []types.PathBinding{kwid},
		},
		{
			Method:       "PUT",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "update_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/UpdateKeyword",
			PathBindings: []types.PathBinding{kwid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "delete_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteKeyword",
			PathBindings: []types.PathBinding{kwid},
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/edges",
			Capability:   "create_edge",
			GrpcMethod:   "/codevaldgit.v1.GitService/CreateEdge",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/edges",
			Capability:   "delete_edge",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteEdge",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/graph/neighbourhood",
			Capability:   "get_neighbourhood",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetNeighborhood",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/graph/search",
			Capability:   "search_by_keywords",
			GrpcMethod:   "/codevaldgit.v1.GitService/SearchByKeywords",
			PathBindings: []types.PathBinding{},
		},
	}
}
