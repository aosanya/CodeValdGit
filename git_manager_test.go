// git_manager_test.go contains unit tests for the v2 [codevaldgit.GitManager]
// implementation.  All storage is provided by the in-memory [fakeDataManager]
// so no external dependencies (ArangoDB, filesystem) are required.
//
// Key design decisions captured here:
//
//   - fakeDataManager processes inline Relationships in CreateEntityRequest so
//     that resolveParentID (which follows belongs_to_repository edges) works.
//   - fakeDataManager implements TraverseGraph with a real BFS, enabling
//     allBlobsAtCommit to locate blobs via has_tree → has_blob traversal.
//   - The implementation explicitly creates has_branch, has_tag, has_tree, and
//     has_parent relationships (fixes added in GIT-010) so that
//     listBranchesByRepo, listTagsByRepo, allBlobsAtCommit, and walkCommitChain
//     all function correctly.
package codevaldgit_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	"github.com/aosanya/CodeValdSharedLib/types"
)

// ── fakeDataManager ───────────────────────────────────────────────────────────

// fakeDataManager is an in-memory implementation of [entitygraph.DataManager]
// used exclusively in unit tests. All operations are O(n) over slices /
// maps — correctness, not performance, is the goal.
//
// Unlike the ArangoDB backend, fakeDataManager honours inline Relationships in
// [entitygraph.CreateEntityRequest], creating them as regular relationship
// records so that resolveParentID and TraverseGraph work without further
// plumbing.
type fakeDataManager struct {
	mu            sync.Mutex
	entities      map[string]entitygraph.Entity
	relationships map[string]entitygraph.Relationship
	counter       int
}

func newFakeDataManager() *fakeDataManager {
	return &fakeDataManager{
		entities:      make(map[string]entitygraph.Entity),
		relationships: make(map[string]entitygraph.Relationship),
	}
}

// nextID returns a unique, deterministic ID for tests.
func (f *fakeDataManager) nextID() string {
	f.counter++
	return fmt.Sprintf("fake-%04d", f.counter)
}

// CreateEntity stores the entity and any inline relationships.
func (f *fakeDataManager) CreateEntity(_ context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	props := req.Properties
	if props == nil {
		props = make(map[string]any)
	}
	e := entitygraph.Entity{
		ID:         id,
		AgencyID:   req.AgencyID,
		TypeID:     req.TypeID,
		Properties: props,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	f.entities[id] = e
	// Persist inline relationships so resolveParentID and ListRelationships work.
	for _, r := range req.Relationships {
		relID := f.nextID()
		f.relationships[relID] = entitygraph.Relationship{
			ID:        relID,
			AgencyID:  req.AgencyID,
			Name:      r.Name,
			FromID:    id,
			ToID:      r.ToID,
			CreatedAt: time.Now().UTC(),
		}
	}
	return e, nil
}

// GetEntity returns the entity or ErrEntityNotFound if absent or soft-deleted.
func (f *fakeDataManager) GetEntity(_ context.Context, agencyID, entityID string) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entities[entityID]
	if !ok || e.Deleted || e.AgencyID != agencyID {
		return entitygraph.Entity{}, fmt.Errorf("GetEntity %s: %w", entityID, entitygraph.ErrEntityNotFound)
	}
	return e, nil
}

// UpdateEntity patches the entity's properties in place.
func (f *fakeDataManager) UpdateEntity(_ context.Context, agencyID, entityID string, req entitygraph.UpdateEntityRequest) (entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entities[entityID]
	if !ok || e.Deleted || e.AgencyID != agencyID {
		return entitygraph.Entity{}, fmt.Errorf("UpdateEntity %s: %w", entityID, entitygraph.ErrEntityNotFound)
	}
	if e.Properties == nil {
		e.Properties = make(map[string]any)
	}
	for k, v := range req.Properties {
		e.Properties[k] = v
	}
	e.UpdatedAt = time.Now().UTC()
	f.entities[entityID] = e
	return e, nil
}

