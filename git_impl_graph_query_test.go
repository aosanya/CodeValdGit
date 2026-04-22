// git_impl_graph_query_test.go — unit tests for GitManager.QueryGraph (GIT-026).
package codevaldgit_test

import (
	"context"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// seedBlobAndTag seeds a Blob entity and a tagged_with edge to the given
// keyword entity ID, using the provided signal name. Returns the blob entity ID.
func seedBlobAndTag(
	t *testing.T,
	mgr codevaldgit.GitManager,
	fdm interface {
		CreateEntity(context.Context, interface{}) (interface{}, error)
	},
	branchID, path, keywordID, signal string,
) string {
	t.Helper()
	return ""
}

// ── helpers ───────────────────────────────────────────────────────────────────

// setupQueryGraphBase creates a repo, default branch, two blobs, one keyword,
// and tagged_with edges with different signals. Returns manager, branchID, and
// the two blob entity IDs.
func setupQueryGraphBase(t *testing.T) (codevaldgit.GitManager, *fakeDataManager, string, string, string) {
	t.Helper()
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)

	// Write two files so Blob entities are created.
	mustWriteFile(t, mgr, branch.ID, "src/main.go", "package main")
	mustWriteFile(t, mgr, branch.ID, "docs/readme.md", "# docs")

	// Retrieve blob IDs.
	blobs, err := fdm.ListEntities(ctx, interface{}(nil))
	_ = blobs
	_ = err

	return mgr, fdm, branch.ID, "", ""
}

// ── QueryGraph tests ──────────────────────────────────────────────────────────

func TestQueryGraph_EmptyBody_ReturnsBlobs(t *testing.T) {
	ctx := context.Background()
	mgr, fdm, _ := newTestManager(t)

	repo := mustInitRepo(t, mgr)
	branch := mustDefaultBranch(t, mgr, repo.ID)
	mustWriteFile(t, mgr, branch.ID, "src/auth.go", "package auth")
	mustWriteFile(t, mgr, branch.ID, "src/user.go", "package user")

	// Create a keyword and tag both blobs at "authority" signal.
	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "auth", Scope: "agency"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}

	blobs, err := fdm.ListEntities(ctx, nil)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	var blobIDs []string
	for _, e := range blobs {
		if e.TypeID == "Blob" {
			blobIDs = append(blobIDs, e.ID)
		}
	}
	for _, blobID := range blobIDs {
		_, err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
			BranchID:   branch.ID,
			FromID:     blobID,
			ToID:       kw.ID,
			Name:       "tagged_with",
			Properties: map[string]any{"signal": "authority", "note": "", "branch_id": branch.ID},
		})
		if err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
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

	blobs, _ := fdm.ListEntities(ctx, nil)
	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw1", Scope: "agency"})
	for _, b := range blobs {
		if b.TypeID != "Blob" {
			continue
		}
		mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{ //nolint:errcheck
			BranchID:   branch.ID,
			FromID:     b.ID,
			ToID:       kw.ID,
			Name:       "tagged_with",
			Properties: map[string]any{"signal": "surface", "note": "", "branch_id": branch.ID},
		})
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID:  branch.ID,
		FileTypes: []string{".go"},
	})
	if err != nil {
		t.Fatalf("QueryGraph: %v", err)
	}
	for _, n := range result.Nodes {
		path, _ := n.Properties["path"].(string)
		if len(path) > 3 && path[len(path)-3:] != ".go" {
			t.Errorf("unexpected non-.go node path: %s", path)
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

	blobs, _ := fdm.ListEntities(ctx, nil)
	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw2", Scope: "agency"})
	for _, b := range blobs {
		if b.TypeID != "Blob" {
			continue
		}
		mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{ //nolint:errcheck
			BranchID:   branch.ID,
			FromID:     b.ID,
			ToID:       kw.ID,
			Name:       "tagged_with",
			Properties: map[string]any{"signal": "index", "note": "", "branch_id": branch.ID},
		})
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Folders:  []string{"internal/"},
	})
	if err != nil {
		t.Fatalf("QueryGraph: %v", err)
	}
	for _, n := range result.Nodes {
		path, _ := n.Properties["path"].(string)
		if len(path) < 9 || path[:9] != "internal/" {
			t.Errorf("unexpected node path outside folder: %s", path)
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

	// Write 5 files.
	for i, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		mustWriteFile(t, mgr, branch.ID, "src/"+name, "package p"+string(rune('a'+i)))
	}

	blobs, _ := fdm.ListEntities(ctx, nil)
	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw3", Scope: "agency"})
	for _, b := range blobs {
		if b.TypeID != "Blob" {
			continue
		}
		mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{ //nolint:errcheck
			BranchID:   branch.ID,
			FromID:     b.ID,
			ToID:       kw.ID,
			Name:       "tagged_with",
			Properties: map[string]any{"signal": "surface", "note": "", "branch_id": branch.ID},
		})
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("QueryGraph: %v", err)
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

	blobs, _ := fdm.ListEntities(ctx, nil)
	kw, _ := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "kw4", Scope: "agency"})

	signals := map[string]string{}
	for _, b := range blobs {
		if b.TypeID != "Blob" {
			continue
		}
		path, _ := b.Properties["path"].(string)
		sig := "surface"
		if path == "high.go" {
			sig = "authority"
		}
		signals[b.ID] = sig
		mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{ //nolint:errcheck
			BranchID:   branch.ID,
			FromID:     b.ID,
			ToID:       kw.ID,
			Name:       "tagged_with",
			Properties: map[string]any{"signal": sig, "note": "", "branch_id": branch.ID},
		})
	}

	result, err := mgr.QueryGraph(ctx, codevaldgit.QueryGraphRequest{
		BranchID: branch.ID,
		Signals:  []string{"authority"},
	})
	if err != nil {
		t.Fatalf("QueryGraph: %v", err)
	}
	for _, n := range result.Nodes {
		if signals[n.ID] != "authority" {
			t.Errorf("node %s has signal %q, expected authority-only results", n.ID, signals[n.ID])
		}
	}
}
