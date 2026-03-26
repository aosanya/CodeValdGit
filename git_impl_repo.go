// Repository lifecycle, branch management, and tag management implementations
// for [gitManager]. These methods replace the stubs in git.go.
//
// All storage operations go through the injected [entitygraph.DataManager].
// No go-git plumbing is used here — commit graph traversal for MergeBranch is
// handled at the entitygraph layer by following has_parent / points_to edges.
package codevaldgit

import (
	"context"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── Repository Lifecycle ──────────────────────────────────────────────────────

// InitRepo creates the single Repository entity for this agency.
// Returns [ErrRepoAlreadyExists] if a repository entity already exists.
// Publishes "cross.git.{agencyID}.repo.created" after a successful write.
func (m *gitManager) InitRepo(ctx context.Context, req CreateRepoRequest) (Repository, error) {
	existing, err := m.listRepositories(ctx)
	if err != nil {
		return Repository{}, fmt.Errorf("InitRepo: %w", err)
	}
	if len(existing) > 0 {
		return Repository{}, ErrRepoAlreadyExists
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
	_, err = m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
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

	repo := entityToRepository(repoEntity, m.agencyID)
	if m.publisher != nil {
		_ = m.publisher.Publish(ctx, fmt.Sprintf("cross.git.%s.repo.created", m.agencyID), m.agencyID)
	}
	return repo, nil
}

// GetRepository retrieves the single Repository entity for this agency.
// Returns [ErrRepoNotInitialised] if no repository has been created yet.
func (m *gitManager) GetRepository(ctx context.Context) (Repository, error) {
	repos, err := m.listRepositories(ctx)
	if err != nil {
		return Repository{}, fmt.Errorf("GetRepository: %w", err)
	}
	if len(repos) == 0 {
		return Repository{}, ErrRepoNotInitialised
	}
	return entityToRepository(repos[0], m.agencyID), nil
}

// DeleteRepo soft-deletes the repository entity and all owned sub-entities.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) DeleteRepo(ctx context.Context) error {
	repo, err := m.GetRepository(ctx)
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
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) PurgeRepo(ctx context.Context) error {
	return m.DeleteRepo(ctx)
}

// ── Branch Management ─────────────────────────────────────────────────────────

// CreateBranch creates a new Branch entity from the specified source branch.
// If req.FromBranchID is empty, the repository default branch is used.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
// Returns [ErrBranchExists] if a branch with the given name already exists.
func (m *gitManager) CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error) {
	repo, err := m.GetRepository(ctx)
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
		if strProp(b.Properties, "name") == req.Name {
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
	return entityToBranch(e, repoID), nil
}

// ListBranches returns all Branch entities for this agency's repository.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) ListBranches(ctx context.Context) ([]Branch, error) {
	repo, err := m.GetRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListBranches: %w", err)
	}
	entities, err := m.listBranchesByRepo(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("ListBranches: %w", err)
	}
	out := make([]Branch, len(entities))
	for i, e := range entities {
		out[i] = entityToBranch(e, repo.ID)
	}
	return out, nil
}

// DeleteBranch removes a Branch entity.
// Returns [ErrBranchNotFound] if no branch with that ID exists.
// Returns an error if branchID refers to the repository's default branch.
func (m *gitManager) DeleteBranch(ctx context.Context, branchID string) error {
	e, err := m.dm.GetEntity(ctx, m.agencyID, branchID)
	if err != nil {
		return ErrBranchNotFound
	}
	if boolProp(e.Properties, "is_default") {
		return fmt.Errorf("DeleteBranch: %w", ErrDefaultBranchDeleteForbidden)
	}
	if err := m.dm.DeleteEntity(ctx, m.agencyID, branchID); err != nil {
		return fmt.Errorf("DeleteBranch: %w", err)
	}
	return nil
}

// MergeBranch merges the given branch into the repository's default branch by
// forwarding the default branch's HEAD commit pointer to the source branch's
// HEAD commit. Returns the updated default [Branch].
//
// In the entitygraph model a "merge" is an atomic HEAD-pointer update — the
// commit history of both branches is preserved via has_parent edges. Conflicts
// cannot arise at the entitygraph layer; callers are responsible for
// coordinating concurrent writes at the application layer.
//
// Returns [ErrBranchNotFound] if no branch with that ID exists.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) MergeBranch(ctx context.Context, branchID string) (Branch, error) {
	repo, err := m.GetRepository(ctx)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: %w", err)
	}
	sourceBranch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: %w", err)
	}
	defaultBranchEntity, err := m.defaultBranch(ctx, repo.ID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: default branch: %w", err)
	}
	if sourceBranch.HeadCommitID == "" {
		// Nothing to merge — source has no commits.
		return defaultBranchEntity, nil
	}

	// Forward the default branch HEAD pointer.
	updated, err := m.advanceBranchHead(ctx, defaultBranchEntity.ID, sourceBranch.HeadCommitID)
	if err != nil {
		return Branch{}, fmt.Errorf("MergeBranch: advance default head: %w", err)
	}
	return updated, nil
}

// ── Tag Management ────────────────────────────────────────────────────────────