// DeleteEntity soft-deletes the entity.
func (f *fakeDataManager) DeleteEntity(_ context.Context, agencyID, entityID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entities[entityID]
	if !ok || e.Deleted || e.AgencyID != agencyID {
		return fmt.Errorf("DeleteEntity %s: %w", entityID, entitygraph.ErrEntityNotFound)
	}
	now := time.Now().UTC()
	e.Deleted = true
	e.DeletedAt = &now
	f.entities[entityID] = e
	return nil
}

// ListEntities returns non-deleted entities matching the filter.
func (f *fakeDataManager) ListEntities(_ context.Context, filter entitygraph.EntityFilter) ([]entitygraph.Entity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []entitygraph.Entity
	for _, e := range f.entities {
		if e.Deleted {
			continue
		}
		if filter.AgencyID != "" && e.AgencyID != filter.AgencyID {
			continue
		}
		if filter.TypeID != "" && e.TypeID != filter.TypeID {
			continue
		}
		if len(filter.Properties) > 0 {
			match := true
			for k, v := range filter.Properties {
				if sv, ok := e.Properties[k]; !ok || sv != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		result = append(result, e)
	}
	return result, nil
}

// UpsertEntity delegates to CreateEntity for simplicity in tests.
func (f *fakeDataManager) UpsertEntity(ctx context.Context, req entitygraph.CreateEntityRequest) (entitygraph.Entity, error) {
	return f.CreateEntity(ctx, req)
}

// CreateRelationship stores a directed edge.
func (f *fakeDataManager) CreateRelationship(_ context.Context, req entitygraph.CreateRelationshipRequest) (entitygraph.Relationship, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	r := entitygraph.Relationship{
		ID:         id,
		AgencyID:   req.AgencyID,
		Name:       req.Name,
		FromID:     req.FromID,
		ToID:       req.ToID,
		Properties: req.Properties,
		CreatedAt:  time.Now().UTC(),
	}
	f.relationships[id] = r
	return r, nil
}

// GetRelationship returns the relationship or ErrRelationshipNotFound.
func (f *fakeDataManager) GetRelationship(_ context.Context, agencyID, relID string) (entitygraph.Relationship, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.relationships[relID]
	if !ok || r.AgencyID != agencyID {
		return entitygraph.Relationship{}, fmt.Errorf("GetRelationship %s: %w", relID, entitygraph.ErrRelationshipNotFound)
	}
	return r, nil
}

// DeleteRelationship removes the edge permanently.
func (f *fakeDataManager) DeleteRelationship(_ context.Context, agencyID, relID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.relationships[relID]
	if !ok || r.AgencyID != agencyID {
		return fmt.Errorf("DeleteRelationship %s: %w", relID, entitygraph.ErrRelationshipNotFound)
	}
	delete(f.relationships, relID)
	return nil
}

// ListRelationships returns all edges matching the filter.
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

// TraverseGraph walks the entity graph from StartID using BFS up to Depth
// hops.  It follows edges whose Name is in the Names filter (all edges when
// Names is empty) in the given Direction ("outbound", "inbound", "any").
func (f *fakeDataManager) TraverseGraph(_ context.Context, req entitygraph.TraverseGraphRequest) (entitygraph.TraverseGraphResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	namesSet := make(map[string]bool, len(req.Names))
	for _, n := range req.Names {
		namesSet[n] = true
	}

	depth := req.Depth
	if depth <= 0 {
		depth = 1
	}

	visited := map[string]bool{req.StartID: true}
	queue := []string{req.StartID}
	var vertices []entitygraph.Entity
	var edges []entitygraph.Relationship

	for d := 0; d < depth && len(queue) > 0; d++ {
		var next []string
		for _, id := range queue {
			for _, r := range f.relationships {
				if len(namesSet) > 0 && !namesSet[r.Name] {
					continue
				}
				var neighborID string
				switch req.Direction {
				case "outbound":
					if r.FromID != id {
						continue
					}
					neighborID = r.ToID
				case "inbound":
					if r.ToID != id {
						continue
					}
					neighborID = r.FromID
				default: // "any"
					if r.FromID == id {
						neighborID = r.ToID
					} else if r.ToID == id {
						neighborID = r.FromID
					} else {
						continue
					}
				}
				if !visited[neighborID] {
					visited[neighborID] = true
					next = append(next, neighborID)
					edges = append(edges, r)
					if e, ok := f.entities[neighborID]; ok && !e.Deleted {
						vertices = append(vertices, e)
					}
				}
			}
		}
		queue = next
	}
	return entitygraph.TraverseGraphResult{Vertices: vertices, Edges: edges}, nil
}

// ── fakeSchemaManager ─────────────────────────────────────────────────────────

// fakeSchemaManager is a no-op stub satisfying [entitygraph.SchemaManager].
// Schema operations are not exercised by the GitManager unit tests.
type fakeSchemaManager struct{}

func (f *fakeSchemaManager) SetSchema(_ context.Context, _ types.Schema) error { return nil }
func (f *fakeSchemaManager) GetSchema(_ context.Context, _ string) (types.Schema, error) {
	return types.Schema{}, nil
}
func (f *fakeSchemaManager) Publish(_ context.Context, _ string) error         { return nil }
func (f *fakeSchemaManager) Activate(_ context.Context, _ string, _ int) error { return nil }
func (f *fakeSchemaManager) GetActive(_ context.Context, _ string) (types.Schema, error) {
	return types.Schema{}, nil
}
func (f *fakeSchemaManager) GetVersion(_ context.Context, _ string, _ int) (types.Schema, error) {
	return types.Schema{}, nil
}
func (f *fakeSchemaManager) ListVersions(_ context.Context, _ string) ([]types.Schema, error) {
	return nil, nil
}

// ── fakePublisher ─────────────────────────────────────────────────────────────

type publishedEvent struct {
	topic    string
	agencyID string
}

// fakePublisher records all Publish calls for assertion in tests.
type fakePublisher struct {
	mu     sync.Mutex
	events []publishedEvent
}

// Publish records the event.
func (p *fakePublisher) Publish(_ context.Context, topic, agencyID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, publishedEvent{topic: topic, agencyID: agencyID})
	return nil
}

// published returns a snapshot of all recorded events.
func (p *fakePublisher) published() []publishedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]publishedEvent(nil), p.events...)
}

