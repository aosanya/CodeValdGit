// git_lazyimport_test.go — GIT-023h: unit tests for Lazy Import v2.
//
//   - FetchBranch idempotency (ErrBranchAlreadyFetched for status=fetched/fetching)
//   - FetchBranch on stub branch: returns job, transitions Branch to "fetching"
//   - ReadFile fast path (content already cached in entity graph)
//   - ReadFile lazy path: ErrBlobContentUnavailable when bare clone absent
//   - ImportRepo: stub branches created in < 10 s from a local git repo
//   - GetFetchBranchStatus: ErrImportJobNotFound for unknown job ID
package codevaldgit_test

import (
"context"
"errors"
"testing"
"time"

gogit "github.com/go-git/go-git/v5"
gogitconfig "github.com/go-git/go-git/v5/config"
gogitplumbing "github.com/go-git/go-git/v5/plumbing"
"github.com/go-git/go-git/v5/plumbing/object"
gogitosfs "github.com/go-git/go-billy/v5/osfs"

codevaldgit "github.com/aosanya/CodeValdGit"
"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// seedStubBranch creates Repository and Branch entities in fdm that mirror the
// shape written by runImport. The Branch entity carries the given status.
// bareClonePath is stored on the Repository entity (empty string is fine for
// tests that do not exercise the bare clone).
func seedStubBranch(
t testing.TB,
ctx context.Context,
fdm *fakeDataManager,
agencyID, status, bareClonePath string,
) (repoID, branchID string) {
t.Helper()
now := time.Now().UTC().Format(time.RFC3339)

repoEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: agencyID,
TypeID:   "Repository",
Properties: map[string]any{
"name":            "test-repo",
"default_branch":  "main",
"bare_clone_path": bareClonePath,
"source_url":      "file:///nonexistent",
"created_at":      now,
"updated_at":      now,
},
})
if err != nil {
t.Fatalf("seedStubBranch: create Repository: %v", err)
}

branchEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: agencyID,
TypeID:   "Branch",
Properties: map[string]any{
"name":            "main",
"status":          status,
"head_commit_sha": "0000000000000000000000000000000000000000",
"source_url":      "file:///nonexistent",
"created_at":      now,
"updated_at":      now,
},
Relationships: []entitygraph.EntityRelationshipRequest{
{Name: "belongs_to_repository", ToID: repoEnt.ID},
},
})
if err != nil {
t.Fatalf("seedStubBranch: create Branch: %v", err)
}

return repoEnt.ID, branchEnt.ID
}

// --- FetchBranch idempotency -------------------------------------------------

// TestFetchBranch_AlreadyFetched verifies that FetchBranch returns
// ErrBranchAlreadyFetched when the branch status is "fetched".
func TestFetchBranch_AlreadyFetched(t *testing.T) {
mgr, fdm, _ := newTestManager(t)
ctx := context.Background()
repoID, branchID := seedStubBranch(t, ctx, fdm, testAgencyID, "fetched", "")

_, err := mgr.FetchBranch(ctx, codevaldgit.FetchBranchRequest{
RepoID:   repoID,
BranchID: branchID,
})
if !errors.Is(err, codevaldgit.ErrBranchAlreadyFetched) {
t.Errorf("FetchBranch(status=fetched): got %v, want ErrBranchAlreadyFetched", err)
}
}

// TestFetchBranch_AlreadyFetching verifies that FetchBranch returns
// ErrBranchAlreadyFetched when the branch status is "fetching".
func TestFetchBranch_AlreadyFetching(t *testing.T) {
mgr, fdm, _ := newTestManager(t)
ctx := context.Background()
repoID, branchID := seedStubBranch(t, ctx, fdm, testAgencyID, "fetching", "")

_, err := mgr.FetchBranch(ctx, codevaldgit.FetchBranchRequest{
RepoID:   repoID,
BranchID: branchID,
})
if !errors.Is(err, codevaldgit.ErrBranchAlreadyFetched) {
t.Errorf("FetchBranch(status=fetching): got %v, want ErrBranchAlreadyFetched", err)
}
}

// TestFetchBranch_StubTransitionsToFetching verifies that calling FetchBranch
// on a "stub" branch:
//   - returns a FetchBranchJob with a non-empty ID
//   - immediately transitions the Branch entity status to "fetching"
//
// The background goroutine will fail (invalid source URL) but the synchronous
// return is what this test asserts.
func TestFetchBranch_StubTransitionsToFetching(t *testing.T) {
mgr, fdm, _ := newTestManager(t)
ctx := context.Background()
repoID, branchID := seedStubBranch(t, ctx, fdm, testAgencyID, "stub", "")

job, err := mgr.FetchBranch(ctx, codevaldgit.FetchBranchRequest{
RepoID:   repoID,
BranchID: branchID,
})
if err != nil {
t.Fatalf("FetchBranch(stub): unexpected error: %v", err)
}
if job.ID == "" {
t.Error("FetchBranch: returned job has empty ID")
}

// Branch entity should now have status "fetching".
branchEnt, err := fdm.GetEntity(ctx, testAgencyID, branchID)
if err != nil {
t.Fatalf("GetEntity(branch): %v", err)
}
gotStatus, _ := branchEnt.Properties["status"].(string)
if gotStatus != "fetching" {
t.Errorf("Branch status = %q, want fetching", gotStatus)
}
}

