// Package registrar — file_routes.go
// File read/write HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// fileRoutes returns routes for file read/write operations on a branch.
// For tree and read/delete, the route uses {branchName} with repository_name
// and branch_name bindings. The gRPC handler calls resolveBranchID which
// resolves by name first and falls back to UUID, so existing callers that
// pass a UUID as the segment continue to work.
func fileRoutes() []types.RouteInfo {
	bid := types.PathBinding{URLParam: "branchId", Field: "branch_id"}
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
	bname := types.PathBinding{URLParam: "branchName", Field: "branch_name"}
	return []types.RouteInfo{
		// write_file keeps branchId because WriteFile proto only has branch_id
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/files",
			Capability:   "write_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/WriteFile",
			PathBindings: []types.PathBinding{bid},
			IsWrite:      true,
		},
		// delete_file keeps branchId because DeleteFile proto only has branch_id
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/files",
			Capability:   "delete_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteFile",
			PathBindings: []types.PathBinding{bid},
			IsWrite:      true,
		},
		// read_file and list_directory use branchName so human-readable names work
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}/files",
			Capability:   "read_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/ReadFile",
			PathBindings: []types.PathBinding{rid, bname},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchName}/tree",
			Capability:   "list_directory",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListDirectory",
			PathBindings: []types.PathBinding{rid, bname},
		},
	}
}