// ── test helpers ──────────────────────────────────────────────────────────────

const testAgencyID = "test-agency-001"

// newTestManager returns a GitManager backed by an in-memory fakeDataManager
// together with the fake and the publisher for direct inspection.
func newTestManager(t *testing.T) (codevaldgit.GitManager, *fakeDataManager, *fakePublisher) {
	t.Helper()
	fdm := newFakeDataManager()
	pub := &fakePublisher{}
	mgr := codevaldgit.NewGitManager(fdm, &fakeSchemaManager{}, pub, testAgencyID)
	return mgr, fdm, pub
}

// mustInitRepo calls InitRepo and fatals the test on error.
func mustInitRepo(t *testing.T, mgr codevaldgit.GitManager) codevaldgit.Repository {
	t.Helper()
	repo, err := mgr.InitRepo(context.Background(), codevaldgit.CreateRepoRequest{
		Name:          "test-repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	return repo
}

// mustDefaultBranch returns the repository's default branch and fatals the test
// if it cannot be found.
func mustDefaultBranch(t *testing.T, mgr codevaldgit.GitManager) codevaldgit.Branch {
	t.Helper()
	branches, err := mgr.ListBranches(context.Background())
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	for _, b := range branches {
		if b.IsDefault {
			return b
		}
	}
	t.Fatal("no default branch found after InitRepo")
	return codevaldgit.Branch{}
}

// mustWriteFile writes a file and fatals on error.
func mustWriteFile(t *testing.T, mgr codevaldgit.GitManager, branchID, path, content string) codevaldgit.Commit {
	t.Helper()
	commit, err := mgr.WriteFile(context.Background(), codevaldgit.WriteFileRequest{
		BranchID: branchID,
		Path:     path,
		Content:  content,
		Message:  "write " + path,
	})
	if err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return commit
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGitManager_InitRepo(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()

	repo, err := mgr.InitRepo(ctx, codevaldgit.CreateRepoRequest{
		Name:          "my-repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if repo.ID == "" {
		t.Error("repo.ID is empty")
	}
	if repo.Name != "my-repo" {
		t.Errorf("repo.Name = %q, want %q", repo.Name, "my-repo")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("repo.DefaultBranch = %q, want %q", repo.DefaultBranch, "main")
	}

	// Second call on the same agency must return ErrRepoAlreadyExists.
	_, err = mgr.InitRepo(ctx, codevaldgit.CreateRepoRequest{Name: "another"})
	if !errors.Is(err, codevaldgit.ErrRepoAlreadyExists) {
		t.Errorf("second InitRepo: got %v, want ErrRepoAlreadyExists", err)
	}
}

func TestGitManager_GetRepository_BeforeInit(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	_, err := mgr.GetRepository(context.Background())
	if !errors.Is(err, codevaldgit.ErrRepoNotInitialised) {
		t.Errorf("GetRepository before init: got %v, want ErrRepoNotInitialised", err)
	}
}

func TestGitManager_GetRepository_AfterInit(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mustInitRepo(t, mgr)

	repo, err := mgr.GetRepository(context.Background())
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Name != "test-repo" {
		t.Errorf("repo.Name = %q, want %q", repo.Name, "test-repo")
	}
}

func TestGitManager_DeleteRepo(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()

	// DeleteRepo before init returns ErrRepoNotInitialised.
	if err := mgr.DeleteRepo(ctx); !errors.Is(err, codevaldgit.ErrRepoNotInitialised) {
		t.Errorf("DeleteRepo before init: got %v, want ErrRepoNotInitialised", err)
	}

	mustInitRepo(t, mgr)
	if err := mgr.DeleteRepo(ctx); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}

	// Repository is gone after deletion.
	_, err := mgr.GetRepository(ctx)
	if !errors.Is(err, codevaldgit.ErrRepoNotInitialised) {
		t.Errorf("GetRepository after delete: got %v, want ErrRepoNotInitialised", err)
	}
}

func TestGitManager_ListBranches(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	mustInitRepo(t, mgr)

	branches, err := mgr.ListBranches(context.Background())
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("ListBranches: got %d branches, want 1", len(branches))
	}
	if branches[0].Name != "main" {
		t.Errorf("branch.Name = %q, want %q", branches[0].Name, "main")
	}
	if !branches[0].IsDefault {
		t.Error("branch.IsDefault = false, want true")
	}

	// ListBranches before init returns ErrRepoNotInitialised.
	mgr2, _, _ := newTestManager(t)
	_, err = mgr2.ListBranches(context.Background())
	if !errors.Is(err, codevaldgit.ErrRepoNotInitialised) {
		t.Errorf("ListBranches before init: got %v, want ErrRepoNotInitialised", err)
	}
}

func TestGitManager_CreateBranch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)

	b, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "feature/xyz"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if b.Name != "feature/xyz" {
		t.Errorf("branch.Name = %q, want %q", b.Name, "feature/xyz")
	}
	if b.ID == "" {
		t.Error("branch.ID is empty")
	}

	// A second branch with the same name returns ErrBranchExists.
	_, err = mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "feature/xyz"})
	if !errors.Is(err, codevaldgit.ErrBranchExists) {
		t.Errorf("duplicate CreateBranch: got %v, want ErrBranchExists", err)
	}
}

