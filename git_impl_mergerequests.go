// git_impl_mergerequests.go — MergeRequest CRUD implementation for [gitManager].
//
// A MergeRequest is the durable record of a request to merge a source branch
// into a target branch. The actual merge work is delegated to MergeBranch;
// this layer adds lifecycle (open → merged | closed | failed), workflow_run_id
// propagation, and the git.merge.* event topics required by FEAT-20260602-001.
package codevaldgit

import (
	"context"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// CreateMergeRequest opens a new MergeRequest for the given source branch.
// Publishes [TopicMergeRequested] on success.
func (m *gitManager) CreateMergeRequest(ctx context.Context, req CreateMergeRequestRequest) (MergeRequest, error) {
	if req.Title == "" {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: title is required")
	}
	if req.SourceBranchID == "" {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: source_branch_id is required")
	}
	repo, err := m.GetRepository(ctx, req.RepositoryID)
	if err != nil {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: %w", err)
	}
	source, err := m.GetBranch(ctx, req.SourceBranchID)
	if err != nil {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: source branch: %w", err)
	}

	// Resolve target branch; default to repo default branch when unspecified.
	var target Branch
	if req.TargetBranchID != "" {
		target, err = m.GetBranch(ctx, req.TargetBranchID)
		if err != nil {
			return MergeRequest{}, fmt.Errorf("CreateMergeRequest: target branch: %w", err)
		}
	} else {
		target, err = m.defaultBranch(ctx, repo.ID)
		if err != nil {
			return MergeRequest{}, fmt.Errorf("CreateMergeRequest: default branch: %w", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	props := map[string]any{
		"title":              req.Title,
		"description":        req.Description,
		"source_branch_id":   source.ID,
		"source_branch_name": source.Name,
		"target_branch_id":   target.ID,
		"target_branch_name": target.Name,
		"status":             MergeRequestStatusOpen,
		"author_name":        req.AuthorName,
		"workflow_run_id":    req.WorkflowRunID,
		"created_at":         now,
		"updated_at":         now,
	}
	entity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID:   m.agencyID,
		TypeID:     "MergeRequest",
		Properties: props,
		Relationships: []entitygraph.EntityRelationshipRequest{
			{Name: "belongs_to_repository", ToID: repo.ID},
			{Name: "has_source_branch", ToID: source.ID},
			{Name: "has_target_branch", ToID: target.ID},
		},
	})
	if err != nil {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: create entity: %w", err)
	}

	// Forward has_merge_request edge (repo → MR) so listMergeRequestsByRepo
	// can locate it without traversing the reverse direction.
	if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "has_merge_request",
		FromID:   repo.ID,
		ToID:     entity.ID,
	}); relErr != nil {
		return MergeRequest{}, fmt.Errorf("CreateMergeRequest: link has_merge_request: %w", relErr)
	}

	mr := entityToMergeRequest(entity, repo.ID)
	m.publish(ctx, TopicMergeRequested, MergeRequestRequestedPayload{
		MergeRequestID: mr.ID,
		RepoID:         repo.ID,
		SourceBranchID: source.ID,
		TargetBranchID: target.ID,
		Title:          mr.Title,
		WorkflowRunID:  mr.WorkflowRunID,
	})
	return mr, nil
}

// GetMergeRequest retrieves a MergeRequest by ID.
func (m *gitManager) GetMergeRequest(ctx context.Context, mrID string) (MergeRequest, error) {
	e, err := m.dm.GetEntity(ctx, m.agencyID, mrID)
	if err != nil {
		return MergeRequest{}, ErrMergeRequestNotFound
	}
	if e.TypeID != "MergeRequest" {
		return MergeRequest{}, ErrMergeRequestNotFound
	}
	repoID := m.resolveParentID(ctx, mrID, "belongs_to_repository")
	return entityToMergeRequest(e, repoID), nil
}

