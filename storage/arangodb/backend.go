// backend.go implements arangoBackend, the ArangoDB implementation of
// codevaldgit.Backend.
//
// arangoBackend uses arangoStorer internally for all git object/ref storage.
// Both the gRPC GitManager and the git Smart HTTP handler share the same
// backend, so a git clone succeeds against any repo created via gRPC.
//
// Construction:
//
//	b, err := NewArangoGitBackend(db)
//
// InitRepo creates an initial empty commit so that git clients can clone
// immediately after repo creation without encountering an empty HEAD error.
package arangodb

import (
	"context"
	"fmt"
	"time"

	driver "github.com/arangodb/go-driver"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// arangoBackend implements codevaldgit.Backend backed by ArangoDB raw git
// collections (gitraw_*). It is constructed once per process and shared across
// all gRPC and Smart HTTP request handlers.
type arangoBackend struct {
	db driver.Database
}

// NewArangoGitBackend constructs an arangoBackend from an already-open
// driver.Database. It ensures all five gitraw_* collections and their indexes
// exist before returning.
//
// Callers should use the existing arangodb.NewBackend / arangodb.NewBackendFromDB
// which call this function after setting up the entitygraph collections.
func NewArangoGitBackend(db driver.Database) (codevaldgit.Backend, error) {
	if db == nil {
		return nil, fmt.Errorf("arangodb: NewArangoGitBackend: database must not be nil")
	}
	ctx := context.Background()
	if err := ensureGitRawCollections(ctx, db); err != nil {
		return nil, fmt.Errorf("arangodb: NewArangoGitBackend: %w", err)
	}
	return &arangoBackend{db: db}, nil
}

// ensureGitRawCollections creates the five gitraw_* document collections and
// their indexes if they do not already exist.
func ensureGitRawCollections(ctx context.Context, db driver.Database) error {
	cols := []string{colObjects, colRefs, colConfig, colIndex, colShallow}
	for _, name := range cols {
		if _, err := ensureDocumentCollection(ctx, db, name); err != nil {
			return fmt.Errorf("ensure %s: %w", name, err)
		}
	}
	// Persistent index on [agencyID, sha] for gitraw_objects (deduplication).
	objCol, err := db.Collection(ctx, colObjects)
	if err != nil {
		return fmt.Errorf("open %s: %w", colObjects, err)
	}
	if _, _, err := objCol.EnsurePersistentIndex(ctx, []string{"agencyID", "sha"}, &driver.EnsurePersistentIndexOptions{
		Unique: true,
		Sparse: false,
		Name:   "idx_objects_agency_sha",
	}); err != nil {
		return fmt.Errorf("index on %s: %w", colObjects, err)
	}
	// Persistent index on [agencyID, refName] for gitraw_refs (fast iteration).
	refCol, err := db.Collection(ctx, colRefs)
	if err != nil {
		return fmt.Errorf("open %s: %w", colRefs, err)
	}
	if _, _, err := refCol.EnsurePersistentIndex(ctx, []string{"agencyID", "refName"}, &driver.EnsurePersistentIndexOptions{
		Name: "idx_refs_agency_refname",
	}); err != nil {
		return fmt.Errorf("index on %s: %w", colRefs, err)
	}
	return nil
}

// ── codevaldgit.Backend implementation ───────────────────────────────────────

// InitRepo provisions a new git store for agencyID. It calls gogit.Init with
// an in-memory worktree (discarded after init) and creates an initial empty
// commit so that git clients can clone immediately.
//
// Returns codevaldgit.ErrRepoAlreadyExists if HEAD already exists for the agency.
func (b *arangoBackend) InitRepo(ctx context.Context, agencyID string) error {
	s := newArangoStorer(b.db, agencyID)
	// Check if already initialised (HEAD exists).
	if _, err := s.Reference(plumbing.HEAD); err == nil {
		return codevaldgit.ErrRepoAlreadyExists
	}
	// Initialise the go-git repository with an in-memory working tree.
	wt := memfs.New()
	repo, err := gogit.InitWithOptions(s, wt, gogit.InitOptions{
		DefaultBranch: plumbing.Main,
	})
	if err != nil {
		return fmt.Errorf("InitRepo %s: init: %w", agencyID, err)
	}
	// Create an initial empty commit so the repo is clonable immediately.
	if err := createInitialCommit(repo); err != nil {
		return fmt.Errorf("InitRepo %s: initial commit: %w", agencyID, err)
	}
	return nil
}

// OpenStorer returns the arangoStorer for agencyID plus a fresh in-memory
// working tree. The Smart HTTP transport only reads the object store; the
// in-memory working tree is never persisted.
//
// Returns codevaldgit.ErrRepoNotFound if no HEAD reference exists for the agency.
func (b *arangoBackend) OpenStorer(ctx context.Context, agencyID string) (storage.Storer, billy.Filesystem, error) {
	s := newArangoStorer(b.db, agencyID)
	// Verify the repo is initialised by checking HEAD.
	if _, err := s.Reference(plumbing.HEAD); err != nil {
		if err == plumbing.ErrReferenceNotFound {
			return nil, nil, codevaldgit.ErrRepoNotFound
		}
		return nil, nil, fmt.Errorf("OpenStorer %s: check HEAD: %w", agencyID, err)
	}
	return s, memfs.New(), nil
}

// DeleteRepo removes all gitraw_* documents for agencyID using AQL batch deletes.
// Both DeleteRepo and PurgeRepo have identical behaviour: ArangoDB provides its
// own durable storage and audit trail; there is no archive concept at the object level.
//
// Returns codevaldgit.ErrRepoNotFound if the agency has no git data.
func (b *arangoBackend) DeleteRepo(ctx context.Context, agencyID string) error {
	return b.purgeAgencyDocs(ctx, agencyID)
}

// PurgeRepo permanently removes all storage for agencyID. Identical to DeleteRepo
// for the ArangoDB backend.
func (b *arangoBackend) PurgeRepo(ctx context.Context, agencyID string) error {
	return b.purgeAgencyDocs(ctx, agencyID)
}

// purgeAgencyDocs deletes all documents across the five gitraw_* collections
// where agencyID matches. Returns codevaldgit.ErrRepoNotFound if nothing exists.
func (b *arangoBackend) purgeAgencyDocs(ctx context.Context, agencyID string) error {
	cols := []string{colObjects, colRefs, colConfig, colIndex, colShallow}
	deleted := 0
	for _, col := range cols {
		query := fmt.Sprintf(
			"FOR doc IN %s FILTER doc.agencyID == @a REMOVE doc IN %s RETURN 1",
			col, col,
		)
		cursor, err := b.db.Query(ctx, query, map[string]any{"a": agencyID})
		if err != nil {
			return fmt.Errorf("purgeAgencyDocs %s: %s: %w", agencyID, col, err)
		}
		// Count removed documents to distinguish "not found" from "exists".
		for cursor.HasMore() {
			var v int
			cursor.ReadDocument(ctx, &v) //nolint:errcheck // counting only
			deleted++
		}
		cursor.Close()
	}
	if deleted == 0 {
		return codevaldgit.ErrRepoNotFound
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// createInitialCommit creates an empty "Initial commit" on the repo's current
// HEAD branch. This ensures the repo is immediately clonable via Smart HTTP.
func createInitialCommit(repo *gogit.Repository) error {
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	sig := &object.Signature{
		Name:  "CodeValdGit",
		Email: "init@codevald.io",
		When:  time.Now().UTC(),
	}
	_, err = wt.Commit("Initial commit", &gogit.CommitOptions{
		Author:            sig,
		Committer:         sig,
		AllowEmptyCommits: true,
	})
	return err
}

// localEnsureDocumentCollection creates a document collection if it does not
// already exist. Returns the opened collection.
func ensureDocumentCollection(ctx context.Context, db driver.Database, name string) (driver.Collection, error) {
	exists, err := db.CollectionExists(ctx, name)
	if err != nil {
		return nil, err
	}
	if exists {
		return db.Collection(ctx, name)
	}
	col, err := db.CreateCollection(ctx, name, nil)
	if err != nil {
		if driver.IsConflict(err) {
			return db.Collection(ctx, name)
		}
		return nil, err
	}
	return col, nil
}
