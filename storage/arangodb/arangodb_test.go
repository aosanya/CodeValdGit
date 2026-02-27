package arangodb_test

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver"
	driverhttp "github.com/arangodb/go-driver/http"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gitindex "github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

// openTestDB connects to the ArangoDB instance at GIT_ARANGO_ENDPOINT
// (default http://localhost:8529) and opens GIT_ARANGO_DATABASE_TEST
// (default codevald_tests). Skips the test if the server is unreachable.
func openTestDB(t *testing.T) driver.Database {
	t.Helper()
	url := os.Getenv("GIT_ARANGO_ENDPOINT")
	if url == "" {
		url = "http://localhost:8529"
	}

	conn, err := driverhttp.NewConnection(driverhttp.ConnectionConfig{
		Endpoints: []string{url},
	})
	if err != nil {
		t.Skipf("ArangoDB connection config error (GIT_ARANGO_ENDPOINT=%s): %v", url, err)
	}

	user := os.Getenv("GIT_ARANGO_USER")
	if user == "" {
		user = "root"
	}
	pass := os.Getenv("GIT_ARANGO_PASSWORD")

	cl, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(user, pass),
	})
	if err != nil {
		t.Skipf("ArangoDB client error: %v", err)
	}

	// Quick ping — skip if unreachable (CI without ArangoDB).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cl.Version(ctx); err != nil {
		t.Skipf("ArangoDB unreachable at %s: %v", url, err)
	}

	dbName := os.Getenv("GIT_ARANGO_DATABASE_TEST")
	if dbName == "" {
		dbName = "codevald_tests"
	}
	ctx2 := context.Background()
	exists, err := cl.DatabaseExists(ctx2, dbName)
	if err != nil {
		t.Fatalf("DatabaseExists: %v", err)
	}
	var db driver.Database
	if exists {
		db, err = cl.Database(ctx2, dbName)
	} else {
		db, err = cl.CreateDatabase(ctx2, dbName, nil)
	}
	if err != nil {
		t.Fatalf("open/create test database %q: %v", dbName, err)
	}
	return db
}

// uniqueAgency returns a unique agency ID for test isolation.
func uniqueAgency(prefix string) string {
	return prefix + "-" + time.Now().Format("20060102T150405.000000")
}

// newTestBackend creates an ArangoBackend wrapping an already-open database.
func newTestBackend(t *testing.T, db driver.Database) codevaldgit.Backend {
	t.Helper()
	b, err := arangodb.NewArangoBackendFromDB(db)
	if err != nil {
		t.Fatalf("NewArangoBackendFromDB: %v", err)
	}
	return b
}

// ─── Backend-level tests ───────────────────────────────────────────────────────

// TestArangoBackend_NilDatabase verifies that NewArangoBackend returns an error
// when Database is nil (does not require a live server).
func TestArangoBackend_NilDatabase(t *testing.T) {
	_, err := arangodb.NewArangoBackendFromDB(nil)
	if err == nil {
		t.Fatal("expected error for nil database, got nil")
	}
}

// TestArangoStorage_SetGet stores a blob and retrieves it by hash.
func TestArangoStorage_SetGet(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("setget")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	// Create a blob manually and store it.
	obj := s.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, _ := obj.Writer()
	w.Write([]byte("hello arango"))
	w.Close()

	hash, err := s.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("SetEncodedObject: %v", err)
	}

	got, err := s.EncodedObject(plumbing.BlobObject, hash)
	if err != nil {
		t.Fatalf("EncodedObject: %v", err)
	}
	r, _ := got.Reader()
	defer r.Close()
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "hello arango" {
		t.Errorf("content = %q, want %q", string(buf[:n]), "hello arango")
	}
}

// TestArangoStorage_Idempotent verifies that storing the same object twice
// returns the same hash without error.
func TestArangoStorage_Idempotent(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("idempotent")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	obj := s.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, _ := obj.Writer()
	w.Write([]byte("idempotent content"))
	w.Close()

	h1, err := s.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("first SetEncodedObject: %v", err)
	}
	h2, err := s.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("second SetEncodedObject: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hashes differ: %s != %s", h1, h2)
	}
}

// TestArangoStorage_RefLifecycle exercises set / read / update / remove on a ref.
func TestArangoStorage_RefLifecycle(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("reflife")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	hash1 := plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	hash2 := plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	name := plumbing.NewBranchReferenceName("test-lifecycle")

	// Set.
	ref1 := plumbing.NewHashReference(name, hash1)
	if err := s.SetReference(ref1); err != nil {
		t.Fatalf("SetReference: %v", err)
	}
	got, err := s.Reference(name)
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if got.Hash() != hash1 {
		t.Errorf("hash = %s, want %s", got.Hash(), hash1)
	}

	// Update.
	ref2 := plumbing.NewHashReference(name, hash2)
	if err := s.SetReference(ref2); err != nil {
		t.Fatalf("SetReference (update): %v", err)
	}
	got2, err := s.Reference(name)
	if err != nil {
		t.Fatalf("Reference after update: %v", err)
	}
	if got2.Hash() != hash2 {
		t.Errorf("updated hash = %s, want %s", got2.Hash(), hash2)
	}

	// Remove.
	if err := s.RemoveReference(name); err != nil {
		t.Fatalf("RemoveReference: %v", err)
	}
	if _, err := s.Reference(name); err != plumbing.ErrReferenceNotFound {
		t.Errorf("after remove: got %v, want ErrReferenceNotFound", err)
	}
}