// --- ReadFile: cached content fast path --------------------------------------

// TestReadFile_CachedBlob verifies that ReadFile returns content directly from
// the entity graph (fast path) when the Blob entity already has content.
func TestReadFile_CachedBlob(t *testing.T) {
mgr, _, _ := newTestManager(t)
ctx := context.Background()

repo := mustInitRepo(t, mgr)
def := mustDefaultBranch(t, mgr, repo.ID)
mustWriteFile(t, mgr, def.ID, "cached.txt", "hello from cache")

blob, err := mgr.ReadFile(ctx, def.ID, "cached.txt")
if err != nil {
t.Fatalf("ReadFile cached: %v", err)
}
if blob.Content != "hello from cache" {
t.Errorf("Content = %q, want %q", blob.Content, "hello from cache")
}
if blob.Path != "cached.txt" {
t.Errorf("Path = %q, want cached.txt", blob.Path)
}
}

// --- ReadFile: lazy path, bare clone absent ----------------------------------

// TestReadFile_LazyLoad_NoBareClone verifies that ReadFile returns
// ErrBlobContentUnavailable when:
//   - the Blob entity exists (metadata only, content field empty)
//   - the Repository bare_clone_path does not exist on disk
func TestReadFile_LazyLoad_NoBareClone(t *testing.T) {
mgr, fdm, _ := newTestManager(t)
ctx := context.Background()

now := time.Now().UTC().Format(time.RFC3339)

// Repository with a non-existent bare_clone_path.
repoEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: testAgencyID,
TypeID:   "Repository",
Properties: map[string]any{
"name":            "lazy-repo",
"default_branch":  "main",
"bare_clone_path": "/this/path/does/not/exist/on/disk",
"source_url":      "file:///nonexistent",
"created_at":      now,
"updated_at":      now,
},
})
if err != nil {
t.Fatalf("create Repository: %v", err)
}

branchEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: testAgencyID,
TypeID:   "Branch",
Properties: map[string]any{
"name":       "main",
"status":     "fetched",
"is_default": true,
"created_at": now,
"updated_at": now,
},
Relationships: []entitygraph.EntityRelationshipRequest{
{Name: "belongs_to_repository", ToID: repoEnt.ID},
},
})
if err != nil {
t.Fatalf("create Branch: %v", err)
}

// Blob with empty content (stub blob written by FetchBranch metadata pass).
blobEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: testAgencyID,
TypeID:   "Blob",
Properties: map[string]any{
"sha":        "dddddddddddddddddddddddddddddddddddddddd",
"path":       "file.txt",
"name":       "file.txt",
"extension":  "txt",
"size":       int64(10),
"encoding":   "utf-8",
"content":    "",
"created_at": now,
},
})
if err != nil {
t.Fatalf("create Blob: %v", err)
}

treeEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: testAgencyID,
TypeID:   "Tree",
Properties: map[string]any{
"sha":        "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
"path":       "",
"created_at": now,
},
Relationships: []entitygraph.EntityRelationshipRequest{
{Name: "has_blob", ToID: blobEnt.ID},
},
})
if err != nil {
t.Fatalf("create Tree: %v", err)
}

commitEnt, err := fdm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
AgencyID: testAgencyID,
TypeID:   "Commit",
Properties: map[string]any{
"sha":        "ffffffffffffffffffffffffffffffffffffffff",
"message":    "stub commit",
"author":     "tester",
"created_at": now,
},
Relationships: []entitygraph.EntityRelationshipRequest{
{Name: "has_tree", ToID: treeEnt.ID},
},
})
if err != nil {
t.Fatalf("create Commit: %v", err)
}

if _, err := fdm.UpdateEntity(ctx, testAgencyID, branchEnt.ID, entitygraph.UpdateEntityRequest{
Properties: map[string]any{"head_commit_id": commitEnt.ID},
}); err != nil {
t.Fatalf("update branch HEAD: %v", err)
}
if _, err := fdm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
AgencyID: testAgencyID,
Name:     "belongs_to_branch",
FromID:   commitEnt.ID,
ToID:     branchEnt.ID,
}); err != nil {
t.Fatalf("create belongs_to_branch: %v", err)
}

_, err = mgr.ReadFile(ctx, branchEnt.ID, "file.txt")
if !errors.Is(err, codevaldgit.ErrBlobContentUnavailable) {
t.Errorf("ReadFile (no bare clone): got %v, want ErrBlobContentUnavailable", err)
}
}

// --- Stub import timing -------------------------------------------------------

