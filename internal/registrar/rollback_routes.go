// Package registrar — rollback_routes.go
// Workflow-run rollback HTTP route exposed via CodeValdCross (FEAT-20260602-004).
package registrar

import "github.com/aosanya/CodeValdSharedLib/types"

// rollbackRoutes returns the routes used by the CodeValdWork rollback
// coordinator to invoke the Git leg of a workflow-run rollback.
//
// The route matches the cross-service convention agreed in
// FEAT-20260602-004: DELETE /git/{agencyId}/by-workflow-run/{workflowRunId}.
func rollbackRoutes() []types.RouteInfo {
	runID := types.PathBinding{URLParam: "workflowRunId", Field: "workflow_run_id"}
	return []types.RouteInfo{
		{
			Method:       "DELETE",
			Pattern:      "/git/{agencyId}/by-workflow-run/{workflowRunId}",
			Capability:   "rollback_by_workflow_run",
			GrpcMethod:   "/codevaldgit.v1.GitService/RollbackByWorkflowRun",
			PathBindings: []types.PathBinding{runID},
			IsWrite:      true,
		},
	}
}