func TestGitManager_GetBranch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)

	def := mustDefaultBranch(t, mgr)
	got, err := mgr.GetBranch(ctx, def.ID)
	if err != nil {
		t.Fatalf("GetBranch: %v", err)
	}
	if got.Name != "main" {
		t.Errorf("GetBranch.Name = %q, want %q", got.Name, "main")
	}
	if !got.IsDefault {
		t.Error("GetBranch.IsDefault = false, want true")
	}

	// Non-existent branch returns ErrBranchNotFound.
	_, err = mgr.GetBranch(ctx, "nonexistent-id")
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("GetBranch missing: got %v, want ErrBranchNotFound", err)
	}
}

func TestGitManager_DeleteBranch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)

	b, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "task/delete-me"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	if err := mgr.DeleteBranch(ctx, b.ID); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	// Branch is gone.
	_, err = mgr.GetBranch(ctx, b.ID)
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("GetBranch after delete: got %v, want ErrBranchNotFound", err)
	}

	// Deleting a non-existent branch returns ErrBranchNotFound.
	if err := mgr.DeleteBranch(ctx, "ghost"); !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("DeleteBranch ghost: got %v, want ErrBranchNotFound", err)
	}

	// Cannot delete the default (protected) branch.
	def := mustDefaultBranch(t, mgr)
	err = mgr.DeleteBranch(ctx, def.ID)
	if !errors.Is(err, codevaldgit.ErrDefaultBranchDeleteForbidden) {
		t.Errorf("DeleteBranch default: got %v, want ErrDefaultBranchDeleteForbidden", err)
	}
}

