// Package registrar — history_routes.go
// Commit log and diff HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// historyRoutes returns routes for commit log and diff operations.
// The log route uses {branchName} with repository_name + branch_name bindings;
// resolveBranchID falls back to UUID so existing UUID callers still work.
func historyRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
	bname := types.PathBinding{URLParam: "branchName", Field: "branch_name"}
	return []types.RouteInfo{
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}/log",
			Capability:   "log",
			GrpcMethod:   "/codevaldgit.v1.GitService/Log",
			PathBindings: []types.PathBinding{rid, bname},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/diff",
			Capability:   "diff",
			GrpcMethod:   "/codevaldgit.v1.GitService/Diff",
			PathBindings: []types.PathBinding{rid},
		},
	}
}
