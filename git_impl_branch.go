// git_impl_branch.go — Branch management implementation for [gitManager].
package codevaldgit

import (
	"context"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── Branch Management ─────────────────────────────────────────────────────────

// CreateBranch creates a new Branch entity from the specified source branch.
// If req.FromBranchID is empty, the repository default branch is used.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
// Returns [ErrBranchExists] if a branch with the given name already exists.
func (m *gitManager) CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error) {
	repo, err := m.GetRepository(ctx, req.RepositoryID)
	if err != nil {
		return Branch{}, fmt.Errorf("CreateBranch: %w", err)
	}

	// Resolve the source branch.
	var sourceBranch Branch
	if req.FromBranchID != "" {
		sourceBranch, err = m.GetBranch(ctx, req.FromBranchID)
		if err != nil {
			return Branch{}, fmt.Errorf("CreateBranch: source branch: %w", err)
		}
	} else {
		sourceBranch, err = m.defaultBranch(ctx, repo.ID)
		if err != nil {
			return Branch{}, fmt.Errorf("CreateBranch: default branch: %w", err)
		}
	}

	// Guard: reject duplicate branch names.
	existing, err := m.listBranchesByRepo(ctx, repo.ID)
	if err != nil {
		return Branch{}, fmt.Errorf("CreateBranch: list branches: %w", err)
	}
	for _, b := range existing {
		if entitygraph.StringProp(b.Properties, "name") == req.Name {
			return Branch{}, ErrBranchExists
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	branchEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"name":           req.Name,
			"is_default":     false,
			"head_commit_id": sourceBranch.HeadCommitID,
			"created_at":     now,
			"updated_at":     now,
		},
		Relationships: []entitygraph.EntityRelationshipRequest{
			{Name: "belongs_to_repository", ToID: repo.ID},
		},
	})
	if err != nil {
		return Branch{}, fmt.Errorf("CreateBranch: create entity: %w", err)
	}

	// If the source branch has a HEAD commit, link this new branch to it too.
	if sourceBranch.HeadCommitID != "" {
		if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "points_to",
			FromID:   branchEntity.ID,
			ToID:     sourceBranch.HeadCommitID,
		}); relErr != nil {
			return Branch{}, fmt.Errorf("CreateBranch: link head commit: %w", relErr)
		}
	}

	// Create the forward has_branch edge (repo → branch) so listBranchesByRepo
	// can locate it via RelationshipFilter{Name:"has_branch", FromID: repoID}.
	if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "has_branch",
		FromID:   repo.ID,
		ToID:     branchEntity.ID,
	}); relErr != nil {
		return Branch{}, fmt.Errorf("CreateBranch: link has_branch: %w", relErr)
	}

	return entityToBranch(branchEntity, repo.ID), nil
}

// GetBranch retrieves a Branch entity by its entitygraph ID.
// Returns [ErrBranchNotFound] if no branch with that ID exists.
func (m *gitManager) GetBranch(ctx context.Context, branchID string) (Branch, error) {
	e, err := m.dm.GetEntity(ctx, m.agencyID, branchID)
	if err != nil {
		return Branch{}, ErrBranchNotFound
	}
	repoID := m.resolveParentID(ctx, branchID, "belongs_to_repository")
	if repoID == "" {
		// Fallback: older push-indexed branches were created without the
		// reverse edge. Look up the forward has_branch edge (repo → branch)
		// and heal by writing the missing belongs_to_repository edge.
		rels, relErr := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: m.agencyID,
			Name:     "has_branch",
			ToID:     branchID,
		})
		if relErr == nil && len(rels) > 0 {
			repoID = rels[0].FromID
			_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: m.agencyID,
				Name:     "belongs_to_repository",
				FromID:   branchID,
				ToID:     repoID,
			})
		}
	}
	return entityToBranch(e, repoID), nil
}

// ListBranches returns all Branch entities for the specified repository.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
func (m *gitManager) ListBranches(ctx context.Context, repoID string) ([]Branch, error) {
	if _, err := m.GetRepository(ctx, repoID); err != nil {
		return nil, fmt.Errorf("ListBranches: %w", err)
	}
	entities, err := m.listBranchesByRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("ListBranches: %w", err)
	}
	out := make([]Branch, len(entities))
	for i, e := range entities {
		out[i] = entityToBranch(e, repoID)
	}
	return out, nil
}