// CreateTag creates an immutable Tag entity pointing to the specified commit.
// Returns [ErrTagAlreadyExists] if a tag with the given name already exists.
// Returns [ErrBranchNotFound] if req.CommitID does not resolve to a Commit entity.
func (m *gitManager) CreateTag(ctx context.Context, req CreateTagRequest) (Tag, error) {
	repo, err := m.GetRepository(ctx)
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: %w", err)
	}

	// Guard: reject duplicate tag names.
	tags, err := m.listTagsByRepo(ctx, repo.ID)
	if err != nil {
		return Tag{}, fmt.Errorf("CreateTag: list tags: %w", err)
	}
	for _, t := range tags {
		if strProp(t.Properties, "name") == req.Name {
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
			"sha":         strProp(commitEntity.Properties, "sha"),
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

// ListTags returns all Tag entities for this agency's repository.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) ListTags(ctx context.Context) ([]Tag, error) {
	repo, err := m.GetRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListTags: %w", err)
	}
	tags, err := m.listTagsByRepo(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("ListTags: %w", err)
	}
	out := make([]Tag, len(tags))
	for i, e := range tags {
		out[i] = entityToTag(e, repo.ID)
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

// ── Internal helpers ──────────────────────────────────────────────────────────

// listRepositories returns all Repository entities for this agency.
func (m *gitManager) listRepositories(ctx context.Context) ([]entitygraph.Entity, error) {
	return m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Repository",
	})
}

// listBranchesByRepo returns all Branch entities whose belongs_to_repository
// edge points to the given repositoryID.
func (m *gitManager) listBranchesByRepo(ctx context.Context, repositoryID string) ([]entitygraph.Entity, error) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "has_branch",
		FromID:   repositoryID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]entitygraph.Entity, 0, len(rels))
	for _, r := range rels {
		e, err := m.dm.GetEntity(ctx, m.agencyID, r.ToID)
		if err != nil {
			continue // skip soft-deleted branches
		}
		out = append(out, e)
	}
	return out, nil
}

// listTagsByRepo returns all Tag entities whose belongs_to_repository edge
// points to the given repositoryID.
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

// defaultBranch returns the Branch entity whose is_default property is true
// for the given repository.
func (m *gitManager) defaultBranch(ctx context.Context, repositoryID string) (Branch, error) {
	branches, err := m.listBranchesByRepo(ctx, repositoryID)
	if err != nil {
		return Branch{}, err
	}
	for _, b := range branches {
		if boolProp(b.Properties, "is_default") {
			return entityToBranch(b, repositoryID), nil
		}
	}
	return Branch{}, ErrBranchNotFound
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

// resolveParentID returns the first ToID for an outbound relationship with the
// given name from the entity identified by entityID. Returns "" on any error.
func (m *gitManager) resolveParentID(ctx context.Context, entityID, relName string) string {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     relName,
		FromID:   entityID,
	})
	if err != nil || len(rels) == 0 {
		return ""
	}
	return rels[0].ToID
}

// advanceBranchHead updates a branch's points_to edge (and head_commit_id
// property) to point at newCommitID. Returns the updated Branch.
func (m *gitManager) advanceBranchHead(ctx context.Context, branchID, newCommitID string) (Branch, error) {
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

	// Update the denormalised head_commit_id property.
	now := time.Now().UTC().Format(time.RFC3339)
	updated, err := m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"head_commit_id": newCommitID,
			"updated_at":     now,
		},
	})
	if err != nil {
		return Branch{}, fmt.Errorf("advanceBranchHead: update entity: %w", err)
	}
	repoID := m.resolveParentID(ctx, branchID, "belongs_to_repository")
	return entityToBranch(updated, repoID), nil
}

// ── Entity → domain converters ────────────────────────────────────────────────

// entityToRepository maps an entitygraph.Entity of type "Repository" to [Repository].
func entityToRepository(e entitygraph.Entity, agencyID string) Repository {
	p := e.Properties
	return Repository{
		ID:            e.ID,
		AgencyID:      agencyID,
		Name:          strProp(p, "name"),
		Description:   strProp(p, "description"),
		DefaultBranch: strProp(p, "default_branch"),
		CreatedAt:     strProp(p, "created_at"),
		UpdatedAt:     strProp(p, "updated_at"),
	}
}

// entityToBranch maps an entitygraph.Entity of type "Branch" to [Branch].
func entityToBranch(e entitygraph.Entity, repositoryID string) Branch {
	p := e.Properties
	return Branch{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         strProp(p, "name"),
		IsDefault:    boolProp(p, "is_default"),
		HeadCommitID: strProp(p, "head_commit_id"),
		CreatedAt:    strProp(p, "created_at"),
		UpdatedAt:    strProp(p, "updated_at"),
	}
}

// entityToTag maps an entitygraph.Entity of type "Tag" to [Tag].
func entityToTag(e entitygraph.Entity, repositoryID string) Tag {
	p := e.Properties
	return Tag{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         strProp(p, "name"),
		SHA:          strProp(p, "sha"),
		Message:      strProp(p, "message"),
		TaggerName:   strProp(p, "tagger_name"),
		TaggerAt:     strProp(p, "tagger_at"),
		CreatedAt:    strProp(p, "created_at"),
	}
}

// ── Property helpers ──────────────────────────────────────────────────────────

// strProp returns the string value of key in props, or "" if absent.
func strProp(props map[string]any, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// boolProp returns the bool value of key in props, or false if absent.
func boolProp(props map[string]any, key string) bool {
	if v, ok := props[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// int64Prop returns the int64 value of key in props, or 0 if absent.
func int64Prop(props map[string]any, key string) int64 {
	if v, ok := props[key]; ok {
		switch vv := v.(type) {
		case int64:
			return vv
		case int:
			return int64(vv)
		case float64:
			return int64(vv)
		}
	}
	return 0
}

// stringSliceProp returns the []string value of key in props, or nil if absent.
func stringSliceProp(props map[string]any, key string) []string {
	if v, ok := props[key]; ok {
		switch vv := v.(type) {
		case []string:
			return vv
		case []any:
			out := make([]string, 0, len(vv))
			for _, item := range vv {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}
