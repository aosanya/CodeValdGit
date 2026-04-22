package gitgraph_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/aosanya/CodeValdGit/internal/gitgraph"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── fakeDataManager ───────────────────────────────────────────────────────────

// fakeDataManager is an in-memory implementation of entitygraph.DataManager
// used exclusively by the gitgraph sync tests.
type fakeDataManager struct {
	mu            sync.Mutex
	entities      map[string]entitygraph.Entity      // id → Entity
	relationships map[string]entitygraph.Relationship // id → Relationship
	nextEntityID  int
	nextRelID     int
}

func newFakeDM() *fakeDataManager {
	return &fakeDataManager{
		entities:      make(map[string]entitygraph.Entity),
		relationships: make(map[string]entitygraph.Relationship),
	}
}

func (f *fakeDataManager) nextEID() string {
	f.nextEntityID++
	return fmt.Sprintf("entity-%d", f.nextEntityID)
}

func (f *fakeDataManager) nextRID() string {
	f.nextRelID++
	return fmt.Sprintf("rel-%d", f.nextRelID)
}

func (f *fakeDataManager) CreateEntity(_ context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := entitygraph.Entity{
		ID:         f.nextEID(),
		AgencyID:   req.AgencyID,
		TypeID:     req.TypeID,
		Properties: req.Properties,
	}
	f.entities[e.ID] = e
	return e, nil
}

func (f *fakeDataManager) GetEntity(_ context.Context, _, entityID string) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entities[entityID]
	if !ok {
		return entitygraph.Entity{}, entitygraph.ErrEntityNotFound
	}
	return e, nil
}

func (f *fakeDataManager) UpdateEntity(_ context.Context, agencyID, entityID string, req entitygraph.UpdateEntityRequest) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entities[entityID]
	if !ok {
		return entitygraph.Entity{}, entitygraph.ErrEntityNotFound
	}
	if e.Properties == nil {
		e.Properties = make(map[string]any)
	}
	for k, v := range req.Properties {
		e.Properties[k] = v
	}
	f.entities[entityID] = e
	return e, nil
}

func (f *fakeDataManager) DeleteEntity(_ context.Context, _, entityID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entities[entityID]; !ok {
		return entitygraph.ErrEntityNotFound
	}
	delete(f.entities, entityID)
	return nil
}