// TestArangoStorage_Index writes and reads the staging index.
func TestArangoStorage_Index(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("index")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	// Empty index on fresh repo.
	idx0, err := s.Index()
	if err != nil {
		t.Fatalf("Index (empty): %v", err)
	}
	if len(idx0.Entries) != 0 {
		t.Errorf("expected empty index, got %d entries", len(idx0.Entries))
	}

	// Write an index with one entry.
	idx1 := &gitindex.Index{Version: 2}
	idx1.Entries = append(idx1.Entries, &gitindex.Entry{
		Name: "README.md",
		Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
	})
	if err := s.SetIndex(idx1); err != nil {
		t.Fatalf("SetIndex: %v", err)
	}

	got, err := s.Index()
	if err != nil {
		t.Fatalf("Index (after set): %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].Name != "README.md" {
		t.Errorf("entry name = %q, want %q", got.Entries[0].Name, "README.md")
	}
}

// TestArangoStorage_Config writes and reads the repo config.
func TestArangoStorage_Config(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("config")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	cfg := config.NewConfig()
	cfg.Core.IsBare = false
	if err := s.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	got, err := s.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.Core.IsBare != false {
		t.Error("Config.Core.IsBare should be false")
	}
}

// TestArangoStorage_Concurrent writes 10 different objects concurrently and
// verifies all are retrievable.
func TestArangoStorage_Concurrent(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("concurrent")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, _, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	const n = 10
	hashes := make([]plumbing.Hash, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			obj := s.NewEncodedObject()
			obj.SetType(plumbing.BlobObject)
			w, _ := obj.Writer()
			w.Write([]byte("content-" + string(rune('a'+i))))
			w.Close()
			hashes[i], errs[i] = s.SetEncodedObject(obj)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	for i, h := range hashes {
		if _, err := s.EncodedObject(plumbing.BlobObject, h); err != nil {
			t.Errorf("object %d (%s) not found: %v", i, h, err)
		}
	}
}

// TestArangoBackend_FullWorkflow runs a complete git workflow (init, branch,
// commit, merge) through the ArangoDB backend to validate it end-to-end.
func TestArangoBackend_FullWorkflow(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("fullwf")
	b := newTestBackend(t, db)
	ctx := context.Background()

	// Init.
	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	t.Cleanup(func() { b.PurgeRepo(context.Background(), agency) })

	s, wt, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}

	r, err := gogit.Open(s, wt)
	if err != nil {
		t.Fatalf("git.Open: %v", err)
	}

	// Create a task branch.
	headRef, err := r.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	taskBranch := plumbing.NewBranchReferenceName("task/wf-001")
	taskRef := plumbing.NewHashReference(taskBranch, headRef.Hash())
	if err := r.Storer.SetReference(taskRef); err != nil {
		t.Fatalf("SetReference task branch: %v", err)
	}

	// Commit a file on the task branch via worktree checkout.
	worktree, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := worktree.Checkout(&gogit.CheckoutOptions{
		Branch: taskBranch,
		Create: false,
	}); err != nil {
		t.Fatalf("Checkout task branch: %v", err)
	}

	f, err := wt.Create("output.md")
	if err != nil {
		t.Fatalf("Create file: %v", err)
	}
	f.Write([]byte("# Output"))
	f.Close()

	if _, err := worktree.Add("output.md"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	commitHash, err := worktree.Commit("add output.md", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "agent-1",
			Email: "agent@codevaldgit",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify the commit exists.
	commit, err := r.CommitObject(commitHash)
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if commit.Message != "add output.md" {
		t.Errorf("commit message = %q, want %q", commit.Message, "add output.md")
	}

	// Fast-forward main to the task branch commit.
	mainRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), commitHash)
	if err := r.Storer.SetReference(mainRef); err != nil {
		t.Fatalf("fast-forward main: %v", err)
	}

	// Verify main now points to the commit.
	main, err := r.Storer.Reference(plumbing.NewBranchReferenceName("main"))
	if err != nil {
		t.Fatalf("Reference(main): %v", err)
	}
	if main.Hash() != commitHash {
		t.Errorf("main hash = %s, want %s", main.Hash(), commitHash)
	}
}

// TestArangoStorage_ConnectionError verifies that using a backend constructed
// with a nil database returns errors, not panics.
func TestArangoStorage_ConnectionError(t *testing.T) {
	_, err := arangodb.NewArangoBackendFromDB(nil)
	if err == nil {
		t.Fatal("expected error for nil database, got nil")
	}
	// Should describe the problem clearly.
	if err.Error() == "" {
		t.Error("error message is empty")
	}
}

// TestArangoBackend_DeleteAndPurge exercises the DeleteRepo/PurgeRepo lifecycle.
func TestArangoBackend_DeleteAndPurge(t *testing.T) {
	db := openTestDB(t)
	agency := uniqueAgency("delpurge")
	b := newTestBackend(t, db)
	ctx := context.Background()

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}

	// DeleteRepo — must succeed.
	if err := b.DeleteRepo(ctx, agency); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}

	// PurgeRepo — must succeed (purges even soft-deleted docs).
	if err := b.PurgeRepo(ctx, agency); err != nil {
		t.Fatalf("PurgeRepo: %v", err)
	}

	// After purge, OpenStorer must return ErrRepoNotFound.
	_, _, err := b.OpenStorer(ctx, agency)
	if err == nil {
		t.Fatal("expected ErrRepoNotFound after purge, got nil")
	}
}

// Ensure driverhttp import doesn't get stripped.
var _ = http.DefaultClient
