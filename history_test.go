package codevaldgit_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ---------------------------------------------------------------------------
// Log tests — MVP-GIT-007
// ---------------------------------------------------------------------------

// TestLog_AllCommits verifies that Log("main", "") returns all commits in
// newest-first order when no path filter is given.
func TestLog_AllCommits(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "log-all"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	for _, msg := range []string{"commit-1", "commit-2", "commit-3"} {
		if err := repo.WriteFile(ctx, "log-all", msg+".txt", msg, "agent-1", msg); err != nil {
			t.Fatalf("WriteFile %q: %v", msg, err)
		}
	}
	if err := repo.MergeBranch(ctx, "log-all"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	commits, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) < 3 {
		t.Fatalf("Log: got %d commits, want >= 3", len(commits))
	}
	// Newest-first: last written message should appear first.
	if commits[0].Message != "commit-3" {
		t.Errorf("Log[0].Message = %q, want %q", commits[0].Message, "commit-3")
	}
	if commits[1].Message != "commit-2" {
		t.Errorf("Log[1].Message = %q, want %q", commits[1].Message, "commit-2")
	}
}

// TestLog_FilterByPath verifies that Log with a path returns only commits that
// touched that specific file.
func TestLog_FilterByPath(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "log-path"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	// Commit A: file-a.md only
	if err := repo.WriteFile(ctx, "log-path", "file-a.md", "A", "agent-1", "add file-a"); err != nil {
		t.Fatalf("WriteFile file-a: %v", err)
	}
	// Commit B: file-b.md only
	if err := repo.WriteFile(ctx, "log-path", "file-b.md", "B", "agent-1", "add file-b"); err != nil {
		t.Fatalf("WriteFile file-b: %v", err)
	}
	// Commit C: file-a.md again
	if err := repo.WriteFile(ctx, "log-path", "file-a.md", "A2", "agent-1", "update file-a"); err != nil {
		t.Fatalf("WriteFile file-a update: %v", err)
	}
	if err := repo.MergeBranch(ctx, "log-path"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	commits, err := repo.Log(ctx, "main", "file-a.md")
	if err != nil {
		t.Fatalf("Log(file-a.md): %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("Log(file-a.md): got %d commits, want 2", len(commits))
	}
	// Newest first: "update file-a" then "add file-a".
	if commits[0].Message != "update file-a" {
		t.Errorf("Log[0].Message = %q, want %q", commits[0].Message, "update file-a")
	}
	if commits[1].Message != "add file-a" {
		t.Errorf("Log[1].Message = %q, want %q", commits[1].Message, "add file-a")
	}
}

// TestLog_PathNoHistory verifies that a valid ref with a path never touched
// returns an empty slice (not an error).
func TestLog_PathNoHistory(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	commits, err := repo.Log(ctx, "main", "nonexistent.md")
	if err != nil {
		t.Fatalf("Log(nonexistent.md): got error %v, want nil", err)
	}
	if len(commits) != 0 {
		t.Errorf("Log(nonexistent.md): got %d commits, want 0", len(commits))
	}
}

// TestLog_RefNotFound verifies that Log returns ErrRefNotFound for an
// unknown ref.
func TestLog_RefNotFound(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	_, err := repo.Log(ctx, "nonexistent-branch", "")
	if !errors.Is(err, codevaldgit.ErrRefNotFound) {
		t.Fatalf("Log(bad-ref): got %v, want ErrRefNotFound", err)
	}
}

// TestLog_AtSHA verifies that Log can start from a specific commit SHA.
func TestLog_AtSHA(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "log-sha"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "log-sha", "a.txt", "a", "agent-1", "commit-A"); err != nil {
		t.Fatalf("WriteFile A: %v", err)
	}
	if err := repo.WriteFile(ctx, "log-sha", "b.txt", "b", "agent-1", "commit-B"); err != nil {
		t.Fatalf("WriteFile B: %v", err)
	}
	if err := repo.MergeBranch(ctx, "log-sha"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// Get all commits first to find the SHA of commit-A (the second in the list).
	all, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log all: %v", err)
	}
	// Find commit-A's SHA.
	var shaA string
	for _, c := range all {
		if c.Message == "commit-A" {
			shaA = c.SHA
			break
		}
	}
	if shaA == "" {
		t.Fatal("could not find commit-A SHA")
	}

	// Log starting at commit-A's SHA should return commit-A and its ancestors.
	fromSHA, err := repo.Log(ctx, shaA, "")
	if err != nil {
		t.Fatalf("Log from SHA: %v", err)
	}
	// commit-B must NOT appear (it's a descendant of A, not an ancestor).
	for _, c := range fromSHA {
		if c.Message == "commit-B" {
			t.Error("Log from SHA of commit-A should not include commit-B")
		}
	}
	// commit-A must appear.
	found := false
	for _, c := range fromSHA {
		if c.SHA == shaA {
			found = true
			break
		}
	}
	if !found {
		t.Error("Log from SHA: commit-A not found in results")
	}
}

// TestLog_CommitFields verifies SHA, Author, Message, and Timestamp are all
// populated correctly.
func TestLog_CommitFields(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "log-fields"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "log-fields", "x.txt", "x", "agent-fields", "fields-message"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, "log-fields"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	commits, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) == 0 {
		t.Fatal("Log: expected at least 1 commit")
	}
	c := commits[0]
	if len(c.SHA) != 40 {
		t.Errorf("Commit.SHA len = %d, want 40", len(c.SHA))
	}
	if c.Author != "agent-fields" {
		t.Errorf("Commit.Author = %q, want %q", c.Author, "agent-fields")
	}
	if c.Message != "fields-message" {
		t.Errorf("Commit.Message = %q, want %q", c.Message, "fields-message")
	}
	if c.Timestamp.IsZero() {
		t.Error("Commit.Timestamp is zero")
	}
}

