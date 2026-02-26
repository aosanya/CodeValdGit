// Package arangodb provides an ArangoDB-backed implementation of
// [codevaldgit.Backend]. Git objects (blobs, trees, commits, refs, index,
// config) are stored in ArangoDB collections partitioned by agencyID, so
// repositories survive container restarts without a mounted volume.
//
// The working tree (billy.Filesystem) remains on a local or in-memory
// filesystem — only the Git object store moves to ArangoDB.
//
// Obtain a Backend with [NewArangoBackend], then pass it to
// codevaldgit.NewRepoManager.
//
// Fully implemented in MVP-GIT-008.
package arangodb

import (
	"context"
	"errors"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ArangoConfig holds the ArangoDB connection and worktree settings
// for the ArangoDB backend.
type ArangoConfig struct {
	// Endpoint is the ArangoDB server URL (e.g. "http://localhost:8529").
	Endpoint string

	// Database is the ArangoDB database name that holds the four Git collections
	// (git_objects, git_refs, git_index, git_config).
	Database string

	// User is the ArangoDB username.
	User string

	// Password is the ArangoDB password.
	Password string

	// WorktreePath is the local path for the billy.Filesystem working tree.
	// Use "" for an in-memory worktree (memfs) — the recommended default for
	// the ArangoDB backend; committed objects persist in ArangoDB regardless.
	WorktreePath string

	// TODO(MVP-GIT-008): add driver.Database field once ArangoDB driver is wired.
	// Database driver.Database
}

// arangoBackend implements [codevaldgit.Backend] using ArangoDB collections.
type arangoBackend struct {
	cfg ArangoConfig
}

// NewArangoBackend constructs an ArangoDB-backed [codevaldgit.Backend].
// The four ArangoDB collections (git_objects, git_refs, git_index, git_config)
// must already exist in the provided database.
// Fully implemented in MVP-GIT-008.
func NewArangoBackend(cfg ArangoConfig) (codevaldgit.Backend, error) {
	return &arangoBackend{cfg: cfg}, nil
}

// InitRepo inserts seed documents for agencyID into the ArangoDB collections.
// Implemented in MVP-GIT-008.
func (b *arangoBackend) InitRepo(_ context.Context, agencyID string) error {
	return errors.New("InitRepo: not yet implemented — see MVP-GIT-008")
}

// OpenStorer returns an ArangoDB storage.Storer and billy.Filesystem for agencyID.
// Implemented in MVP-GIT-008.
func (b *arangoBackend) OpenStorer(_ context.Context, agencyID string) (storage.Storer, billy.Filesystem, error) {
	return nil, nil, errors.New("OpenStorer: not yet implemented — see MVP-GIT-008")
}

// DeleteRepo sets a deleted flag on all agency documents in ArangoDB (auditable, non-destructive).
// Implemented in MVP-GIT-008.
func (b *arangoBackend) DeleteRepo(_ context.Context, agencyID string) error {
	return errors.New("DeleteRepo: not yet implemented — see MVP-GIT-008")
}

// PurgeRepo deletes all documents where agencyID matches from all four collections.
// Implemented in MVP-GIT-008.
func (b *arangoBackend) PurgeRepo(_ context.Context, agencyID string) error {
	return errors.New("PurgeRepo: not yet implemented — see MVP-GIT-008")
}