// makeLocalGitSource creates a non-bare git repository in a temp directory with
// one commit (README.md). Returns the directory path which go-git can clone via
// local path (no file:// prefix needed).
func makeLocalGitSource(t *testing.T) string {
t.Helper()
srcDir := t.TempDir()

repo, err := gogit.PlainInit(srcDir, false)
if err != nil {
t.Fatalf("PlainInit: %v", err)
}
wt, err := repo.Worktree()
if err != nil {
t.Fatalf("Worktree: %v", err)
}

// Write README using the osfs-backed worktree filesystem.
srcFS := gogitosfs.New(srcDir)
f, err := srcFS.Create("README.md")
if err != nil {
t.Fatalf("create README.md: %v", err)
}
if _, err := f.Write([]byte("# test")); err != nil {
_ = f.Close()
t.Fatalf("write README.md: %v", err)
}
_ = f.Close()

if _, err := wt.Add("README.md"); err != nil {
t.Fatalf("git add: %v", err)
}
if _, err := wt.Commit("Initial commit", &gogit.CommitOptions{
Author: &object.Signature{Name: "tester", Email: "t@t.com", When: time.Now()},
}); err != nil {
t.Fatalf("commit: %v", err)
}

// Create a bare remote and push to it so go-git shallow clone works.
bareDir := t.TempDir()
bareRepo, err := gogit.PlainInit(bareDir, true)
if err != nil {
t.Fatalf("bare PlainInit: %v", err)
}
_, err = bareRepo.CreateRemote(&gogitconfig.RemoteConfig{
Name: "origin",
URLs: []string{srcDir},
})
if err != nil {
t.Fatalf("CreateRemote: %v", err)
}
if err := bareRepo.Fetch(&gogit.FetchOptions{
RemoteName: "origin",
RefSpecs:   []gogitconfig.RefSpec{"+refs/heads/*:refs/heads/*"},
}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
t.Fatalf("Fetch into bare: %v", err)
}

return bareDir
}

// TestImportRepo_StubBranches_Fast verifies that:
//  1. ImportRepo on a local git repository completes within 10 seconds.
//  2. Only Branch entities with status="stub" are written — no Commit, Tree,
//     or Blob entities are created during the Phase-1 import.
//  3. A pub/sub event is published for the agency.
func TestImportRepo_StubBranches_Fast(t *testing.T) {
bareDir := makeLocalGitSource(t)
mgr, fdm, pub := newTestManager(t)
ctx := context.Background()

start := time.Now()
job, err := mgr.ImportRepo(ctx, codevaldgit.ImportRepoRequest{
Name:      "stub-test",
SourceURL: bareDir,
})
if err != nil {
t.Fatalf("ImportRepo: %v", err)
}
if job.ID == "" {
t.Fatal("ImportRepo returned empty job ID")
}

// Poll until the job reaches a terminal state.
deadline := time.After(10 * time.Second)
var final codevaldgit.ImportJob
poll:
for {
select {
case <-deadline:
t.Fatalf("ImportRepo did not complete within 10 s (last status: %q)", final.Status)
case <-time.After(50 * time.Millisecond):
j, err := mgr.GetImportStatus(ctx, job.ID)
if err != nil {
t.Fatalf("GetImportStatus: %v", err)
}
final = j
if j.Status == "completed" || j.Status == "failed" {
break poll
}
}
}

elapsed := time.Since(start)
if final.Status != "completed" {
t.Errorf("Import status = %q (err: %q), want completed", final.Status, final.ErrorMessage)
}
if elapsed >= 10*time.Second {
t.Errorf("ImportRepo took %v, want < 10 s", elapsed)
}

// At least one Branch entity should exist with status="stub".
branches, _ := fdm.ListEntities(ctx, entitygraph.EntityFilter{
AgencyID: testAgencyID,
TypeID:   "Branch",
})
if len(branches) == 0 {
t.Error("no Branch entities created by ImportRepo")
}
for _, b := range branches {
s, _ := b.Properties["status"].(string)
if s != "stub" {
t.Errorf("Branch %q has status=%q, want stub", b.ID, s)
}
}

// No heavy objects (Commit, Tree, Blob) should exist after Phase-1 import.
for _, typeID := range []string{"Commit", "Tree", "Blob"} {
got, _ := fdm.ListEntities(ctx, entitygraph.EntityFilter{
AgencyID: testAgencyID,
TypeID:   typeID,
})
if len(got) > 0 {
t.Errorf("%d %s entities found after stub import (want 0)", len(got), typeID)
}
}

// A pub/sub event should have been published.
events := pub.published()
var found bool
for _, ev := range events {
if ev.agencyID == testAgencyID {
found = true
break
}
}
if !found {
t.Error("no pub/sub event published for the agency after ImportRepo")
}
}

// --- GetFetchBranchStatus: not found -----------------------------------------

// TestGetFetchBranchStatus_NotFound verifies that GetFetchBranchStatus returns
// ErrImportJobNotFound for an unknown job ID.
func TestGetFetchBranchStatus_NotFound(t *testing.T) {
mgr, _, _ := newTestManager(t)
_, err := mgr.GetFetchBranchStatus(context.Background(), "nonexistent-job-id")
if !errors.Is(err, codevaldgit.ErrImportJobNotFound) {
t.Errorf("GetFetchBranchStatus unknown ID: got %v, want ErrImportJobNotFound", err)
}
}

// suppress unused-import lint errors for identifiers used only in composite
// literals that the compiler still considers "unused" in some toolchain
// versions.
var _ = gogitplumbing.NewHash
