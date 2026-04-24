// Package registrar — fetch_branch_routes.go
// FetchBranch async history routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// fetchBranchRoutes returns routes for on-demand branch-history fetch operations.
// The POST route binds {repoName} to the proto repo_id field; the handler
// resolves it via resolveRepoID so callers may pass either a name or a UUID.
func fetchBranchRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repo_id"}
	bname := types.PathBinding{URLParam: "branchName", Field: "branch_name"}
	jid := types.PathBinding{URLParam: "jobId", Field: "job_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}/fetch",
			Capability:   "fetch_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/FetchBranch",
			PathBindings: []types.PathBinding{rid, bname},
			IsWrite:      true,
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/fetch-jobs/{jobId}",
			Capability:   "get_fetch_branch_status",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetFetchBranchStatus",
			PathBindings: []types.PathBinding{jid},
		},
	}
}