func TestGitManager_WriteFile(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	commit, err := mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID:   def.ID,
		Path:       "README.md",
		Content:    "# Hello",
		AuthorName: "tester",
		Message:    "Add README",
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if commit.SHA == "" {
		t.Error("commit.SHA is empty")
	}
	if commit.Message != "Add README" {
		t.Errorf("commit.Message = %q, want %q", commit.Message, "Add README")
	}
	if commit.ID == "" {
		t.Error("commit.ID is empty")
	}

	// Default message when empty.
	commit2, err := mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID: def.ID,
		Path:     "NOTES.md",
		Content:  "notes",
	})
	if err != nil {
		t.Fatalf("WriteFile (empty message): %v", err)
	}
	if commit2.Message != "Update NOTES.md" {
		t.Errorf("commit.Message = %q, want %q", commit2.Message, "Update NOTES.md")
	}

	// WriteFile on a non-existent branch returns ErrBranchNotFound.
	_, err = mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID: "bad-branch-id",
		Path:     "x.txt",
		Content:  "x",
	})
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("WriteFile bad branch: got %v, want ErrBranchNotFound", err)
	}
}

func TestGitManager_ReadFile(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	mustWriteFile(t, mgr, def.ID, "hello.txt", "hello world")

	blob, err := mgr.ReadFile(ctx, def.ID, "hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if blob.Content != "hello world" {
		t.Errorf("blob.Content = %q, want %q", blob.Content, "hello world")
	}
	if blob.Path != "hello.txt" {
		t.Errorf("blob.Path = %q, want %q", blob.Path, "hello.txt")
	}

	// File that was never written returns ErrFileNotFound.
	_, err = mgr.ReadFile(ctx, def.ID, "missing.txt")
	if !errors.Is(err, codevaldgit.ErrFileNotFound) {
		t.Errorf("ReadFile missing: got %v, want ErrFileNotFound", err)
	}

	// ReadFile on a non-existent branch returns ErrBranchNotFound.
	_, err = mgr.ReadFile(ctx, "bad-branch", "hello.txt")
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("ReadFile bad branch: got %v, want ErrBranchNotFound", err)
	}
}

