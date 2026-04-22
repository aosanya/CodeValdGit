// git_impl_graph_query_test.go — unit tests for GitManager.QueryGraph (GIT-026).
package codevaldgit_test

import (
	"context"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// seedTaggedWith creates a tagged_with relationship directly on fdm so that
// the signal/note/branch_id properties are preserved exactly as syncGitGraph
// would write them.
func seedTaggedWith(t *testing.T, fdm *fakeDataManager, blobID, kwID, signal, branchID string) {
	t.Helper()
	ctx := context.Background()
	_, err := fdm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: testAgencyID,
		Name:     "tagged_with",
		FromID:   blobID,
		ToID:     kwID,
		Properties: map[string]any{
			"signal":    signal,
			"note":      "",
			"branch_id": branchID,
		},
	})
	if err != nil {
		t.Fatalf("seedTaggedWith: %v", err)
	}
}

// listBlobIDs returns the entity IDs of all Blob entities in fdm.
func listBlobIDs(t *testing.T, fdm *fakeDataManager) []string {
	t.Helper()
	ctx := context.Background()
	blobs, err := fdm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: testAgencyID,
		TypeID:   "Blob",
	})
	if err != nil {
		t.Fatalf("listBlobIDs: %v", err)
	}
	ids := make([]string, len(blobs))
	for i, b := range blobs {
		ids[i] = b.ID
	}
	return ids
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestQueryGraph_EmptyBody_ReturnsTaggedBlobs(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "src/auth.go", "package auth")
	mustWriteFile(t, mgr, branch.ID, "src/user.go", "package user")

	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "auth", Scope: "agency"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}
	for _, id := range listBlobIDs(t, fdm) {
		seedTaggedWith(t, fdm, id, kw.ID, "authority", branch.ID)
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{BranchID: branch.ID})
	if err != nil {
		t.Fatalf("QueryGraph: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Error("expected nodes, got empty result")
	}
}

func TestQueryGraph_FileTypeFilter(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "src/main.go", "package main")
	mustWriteFile(t, mgr, branch.ID, "docs/guide.md", "# guide")

	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw1", Scope: "agency"})
	for _, id := range listBlobIDs(t, fdm) {
		seedTaggedWith(t, fdm, id, kw.ID, "surface", branch.ID)
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID:  branch.ID,
		FileTypes: []string{".go"},
	})
	if err != nil {
		t.Fatalf("QueryGraph file-type filter: %v", err)
	}
	for _, n := range result.Nodes {
		path, _ := n.Properties["path"].(string)
		if len(path) < 3 || path[len(path)-3:] != ".go" {
			t.Errorf("unexpected non-.go node path: %q", path)
		}
	}
}

func TestQueryGraph_FolderFilter(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "internal/server.go", "package server")
	mustWriteFile(t, mgr, branch.ID, "cmd/main.go", "package main")

	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw2", Scope: "agency"})
	for _, id := range listBlobIDs(t, fdm) {
		seedTaggedWith(t, fdm, id, kw.ID, "index", branch.ID)
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Folders:  []string{"internal/"},
	})
	if err != nil {
		t.Fatalf("QueryGraph folder filter: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one node in internal/")
	}
	for _, n := range result.Nodes {
		path, _ := n.Properties["path"].(string)
		if len(path) < 9 || path[:9] != "internal/" {
			t.Errorf("unexpected node outside folder: %q", path)
		}
	}
}

func TestQueryGraph_BranchNotFound(t *testing.T) {
	ctx := context.Background()
	mgr, _, _ := newTestManager(t)

	_, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{BranchID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent branch, got nil")
	}
}

func TestQueryGraph_LimitEnforced(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		mustWriteFile(t, mgr, branch.ID, "src/"+name, "package p")
	}

	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw3", Scope: "agency"})
	for _, id := range listBlobIDs(t, fdm) {
		seedTaggedWith(t, fdm, id, kw.ID, "surface", branch.ID)
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("QueryGraph limit: %v", err)
	}
	if len(result.Nodes) > 2 {
		t.Errorf("limit=2: got %d nodes, want ≤2", len(result.Nodes))
	}
}

