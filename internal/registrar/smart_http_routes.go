// Package registrar — smart_http_routes.go
// Git Smart HTTP protocol routes for CodeValdGit via CodeValdCross.
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// smartHTTPRoutes returns the Git Smart HTTP protocol routes.
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