func TestGitManager_DeleteFile(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	mustWriteFile(t, mgr, def.ID, "file.txt", "content")

	// DeleteFile on an existing file returns a commit and no error.
	delCommit, err := mgr.DeleteFile(ctx, codevaldgit.DeleteFileRequest{
		BranchID:   def.ID,
		Path:       "file.txt",
		AuthorName: "tester",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if delCommit.SHA == "" {
		t.Error("deletion commit.SHA is empty")
	}

	// Attempting to delete a file that was never written returns ErrFileNotFound.
	_, err = mgr.DeleteFile(ctx, codevaldgit.DeleteFileRequest{BranchID: def.ID, Path: "nope.txt"})
	if !errors.Is(err, codevaldgit.ErrFileNotFound) {
		t.Errorf("DeleteFile missing: got %v, want ErrFileNotFound", err)
	}
}

func TestGitManager_ListDirectory(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	// Write one file at the root.
	mustWriteFile(t, mgr, def.ID, "README.md", "# docs")

	// Root listing should include README.md.
	entries, err := mgr.ListDirectory(ctx, def.ID, "")
	if err != nil {
		t.Fatalf("ListDirectory root: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["README.md"] {
		t.Errorf("root listing missing README.md; got entries: %v", entries)
	}

	// Write a file in a subdirectory on a fresh branch.
	sub, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "task/sub"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	mustWriteFile(t, mgr, sub.ID, "src/main.go", "package main")

	// Root listing of the sub branch should show the "src" directory entry.
	rootEntries, err := mgr.ListDirectory(ctx, sub.ID, "")
	if err != nil {
		t.Fatalf("ListDirectory root (sub branch): %v", err)
	}
	rootNames := make(map[string]bool, len(rootEntries))
	for _, e := range rootEntries {
		rootNames[e.Name] = true
	}
	if !rootNames["src"] {
		t.Errorf("root listing of sub branch missing 'src'; got: %v", rootEntries)
	}

	// Subdirectory listing should show main.go.
	subEntries, err := mgr.ListDirectory(ctx, sub.ID, "src")
	if err != nil {
		t.Fatalf("ListDirectory src: %v", err)
	}
	subNames := make(map[string]bool, len(subEntries))
	for _, e := range subEntries {
		subNames[e.Name] = true
	}
	if !subNames["main.go"] {
		t.Errorf("src listing missing main.go; got: %v", subEntries)
	}
}

func TestGitManager_Log(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	messages := []string{"commit A", "commit B", "commit C"}
	for _, msg := range messages {
		_, err := mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
			BranchID: def.ID,
			Path:     "file.txt",
			Content:  msg,
			Message:  msg,
		})
		if err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	entries, err := mgr.Log(ctx, def.ID, codevaldgit.LogFilter{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("Log: got %d entries, want 3", len(entries))
	}
	// Log returns newest-first.
	if entries[0].Message != "commit C" {
		t.Errorf("entries[0].Message = %q, want %q", entries[0].Message, "commit C")
	}
	if entries[2].Message != "commit A" {
		t.Errorf("entries[2].Message = %q, want %q", entries[2].Message, "commit A")
	}

	// Path-filtered log: write a second file path to verify filtering.
	_, err = mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID: def.ID, Path: "other.txt", Content: "x", Message: "other file",
	})
	if err != nil {
		t.Fatalf("WriteFile other: %v", err)
	}

	filtered, err := mgr.Log(ctx, def.ID, codevaldgit.LogFilter{Path: "other.txt"})
	if err != nil {
		t.Fatalf("Log filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("Log filtered: got %d entries, want 1", len(filtered))
	}
	if filtered[0].Message != "other file" {
		t.Errorf("filtered entry.Message = %q, want %q", filtered[0].Message, "other file")
	}

	// Log on a non-existent branch returns ErrBranchNotFound.
	_, err = mgr.Log(ctx, "ghost-branch", codevaldgit.LogFilter{})
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("Log ghost branch: got %v, want ErrBranchNotFound", err)
	}
}

func TestGitManager_MergeBranch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)

	feature, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "task/merge-test"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	mustWriteFile(t, mgr, feature.ID, "feature.txt", "feature content")

	// Merge the feature branch into the default branch.
	updated, err := mgr.MergeBranch(ctx, feature.ID)
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	if !updated.IsDefault {
		t.Error("MergeBranch returned non-default branch")
	}
	if updated.HeadCommitID == "" {
		t.Error("merged default branch has empty HeadCommitID")
	}

	// The file written on the feature branch is now visible on the default branch.
	def := mustDefaultBranch(t, mgr)
	blob, err := mgr.ReadFile(ctx, def.ID, "feature.txt")
	if err != nil {
		t.Fatalf("ReadFile after merge: %v", err)
	}
	if blob.Content != "feature content" {
		t.Errorf("merged blob.Content = %q, want %q", blob.Content, "feature content")
	}

	// Merging a non-existent branch returns ErrBranchNotFound.
	_, err = mgr.MergeBranch(ctx, "ghost-branch")
	if !errors.Is(err, codevaldgit.ErrBranchNotFound) {
		t.Errorf("MergeBranch ghost: got %v, want ErrBranchNotFound", err)
	}
}

