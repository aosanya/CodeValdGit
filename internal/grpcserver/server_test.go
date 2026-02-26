package grpcserver_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/grpcserver"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

// mockRepo is a minimal codevaldgit.Repo that returns preset results.
type mockRepo struct {
	createBranchErr error
	mergeBranchErr  error
	deleteBranchErr error
	writeFileErr    error
	readFileResult  string
	readFileErr     error
	deleteFileErr   error
	listDirEntries  []codevaldgit.FileEntry
	listDirErr      error
	logCommits      []codevaldgit.Commit
	logErr          error
	diffDiffs       []codevaldgit.FileDiff
	diffErr         error
}

func (m *mockRepo) CreateBranch(_ context.Context, _ string) error { return m.createBranchErr }
func (m *mockRepo) MergeBranch(_ context.Context, _ string) error  { return m.mergeBranchErr }
func (m *mockRepo) DeleteBranch(_ context.Context, _ string) error { return m.deleteBranchErr }
func (m *mockRepo) WriteFile(_ context.Context, _, _, _, _, _ string) error {
	return m.writeFileErr
}
func (m *mockRepo) ReadFile(_ context.Context, _, _ string) (string, error) {
	return m.readFileResult, m.readFileErr
}
func (m *mockRepo) DeleteFile(_ context.Context, _, _, _, _ string) error { return m.deleteFileErr }
func (m *mockRepo) ListDirectory(_ context.Context, _, _ string) ([]codevaldgit.FileEntry, error) {
	return m.listDirEntries, m.listDirErr
}
func (m *mockRepo) Log(_ context.Context, _, _ string) ([]codevaldgit.Commit, error) {
	return m.logCommits, m.logErr
}
func (m *mockRepo) Diff(_ context.Context, _, _ string) ([]codevaldgit.FileDiff, error) {
	return m.diffDiffs, m.diffErr
}

// mockManager is a minimal codevaldgit.RepoManager that returns preset results.
type mockManager struct {
	initRepoErr   error
	openRepoRepo  codevaldgit.Repo
	openRepoErr   error
	deleteRepoErr error
	purgeRepoErr  error
}

func (m *mockManager) InitRepo(_ context.Context, _ string) error { return m.initRepoErr }
func (m *mockManager) OpenRepo(_ context.Context, _ string) (codevaldgit.Repo, error) {
	return m.openRepoRepo, m.openRepoErr
}
func (m *mockManager) DeleteRepo(_ context.Context, _ string) error { return m.deleteRepoErr }
func (m *mockManager) PurgeRepo(_ context.Context, _ string) error  { return m.purgeRepoErr }

// requireCode asserts that err is a gRPC status error with the expected code.
func requireCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %v, got %v (msg: %s)", want, st.Code(), st.Message())
	}
}

// ── InitRepo tests ────────────────────────────────────────────────────────────