// ListMergeRequests returns MRs matching the filter. An empty filter returns
// every MR for the agency.
func (m *gitManager) ListMergeRequests(ctx context.Context, filter MergeRequestFilter) ([]MergeRequest, error) {
	var entities []entitygraph.Entity
	var err error
	if filter.RepositoryID != "" {
		entities, err = m.listMergeRequestsByRepo(ctx, filter.RepositoryID)
	} else {
		entities, err = m.listAllMergeRequests(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("ListMergeRequests: %w", err)
	}

	out := make([]MergeRequest, 0, len(entities))
	for _, e := range entities {
		if filter.Status != "" && entitygraph.StringProp(e.Properties, "status") != filter.Status {
			continue
		}
		if filter.WorkflowRunID != "" && entitygraph.StringProp(e.Properties, "workflow_run_id") != filter.WorkflowRunID {
			continue
		}
		repoID := filter.RepositoryID
		if repoID == "" {
			repoID = m.resolveParentID(ctx, e.ID, "belongs_to_repository")
		}
		out = append(out, entityToMergeRequest(e, repoID))
	}
	return out, nil
}

// CompleteMergeRequest performs the merge and transitions the MR to "merged".
// On failure transitions to "failed" and publishes [TopicMergeFailed].
func (m *gitManager) CompleteMergeRequest(ctx context.Context, mrID string) (MergeRequest, error) {
	mr, err := m.GetMergeRequest(ctx, mrID)
	if err != nil {
		return MergeRequest{}, err
	}
	if mr.Status != MergeRequestStatusOpen {
		return MergeRequest{}, ErrMergeRequestNotOpen
	}

	mergedBranch, mergeErr := m.MergeBranch(ctx, mr.SourceBranchID)
	if mergeErr != nil {
		updated, _ := m.transitionMRStatus(ctx, mr.ID, MergeRequestStatusFailed, "", mergeErr.Error())
		m.publish(ctx, TopicMergeFailed, MergeRequestFailedPayload{
			MergeRequestID: mr.ID,
			RepoID:         mr.RepositoryID,
			SourceBranchID: mr.SourceBranchID,
			ErrorMessage:   mergeErr.Error(),
			WorkflowRunID:  mr.WorkflowRunID,
		})
		if updated.ID == "" {
			return mr, fmt.Errorf("CompleteMergeRequest: %w", mergeErr)
		}
		return updated, fmt.Errorf("CompleteMergeRequest: %w", mergeErr)
	}

	updated, err := m.transitionMRStatus(ctx, mr.ID, MergeRequestStatusMerged, mergedBranch.SHA, "")
	if err != nil {
		return mr, fmt.Errorf("CompleteMergeRequest: persist merged status: %w", err)
	}
	m.publish(ctx, TopicMergeCompleted, MergeRequestCompletedPayload{
		MergeRequestID:  mr.ID,
		RepoID:          mr.RepositoryID,
		SourceBranchID:  mr.SourceBranchID,
		TargetBranchID:  mr.TargetBranchID,
		MergedCommitSHA: mergedBranch.SHA,
		WorkflowRunID:   mr.WorkflowRunID,
	})
	return updated, nil
}

// CloseMergeRequest transitions an open MR to "closed" without merging.
func (m *gitManager) CloseMergeRequest(ctx context.Context, mrID string) (MergeRequest, error) {
	mr, err := m.GetMergeRequest(ctx, mrID)
	if err != nil {
		return MergeRequest{}, err
	}
	if mr.Status != MergeRequestStatusOpen {
		return MergeRequest{}, ErrMergeRequestNotOpen
	}
	updated, err := m.transitionMRStatus(ctx, mr.ID, MergeRequestStatusClosed, "", "")
	if err != nil {
		return mr, fmt.Errorf("CloseMergeRequest: %w", err)
	}
	return updated, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// transitionMRStatus updates status, merged_commit_sha, error_message, and
// updated_at on the MR entity and returns the re-read [MergeRequest].
func (m *gitManager) transitionMRStatus(ctx context.Context, mrID, status, mergedSHA, errMsg string) (MergeRequest, error) {
	props := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if mergedSHA != "" {
		props["merged_commit_sha"] = mergedSHA
	}
	if errMsg != "" {
		props["error_message"] = errMsg
	}
	entity, err := m.dm.UpdateEntity(ctx, m.agencyID, mrID, entitygraph.UpdateEntityRequest{
		Properties: props,
	})
	if err != nil {
		return MergeRequest{}, err
	}
	repoID := m.resolveParentID(ctx, mrID, "belongs_to_repository")
	return entityToMergeRequest(entity, repoID), nil
}

// listMergeRequestsByRepo returns all MR entities linked to the given repository.
func (m *gitManager) listMergeRequestsByRepo(ctx context.Context, repositoryID string) ([]entitygraph.Entity, error) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "has_merge_request",
		FromID:   repositoryID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]entitygraph.Entity, 0, len(rels))
	for _, r := range rels {
		e, err := m.dm.GetEntity(ctx, m.agencyID, r.ToID)
		if err != nil {
			continue // skip soft-deleted MRs
		}
		out = append(out, e)
	}
	return out, nil
}

// listAllMergeRequests returns every MR entity for this agency by listing all
// repositories first and concatenating their MRs. The has_merge_request edge
// is keyed by FromID, so a per-repo loop is the natural traversal.
func (m *gitManager) listAllMergeRequests(ctx context.Context) ([]entitygraph.Entity, error) {
	repos, err := m.listRepositories(ctx)
	if err != nil {
		return nil, err
	}
	var out []entitygraph.Entity
	for _, repo := range repos {
		repoMRs, err := m.listMergeRequestsByRepo(ctx, repo.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, repoMRs...)
	}
	return out, nil
}

// entityToMergeRequest maps an entitygraph.Entity of type "MergeRequest" to
// [MergeRequest].
func entityToMergeRequest(e entitygraph.Entity, repositoryID string) MergeRequest {
	p := e.Properties
	return MergeRequest{
		ID:               e.ID,
		RepositoryID:     repositoryID,
		Title:            entitygraph.StringProp(p, "title"),
		Description:      entitygraph.StringProp(p, "description"),
		SourceBranchID:   entitygraph.StringProp(p, "source_branch_id"),
		SourceBranchName: entitygraph.StringProp(p, "source_branch_name"),
		TargetBranchID:   entitygraph.StringProp(p, "target_branch_id"),
		TargetBranchName: entitygraph.StringProp(p, "target_branch_name"),
		Status:           entitygraph.StringProp(p, "status"),
		MergedCommitSHA:  entitygraph.StringProp(p, "merged_commit_sha"),
		AuthorName:       entitygraph.StringProp(p, "author_name"),
		ErrorMessage:     entitygraph.StringProp(p, "error_message"),
		WorkflowRunID:    entitygraph.StringProp(p, "workflow_run_id"),
		CreatedAt:        entitygraph.StringProp(p, "created_at"),
		UpdatedAt:        entitygraph.StringProp(p, "updated_at"),
	}
}
