// backend.go implements arangoBackend, the ArangoDB implementation of
// codevaldgit.Backend.
//
// arangoBackend uses arangoStorer internally for all git object/ref storage.
// Both the gRPC GitManager and the git Smart HTTP handler share the same
// backend, so a git clone succeeds against any repo created via gRPC.
//
// Construction:
//
//	b := NewArangoStorerBackend(dm)
//
// InitRepo is a no-op: the gRPC GitManager.InitRepo creates the Repository
// entity in entitygraph. OpenStorer verifies the Repository entity exists
// before returning the storer, so the first successful git clone is gated on
// a prior GitManager.InitRepo call.
package arangodb

import (
	"context"
	"fmt"

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

// InitRepo is a no-op for the ArangoDB backend.
// Repository entity creation is owned by gitManager.InitRepo, which writes
// the Repository, Agency, and Branch entities to entitygraph before a client
// can clone. OpenStorer gates git access on the Repository entity existing.
func (b *arangoBackend) InitRepo(_ context.Context, _ string) error {
	return nil
}

// OpenStorer returns the arangoStorer for agencyID plus a fresh in-memory
// working tree. The Smart HTTP transport only reads the object store; the
// in-memory working tree is never persisted.
//
// Returns codevaldgit.ErrRepoNotFound if no Repository entity exists for the agency.
func (b *arangoBackend) OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error) {
	repos, err := b.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: agencyID,
		TypeID:   "Repository",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("OpenStorer %s: list repositories: %w", agencyID, err)
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
