// Package registrar — this file declares all HTTP routes CodeValdGit exposes
// via CodeValdCross. Routes are grouped by concern and combined by gitRoutes()
// in registrar.go. All gRPC method paths reference the v2 GitService.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// repoRoutes returns routes for repository lifecycle operations.
// A repository ID is required in the path for all single-repo operations;
// POST /repositories creates a new repo and GET /repositories lists them all.
func repoRoutes() []types.RouteInfo {
	rid := []types.PathBinding{{URLParam: "repoId", Field: "repository_id"}}
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
			Pattern:      "/git/{agencyId}/repositories/{repoId}",
			Capability:   "get_repository",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetRepository",
			PathBindings: rid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoId}",
			Capability:   "delete_repo",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteRepo",
			PathBindings: rid,
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoId}/purge",
			Capability:   "purge_repo",
			GrpcMethod:   "/codevaldgit.v1.GitService/PurgeRepo",
			PathBindings: rid,
		},
	}
}

// branchRoutes returns routes for branch CRUD and merge operations.
func branchRoutes() []types.RouteInfo {
	bid := []types.PathBinding{{URLParam: "branchId", Field: "branch_id"}}
	return []types.RouteInfo{
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/branches",
			Capability: "create_branch",
			GrpcMethod: "/codevaldgit.v1.GitService/CreateBranch",
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/branches",
			Capability: "list_branches",
			GrpcMethod: "/codevaldgit.v1.GitService/ListBranches",
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/branches/{branchId}",
			Capability:   "get_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetBranch",
			PathBindings: bid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/branches/{branchId}",
			Capability:   "delete_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteBranch",
			PathBindings: bid,
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/branches/{branchId}/merge",
			Capability:   "merge_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/MergeBranch",
			PathBindings: bid,
		},
	}
}

// tagRoutes returns routes for tag CRUD operations.
func tagRoutes() []types.RouteInfo {
	tid := []types.PathBinding{{URLParam: "tagId", Field: "tag_id"}}
	return []types.RouteInfo{
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/tags",
			Capability: "create_tag",
			GrpcMethod: "/codevaldgit.v1.GitService/CreateTag",
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/tags",
			Capability: "list_tags",
			GrpcMethod: "/codevaldgit.v1.GitService/ListTags",
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/tags/{tagId}",
			Capability:   "get_tag",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetTag",
			PathBindings: tid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/tags/{tagId}",
			Capability:   "delete_tag",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteTag",
			PathBindings: tid,
		},
	}
}

// fileRoutes returns routes for file read/write operations on a branch.
func fileRoutes() []types.RouteInfo {
	bid := []types.PathBinding{{URLParam: "branchId", Field: "branch_id"}}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/branches/{branchId}/files",
			Capability:   "write_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/WriteFile",
			PathBindings: bid,
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/branches/{branchId}/files",
			Capability:   "read_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/ReadFile",
			PathBindings: bid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/branches/{branchId}/files",
			Capability:   "delete_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteFile",
			PathBindings: bid,
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/branches/{branchId}/tree",
			Capability:   "list_directory",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListDirectory",
			PathBindings: bid,
		},
	}
}

// historyRoutes returns routes for commit log and diff operations.
func historyRoutes() []types.RouteInfo {
	bid := []types.PathBinding{{URLParam: "branchId", Field: "branch_id"}}
	return []types.RouteInfo{
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/branches/{branchId}/log",
			Capability:   "log",
			GrpcMethod:   "/codevaldgit.v1.GitService/Log",
			PathBindings: bid,
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/diff",
			Capability: "diff",
			GrpcMethod: "/codevaldgit.v1.GitService/Diff",
		},
	}
}

// smartHTTPRoutes returns the Git Smart HTTP protocol routes served directly
// by CodeValdGit's git-HTTP handler (internal/server/githttp.go) on the cmux
// port. GrpcMethod is empty because these are direct HTTP pass-through routes,
// not gRPC proxy routes. CodeValdCross will need a direct-proxy capability to
// forward these once GIT-009 and the corresponding Cross task are complete.
func smartHTTPRoutes() []types.RouteInfo {
	return []types.RouteInfo{
		{
			Method:     "GET",
			Pattern:    "/{agencyId}/{repoId}/info/refs",
			Capability: "git_info_refs",
		},
		{
			Method:     "POST",
			Pattern:    "/{agencyId}/{repoId}/git-upload-pack",
			Capability: "git_upload_pack",
		},
		{
			Method:     "POST",
			Pattern:    "/{agencyId}/{repoId}/git-receive-pack",
			Capability: "git_receive_pack",
		},
	}
}

// importRoutes returns routes for async repository import operations.
// ImportRepo starts a background clone; GetImportStatus polls the job;
// CancelImport requests cancellation of a running job.
func importRoutes() []types.RouteInfo {
	jid := []types.PathBinding{{URLParam: "jobId", Field: "job_id"}}
	return []types.RouteInfo{
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/import-jobs",
			Capability: "import_repo",
			GrpcMethod: "/codevaldgit.v1.GitService/ImportRepo",
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/import-jobs/{jobId}",
			Capability:   "get_import_status",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetImportStatus",
			PathBindings: jid,
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/import-jobs/{jobId}",
			Capability:   "cancel_import",
			GrpcMethod:   "/codevaldgit.v1.GitService/CancelImport",
			PathBindings: jid,
		},
	}
}
