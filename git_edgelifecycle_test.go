// git_edgelifecycle_test.go contains unit tests for the GIT-022 edge lifecycle
// hooks:
//
//   - GIT-022a: tagged_with and references edges replicated to the default
//     branch after MergeBranch.
//   - GIT-022b: Branch-scoped edges deleted when DeleteBranch is called
//     without a preceding merge.
//   - GIT-022c: Blob-scoped edges removed when DeleteFile is called.
//
// All storage is provided by the in-memory [fakeDataManager] defined in
// git_manager_test.go — no external dependencies are required.
package codevaldgit_test

import (
	"context"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// countEdgesByBranchID returns the number of relationships with the given name
// and FromID whose "branch_id" property equals targetBranchID.
func countEdgesByBranchID(t *testing.T, dm *fakeDataManager, agencyID, fromID, edgeName, targetBranchID string) int {
	t.Helper()
	ctx := context.Background()
	rels, err := dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: agencyID,
		FromID:   fromID,
		Name:     edgeName,
	})
	if err != nil {
		t.Fatalf("countEdgesByBranchID: ListRelationships: %v", err)
	}
	count := 0
	for _, r := range rels {
		if v, ok := r.Properties["branch_id"]; ok && v == targetBranchID {
			count++
		}
	}
	return count
}

// newTestManagerAndSeedRepo creates a GitManager backed by fakeDataManager,
// initialises a repository, and returns the manager, dm, repoID, and the
// default branchID.
func newTestManagerAndSeedRepo(t *testing.T) (codevaldgit.GitManager, *fakeDataManager, string, string) {
	t.Helper()
	dm := newFakeDataManager()
	sm := &fakeSchemaManager{}
	mgr := codevaldgit.NewGitManager(dm, sm, nil, "test-agency", nil)

	ctx := context.Background()
	repo, err := mgr.InitRepo(ctx, codevaldgit.CreateRepoRequest{Name: "testrepo"})
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}

	branches, err := mgr.ListBranches(ctx, repo.ID)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) == 0 {
		t.Fatal("InitRepo: no branches created")
	}
	return mgr, dm, repo.ID, branches[0].ID
}

// createTaskBranch creates a branch from the default branch of repoID and
// returns its ID.
func createTaskBranch(t *testing.T, mgr codevaldgit.GitManager, repoID, name string) string {
	t.Helper()
	ctx := context.Background()
	branch, err := mgr.CreateBranch(ctx, codevaldgit.CreateBranchRequest{
		RepositoryID: repoID,
		Name:         name,
	})
	if err != nil {
		t.Fatalf("CreateBranch %q: %v", name, err)
	}
	return branch.ID
}

// writeTestFile writes a file on the given branch and returns the resulting
// Blob ID (resolved from the branch's HEAD after the write).
func writeTestFile(t *testing.T, mgr codevaldgit.GitManager, branchID, path, content string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := mgr.WriteFile(ctx, codevaldgit.WriteFileRequest{
		BranchID:   branchID,
		Path:       path,
		Content:    content,
		AuthorName: "test-author",
	}); err != nil {
		t.Fatalf("WriteFile %q on branch %q: %v", path, branchID, err)
	}
	blob, err := mgr.ReadFile(ctx, branchID, path)
	if err != nil {
		t.Fatalf("ReadFile %q after WriteFile: %v", path, err)
	}
	return blob.ID
}

// ── GIT-022a: Replicate edges on MergeBranch ─────────────────────────────────