func TestGitManager_Tags(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	commit := mustWriteFile(t, mgr, def.ID, "release.txt", "v1.0")

	// CreateTag succeeds.
	tag, err := mgr.CreateTag(ctx, codevaldgit.CreateTagRequest{
		Name:       "v1.0.0",
		CommitID:   commit.ID,
		Message:    "First release",
		TaggerName: "tester",
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if tag.ID == "" {
		t.Error("tag.ID is empty")
	}
	if tag.Name != "v1.0.0" {
		t.Errorf("tag.Name = %q, want %q", tag.Name, "v1.0.0")
	}

	// Duplicate name returns ErrTagAlreadyExists.
	_, err = mgr.CreateTag(ctx, codevaldgit.CreateTagRequest{Name: "v1.0.0", CommitID: commit.ID})
	if !errors.Is(err, codevaldgit.ErrTagAlreadyExists) {
		t.Errorf("duplicate CreateTag: got %v, want ErrTagAlreadyExists", err)
	}

	// GetTag by ID.
	got, err := mgr.GetTag(ctx, tag.ID)
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if got.Name != "v1.0.0" {
		t.Errorf("GetTag.Name = %q, want %q", got.Name, "v1.0.0")
	}

	// GetTag with unknown ID returns ErrTagNotFound.
	_, err = mgr.GetTag(ctx, "bad-id")
	if !errors.Is(err, codevaldgit.ErrTagNotFound) {
		t.Errorf("GetTag missing: got %v, want ErrTagNotFound", err)
	}

	// ListTags returns all tags.
	tags, err := mgr.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("ListTags: got %d, want 1", len(tags))
	}

	// DeleteTag removes the tag.
	if err := mgr.DeleteTag(ctx, tag.ID); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	_, err = mgr.GetTag(ctx, tag.ID)
	if !errors.Is(err, codevaldgit.ErrTagNotFound) {
		t.Errorf("GetTag after delete: got %v, want ErrTagNotFound", err)
	}

	// DeleteTag with unknown ID returns ErrTagNotFound.
	if err := mgr.DeleteTag(ctx, "ghost"); !errors.Is(err, codevaldgit.ErrTagNotFound) {
		t.Errorf("DeleteTag ghost: got %v, want ErrTagNotFound", err)
	}
}

func TestGitManager_Diff(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()
	mustInitRepo(t, mgr)
	def := mustDefaultBranch(t, mgr)

	// Write a file on the default branch.
	mustWriteFile(t, mgr, def.ID, "file-a.txt", "content a")

	// Create a feature branch (inherits main's HEAD which has file-a.txt).
	feature, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{Name: "feature/diff-test"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Write a different file on the feature branch.
	mustWriteFile(t, mgr, feature.ID, "file-b.txt", "content b")

	// Diff from main (has file-a.txt) to feature (has file-b.txt).
	diffs, err := mgr.Diff(ctx, def.ID, feature.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	diffMap := make(map[string]codevaldgit.FileDiff, len(diffs))
	for _, d := range diffs {
		diffMap[d.Path] = d
	}

	// file-b.txt is new in feature → "added".
	if d, ok := diffMap["file-b.txt"]; !ok || d.Operation != "added" {
		t.Errorf("file-b.txt: want Operation=added, got %+v (ok=%v)", diffMap["file-b.txt"], ok)
	}
	// file-a.txt exists in main but not in feature → "deleted".
	if d, ok := diffMap["file-a.txt"]; !ok || d.Operation != "deleted" {
		t.Errorf("file-a.txt: want Operation=deleted, got %+v (ok=%v)", d, ok)
	}
}

func TestGitManager_Publisher(t *testing.T) {
	mgr, _, pub := newTestManager(t)

	_, err := mgr.InitRepo(context.Background(), codevaldgit.CreateRepoRequest{Name: "test-repo"})
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}

	events := pub.published()
	if len(events) != 1 {
		t.Fatalf("published events: got %d, want 1", len(events))
	}
	wantTopic := "cross.git." + testAgencyID + ".repo.created"
	if events[0].topic != wantTopic {
		t.Errorf("event.topic = %q, want %q", events[0].topic, wantTopic)
	}
	if events[0].agencyID != testAgencyID {
		t.Errorf("event.agencyID = %q, want %q", events[0].agencyID, testAgencyID)
	}
}

// ── Import Tests ──────────────────────────────────────────────────────────────

// TestImportRepo_RejectsIfRepoExists verifies that ImportRepo returns
// ErrRepoAlreadyExists when a Repository entity already exists for the agency.
func TestImportRepo_RejectsIfRepoExists(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	ctx := context.Background()

	// Seed a repository so the agency is considered initialised.
	mustInitRepo(t, mgr)

	_, err := mgr.ImportRepo(ctx, codevaldgit.ImportRepoRequest{
		Name:      "import-attempt",
		SourceURL: "https://example.com/repo.git",
	})
	if !errors.Is(err, codevaldgit.ErrRepoAlreadyExists) {
		t.Errorf("ImportRepo after InitRepo: got %v, want ErrRepoAlreadyExists", err)
	}
}

// TestImportRepo_RejectsIfImportInProgress verifies that ImportRepo returns
// ErrImportInProgress when a pending ImportJob entity already exists.
func TestImportRepo_RejectsIfImportInProgress(t *testing.T) {
	mgr, fdm, _ := newTestManager(t)
	ctx := context.Background()

	// Manually create a pending ImportJob entity in the fake store.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: testAgencyID,
		TypeID:   "ImportJob",
		Properties: map[string]any{
			"agency_id":     testAgencyID,
			"source_url":    "https://example.com/first.git",
			"status":        "pending",
			"error_message": "",
			"created_at":    now,
			"updated_at":    now,
		},
	})
	if err != nil {
		t.Fatalf("seed ImportJob: %v", err)
	}

	_, err = mgr.ImportRepo(ctx, codevaldgit.ImportRepoRequest{
		Name:      "second-import",
		SourceURL: "https://example.com/second.git",
	})
	if !errors.Is(err, codevaldgit.ErrImportInProgress) {
		t.Errorf("ImportRepo with existing pending job: got %v, want ErrImportInProgress", err)
	}
}

