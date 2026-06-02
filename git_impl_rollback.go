// git_impl_rollback.go — Workflow-run rollback implementation for [gitManager].
//
// FEAT-20260602-004 (Git leg of the cross-service rollback coordinator owned
// by CodeValdWork). Hard-deletes every non-default Branch carrying the given
// workflow_run_id and flips every matching MergeRequest to "rolled_back".
//
// The hybrid hard-delete + audit semantics live here:
//   - Branches are removed because a dangling task branch after a rolled-back
//     run has no operational meaning.
//   - MergeRequests are mutated, not deleted, so the audit trail of "this run
//     merged X into main" survives the rollback. merged_commit_sha is
//     preserved on the entity for the same reason.
//
// Note: the underlying commit chain on the default branch is left intact —
// commits in this service are content-addressed and immutable. A future
// follow-up may add a true compensating-commit pass once the squash-merge
// rework lands (see GIT-012 / GIT-013); the present leg is sufficient for the
// "wipe transient task branches" guarantee the coordinator depends on.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// RollbackByWorkflowRun implements [GitManager].
func (m *gitManager) RollbackByWorkflowRun(ctx context.Context, workflowRunID string) (RollbackResult, error) {
	if workflowRunID == "" {
		return RollbackResult{}, ErrWorkflowRunIDRequired
	}

	result := RollbackResult{WorkflowRunID: workflowRunID}

	mrCount, mrErr := m.rollbackMergeRequestsForRun(ctx, workflowRunID)
	if mrErr != nil {
		return result, fmt.Errorf("RollbackByWorkflowRun: merge requests: %w", mrErr)
	}
	result.MergeRequestsRolledBack = mrCount

	deleted, skipped, brErr := m.deleteBranchesForRun(ctx, workflowRunID)
	if brErr != nil {
		// MR rollback already succeeded; surface the branch failure but echo
		// the partial counts so the coordinator can log them.
		result.BranchesDeleted = deleted
		result.DefaultBranchesSkipped = skipped
		return result, fmt.Errorf("RollbackByWorkflowRun: branches: %w", brErr)
	}
	result.BranchesDeleted = deleted
	result.DefaultBranchesSkipped = skipped

	m.publish(ctx, TopicWorkflowRunRolledBack, WorkflowRunRolledBackPayload{
		WorkflowRunID:           result.WorkflowRunID,
		BranchesDeleted:         result.BranchesDeleted,
		MergeRequestsRolledBack: result.MergeRequestsRolledBack,
		DefaultBranchesSkipped:  result.DefaultBranchesSkipped,
	})
	return result, nil
}

// rollbackMergeRequestsForRun flips every MergeRequest carrying workflowRunID
// to "rolled_back" and emits one [TopicMergeRolledBack] per transition. MRs
// already in "rolled_back" are skipped so re-invocation is idempotent.
func (m *gitManager) rollbackMergeRequestsForRun(ctx context.Context, workflowRunID string) (int, error) {
	mrs, err := m.ListMergeRequests(ctx, MergeRequestFilter{WorkflowRunID: workflowRunID})
	if err != nil {
		return 0, fmt.Errorf("list merge requests: %w", err)
	}
	count := 0
	for _, mr := range mrs {
		if mr.Status == MergeRequestStatusRolledBack {
			continue
		}
		priorStatus := mr.Status
		if _, err := m.dm.UpdateEntity(ctx, m.agencyID, mr.ID, entitygraph.UpdateEntityRequest{
			Properties: map[string]any{
				"status":     MergeRequestStatusRolledBack,
				"updated_at": time.Now().UTC().Format(time.RFC3339),
			},
		}); err != nil {
			return count, fmt.Errorf("mark MR %s rolled_back: %w", mr.ID, err)
		}
		m.publish(ctx, TopicMergeRolledBack, MergeRequestRolledBackPayload{
			MergeRequestID:  mr.ID,
			RepoID:          mr.RepositoryID,
			SourceBranchID:  mr.SourceBranchID,
			PriorStatus:     priorStatus,
			MergedCommitSHA: mr.MergedCommitSHA,
			WorkflowRunID:   workflowRunID,
		})
		count++
	}
	return count, nil
}

// deleteBranchesForRun hard-deletes every non-default Branch carrying
// workflowRunID. The default branch is never deleted — even when tagged with
// the run — because losing the repository's primary ref would break every
// future operation against the repo. The skipped counter is surfaced so
// callers can log and investigate the (always unexpected) condition.
func (m *gitManager) deleteBranchesForRun(ctx context.Context, workflowRunID string) (deleted, skippedDefault int, err error) {
	entities, listErr := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Branch",
	})
	if listErr != nil {
		return 0, 0, fmt.Errorf("list branches: %w", listErr)
	}
	for _, e := range entities {
		if entitygraph.StringProp(e.Properties, "workflow_run_id") != workflowRunID {
			continue
		}
		if entitygraph.BoolProp(e.Properties, "is_default") {
			skippedDefault++
			continue
		}
		if delErr := m.DeleteBranch(ctx, e.ID); delErr != nil {
			// Tolerate already-deleted branches so re-invocation stays idempotent.
			if errors.Is(delErr, ErrBranchNotFound) {
				continue
			}
			return deleted, skippedDefault, fmt.Errorf("delete branch %s: %w", e.ID, delErr)
		}
		deleted++
	}
	return deleted, skippedDefault, nil
}