// GetBranchByName retrieves a Branch entity by its human-readable name.
// Returns [ErrBranchNotFound] if no branch with that name exists for the
// specified repository.
func (m *gitManager) GetBranchByName(ctx context.Context, repoID string, branchName string) (Branch, error) {
	entities, err := m.listBranchesByRepo(ctx, repoID)
	if err != nil {
		return Branch{}, fmt.Errorf("GetBranchByName: %w", err)
	}
	for _, e := range entities {
		if entitygraph.StringProp(e.Properties, "name") == branchName {
			return entityToBranch(e, repoID), nil
		}
	}
	return Branch{}, ErrBranchNotFound
}

// DeleteBranch removes a Branch entity.
// Returns [ErrBranchNotFound] if no branch with that ID exists.
// Returns [ErrDefaultBranchDeleteForbidden] if branchID is the default branch.
func (m *gitManager) DeleteBranch(ctx context.Context, branchID string) error {
	e, err := m.dm.GetEntity(ctx, m.agencyID, branchID)
	if err != nil {
		return ErrBranchNotFound
	}
	if entitygraph.BoolProp(e.Properties, "is_default") {
		return fmt.Errorf("DeleteBranch: %w", ErrDefaultBranchDeleteForbidden)
	}

	// GIT-022b: Delete branch-scoped documentation edges before removing the
	// branch entity so no dangling edges are left behind.
	headCommitID := entitygraph.StringProp(e.Properties, "head_commit_id")
	m.deleteDocEdgesForBranch(ctx, branchID, headCommitID)

	if err := m.dm.DeleteEntity(ctx, m.agencyID, branchID); err != nil {
		return fmt.Errorf("DeleteBranch: %w", err)
	}
	return nil
}

// MergeBranch merges the given branch into the repository's default branch by
// forwarding the default branch's HEAD commit pointer to the source branch's
// HEAD commit. Returns the updated default [Branch].
//
// The entire advance-head operation runs inside a per-agency [RefLocker] lock
// so that two concurrent MergeBranch calls for the same agency cannot produce
// a lost update. The CAS guard in [advanceBranchHead] provides a second layer
// of protection: if the default branch HEAD changed between the read and the
// write, [ErrMergeConcurrencyConflict] is returned and the caller may retry.
//
// Returns [ErrBranchNotFound] if no branch with that ID exists.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
// Returns [ErrMergeConcurrencyConflict] if a concurrent merge advanced the
// default branch HEAD before this one could complete.
func (m *gitManager) MergeBranch(ctx context.Context, branchID string) (Branch, error) {
	sourceBranch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: %w", err)
	}
	repo, err := m.GetRepository(ctx, sourceBranch.RepositoryID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: %w", err)
	}
	defaultBranchEntity, err := m.defaultBranch(ctx, repo.ID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: default branch: %w", err)
	}
	if sourceBranch.HeadCommitID == "" {
		return defaultBranchEntity, nil
	}

	var updated Branch
	lockErr := m.locker.WithMergeLock(ctx, m.agencyID, func() error {
		// Re-read the default branch inside the lock so we hold the freshest
		// HeadCommitID as the CAS guard. This ensures two sequential merges
		// both succeed; the CAS only fires if the entity was modified by an
		// out-of-band write (e.g. another service instance).
		currentDefault, readErr := m.defaultBranch(ctx, repo.ID)
		if readErr != nil {
			return fmt.Errorf("re-read default branch: %w", readErr)
		}
		var advErr error
		updated, advErr = m.advanceBranchHead(ctx, currentDefault.ID, sourceBranch.HeadCommitID, currentDefault.HeadCommitID)
		return advErr
	})
	if lockErr != nil {
		return Branch{}, fmt.Errorf("MergeBranch: advance default head: %w", lockErr)
	}

	// GIT-022a: Replicate branch-scoped documentation edges (tagged_with,
	// references) from source branch blobs to the default branch.
	m.replicateDocEdges(ctx, sourceBranch.ID, defaultBranchEntity.ID, sourceBranch.HeadCommitID)

	return updated, nil
}

// ── Branch internal helpers ───────────────────────────────────────────────────