func TestServer_InitRepo_OK(t *testing.T) {
	mgr := &mockManager{}
	srv := grpcserver.New(mgr)
	resp, err := srv.InitRepo(context.Background(), &pb.InitRepoRequest{AgencyId: "agency-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestServer_InitRepo_AlreadyExists(t *testing.T) {
	mgr := &mockManager{initRepoErr: codevaldgit.ErrRepoAlreadyExists}
	srv := grpcserver.New(mgr)
	_, err := srv.InitRepo(context.Background(), &pb.InitRepoRequest{AgencyId: "agency-1"})
	requireCode(t, err, codes.AlreadyExists)
}

func TestServer_InitRepo_NotFound(t *testing.T) {
	mgr := &mockManager{initRepoErr: codevaldgit.ErrRepoNotFound}
	srv := grpcserver.New(mgr)
	_, err := srv.InitRepo(context.Background(), &pb.InitRepoRequest{AgencyId: "agency-1"})
	requireCode(t, err, codes.NotFound)
}

// ── MergeBranch tests ─────────────────────────────────────────────────────────

func TestServer_MergeBranch_OK(t *testing.T) {
	mgr := &mockManager{openRepoRepo: &mockRepo{}}
	srv := grpcserver.New(mgr)
	resp, err := srv.MergeBranch(context.Background(), &pb.MergeBranchRequest{
		AgencyId: "agency-1",
		TaskId:   "task-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestServer_MergeBranch_Conflict(t *testing.T) {
	conflict := &codevaldgit.ErrMergeConflict{
		TaskID:           "task-001",
		ConflictingFiles: []string{"README.md", "main.go"},
	}
	mgr := &mockManager{openRepoRepo: &mockRepo{mergeBranchErr: conflict}}
	srv := grpcserver.New(mgr)
	_, err := srv.MergeBranch(context.Background(), &pb.MergeBranchRequest{
		AgencyId: "agency-1",
		TaskId:   "task-001",
	})
	requireCode(t, err, codes.Aborted)

	// Verify MergeConflictInfo detail is packed in the status.
	st, _ := status.FromError(err)
	details := st.Details()
	if len(details) == 0 {
		t.Fatal("expected MergeConflictInfo detail in status, got none")
	}
	info, ok := details[0].(*pb.MergeConflictInfo)
	if !ok {
		t.Fatalf("expected *pb.MergeConflictInfo detail, got %T", details[0])
	}
	if info.TaskId != "task-001" {
		t.Errorf("expected TaskId %q, got %q", "task-001", info.TaskId)
	}
	if len(info.ConflictingFiles) != 2 {
		t.Errorf("expected 2 conflicting files, got %d", len(info.ConflictingFiles))
	}
}

func TestServer_MergeBranch_RepoNotFound(t *testing.T) {
	mgr := &mockManager{openRepoErr: codevaldgit.ErrRepoNotFound}
	srv := grpcserver.New(mgr)
	_, err := srv.MergeBranch(context.Background(), &pb.MergeBranchRequest{
		AgencyId: "missing",
		TaskId:   "task-001",
	})
	requireCode(t, err, codes.NotFound)
}

// ── ReadFile tests ────────────────────────────────────────────────────────────

func TestServer_ReadFile_OK(t *testing.T) {
	mgr := &mockManager{openRepoRepo: &mockRepo{readFileResult: "hello world"}}
	srv := grpcserver.New(mgr)
	resp, err := srv.ReadFile(context.Background(), &pb.ReadFileRequest{
		AgencyId: "agency-1",
		Ref:      "main",
		Path:     "README.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", resp.Content)
	}
}

func TestServer_ReadFile_FileNotFound(t *testing.T) {
	mgr := &mockManager{openRepoRepo: &mockRepo{readFileErr: codevaldgit.ErrFileNotFound}}
	srv := grpcserver.New(mgr)
	_, err := srv.ReadFile(context.Background(), &pb.ReadFileRequest{
		AgencyId: "agency-1",
		Ref:      "main",
		Path:     "missing.md",
	})
	requireCode(t, err, codes.NotFound)
}

// ── ListDirectory tests ───────────────────────────────────────────────────────

func TestServer_ListDirectory_OK(t *testing.T) {
	entries := []codevaldgit.FileEntry{
		{Name: "README.md", Path: "README.md", IsDir: false, Size: 42},
		{Name: "src", Path: "src", IsDir: true, Size: 0},
	}
	mgr := &mockManager{openRepoRepo: &mockRepo{listDirEntries: entries}}
	srv := grpcserver.New(mgr)
	resp, err := srv.ListDirectory(context.Background(), &pb.ListDirectoryRequest{
		AgencyId: "agency-1",
		Ref:      "main",
		Path:     "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Name != "README.md" {
		t.Errorf("expected README.md, got %q", resp.Entries[0].Name)
	}
}

// ── Log tests ─────────────────────────────────────────────────────────────────

func TestServer_Log_OK(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	commits := []codevaldgit.Commit{
		{SHA: "abc123", Author: "agent-1", Message: "init", Timestamp: now},
	}
	mgr := &mockManager{openRepoRepo: &mockRepo{logCommits: commits}}
	srv := grpcserver.New(mgr)
	resp, err := srv.Log(context.Background(), &pb.LogRequest{
		AgencyId: "agency-1",
		Ref:      "main",
		Path:     "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(resp.Commits))
	}
	if resp.Commits[0].Sha != "abc123" {
		t.Errorf("expected SHA abc123, got %q", resp.Commits[0].Sha)
	}
}

// ── Error mapping table-driven test ──────────────────────────────────────────

func TestServer_ErrorMapping(t *testing.T) {
	rows := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{"ErrRepoNotFound", codevaldgit.ErrRepoNotFound, codes.NotFound},
		{"ErrBranchNotFound", codevaldgit.ErrBranchNotFound, codes.NotFound},
		{"ErrFileNotFound", codevaldgit.ErrFileNotFound, codes.NotFound},
		{"ErrRefNotFound", codevaldgit.ErrRefNotFound, codes.NotFound},
		{"ErrRepoAlreadyExists", codevaldgit.ErrRepoAlreadyExists, codes.AlreadyExists},
		{"ErrBranchExists", codevaldgit.ErrBranchExists, codes.AlreadyExists},
		{"ErrMergeConflict", &codevaldgit.ErrMergeConflict{TaskID: "t1"}, codes.Aborted},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			mgr := &mockManager{initRepoErr: row.err}
			srv := grpcserver.New(mgr)
			_, err := srv.InitRepo(context.Background(), &pb.InitRepoRequest{AgencyId: "x"})
			requireCode(t, err, row.wantCode)
		})
	}
}
