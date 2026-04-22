// git_011_internal_test.go — GIT-011 white-box test for the CAS guard in
// advanceBranchHead. This file uses package codevaldgit (not _test) so it
// can call the unexported advanceBranchHead directly.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// casTestDM is a minimal DataManager for testing the CAS guard.
// GetEntity returns a pre-set entity; all other methods are no-ops or stubs.
type casTestDM struct {
	entities map[string]entitygraph.Entity
}

func newCasTestDM() *casTestDM {
	return &casTestDM{entities: make(map[string]entitygraph.Entity)}
}

func (d *casTestDM) set(id string, e entitygraph.Entity) { d.entities[id] = e }

func (d *casTestDM) GetEntity(_ context.Context, _, id string) (entitygraph.Entity, error) {
	e, ok := d.entities[id]
	if !ok {
		return entitygraph.Entity{}, fmt.Errorf("%w", entitygraph.ErrEntityNotFound)
	}
	return e, nil
}

func (d *casTestDM) UpdateEntity(_ context.Context, _, id string, req entitygraph.UpdateEntityRequest) (entitygraph.Entity, error) {
	e := d.entities[id]
	for k, v := range req.Properties {
		e.Properties[k] = v
	}
	d.entities[id] = e
	return e, nil
}

func (d *casTestDM) ListRelationships(_ context.Context, _ entitygraph.RelationshipFilter) ([]entitygraph.Relationship, error) {
	return nil, nil
}

func (d *casTestDM) CreateRelationship(_ context.Context, req entitygraph.CreateRelationshipRequest) (entitygraph.Relationship, error) {
	return entitygraph.Relationship{ID: "rel-1", AgencyID: req.AgencyID, Name: req.Name, FromID: req.FromID, ToID: req.ToID}, nil
}

func (d *casTestDM) DeleteRelationship(_ context.Context, _, _ string) error { return nil }

// Stubs for unused DataManager methods.
func (d *casTestDM) CreateEntity(_ context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	return entitygraph.Entity{ID: "stub", AgencyID: req.AgencyID, TypeID: req.TypeID, Properties: req.Properties, CreatedAt: time.Now()}, nil
}
func (d *casTestDM) DeleteEntity(_ context.Context, _, _ string) error { return nil }
func (d *casTestDM) ListEntities(_ context.Context, _ entitygraph.EntityFilter) ([]entitygraph.Entity, error) {
	return nil, nil
}
func (d *casTestDM) UpsertEntity(ctx context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	return d.CreateEntity(ctx, req)
}
func (d *casTestDM) GetRelationship(_ context.Context, _, _ string) (entitygraph.Relationship, error) {
	return entitygraph.Relationship{}, entitygraph.ErrRelationshipNotFound
}

func (d *casTestDM) TraverseGraph(_ context.Context, _ entitygraph.TraverseGraphRequest) (entitygraph.TraverseGraphResult, error) {
	return entitygraph.TraverseGraphResult{}, nil
}

// TestGIT011_AdvanceBranchHead_StaleHeadReturnsConflict verifies that
// advanceBranchHead returns ErrMergeConcurrencyConflict when the supplied
// expectedHeadCommitID does not match the branch's current head_commit_id.
func TestGIT011_AdvanceBranchHead_StaleHeadReturnsConflict(t *testing.T) {
	t.Parallel()

	dm := newCasTestDM()
	const (
		agencyID  = "test-agency"
		branchID  = "branch-001"
		commitID  = "commit-new"
		actualHead = "commit-current"
		staleHead  = "commit-stale"
	)

	// Seed the branch entity with head_commit_id = actualHead.
	dm.set(branchID, entitygraph.Entity{
		ID:       branchID,
		AgencyID: agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"head_commit_id": actualHead,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	// Seed the commit entity so the advance path doesn't fail on GetEntity.
	dm.set(commitID, entitygraph.Entity{
		ID:       commitID,
		AgencyID: agencyID,
		TypeID:   "Commit",
		Properties: map[string]any{
			"sha": "abc123",
		},
	})

	mgr := &gitManager{
		dm:       dm,
		agencyID: agencyID,
		locker:   &mutexLocker{},
	}

	// Pass staleHead as the expected value — the entity holds actualHead.
	_, err := mgr.advanceBranchHead(context.Background(), branchID, commitID, staleHead)
	if !errors.Is(err, ErrMergeConcurrencyConflict) {
		t.Fatalf("expected ErrMergeConcurrencyConflict, got %v", err)
	}
}

// TestGIT011_AdvanceBranchHead_MatchingHeadSucceeds verifies that
// advanceBranchHead succeeds when the supplied expectedHeadCommitID matches
// the branch's current head_commit_id.
func TestGIT011_AdvanceBranchHead_MatchingHeadSucceeds(t *testing.T) {
	t.Parallel()

	dm := newCasTestDM()
	const (
		agencyID    = "test-agency"
		branchID    = "branch-002"
		commitID    = "commit-new2"
		currentHead = "commit-current2"
	)

	dm.set(branchID, entitygraph.Entity{
		ID:       branchID,
		AgencyID: agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"head_commit_id": currentHead,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	dm.set(commitID, entitygraph.Entity{
		ID:       commitID,
		AgencyID: agencyID,
		TypeID:   "Commit",
		Properties: map[string]any{
			"sha": "def456",
		},
	})

	mgr := &gitManager{
		dm:       dm,
		agencyID: agencyID,
		locker:   &mutexLocker{},
	}

	_, err := mgr.advanceBranchHead(context.Background(), branchID, commitID, currentHead)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
