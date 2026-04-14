package codevaldgit

import (
	"context"
	"fmt"
)

// repoManager is the concrete implementation of [RepoManager].
// It holds an injected [Backend] and delegates all storage-specific lifecycle
// operations to it. The Repo implementation (branches, files, history) is
// entirely backend-agnostic and lives in repo.go.
type repoManager struct {
	backend Backend
}

// NewRepoManager constructs a [RepoManager] backed by the given [Backend].
// Use storage/filesystem.NewFilesystemBackend or storage/arangodb.NewArangoBackend
// to construct a Backend, then pass it here.
// Returns an error if b is nil.
func NewRepoManager(b Backend) (RepoManager, error) {
	if b == nil {
		return nil, fmt.Errorf("NewRepoManager: backend must not be nil")
	}
	return &repoManager{backend: b}, nil
}

// InitRepo delegates to [Backend.InitRepo].
func (m *repoManager) InitRepo(ctx context.Context, agencyID, repoName string) error {
	return m.backend.InitRepo(ctx, agencyID, repoName)
}

// OpenRepo delegates to [Backend.OpenStorer] to obtain a storage.Storer and
// billy.Filesystem, then wraps them in the shared Repo implementation.
func (m *repoManager) OpenRepo(ctx context.Context, agencyID, repoName string) (Repo, error) {
	storer, fs, err := m.backend.OpenStorer(ctx, agencyID, repoName)
	if err != nil {
		return nil, fmt.Errorf("OpenRepo %s/%s: %w", agencyID, repoName, err)
	}
	r, err := newRepo(storer, fs)
	if err != nil {
		return nil, fmt.Errorf("OpenRepo %s/%s: %w", agencyID, repoName, err)
	}
	return r, nil
}

// DeleteRepo delegates to [Backend.DeleteRepo].
func (m *repoManager) DeleteRepo(ctx context.Context, agencyID string) error {
	return m.backend.DeleteRepo(ctx, agencyID)
}

// PurgeRepo delegates to [Backend.PurgeRepo].
func (m *repoManager) PurgeRepo(ctx context.Context, agencyID string) error {
	return m.backend.PurgeRepo(ctx, agencyID)
}