// TestGetImportStatus_NotFound verifies that GetImportStatus returns
// ErrImportJobNotFound for an unknown job ID.
func TestGetImportStatus_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	_, err := mgr.GetImportStatus(context.Background(), "does-not-exist")
	if !errors.Is(err, codevaldgit.ErrImportJobNotFound) {
		t.Errorf("GetImportStatus unknown ID: got %v, want ErrImportJobNotFound", err)
	}
}

// TestCancelImport_NotFound verifies that CancelImport returns
// ErrImportJobNotFound for an unknown job ID.
func TestCancelImport_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	err := mgr.CancelImport(context.Background(), "does-not-exist")
	if !errors.Is(err, codevaldgit.ErrImportJobNotFound) {
		t.Errorf("CancelImport unknown ID: got %v, want ErrImportJobNotFound", err)
	}
}

// TestCancelImport_TerminalState verifies that CancelImport returns
// ErrImportJobNotCancellable when the job has already reached a terminal state.
func TestCancelImport_TerminalState(t *testing.T) {
	for _, terminalStatus := range []string{"completed", "failed", "cancelled"} {
		t.Run("status="+terminalStatus, func(t *testing.T) {
			mgr, fdm, _ := newTestManager(t)
			ctx := context.Background()

			now := time.Now().UTC().Format(time.RFC3339)
			jobEntity, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
				AgencyID: testAgencyID,
				TypeID:   "ImportJob",
				Properties: map[string]any{
					"agency_id":     testAgencyID,
					"source_url":    "https://example.com/repo.git",
					"status":        terminalStatus,
					"error_message": "",
					"created_at":    now,
					"updated_at":    now,
				},
			})
			if err != nil {
				t.Fatalf("seed ImportJob: %v", err)
			}

			err = mgr.CancelImport(ctx, jobEntity.ID)
			if !errors.Is(err, codevaldgit.ErrImportJobNotCancellable) {
				t.Errorf("CancelImport status=%s: got %v, want ErrImportJobNotCancellable", terminalStatus, err)
			}
		})
	}
}