func (f *fakeDataManager) ListEntities(_ context.Context, filter entitygraph.EntityFilter) ([]entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []entitygraph.Entity
	for _, e := range f.entities {
		if filter.AgencyID != "" && e.AgencyID != filter.AgencyID {
			continue
		}
		if filter.TypeID != "" && e.TypeID != filter.TypeID {
			continue
		}
		if !propsMatch(e.Properties, filter.Properties) {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func (f *fakeDataManager) UpsertEntity(_ context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	return f.CreateEntity(context.Background(), req)
}

func (f *fakeDataManager) CreateRelationship(_ context.Context, req entitygraph.CreateRelationshipRequest) (entitygraph.Relationship, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := entitygraph.Relationship{
		ID:         f.nextRID(),
		AgencyID:   req.AgencyID,
		Name:       req.Name,
		FromID:     req.FromID,
		ToID:       req.ToID,
		Properties: req.Properties,
	}
	f.relationships[r.ID] = r
	return r, nil
}

func (f *fakeDataManager) GetRelationship(_ context.Context, _, relID string) (entitygraph.Relationship, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.relationships[relID]
	if !ok {
		return entitygraph.Relationship{}, entitygraph.ErrRelationshipNotFound
	}
	return r, nil
}

func (f *fakeDataManager) DeleteRelationship(_ context.Context, _, relID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.relationships[relID]; !ok {
		return entitygraph.ErrRelationshipNotFound
	}
	delete(f.relationships, relID)
	return nil
}

func (f *fakeDataManager) ListRelationships(_ context.Context, filter entitygraph.RelationshipFilter) ([]entitygraph.Relationship, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []entitygraph.Relationship
	for _, r := range f.relationships {
		if filter.AgencyID != "" && r.AgencyID != filter.AgencyID {
			continue
		}
		if filter.FromID != "" && r.FromID != filter.FromID {
			continue
		}
		if filter.ToID != "" && r.ToID != filter.ToID {
			continue
		}
		if filter.Name != "" && r.Name != filter.Name {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}

func (f *fakeDataManager) TraverseGraph(_ context.Context, _ entitygraph.TraverseGraphRequest) (entitygraph.TraverseGraphResult, error) {
	return entitygraph.TraverseGraphResult{}, nil
}

// propsMatch returns true if all entries in want appear in got with equal values.
func propsMatch(got, want map[string]any) bool {
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", gv) != fmt.Sprintf("%v", wv) {
			return false
		}
	}
	return true
}

// ── helpers ───────────────────────────────────────────────────────────────────

// addBlob inserts a Blob entity with the given path and returns its ID.
func addBlob(dm *fakeDataManager, agencyID, path string) string {
	e, _ := dm.CreateEntity(context.Background(), entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"path": path,
			"name": path,
			"sha":  "deadbeef",
		},
	})
	return e.ID
}

const testAgency = "agency-test"

// ── Syncer tests ──────────────────────────────────────────────────────────────

func TestSyncer_Sync_KeywordCreate(t *testing.T) {
	dm := newFakeDM()
	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)

	files := []gitgraph.MappingFile{
		{Keywords: []gitgraph.KeywordDef{{Name: "auth", Description: "Authentication", Scope: "agency"}}},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	kwEntities, _ := dm.ListEntities(context.Background(), entitygraph.EntityFilter{
		AgencyID: testAgency,
		TypeID:   "Keyword",
	})
	if len(kwEntities) != 1 {
		t.Fatalf("expected 1 Keyword entity, got %d", len(kwEntities))
	}
	if kwEntities[0].Properties["name"] != "auth" {
		t.Fatalf("unexpected keyword name: %v", kwEntities[0].Properties["name"])
	}
}

func TestSyncer_Sync_KeywordUpdate(t *testing.T) {
	dm := newFakeDM()
	// Pre-populate an existing keyword.
	existing, _ := dm.CreateEntity(context.Background(), entitygraph.CreateEntityRequest{
		AgencyID:   testAgency,
		TypeID:     "Keyword",
		Properties: map[string]any{"name": "auth", "description": "old", "scope": "repo"},
	})

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{Keywords: []gitgraph.KeywordDef{{Name: "auth", Description: "new description", Scope: "agency"}}},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	// There should still be exactly one Keyword entity.
	kwEntities, _ := dm.ListEntities(context.Background(), entitygraph.EntityFilter{
		AgencyID: testAgency,
		TypeID:   "Keyword",
	})
	if len(kwEntities) != 1 {
		t.Fatalf("expected 1 Keyword entity, got %d", len(kwEntities))
	}
	updated := dm.entities[existing.ID]
	if updated.Properties["description"] != "new description" {
		t.Fatalf("expected description updated to %q, got %q", "new description", updated.Properties["description"])
	}
	if updated.Properties["scope"] != "agency" {
		t.Fatalf("expected scope updated to %q, got %q", "agency", updated.Properties["scope"])
	}
}

func TestSyncer_Sync_TaggedWithEdgeCreate(t *testing.T) {
	dm := newFakeDM()
	blobID := addBlob(dm, testAgency, "lib/auth.dart")

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{
			Keywords: []gitgraph.KeywordDef{{Name: "auth"}},
			Mappings: []gitgraph.MappingEntry{
				{
					File:     "lib/auth.dart",
					Keywords: []string{"auth"},
					Depths:   []gitgraph.DepthEntry{{Keyword: "auth", Signal: "authority", Note: "canonical"}},
				},
			},
		},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   blobID,
		Name:     "tagged_with",
	})
	if len(rels) != 1 {
		t.Fatalf("expected 1 tagged_with edge, got %d", len(rels))
	}
	if rels[0].Properties["signal"] != "authority" {
		t.Fatalf("expected signal=authority, got %v", rels[0].Properties["signal"])
	}
	if rels[0].Properties["note"] != "canonical" {
		t.Fatalf("expected note=canonical, got %v", rels[0].Properties["note"])
	}
	if rels[0].Properties["branch_id"] != "branch-1" {
		t.Fatalf("expected branch_id=branch-1, got %v", rels[0].Properties["branch_id"])
	}
}

