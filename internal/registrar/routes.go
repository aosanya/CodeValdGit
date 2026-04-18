// Package registrar — this file declares all HTTP routes CodeValdGit exposes
// via CodeValdCross. Routes are grouped by concern and combined by gitRoutes()
// in registrar.go. All gRPC method paths reference the v2 GitService.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// repoRoutes returns routes for repository lifecycle operations.
// A repository ID is required in the path for all single-repo operations;
// POST /repositories creates a new repo and GET /repositories lists them all.
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

// branchRoutes returns routes for branch CRUD and merge operations.
// All routes are nested under /git/{agencyId}/repositories/{repoId}/ so that
// the repository context is always present in the URL (GIT-018).
func branchRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
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
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}",
			Capability:   "get_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetBranch",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}",
			Capability:   "delete_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteBranch",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/merge",
			Capability:   "merge_branch",
			GrpcMethod:   "/codevaldgit.v1.GitService/MergeBranch",
			PathBindings: []types.PathBinding{bid},
		},
	}
}

// tagRoutes returns routes for tag CRUD operations.
// All routes are nested under /git/{agencyId}/repositories/{repoId}/ (GIT-018).
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

// fileRoutes returns routes for file read/write operations on a branch.
// All routes are nested under /git/{agencyId}/repositories/{repoId}/branches/{branchId}/ (GIT-018).
func fileRoutes() []types.RouteInfo {
	bid := types.PathBinding{URLParam: "branchId", Field: "branch_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/files",
			Capability:   "write_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/WriteFile",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/files",
			Capability:   "read_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/ReadFile",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/files",
			Capability:   "delete_file",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteFile",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/tree",
			Capability:   "list_directory",
			GrpcMethod:   "/codevaldgit.v1.GitService/ListDirectory",
			PathBindings: []types.PathBinding{bid},
		},
	}
}

// historyRoutes returns routes for commit log and diff operations.
// All routes are nested under /git/{agencyId}/repositories/{repoId}/ (GIT-018).
func historyRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoName", Field: "repository_name"}
	bid := types.PathBinding{URLParam: "branchId", Field: "branch_id"}
	return []types.RouteInfo{
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/{repoName}/branches/{branchId}/log",
			Capability:   "log",
			GrpcMethod:   "/codevaldgit.v1.GitService/Log",
			PathBindings: []types.PathBinding{bid},
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

// smartHTTPRoutes returns the Git Smart HTTP protocol routes served directly
// by CodeValdGit's git-HTTP handler (internal/server/githttp.go) on the cmux
// port. GrpcMethod is empty because these are direct HTTP pass-through routes,
// not gRPC proxy routes. CodeValdCross will need a direct-proxy capability to
// forward these once GIT-009 and the corresponding Cross task are complete.
func smartHTTPRoutes() []types.RouteInfo {
	return []types.RouteInfo{
		{
			Method:     "GET",
			Pattern:    "/{agencyId}/{repoName}/info/refs",
			Capability: "git_info_refs",
		},
		{
			Method:     "POST",
			Pattern:    "/{agencyId}/{repoName}/git-upload-pack",
			Capability: "git_upload_pack",
		},
		{
			Method:     "POST",
			Pattern:    "/{agencyId}/{repoName}/git-receive-pack",
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

// docsRoutes returns routes for the documentation layer (GIT-021c):
// keyword CRUD, branch-scoped edge CRUD, and graph query operations.
// All routes are nested under /git/{agencyId}/.
func docsRoutes() []types.RouteInfo {
	kid := types.PathBinding{URLParam: "keywordId", Field: "keyword_id"}
	bid := types.PathBinding{URLParam: "branchId", Field: "branch_id"}
	eid := types.PathBinding{URLParam: "entityId", Field: "entity_id"}
	return []types.RouteInfo{
		// Keyword CRUD
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/keywords",
			Capability: "create_keyword",
			GrpcMethod: "/codevaldgit.v1.GitService/CreateKeyword",
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "get_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetKeyword",
			PathBindings: []types.PathBinding{kid},
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/keywords",
			Capability: "list_keywords",
			GrpcMethod: "/codevaldgit.v1.GitService/ListKeywords",
		},
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/keyword-tree",
			Capability: "get_keyword_tree",
			GrpcMethod: "/codevaldgit.v1.GitService/GetKeywordTree",
		},
		{
			Method:       "PUT",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "update_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/UpdateKeyword",
			PathBindings: []types.PathBinding{kid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/keywords/{keywordId}",
			Capability:   "delete_keyword",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteKeyword",
			PathBindings: []types.PathBinding{kid},
		},
		// Branch-scoped edge CRUD
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/branches/{branchId}/edges",
			Capability:   "create_edge",
			GrpcMethod:   "/codevaldgit.v1.GitService/CreateEdge",
			PathBindings: []types.PathBinding{bid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/branches/{branchId}/edges",
			Capability:   "delete_edge",
			GrpcMethod:   "/codevaldgit.v1.GitService/DeleteEdge",
			PathBindings: []types.PathBinding{bid},
		},
		// Graph queries
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/branches/{branchId}/graph/{entityId}/neighborhood",
			Capability:   "get_neighborhood",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetNeighborhood",
			PathBindings: []types.PathBinding{bid, eid},
		},
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/branches/{branchId}/graph/search",
			Capability:   "search_by_keywords",
			GrpcMethod:   "/codevaldgit.v1.GitService/SearchByKeywords",
			PathBindings: []types.PathBinding{bid},
		},
	}
}
