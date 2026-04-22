// Package registrar — tag_routes.go
// Tag CRUD HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// tagRoutes returns routes for tag CRUD operations.
func tagRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
	tid := types.PathBinding{URLParam: "tagId", Field: "tag_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/tags",
			Capability:   "create_tag",
			GrpcMethod:   "/codevaldgit.v1.GitService/CreateTag",
			PathBindings: []types.PathBinding{rid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/tags",
			Capability:   "list_tags",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListTags",
			PathBindings: []types.PathBinding{rid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/tags/{tagId}",
			Capability:   "get_tag",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetTag",
			PathBindings: []types.PathBinding{rid, tid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/tags/{tagId}",
			Capability:   "delete_tag",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteTag",
			PathBindings: []types.PathBinding{rid, tid},
		},
	}
}