func TestSyncer_Sync_TaggedWithEdge_DefaultSignalSurface(t *testing.T) {
	dm := newFakeDM()
	addBlob(dm, testAgency, "lib/auth.dart")

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{
			Keywords: []gitgraph.KeywordDef{{Name: "auth"}},
			Mappings: []gitgraph.MappingEntry{
				{File: "lib/auth.dart", Keywords: []string{"auth"}}, // no depths[]
			},
		},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		Name:     "tagged_with",
	})
	if len(rels) != 1 {
		t.Fatalf("expected 1 tagged_with edge, got %d", len(rels))
	}
	if rels[0].Properties["signal"] != "surface" {
		t.Fatalf("expected signal=surface, got %v", rels[0].Properties["signal"])
	}
}

func TestSyncer_Sync_ReferencesEdgeCreate(t *testing.T) {
	dm := newFakeDM()
	blobID := addBlob(dm, testAgency, "lib/auth.dart")
	addBlob(dm, testAgency, "lib/provider.dart")

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{
			Mappings: []gitgraph.MappingEntry{
				{
					File:       "lib/auth.dart",
					References: []gitgraph.RefEntry{{File: "lib/provider.dart", Descriptor: "depends_on"}},
				},
			},
		},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   blobID,
		Name:     "references",
	})
	if len(rels) != 1 {
		t.Fatalf("expected 1 references edge, got %d", len(rels))
	}
	if rels[0].Properties["descriptor"] != "depends_on" {
		t.Fatalf("expected descriptor=depends_on, got %v", rels[0].Properties["descriptor"])
	}
}

func TestSyncer_Sync_TestedByEdgeCreate(t *testing.T) {
	dm := newFakeDM()
	blobID := addBlob(dm, testAgency, "lib/auth.dart")
	addBlob(dm, testAgency, "test/auth_test.dart")

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{
			Mappings: []gitgraph.MappingEntry{
				{
					File:     "lib/auth.dart",
					TestedBy: []gitgraph.TestedByEntry{{File: "test/auth_test.dart"}},
				},
			},
		},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   blobID,
		Name:     "references",
	})
	if len(rels) != 1 {
		t.Fatalf("expected 1 references edge, got %d", len(rels))
	}
	if rels[0].Properties["descriptor"] != "tested_by" {
		t.Fatalf("expected descriptor=tested_by, got %v", rels[0].Properties["descriptor"])
	}
}

func TestSyncer_Sync_EdgeDelete(t *testing.T) {
	dm := newFakeDM()
	blobID := addBlob(dm, testAgency, "lib/auth.dart")

	// Pre-populate a keyword and a stale tagged_with edge on branch-1.
	kw, _ := dm.CreateEntity(context.Background(), entitygraph.CreateEntityRequest{
		AgencyID:   testAgency,
		TypeID:     "Keyword",
		Properties: map[string]any{"name": "stale-kw"},
	})
	dm.CreateRelationship(context.Background(), entitygraph.CreateRelationshipRequest{
		AgencyID:   testAgency,
		Name:       "tagged_with",
		FromID:     blobID,
		ToID:       kw.ID,
		Properties: map[string]any{"signal": "surface", "branch_id": "branch-1"},
	})

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	// Sync with no mappings for lib/auth.dart — all existing edges should be removed.
	files := []gitgraph.MappingFile{
		{Mappings: []gitgraph.MappingEntry{{File: "lib/auth.dart"}}},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   blobID,
		Name:     "tagged_with",
	})
	if len(rels) != 0 {
		t.Fatalf("expected stale edge deleted, got %d edges remaining", len(rels))
	}
}

func TestSyncer_Sync_EdgeOnOtherBranchUntouched(t *testing.T) {
	dm := newFakeDM()
	blobID := addBlob(dm, testAgency, "lib/auth.dart")

	// Pre-populate a tagged_with edge on a different branch.
	kw, _ := dm.CreateEntity(context.Background(), entitygraph.CreateEntityRequest{
		AgencyID:   testAgency,
		TypeID:     "Keyword",
		Properties: map[string]any{"name": "other-kw"},
	})
	dm.CreateRelationship(context.Background(), entitygraph.CreateRelationshipRequest{
		AgencyID:   testAgency,
		Name:       "tagged_with",
		FromID:     blobID,
		ToID:       kw.ID,
		Properties: map[string]any{"signal": "surface", "branch_id": "branch-other"},
	})

	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	files := []gitgraph.MappingFile{
		{Mappings: []gitgraph.MappingEntry{{File: "lib/auth.dart"}}},
	}
	// Sync on branch-1 — the edge on branch-other must remain.
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	rels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   blobID,
		Name:     "tagged_with",
	})
	if len(rels) != 1 {
		t.Fatalf("expected other-branch edge to remain, got %d", len(rels))
	}
}

