// Package registrar — fetch_branch_routes.go
// FetchBranch async history routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// fetchBranchRoutes returns routes for on-demand branch-history fetch operations.
func fetchBranchRoutes() []types.RouteInfo {
	jid := types.PathBinding{URLParam: "jobId", Field: "job_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/fetch-branch",
			Capability:   "fetch_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/FetchBranch",
			PathBindings: []types.PathBinding{},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/fetch-branch/{jobId}/status",
			Capability:   "get_fetch_branch_status",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetFetchBranchStatus",
			PathBindings: []types.PathBinding{jid},
		},
	}
}