func TestQueryGraph_SignalFilter(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "high.go", "package p")
	mustWriteFile(t, mgr, branch.ID, "low.go", "package p")

	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw4", Scope: "agency"})

	// Assign signals per path.
	pathSignal := map[string]string{"high.go": "authority", "low.go": "surface"}
	blobsByID := map[string]string{} // id → path
	allBlobs, _ := fdm.ListEntities(ctx, entitygraph.EntityFilter{AgencyID: testAgencyID, TypeID: "Blob"})
	for _, b := range allBlobs {
		path, _ := b.Properties["path"].(string)
		sig := pathSignal[path]
		if sig == "" {
			sig = "surface"
		}
		blobsByID[b.ID] = path
		seedTaggedWith(t, fdm, b.ID, kw.ID, sig, branch.ID)
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Signals:  []string{"authority"},
	})
	if err != nil {
		t.Fatalf("QueryGraph signal filter: %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one authority node")
	}
	for _, n := range result.Nodes {
		path := blobsByID[n.ID]
		if path != "high.go" {
			t.Errorf("signal filter: unexpected node path %q (want high.go only)", path)
		}
	}
}

func TestQueryGraph_KeywordIDFilter(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "a.go", "package a")
	mustWriteFile(t, mgr, branch.ID, "b.go", "package b")

	kwA, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kwA", Scope: "agency"})
	kwB, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kwB", Scope: "agency"})

	allBlobs, _ := fdm.ListEntities(ctx, entitygraph.EntityFilter{AgencyID: testAgencyID, TypeID: "Blob"})
	var aID, bID string
	for _, b := range allBlobs {
		path, _ := b.Properties["path"].(string)
		if path == "a.go" {
			aID = b.ID
		} else if path == "b.go" {
			bID = b.ID
		}
	}
	seedTaggedWith(t, fdm, aID, kwA.ID, "authority", branch.ID)
	seedTaggedWith(t, fdm, bID, kwB.ID, "authority", branch.ID)

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID:   branch.ID,
		KeywordIDs: []string{kwA.ID},
	})
	if err != nil {
		t.Fatalf("QueryGraph keyword filter: %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].ID != aID {
		t.Errorf("keyword filter: want node %s only, got %v", aID, result.Nodes)
	}
}

func TestQueryGraph_EdgesOnlyBetweenReturnedNodes(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "x.go", "package x")
	mustWriteFile(t, mgr, branch.ID, "y.go", "package y")

	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kwE", Scope: "agency"})
	allBlobs, _ := fdm.ListEntities(ctx, entitygraph.EntityFilter{AgencyID: testAgencyID, TypeID: "Blob"})
	var xID, yID string
	for _, b := range allBlobs {
		path, _ := b.Properties["path"].(string)
		if path == "x.go" {
			xID = b.ID
		} else if path == "y.go" {
			yID = b.ID
		}
		seedTaggedWith(t, fdm, b.ID, kw.ID, "surface", branch.ID)
	}
	// Add a blob→blob references edge.
	fdm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{ //nolint:errcheck
		AgencyID: testAgencyID,
		Name:     "references",
		FromID:   xID,
		ToID:     yID,
		Properties: map[string]any{
			"descriptor": "depends_on",
			"branch_id":  branch.ID,
		},
	})

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{BranchID: branch.ID})
	if err != nil {
		t.Fatalf("QueryGraph edges test: %v", err)
	}
	nodeSet := make(map[string]bool)
	for _, n := range result.Nodes {
		nodeSet[n.ID] = true
	}
	for _, e := range result.Edges {
		if !nodeSet[e.FromID] || !nodeSet[e.ToID] {
			t.Errorf("edge %s references nodes outside result set", e.ID)
		}
	}
}