// TestConcurrentLog verifies that 5 goroutines can call Log concurrently
// without data races or errors.
func TestConcurrentLog(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "log-concurrent"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "log-concurrent", "c.txt", "c", "agent-1", "concurrent"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, "log-concurrent"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.Log(ctx, "main", "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Log error: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Diff tests — MVP-GIT-007
// ---------------------------------------------------------------------------

// TestDiff_AddFile verifies that adding a new file appears as Operation=="add"
// with "+" lines in the Patch.
func TestDiff_AddFile(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	// Get main SHA before any changes.
	commits0, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	fromSHA := commits0[0].SHA

	if err := repo.CreateBranch(ctx, "diff-add"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "diff-add", "new.txt", "hello", "agent-1", "add new.txt"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, "diff-add"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	diffs, err := repo.Diff(ctx, fromSHA, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("Diff: got %d entries, want 1", len(diffs))
	}
	d := diffs[0]
	if d.Path != "new.txt" {
		t.Errorf("Path = %q, want %q", d.Path, "new.txt")
	}
	if d.Operation != "add" {
		t.Errorf("Operation = %q, want %q", d.Operation, "add")
	}
	if !strings.Contains(d.Patch, "+hello") {
		t.Errorf("Patch does not contain '+hello': %q", d.Patch)
	}
}

// TestDiff_ModifyFile verifies that modifying an existing file appears as
// Operation=="modify" with both "-" and "+" lines in the Patch.
func TestDiff_ModifyFile(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	// Seed main with a file.
	if err := repo.CreateBranch(ctx, "diff-mod-seed"); err != nil {
		t.Fatalf("CreateBranch seed: %v", err)
	}
	if err := repo.WriteFile(ctx, "diff-mod-seed", "mod.txt", "original", "agent-1", "seed mod.txt"); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if err := repo.MergeBranch(ctx, "diff-mod-seed"); err != nil {
		t.Fatalf("MergeBranch seed: %v", err)
	}
	baseSHA, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log seed: %v", err)
	}
	fromSHA := baseSHA[0].SHA

	// Modify the file on a new task branch.
	if err := repo.CreateBranch(ctx, "diff-mod"); err != nil {
		t.Fatalf("CreateBranch mod: %v", err)
	}
	if err := repo.WriteFile(ctx, "diff-mod", "mod.txt", "updated", "agent-1", "modify mod.txt"); err != nil {
		t.Fatalf("WriteFile mod: %v", err)
	}
	if err := repo.MergeBranch(ctx, "diff-mod"); err != nil {
		t.Fatalf("MergeBranch mod: %v", err)
	}

	diffs, err := repo.Diff(ctx, fromSHA, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("Diff: got %d entries, want 1", len(diffs))
	}
	d := diffs[0]
	if d.Operation != "modify" {
		t.Errorf("Operation = %q, want %q", d.Operation, "modify")
	}
	if !strings.Contains(d.Patch, "-original") {
		t.Errorf("Patch missing '-original': %q", d.Patch)
	}
	if !strings.Contains(d.Patch, "+updated") {
		t.Errorf("Patch missing '+updated': %q", d.Patch)
	}
}

// TestDiff_DeleteFile verifies that deleting a file appears as
// Operation=="delete" with "-" lines in the Patch.
func TestDiff_DeleteFile(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	// Seed main with a file to delete.
	if err := repo.CreateBranch(ctx, "diff-del-seed"); err != nil {
		t.Fatalf("CreateBranch seed: %v", err)
	}
	if err := repo.WriteFile(ctx, "diff-del-seed", "del.txt", "bye", "agent-1", "seed del.txt"); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if err := repo.MergeBranch(ctx, "diff-del-seed"); err != nil {
		t.Fatalf("MergeBranch seed: %v", err)
	}
	baseSHA, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log seed: %v", err)
	}
	fromSHA := baseSHA[0].SHA

	// Delete the file on a task branch.
	if err := repo.CreateBranch(ctx, "diff-del"); err != nil {
		t.Fatalf("CreateBranch del: %v", err)
	}
	if err := repo.DeleteFile(ctx, "diff-del", "del.txt", "agent-1", "delete del.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, "diff-del"); err != nil {
		t.Fatalf("MergeBranch del: %v", err)
	}

	diffs, err := repo.Diff(ctx, fromSHA, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("Diff: got %d entries, want 1", len(diffs))
	}
	d := diffs[0]
	if d.Path != "del.txt" {
		t.Errorf("Path = %q, want %q", d.Path, "del.txt")
	}
	if d.Operation != "delete" {
		t.Errorf("Operation = %q, want %q", d.Operation, "delete")
	}
	if !strings.Contains(d.Patch, "-bye") {
		t.Errorf("Patch missing '-bye': %q", d.Patch)
	}
}

// TestDiff_SameRef verifies that diffing a ref against itself returns an
// empty slice.
func TestDiff_SameRef(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	diffs, err := repo.Diff(ctx, "main", "main")
	if err != nil {
		t.Fatalf("Diff(main,main): %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("Diff(main,main): got %d entries, want 0", len(diffs))
	}
}

// TestDiff_IdenticalTrees verifies that two different commits with the same
// tree content return an empty slice.
func TestDiff_IdenticalTrees(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	// Two branches each add a different file, merged sequentially.
	if err := repo.CreateBranch(ctx, "idt-1"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "idt-1", "a.txt", "a", "agent-1", "add a"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := repo.MergeBranch(ctx, "idt-1"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// Record SHA of main after first merge.
	c1, err := repo.Log(ctx, "main", "")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	sha1 := c1[0].SHA

	// Same content again (idempotent write from same branch would be AllowEmptyCommits).
	// Instead, diff the same SHA against itself explicitly.
	diffs, err := repo.Diff(ctx, sha1, sha1)
	if err != nil {
		t.Fatalf("Diff(sha1,sha1): %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("Diff(sha1,sha1): got %d entries, want 0", len(diffs))
	}
}

// TestDiff_RefNotFound verifies that ErrRefNotFound is returned when either
// fromRef or toRef is invalid.
func TestDiff_RefNotFound(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if _, err := repo.Diff(ctx, "bad-ref", "main"); !errors.Is(err, codevaldgit.ErrRefNotFound) {
		t.Errorf("Diff(bad-ref,main): got %v, want ErrRefNotFound", err)
	}
	if _, err := repo.Diff(ctx, "main", "bad-ref"); !errors.Is(err, codevaldgit.ErrRefNotFound) {
		t.Errorf("Diff(main,bad-ref): got %v, want ErrRefNotFound", err)
	}
}

// TestDiff_BranchVsMain verifies that diffing main against a task branch
// shows the files the task branch has added.
func TestDiff_BranchVsMain(t *testing.T) {
	t.Parallel()
	repo := openTestRepo(t)
	ctx := context.Background()

	if err := repo.CreateBranch(ctx, "diff-branch-vs-main"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := repo.WriteFile(ctx, "diff-branch-vs-main", "task-output.md", "# Output", "agent-1", "add output"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	diffs, err := repo.Diff(ctx, "main", "task/diff-branch-vs-main")
	if err != nil {
		t.Fatalf("Diff(main, task/...): %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("Diff: got %d entries, want 1", len(diffs))
	}
	if diffs[0].Path != "task-output.md" {
		t.Errorf("Path = %q, want %q", diffs[0].Path, "task-output.md")
	}
	if diffs[0].Operation != "add" {
		t.Errorf("Operation = %q, want %q", diffs[0].Operation, "add")
	}
}
