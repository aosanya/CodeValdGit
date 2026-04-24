// Package registrar — import_routes.go
// Repository import HTTP routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// importRoutes returns routes for async repository import operations.
func importRoutes() []types.RouteInfo {
	rid := types.PathBinding{URLParam: "repoId", Field: "repo_id"}
	return []types.RouteInfo{
		{
			Method:       "POST",
			Pattern:      "/git/{agencyId}/repositories/import",
			Capability:   "import_repo",
			GrpcMethod:   "/codevaldgit.v1.GitService/ImportRepo",
			PathBindings: []types.PathBinding{},
			IsWrite:      true,
		},
		{
			Method:       "GET",
			Pattern:      "/git/{agencyId}/repositories/import/{repoId}/status",
			Capability:   "get_import_status",
			GrpcMethod:   "/codevaldgit.v1.GitService/GetImportStatus",
			PathBindings: []types.PathBinding{rid},
		},
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/repositories/import/{repoId}",
			Capability:   "cancel_import",
			GrpcMethod:   "/codevaldgit.v1.GitService/CancelImport",
			PathBindings: []types.PathBinding{rid},
			IsWrite:      true,
		},
	}
}
