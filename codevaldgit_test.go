package codevaldgit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/storage"

	codevaldgit "github.com/aosanya/CodeValdGit"
)

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks.
// These fail at build time if a mock no longer satisfies the interface.
// ---------------------------------------------------------------------------

// mockRepoManager is a minimal stub used only for compile-time checks.
type mockRepoManager struct{}

func (m *mockRepoManager) InitRepo(_ context.Context, _, _ string) error { return nil }
func (m *mockRepoManager) OpenRepo(_ context.Context, _, _ string) (codevaldgit.Repo, error) {
	return nil, nil
}
func (m *mockRepoManager) DeleteRepo(_ context.Context, _ string) error { return nil }
func (m *mockRepoManager) PurgeRepo(_ context.Context, _ string) error  { return nil }

// mockRepo is a minimal stub used only for compile-time checks.
type mockRepo struct{}

func (r *mockRepo) CreateBranch(_ context.Context, _ string) error          { return nil }
func (r *mockRepo) MergeBranch(_ context.Context, _ string) error           { return nil }
func (r *mockRepo) DeleteBranch(_ context.Context, _ string) error          { return nil }
func (r *mockRepo) WriteFile(_ context.Context, _, _, _, _, _ string) error { return nil }
func (r *mockRepo) ReadFile(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *mockRepo) DeleteFile(_ context.Context, _, _, _, _ string) error   { return nil }
func (r *mockRepo) ListDirectory(_ context.Context, _, _ string) ([]codevaldgit.FileEntry, error) {
	return nil, nil
}
func (r *mockRepo) Log(_ context.Context, _, _ string) ([]codevaldgit.CommitEntry, error) {
	return nil, nil
}
func (r *mockRepo) Diff(_ context.Context, _, _ string) ([]codevaldgit.FileDiff, error) {
	return nil, nil
}

// mockBackend is a minimal stub used only for compile-time checks.
type mockBackend struct{}

func (b *mockBackend) InitRepo(_ context.Context, _, _ string) error { return nil }
func (b *mockBackend) OpenStorer(_ context.Context, _, _ string) (storage.Storer, billy.Filesystem, error) {
	return nil, nil, nil
}
func (b *mockBackend) DeleteRepo(_ context.Context, _ string) error { return nil }
func (b *mockBackend) PurgeRepo(_ context.Context, _ string) error  { return nil }

// Compile-time assertions: these lines won't compile if the mocks are wrong.
var _ codevaldgit.RepoManager = (*mockRepoManager)(nil)
var _ codevaldgit.Repo = (*mockRepo)(nil)
var _ codevaldgit.Backend = (*mockBackend)(nil)

// ---------------------------------------------------------------------------
// JSON serialization tests for shared value types.
// ---------------------------------------------------------------------------

func TestFileEntry_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	entry := codevaldgit.FileEntry{
		Name:  "report.md",
		Path:  "output/report.md",
		IsDir: false,
		Size:  1024,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal FileEntry: %v", err)
	}
	var got codevaldgit.FileEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal FileEntry: %v", err)
	}
	if got != entry {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, entry)
	}
}

func TestCommit_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	c := codevaldgit.CommitEntry{
		SHA:       "abc123def456abc123def456abc123def456abc1",
		Author:    "agent-001",
		Message:   "GIT-001: initial commit",
		Timestamp: ts,
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal Commit: %v", err)
	}
	var got codevaldgit.CommitEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal Commit: %v", err)
	}
	if got.SHA != c.SHA || got.Author != c.Author || got.Message != c.Message {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, c)
	}
	if !got.Timestamp.Equal(c.Timestamp) {
		t.Errorf("Timestamp mismatch: got %v, want %v", got.Timestamp, c.Timestamp)
	}
}

func TestFileDiff_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	d := codevaldgit.FileDiff{
		Path:      "src/main.go",
		Operation: "modify",
		Patch:     "@@ -1,3 +1,4 @@\n func main() {}\n",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal FileDiff: %v", err)
	}
	var got codevaldgit.FileDiff
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal FileDiff: %v", err)
	}
	if got != d {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, d)
	}
}

func TestErrMergeConflict_Error(t *testing.T) {
	t.Parallel()
	err := &codevaldgit.ErrMergeConflict{
		TaskID:           "task-abc-001",
		ConflictingFiles: []string{"src/main.go", "README.md"},
	}
	msg := err.Error()
	if msg == "" {
		t.Fatal("ErrMergeConflict.Error() returned empty string")
	}
	// Must mention the task ID and at least one file.
	for _, want := range []string{"task-abc-001", "src/main.go"} {
		if !contains(msg, want) {
			t.Errorf("ErrMergeConflict.Error() %q does not contain %q", msg, want)
		}
	}
}

func TestSentinelErrors_NotNil(t *testing.T) {
	t.Parallel()
	sentinels := []error{
		codevaldgit.ErrRepoNotFound,
		codevaldgit.ErrRepoAlreadyExists,
		codevaldgit.ErrBranchNotFound,
		codevaldgit.ErrBranchExists,
		codevaldgit.ErrFileNotFound,
		codevaldgit.ErrRefNotFound,
	}
	for _, err := range sentinels {
		if err == nil {
			t.Errorf("sentinel error is nil")
		}
	}
}

// contains is a simple substring check to avoid importing strings in test.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
