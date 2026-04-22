// Package registrar — branch_routes.go
// Branch CRUD and merge HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// branchRoutes returns routes for branch CRUD and merge operations.
func branchRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
	bname := types.PathBinding{URLParam: "branchName", Field: "branch_name"}
	bid := types.PathBinding{URLParam: "branchId", Field: "branch_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches",
			Capability:   "create_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/CreateBranch",
			PathBindings: []types.PathBinding{rid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches",
			Capability:   "list_branches",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListBranches",
			PathBindings: []types.PathBinding{rid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}",
			Capability:   "get_branch_by_name",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetBranchByName",
			PathBindings: []types.PathBinding{rid, bname},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/id/{branchId}",
			Capability:   "get_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetBranch",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}",
			Capability:   "delete_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteBranch",
			PathBindings: []types.PathBinding{rid, bname},
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}/merge",
			Capability:   "merge_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/MergeBranch",
			PathBindings: []types.PathBinding{rid, bname},
		},
	}
}