// listBranchesByRepo returns all Branch entities linked to the given
// repositoryID. It first queries the forward has_branch edge (repo→branch)
// and falls back to the reverse belongs_to_repository edge (branch→repo) for
// repos imported before the has_branch edge was created on import.
func (m *gitManager) listBranchesByRepo(ctx context.Context, repositoryID string) ([]entitygraph.Entity, error) {
	// Primary: forward has_branch edges (repo → branch).
	forwardRels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "has_branch",
		FromID:   repositoryID,
	})
	if err != nil {
		return nil, err
	}

	if len(forwardRels) > 0 {
		out := make([]entitygraph.Entity, 0, len(forwardRels))
		for _, r := range forwardRels {
			e, err := m.dm.GetEntity(ctx, m.agencyID, r.ToID)
			if err != nil {
				continue // skip soft-deleted branches
			}
			out = append(out, e)
		}
		return out, nil
	}

	// Fallback: reverse belongs_to_repository edges (branch → repo).
	// Used for repos that were imported before the has_branch edge was written.
	reverseRels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "belongs_to_repository",
		ToID:     repositoryID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]entitygraph.Entity, 0, len(reverseRels))
	for _, r := range reverseRels {
		e, err := m.dm.GetEntity(ctx, m.agencyID, r.FromID)
		if err != nil {
			continue // skip soft-deleted branches
		}
		if e.TypeID != "Branch" {
			continue // ignore non-branch entities sharing the relationship name
		}
		out = append(out, e)
	}
	return out, nil
}

// defaultBranch returns the Branch entity whose is_default property is true
// for the given repository.
func (m *gitManager) defaultBranch(ctx context.Context, repositoryID string) (Branch, error) {
	branches, err := m.listBranchesByRepo(ctx, repositoryID)
	if err != nil {
		return Branch{}, err
	}
	for _, b := range branches {
		if entitygraph.BoolProp(b.Properties, "is_default") {
			return entityToBranch(b, repositoryID), nil
		}
	}
	return Branch{}, ErrBranchNotFound
}

// advanceBranchHead updates a branch's points_to edge, head_commit_id, and
// sha properties to point at newCommitID. The sha is copied from the Commit
// entity so that the go-git storage.Storer can resolve refs without an extra
// graph traversal. Returns the updated Branch.
//
// expectedHeadCommitID is a CAS guard: if non-empty, the current
// head_commit_id on the branch must match before the update proceeds.
// Returns [ErrMergeConcurrencyConflict] if the check fails.
// Pass "" to skip the check (used by IndexPushedBranch).
func (m *gitManager) advanceBranchHead(ctx context.Context, branchID, newCommitID, expectedHeadCommitID string) (Branch, error) {
	// CAS guard — only enforce when the caller supplies an expected value.
	if expectedHeadCommitID != "" {
		current, err := m.dm.GetEntity(ctx, m.agencyID, branchID)
		if err != nil {
			return Branch{}, fmt.Errorf("advanceBranchHead: read current branch: %w", err)
		}
		if entitygraph.StringProp(current.Properties, "head_commit_id") != expectedHeadCommitID {
			return Branch{}, ErrMergeConcurrencyConflict
		}
	}

	// Remove old points_to edge if it exists.
	oldRels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "points_to",
		FromID:   branchID,
	})
	if err == nil {
		for _, r := range oldRels {
			_ = m.dm.DeleteRelationship(ctx, m.agencyID, r.ID)
		}
	}

	// Create the new points_to edge.
	if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "points_to",
		FromID:   branchID,
		ToID:     newCommitID,
	}); err != nil {
		return Branch{}, fmt.Errorf("advanceBranchHead: link commit: %w", err)
	}

	// Fetch the new Commit entity to read its real git SHA.
	commitEntity, err := m.dm.GetEntity(ctx, m.agencyID, newCommitID)
	if err != nil {
		return Branch{}, fmt.Errorf("advanceBranchHead: get commit: %w", err)
	}
	commitSHA := entitygraph.StringProp(commitEntity.Properties, "sha")

	// Update head_commit_id, sha, and updated_at in a single call.
	now := time.Now().UTC().Format(time.RFC3339)
	updateProps := map[string]any{
		"head_commit_id": newCommitID,
		"updated_at":     now,
	}
	if commitSHA != "" {
		updateProps["sha"] = commitSHA
	}
	updated, err := m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
		Properties: updateProps,
	})
	if err != nil {
		return Branch{}, fmt.Errorf("advanceBranchHead: update entity: %w", err)
	}
	repoID := m.resolveParentID(ctx, branchID, "belongs_to_repository")
	return entityToBranch(updated, repoID), nil
}