// TestEdgeLifecycle_022a_TaggedWith verifies that a tagged_with edge created
// on a task branch blob is replicated to the default branch after MergeBranch.
func TestEdgeLifecycle_022a_TaggedWith(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, dm, repoID, defaultBranchID := newTestManagerAndSeedRepo(t)

	taskBranchID := createTaskBranch(t, mgr, repoID, "task-kw-001")

	// Write a file on the task branch.
	blobID := writeTestFile(t, mgr, taskBranchID, "docs/readme.md", "# Hello")

	// Create a keyword.
	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "docs"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}

	// Tag the blob on the task branch.
	if err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         taskBranchID,
		FromEntityID:     blobID,
		ToEntityID:       kw.ID,
		RelationshipName: "tagged_with",
	}); err != nil {
		t.Fatalf("CreateEdge tagged_with: %v", err)
	}

	// Verify edge exists scoped to the task branch.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 1 {
		t.Fatalf("before merge: want 1 tagged_with on task branch, got %d", got)
	}
	// Not yet on default branch.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", defaultBranchID); got != 0 {
		t.Fatalf("before merge: want 0 tagged_with on default branch, got %d", got)
	}

	// Merge.
	if _, err := mgr.MergeBranch(ctx, taskBranchID); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// After merge the edge must be replicated to the default branch.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", defaultBranchID); got != 1 {
		t.Fatalf("after merge: want 1 tagged_with on default branch, got %d", got)
	}
	// Task-branch edge must still be present.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 1 {
		t.Fatalf("after merge: task-branch tagged_with should still exist, got %d", got)
	}
}

// TestEdgeLifecycle_022a_References verifies that a references edge (with
// descriptor property) created on a task branch is replicated to the default
// branch after MergeBranch. The edge is placed on the blob that is in the
// HEAD commit's tree (blobB written last), pointing to blobA.
func TestEdgeLifecycle_022a_References(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, dm, repoID, defaultBranchID := newTestManagerAndSeedRepo(t)

	taskBranchID := createTaskBranch(t, mgr, repoID, "task-ref-001")

	blobA := writeTestFile(t, mgr, taskBranchID, "src/a.go", "package a")
	// blobB is written last and is therefore in the HEAD commit's tree.
	blobB := writeTestFile(t, mgr, taskBranchID, "src/b.go", "package b")

	// Create a references edge FROM blobB (HEAD blob) → blobA.
	if err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         taskBranchID,
		FromEntityID:     blobB,
		ToEntityID:       blobA,
		RelationshipName: "references",
		Properties:       map[string]any{"descriptor": "depends_on"},
	}); err != nil {
		t.Fatalf("CreateEdge references: %v", err)
	}

	if got := countEdgesByBranchID(t, dm, "test-agency", blobB, "references", defaultBranchID); got != 0 {
		t.Fatalf("before merge: want 0 references on default branch, got %d", got)
	}

	if _, err := mgr.MergeBranch(ctx, taskBranchID); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// After merge: check the replicated edge exists and carries the descriptor.
	rels, err := dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: "test-agency",
		FromID:   blobB,
		Name:     "references",
	})
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	var found bool
	for _, r := range rels {
		if v, ok := r.Properties["branch_id"]; ok && v == defaultBranchID {
			found = true
			if d, ok := r.Properties["descriptor"]; !ok || d != "depends_on" {
				t.Errorf("replicated references edge: descriptor = %v, want \"depends_on\"", d)
			}
		}
	}
	if !found {
		t.Fatal("after merge: no references edge found scoped to default branch")
	}
}

// TestEdgeLifecycle_022a_NoEdges verifies that MergeBranch succeeds normally
// when there are no documentation edges on the source branch.
func TestEdgeLifecycle_022a_NoEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, _, repoID, _ := newTestManagerAndSeedRepo(t)

	taskBranchID := createTaskBranch(t, mgr, repoID, "task-noedge-001")
	writeTestFile(t, mgr, taskBranchID, "docs/readme.md", "# Hello")

	// MergeBranch must succeed even with no doc edges.
	if _, err := mgr.MergeBranch(ctx, taskBranchID); err != nil {
		t.Fatalf("MergeBranch (no edges): %v", err)
	}
}

// ── GIT-022b: Delete edges on branch delete ───────────────────────────────────

