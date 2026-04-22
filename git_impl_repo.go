// git_impl_repo.go — Repository lifecycle implementation for [gitManager].
//
// Branch management is in git_impl_branch.go.
// Tag management is in git_impl_tag.go.
// Entity converters and property helpers are in git_impl_converters.go.
package codevaldgit

import (
	"context"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── Repository Lifecycle ──────────────────────────────────────────────────────

// InitRepo creates a new Repository entity for this agency.
// Returns [ErrRepoAlreadyExists] if a repository with the same name already exists.
// Publishes "cross.git.{agencyID}.repo.created" after a successful write.
func (m *gitManager) InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error) {
	existing, err := m.listRepositories(ctx)
	if err != nil {
		return Repository{}, fmt.Errorf("InitRepo: %w", err)
	}
	for _, r := range existing {
		if strProp(r.Properties, "name") == req.Name {
			return Repository{}, ErrRepoAlreadyExists
		}
	}

	// Ensure the Agency root entity exists; create it if not.
	agencyEntityID, err := m.ensureAgencyEntity(ctx)
	if err != nil {
		return Repository{}, fmt.Errorf("InitRepo: ensure agency: %w", err)
	}

	defaultBranch := req.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	repoEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":           req.Name,
			"description":    req.Description,
			"default_branch": defaultBranch,
			"created_at":     now,
			"updated_at":     now,
		},
		Relationships: []entitygraph.EntityRelationshipRequest{
			{Name: "belongs_to_agency", ToID: agencyEntityID},
		},
	})
	if err != nil {
		return Repository{}, fmt.Errorf("InitRepo: create entity: %w", err)
	}

	// Create the default branch pointing to no commit yet.
	branchNow := time.Now().UTC().Format(time.RFC3339)
	defaultBranchEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"name":       defaultBranch,
			"is_default": true,
			"created_at": branchNow,
			"updated_at": branchNow,
		},
		Relationships: []entitygraph.EntityRelationshipRequest{
			{Name: "belongs_to_repository", ToID: repoEntity.ID},
		},
	})
	if err != nil {
		return Repository{}, fmt.Errorf("InitRepo: create default branch: %w", err)
	}

	// Create the forward has_branch edge (repo → branch) so listBranchesByRepo
	// can locate it via RelationshipFilter{Name:"has_branch", FromID: repoID}.
	if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "has_branch",
		FromID:   repoEntity.ID,
		ToID:     defaultBranchEntity.ID,
	}); err != nil {
		return Repository{}, fmt.Errorf("InitRepo: link default branch: %w", err)
	}

	repo := entityToRepository(repoEntity, m.agencyID)
	if m.publisher != nil {
		_ = m.publisher.Publish(ctx, fmt.Sprintf("cross.git.%s.repo.created", m.agencyID), m.agencyID)
	}
	return repo, nil
}

// ListRepositories returns all Repository entities for this agency.
func (m *gitManager) ListRepositories(ctx context.Context) ([]Repository, error) {
	entities, err := m.listRepositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListRepositories: %w", err)
	}
	out := make([]Repository, len(entities))
	for i, e := range entities {
		out[i] = entityToRepository(e, m.agencyID)
	}
	return out, nil
}

// GetRepository retrieves a Repository entity by its ID.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
func (m *gitManager) GetRepository(ctx context.Context, repoID string) (Repository, error) {
	e, err := m.dm.GetEntity(ctx, m.agencyID, repoID)
	if err != nil {
		return Repository{}, ErrRepoNotInitialised
	}
	if e.TypeID != "Repository" {
		return Repository{}, ErrRepoNotInitialised
	}
	return entityToRepository(e, m.agencyID), nil
}

// GetRepositoryByName retrieves a Repository entity by its human-readable name.
// Returns [ErrRepoNotInitialised] if no repository with that name exists.
func (m *gitManager) GetRepositoryByName(ctx context.Context, repoName string) (Repository, error) {
	entities, err := m.listRepositories(ctx)
	if err != nil {
		return Repository{}, fmt.Errorf("GetRepositoryByName: %w", err)
	}
	for _, e := range entities {
		if strProp(e.Properties, "name") == repoName {
			return entityToRepository(e, m.agencyID), nil
		}
	}
	return Repository{}, ErrRepoNotInitialised
}

// DeleteRepo soft-deletes the specified repository entity and all owned sub-entities.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
func (m *gitManager) DeleteRepo(ctx context.Context, repoID string) error {
	repo, err := m.GetRepository(ctx, repoID)
	if err != nil {
		return fmt.Errorf("DeleteRepo: %w", err)
	}

	// Soft-delete all branches.
	branches, err := m.listBranchesByRepo(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("DeleteRepo: list branches: %w", err)
	}
	for _, b := range branches {
		if delErr := m.dm.DeleteEntity(ctx, m.agencyID, b.ID); delErr != nil {
			return fmt.Errorf("DeleteRepo: delete branch %s: %w", b.ID, delErr)
		}
	}

	// Soft-delete all tags.
	tags, err := m.listTagsByRepo(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("DeleteRepo: list tags: %w", err)
	}
	for _, t := range tags {
		if delErr := m.dm.DeleteEntity(ctx, m.agencyID, t.ID); delErr != nil {
			return fmt.Errorf("DeleteRepo: delete tag %s: %w", t.ID, delErr)
		}
	}

	// Soft-delete the repository itself.
	if err := m.dm.DeleteEntity(ctx, m.agencyID, repo.ID); err != nil {
		return fmt.Errorf("DeleteRepo: delete repo: %w", err)
	}
	return nil
}

// PurgeRepo is a no-op alias for DeleteRepo in the entitygraph model — soft
// deletion is the only supported deletion strategy in v1.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
func (m *gitManager) PurgeRepo(ctx context.Context, repoID string) error {
	return m.DeleteRepo(ctx, repoID)
}

// ── Repository internal helpers ───────────────────────────────────────────────

// listRepositories returns all Repository entities for this agency.
func (m *gitManager) listRepositories(ctx context.Context) ([]entitygraph.Entity, error) {
	return m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Repository",
	})
}

// ensureAgencyEntity returns the ID of the Agency entity for this agency,
// creating it if it does not yet exist.
func (m *gitManager) ensureAgencyEntity(ctx context.Context) (string, error) {
	entities, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Agency",
	})
	if err != nil {
		return "", err
	}
	if len(entities) > 0 {
		return entities[0].ID, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	e, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Agency",
		Properties: map[string]any{
			"name":       m.agencyID,
			"created_at": now,
			"updated_at": now,
		},
	})
	if err != nil {
		return "", err
	}
	return e.ID, nil
}
