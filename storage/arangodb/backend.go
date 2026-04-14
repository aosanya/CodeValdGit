// backend.go implements arangoBackend, the ArangoDB implementation of
// codevaldgit.Backend.
//
// arangoBackend uses arangoStorer internally for all git object/ref storage.
// Both the gRPC GitManager and the git Smart HTTP handler share the same
// backend, so a git clone / push succeeds against any repo whether it was
// initialised via the gRPC GitManager or auto-created by the Smart HTTP
// backendLoader on first access.
//
// Construction:
//
//	b := NewArangoStorerBackend(dm)
package arangodb

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// arangoBackend implements codevaldgit.Backend backed by ArangoDB.
// All git state — objects (Blob, Tree, Commit, Tag), refs, config, index, and
// shallow — is stored via dm (entitygraph.DataManager).
// It is constructed once per process and shared across all gRPC and Smart HTTP
// request handlers.
type arangoBackend struct {
	dm entitygraph.DataManager
}

// ── codevaldgit.Backend implementation ───────────────────────────────────────

// InitRepo creates the Agency, Repository, and default Branch entities in
// entitygraph for the given agencyID and repoName. It is idempotent — if a
// Repository entity with the given name already exists for the agency,
// [codevaldgit.ErrRepoAlreadyExists] is returned.
//
// This mirrors what [gitManager.InitRepo] does so that the git Smart HTTP
// backendLoader can auto-create a repository on first push without requiring a
// prior gRPC InitRepo call.
func (b *arangoBackend) InitRepo(ctx context.Context, agencyID, repoName string) error {
	// Idempotency check — if the repository already exists, bail early.
	existing, err := b.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   agencyID,
		TypeID:     "Repository",
		Properties: map[string]any{"name": repoName},
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s/%s: check existing: %w", agencyID, repoName, err)
	}
	if len(existing) > 0 {
		return codevaldgit.ErrRepoAlreadyExists
	}

	// Ensure the Agency root entity exists.
	agencyEntityID, err := b.ensureAgencyEntity(ctx, agencyID)
	if err != nil {
		return fmt.Errorf("InitRepo %s/%s: ensure agency: %w", agencyID, repoName, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	repoEntity, err := b.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":           repoName,
			"description":    "",
			"default_branch": "main",
			"created_at":     now,
			"updated_at":     now,
		},
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s/%s: create repository entity: %w", agencyID, repoName, err)
	}

	// Link Repository → Agency.
	if _, err := b.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: agencyID,
		Name:     "belongs_to_agency",
		FromID:   repoEntity.ID,
		ToID:     agencyEntityID,
	}); err != nil {
		return fmt.Errorf("InitRepo %s/%s: link agency: %w", agencyID, repoName, err)
	}

	// Create the default "main" Branch entity.
	branchEntity, err := b.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"name":       "main",
			"is_default": true,
			"created_at": now,
			"updated_at": now,
		},
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s/%s: create default branch entity: %w", agencyID, repoName, err)
	}

	// Link Branch → Repository (belongs_to_repository).
	if _, err := b.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: agencyID,
		Name:     "belongs_to_repository",
		FromID:   branchEntity.ID,
		ToID:     repoEntity.ID,
	}); err != nil {
		return fmt.Errorf("InitRepo %s/%s: link branch to repository: %w", agencyID, repoName, err)
	}

	// Link Repository → Branch (has_branch).
	if _, err := b.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: agencyID,
		Name:     "has_branch",
		FromID:   repoEntity.ID,
		ToID:     branchEntity.ID,
	}); err != nil {
		return fmt.Errorf("InitRepo %s/%s: link has_branch: %w", agencyID, repoName, err)
	}

	return nil
}

// ensureAgencyEntity returns the entitygraph ID of the Agency entity for
// agencyID, creating it if it does not yet exist.
func (b *arangoBackend) ensureAgencyEntity(ctx context.Context, agencyID string) (string, error) {
	entities, err := b.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: agencyID,
		TypeID:   "Agency",
	})
	if err != nil {
		return "", fmt.Errorf("ensureAgencyEntity %s: list: %w", agencyID, err)
	}
	if len(entities) > 0 {
		return entities[0].ID, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	e, err := b.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Agency",
		Properties: map[string]any{
			"name":       agencyID,
			"created_at": now,
			"updated_at": now,
		},
	})
	if err != nil {
		return "", fmt.Errorf("ensureAgencyEntity %s: create: %w", agencyID, err)
	}
	return e.ID, nil
}

// OpenStorer returns the arangoStorer for the named repository within agencyID,
// plus a fresh in-memory working tree. The Smart HTTP transport only reads the
// object store; the in-memory working tree is never persisted.
//
// Returns codevaldgit.ErrRepoNotFound if no Repository entity with the given
// name exists for the agency.
func (b *arangoBackend) OpenStorer(ctx context.Context, agencyID, repoName string) (storage.Storer, billy.Filesystem, error) {
	repos, err := b.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   agencyID,
		TypeID:     "Repository",
		Properties: map[string]any{"name": repoName},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("OpenStorer %s/%s: list repositories: %w", agencyID, repoName, err)
	}
	if len(repos) == 0 {
		return nil, nil, codevaldgit.ErrRepoNotFound
	}
	return newArangoStorer(b.dm, agencyID), memfs.New(), nil
}

// DeleteRepo soft-deletes the Repository entity for agencyID via
// dm.DeleteEntity. Downstream entitygraph records (branches, commits, blobs)
// are retained as orphans (auditable, non-destructive).
//
// Returns codevaldgit.ErrRepoNotFound if no Repository entity exists.
func (b *arangoBackend) DeleteRepo(ctx context.Context, agencyID string) error {
	return b.deleteRepoEntity(ctx, agencyID)
}

// PurgeRepo permanently removes all repository data for agencyID.
// For the ArangoDB backend this is identical to DeleteRepo: entitygraph
// soft-deletes the Repository entity; there is no separate hard-delete path.
func (b *arangoBackend) PurgeRepo(ctx context.Context, agencyID string) error {
	return b.deleteRepoEntity(ctx, agencyID)
}

// deleteRepoEntity locates the Repository entity for agencyID and calls
// dm.DeleteEntity on it. Returns codevaldgit.ErrRepoNotFound if none exists.
func (b *arangoBackend) deleteRepoEntity(ctx context.Context, agencyID string) error {
	repos, err := b.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: agencyID,
		TypeID:   "Repository",
	})
	if err != nil {
		return fmt.Errorf("deleteRepoEntity %s: list: %w", agencyID, err)
	}
	if len(repos) == 0 {
		return codevaldgit.ErrRepoNotFound
	}
	for _, repo := range repos {
		if err := b.dm.DeleteEntity(ctx, agencyID, repo.ID); err != nil {
			return fmt.Errorf("deleteRepoEntity %s: delete %s: %w", agencyID, repo.ID, err)
		}
	}
	return nil
}