func TestSyncer_Sync_BlobNotFound_Skips(t *testing.T) {
	dm := newFakeDM()
	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)

	// Reference a file path that has no Blob entity.
	files := []gitgraph.MappingFile{
		{Mappings: []gitgraph.MappingEntry{{File: "nonexistent.go"}}},
	}
	// Must not panic or return an error.
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
}

func TestSyncer_Sync_ParentEdge(t *testing.T) {
	dm := newFakeDM()
	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)

	files := []gitgraph.MappingFile{
		{
			Keywords: []gitgraph.KeywordDef{
				{Name: "auth"},
				{Name: "login", Parent: "auth"},
			},
		},
	}
	if err := s.Sync(context.Background(), "branch-1", files); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	// Expect a belongs_to_parent edge from login → auth.
	kwEntities, _ := dm.ListEntities(context.Background(), entitygraph.EntityFilter{
		AgencyID: testAgency,
		TypeID:   "Keyword",
	})
	if len(kwEntities) != 2 {
		t.Fatalf("expected 2 keywords, got %d", len(kwEntities))
	}
	// Find the "login" keyword entity ID.
	var loginID string
	for _, e := range kwEntities {
		if e.Properties["name"] == "login" {
			loginID = e.ID
		}
	}
	if loginID == "" {
		t.Fatal("login keyword entity not found")
	}

	parentRels, _ := dm.ListRelationships(context.Background(), entitygraph.RelationshipFilter{
		AgencyID: testAgency,
		FromID:   loginID,
		Name:     "belongs_to_parent",
	})
	if len(parentRels) != 1 {
		t.Fatalf("expected 1 belongs_to_parent edge for login, got %d", len(parentRels))
	}
}

func TestSyncer_Sync_PersistSignals(t *testing.T) {
	dm := newFakeDM()
	vocab := gitgraph.SignalVocab{
		Signals: []gitgraph.SignalDef{
			{Name: "surface", Layer: 2, Description: "surface signal"},
		},
	}
	s := gitgraph.NewSyncer(dm, testAgency, vocab)

	// The Signal TypeID is not in the fake schema, so CreateEntity will succeed
	// (the fake does no schema validation). We verify the attempt is made.
	if err := s.Sync(context.Background(), "branch-1", nil); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	sigEntities, _ := dm.ListEntities(context.Background(), entitygraph.EntityFilter{
		AgencyID: testAgency,
		TypeID:   "Signal",
	})
	if len(sigEntities) != 1 {
		t.Fatalf("expected 1 Signal entity, got %d", len(sigEntities))
	}
	if sigEntities[0].Properties["name"] != "surface" {
		t.Fatalf("unexpected signal name: %v", sigEntities[0].Properties["name"])
	}
}

func TestSyncer_Sync_PersistSignals_ExistingSkipped(t *testing.T) {
	dm := newFakeDM()
	// Pre-insert a Signal entity.
	dm.CreateEntity(context.Background(), entitygraph.CreateEntityRequest{
		AgencyID:   testAgency,
		TypeID:     "Signal",
		Properties: map[string]any{"name": "surface", "layer": 2},
	})

	vocab := gitgraph.SignalVocab{
		Signals: []gitgraph.SignalDef{{Name: "surface", Layer: 2}},
	}
	s := gitgraph.NewSyncer(dm, testAgency, vocab)
	if err := s.Sync(context.Background(), "branch-1", nil); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	// Should still be exactly one Signal entity (no duplicate created).
	sigEntities, _ := dm.ListEntities(context.Background(), entitygraph.EntityFilter{
		AgencyID: testAgency,
		TypeID:   "Signal",
	})
	if len(sigEntities) != 1 {
		t.Fatalf("expected 1 Signal entity (no duplicate), got %d", len(sigEntities))
	}
}

func TestSyncer_Sync_EmptyFiles_NoError(t *testing.T) {
	dm := newFakeDM()
	s := gitgraph.NewSyncer(dm, testAgency, gitgraph.DefaultSignals)
	if err := s.Sync(context.Background(), "branch-1", nil); err != nil {
		t.Fatalf("Sync with nil files returned error: %v", err)
	}
	if err := s.Sync(context.Background(), "branch-1", []gitgraph.MappingFile{}); err != nil {
		t.Fatalf("Sync with empty files returned error: %v", err)
	}
}
