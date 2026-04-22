// Package registrar — repo_routes.go
// Repository lifecycle HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// repoRoutes returns routes for repository lifecycle operations.
func repoRoutes() []types.RouteInfo {
	rid := []types.PathBinding{{URLParam: "repoName", Field: "repository_name"}}
	return []types.RouteInfo{
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/repositories",
			Capability: "init_repo",
			GrpcMethod: "/codevaldgit.v1.GitService/InitRepo",
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/repositories",
			Capability: "list_repositories",
			GrpcMethod: "/codevaldgit.v1.GitService/ListRepositories",
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}",
			Capability:   "get_repository",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetRepositoryByName",
			PathBindings: rid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}",
			Capability:   "delete_repo",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteRepo",
			PathBindings: rid,
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/purge",
			Capability:   "purge_repo",
			GrpcMethod:   "/codevaldgit.v1.GitService/PurgeRepo",
			PathBindings: rid,
		},
	}
}
