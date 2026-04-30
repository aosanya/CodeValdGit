// git_impl_tag.go — Tag management implementation for [gitManager].
package codevaldgit

import (
	"context"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── Tag Management ────────────────────────────────────────────────────────────

// CreateTag creates an immutable Tag entity pointing to the specified commit.
// Returns [ErrTagAlreadyExists] if a tag with the given name already exists.
// Returns [ErrBranchNotFound] if req.CommitID does not resolve to a Commit entity.
func (m *gitManager) CreateTag(ctx context.Context, req CreateTagRequest) (Tag, error) {
	repo, err := m.GetRepository(ctx, req.RepositoryID)
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: %w", err)
	}

	// Guard: reject duplicate tag names.
	tags, err := m.listTagsByRepo(ctx, repo.ID)
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: list tags: %w", err)
	}
	for _, t := range tags {
		if entitygraph.StringProp(t.Properties, "name") == req.Name {
			return Tag{}, ErrTagAlreadyExists
		}
	}

	// Validate the target commit exists.
	commitEntity, err := m.dm.GetEntity(ctx, m.agencyID, req.CommitID)
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: commit %s: %w", req.CommitID, ErrBranchNotFound)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tagEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Tag",
		Properties: map[string]any{
			"name":        req.Name,
			"sha":         entitygraph.StringProp(commitEntity.Properties, "sha"),
			"message":     req.Message,
			"tagger_name": req.TaggerName,
			"tagger_at":   now,
			"created_at":  now,
		},
		Relationships: []entitygraph.EntityRelationshipRequest{
			{Name: "belongs_to_repository", ToID: repo.ID},
			{Name: "points_to", ToID: req.CommitID},
		},
	})
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: create entity: %w", err)
	}

	// Create the forward has_tag edge (repo → tag) so listTagsByRepo
	// can locate it via RelationshipFilter{Name:"has_tag", FromID: repoID}.
	if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "has_tag",
		FromID:   repo.ID,
		ToID:     tagEntity.ID,
	}); relErr != nil {
		return Tag{}, fmt.Errorf("CreateTag: link has_tag: %w", relErr)
	}

	return entityToTag(tagEntity, repo.ID), nil
}

// GetTag retrieves a Tag entity by its entitygraph ID.
// Returns [ErrTagNotFound] if no tag with that ID exists.
func (m *gitManager) GetTag(ctx context.Context, tagID string) (Tag, error) {
	e, err := m.dm.GetEntity(ctx, m.agencyID, tagID)
	if err != nil {
		return Tag{}, ErrTagNotFound
	}
	repoID := m.resolveParentID(ctx, tagID, "belongs_to_repository")
	return entityToTag(e, repoID), nil
}

// ListTags returns all Tag entities for the specified repository.
// Returns [ErrRepoNotInitialised] if no repository with that ID exists.
func (m *gitManager) ListTags(ctx context.Context, repoID string) ([]Tag, error) {
	if _, err := m.GetRepository(ctx, repoID); err != nil {
		return nil, fmt.Errorf("ListTags: %w", err)
	}
	tags, err := m.listTagsByRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("ListTags: %w", err)
	}
	out := make([]Tag, len(tags))
	for i, e := range tags {
		out[i] = entityToTag(e, repoID)
	}
	return out, nil
}

// DeleteTag removes a Tag entity.
// Returns [ErrTagNotFound] if no tag with that ID exists.
func (m *gitManager) DeleteTag(ctx context.Context, tagID string) error {
	if _, err := m.dm.GetEntity(ctx, m.agencyID, tagID); err != nil {
		return ErrTagNotFound
	}
	if err := m.dm.DeleteEntity(ctx, m.agencyID, tagID); err != nil {
		return fmt.Errorf("DeleteTag: %w", err)
	}
	return nil
}

// listTagsByRepo returns all Tag entities whose has_tag edge points from the
// given repositoryID.
func (m *gitManager) listTagsByRepo(ctx context.Context, repositoryID string) ([]entitygraph.Entity, error) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "has_tag",
		FromID:   repositoryID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]entitygraph.Entity, 0, len(rels))
	for _, r := range rels {
		e, err := m.dm.GetEntity(ctx, m.agencyID, r.ToID)
		if err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