// TestEdgeLifecycle_022b_DeleteBranchCleansEdges verifies that when a branch
// is deleted without merging, all tagged_with edges scoped to that branch are
// removed from the blobs.
func TestEdgeLifecycle_022b_DeleteBranchCleansEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, dm, repoID, _ := newTestManagerAndSeedRepo(t)

	taskBranchID := createTaskBranch(t, mgr, repoID, "task-del-001")
	blobID := writeTestFile(t, mgr, taskBranchID, "src/main.go", "package main")

	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "service"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}
	if err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         taskBranchID,
		FromEntityID:     blobID,
		ToEntityID:       kw.ID,
		RelationshipName: "tagged_with",
	}); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	// Confirm edge exists before delete.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 1 {
		t.Fatalf("before DeleteBranch: want 1 edge, got %d", got)
	}

	// Delete the branch without merging.
	if err := mgr.DeleteBranch(ctx, taskBranchID); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}

	// Edge must be gone.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 0 {
		t.Fatalf("after DeleteBranch: want 0 edges, got %d", got)
	}
}

// TestEdgeLifecycle_022b_OnlyDeletesScopedEdges verifies that DeleteBranch
// only removes edges scoped to the deleted branch, leaving edges from other
// branches intact.
func TestEdgeLifecycle_022b_OnlyDeletesScopedEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, dm, repoID, defaultBranchID := newTestManagerAndSeedRepo(t)

	// Create two task branches. Write the same file on branchA, merge it to
	// main (replicating edges), then delete branchA. BranchB's edges should
	// remain on the default branch.
	branchAID := createTaskBranch(t, mgr, repoID, "task-scope-a")
	blobID := writeTestFile(t, mgr, branchAID, "src/util.go", "package util")

	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "util"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}
	if err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         branchAID,
		FromEntityID:     blobID,
		ToEntityID:       kw.ID,
		RelationshipName: "tagged_with",
	}); err != nil {
		t.Fatalf("CreateEdge on branchA: %v", err)
	}

	// Merge branchA → default so the edge is replicated.
	if _, err := mgr.MergeBranch(ctx, branchAID); err != nil {
		t.Fatalf("MergeBranch branchA: %v", err)
	}

	// Now delete branchA. Its scoped edge should be removed; the default-branch
	// scoped edge should remain.
	if err := mgr.DeleteBranch(ctx, branchAID); err != nil {
		t.Fatalf("DeleteBranch branchA: %v", err)
	}

	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", branchAID); got != 0 {
		t.Errorf("after delete branchA: want 0 edges on branchA, got %d", got)
	}
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", defaultBranchID); got != 1 {
		t.Errorf("after delete branchA: default-branch edge should survive, got %d", got)
	}
}

// ── GIT-022c: Remove edges on file delete ────────────────────────────────────

// TestEdgeLifecycle_022c_DeleteFileRemovesEdges verifies that when DeleteFile
// is called, all tagged_with edges scoped to that branch on the deleted blob
// are removed before the deletion commit is written.
func TestEdgeLifecycle_022c_DeleteFileRemovesEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, dm, repoID, _ := newTestManagerAndSeedRepo(t)

	taskBranchID := createTaskBranch(t, mgr, repoID, "task-delfile-001")
	blobID := writeTestFile(t, mgr, taskBranchID, "docs/guide.md", "# Guide")

	kw, err := mgr.CreateKeyword(ctx, codevaldgit.CreateKeywordRequest{Name: "guide"})
	if err != nil {
		t.Fatalf("CreateKeyword: %v", err)
	}
	if err := mgr.CreateEdge(ctx, codevaldgit.CreateEdgeRequest{
		BranchID:         taskBranchID,
		FromEntityID:     blobID,
		ToEntityID:       kw.ID,
		RelationshipName: "tagged_with",
	}); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	// Confirm edge exists before delete.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 1 {
		t.Fatalf("before DeleteFile: want 1 edge, got %d", got)
	}

	// Delete the file.
	if _, err := mgr.DeleteFile(ctx, codevaldgit.DeleteFileRequest{
		BranchID:   taskBranchID,
		Path:       "docs/guide.md",
		AuthorName: "test",
	}); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	// Edge on the original blob must be removed.
	if got := countEdgesByBranchID(t, dm, "test-agency", blobID, "tagged_with", taskBranchID); got != 0 {
		t.Fatalf("after DeleteFile: want 0 edges, got %d", got)
	}
}
