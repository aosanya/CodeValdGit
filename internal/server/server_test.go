// server_test.go tests the gRPC [Server] handler by wiring it to an in-memory
// [fakeGitManager] via a real gRPC connection (bufconn).  Each test covers a
// representative RPC — verifying that domain types are mapped to proto types
// correctly and that domain errors are translated to the expected gRPC status
// codes.
package server_test

import (
	"context"
	"errors"
	"net"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// ── fakeGitManager ────────────────────────────────────────────────────────────

// fakeGitManager is a configurable stub that satisfies [codevaldgit.GitManager].
// Each field is a function so individual tests can inject specific behaviour
// (errors, return values) without resetting global state.
type fakeGitManager struct {
	initRepo            func(ctx context.Context, req codevaldgit.CreateRepoRequest) (codevaldgit.Repository, error)
	listRepositories    func(ctx context.Context) ([]codevaldgit.Repository, error)
	getRepository       func(ctx context.Context, repoID string) (codevaldgit.Repository, error)
	getRepositoryByName func(ctx context.Context, repoName string) (codevaldgit.Repository, error)
	deleteRepo          func(ctx context.Context, repoID string) error
	purgeRepo           func(ctx context.Context, repoID string) error
	createTag           func(ctx context.Context, req codevaldgit.CreateTagRequest) (codevaldgit.Tag, error)
	createBranch        func(ctx context.Context, req codevaldgit.CreateBranchRequest) (codevaldgit.Branch, error)
	getBranch           func(ctx context.Context, branchID string) (codevaldgit.Branch, error)
	listBranches        func(ctx context.Context, repoID string) ([]codevaldgit.Branch, error)
	deleteBranch        func(ctx context.Context, branchID string) error
	mergeBranch         func(ctx context.Context, branchID string) (codevaldgit.Branch, error)
	getTag              func(ctx context.Context, tagID string) (codevaldgit.Tag, error)
	listTags            func(ctx context.Context, repoID string) ([]codevaldgit.Tag, error)
	deleteTag           func(ctx context.Context, tagID string) error
	writeFile           func(ctx context.Context, req codevaldgit.WriteFileRequest) (codevaldgit.Commit, error)
	readFile            func(ctx context.Context, branchID, path string) (codevaldgit.Blob, error)
	deleteFile          func(ctx context.Context, req codevaldgit.DeleteFileRequest) (codevaldgit.Commit, error)
	listDirectory       func(ctx context.Context, branchID, path string) ([]codevaldgit.FileEntry, error)
	log                 func(ctx context.Context, branchID string, filter codevaldgit.LogFilter) ([]codevaldgit.CommitEntry, error)
	diffFunc            func(ctx context.Context, fromRef, toRef string) ([]codevaldgit.FileDiff, error)
	createKeyword       func(ctx context.Context, req codevaldgit.CreateKeywordRequest) (codevaldgit.Keyword, error)
	getKeyword          func(ctx context.Context, kwID string) (codevaldgit.Keyword, error)
	listKeywords        func(ctx context.Context, filter codevaldgit.KeywordFilter) ([]codevaldgit.Keyword, error)
	getKeywordTree      func(ctx context.Context, kwID string) ([]codevaldgit.KeywordTreeNode, error)
	updateKeyword       func(ctx context.Context, kwID string, req codevaldgit.UpdateKeywordRequest) (codevaldgit.Keyword, error)
	deleteKeyword       func(ctx context.Context, kwID string) error
	createEdge          func(ctx context.Context, req codevaldgit.CreateEdgeRequest) error
	deleteEdge          func(ctx context.Context, req codevaldgit.DeleteEdgeRequest) error
	getNeighborhood     func(ctx context.Context, branchID, entityID string, depth int) (codevaldgit.GraphResult, error)
	searchByKeywords    func(ctx context.Context, req codevaldgit.SearchByKeywordsRequest) (codevaldgit.GraphResult, error)
}

func (f *fakeGitManager) InitRepo(ctx context.Context, req codevaldgit.CreateRepoRequest) (codevaldgit.Repository, error) {
	if f.initRepo != nil {
		return f.initRepo(ctx, req)
	}
	return codevaldgit.Repository{ID: "repo-1", Name: req.Name, DefaultBranch: "main"}, nil
}
func (f *fakeGitManager) GetRepository(ctx context.Context, repoID string) (codevaldgit.Repository, error) {
	if f.getRepository != nil {
		return f.getRepository(ctx, repoID)
	}
	return codevaldgit.Repository{ID: repoID, Name: "test-repo", DefaultBranch: "main"}, nil
}
func (f *fakeGitManager) GetRepositoryByName(ctx context.Context, repoName string) (codevaldgit.Repository, error) {
	if f.getRepositoryByName != nil {
		return f.getRepositoryByName(ctx, repoName)
	}
	return codevaldgit.Repository{ID: "repo-1", Name: repoName, DefaultBranch: "main"}, nil
}
func (f *fakeGitManager) ListRepositories(ctx context.Context) ([]codevaldgit.Repository, error) {
	if f.listRepositories != nil {
		return f.listRepositories(ctx)
	}
	return []codevaldgit.Repository{{ID: "repo-1", Name: "test-repo", DefaultBranch: "main"}}, nil
}
func (f *fakeGitManager) DeleteRepo(ctx context.Context, repoID string) error {
	if f.deleteRepo != nil {
		return f.deleteRepo(ctx, repoID)
	}
	return nil
}
func (f *fakeGitManager) PurgeRepo(ctx context.Context, repoID string) error {
	if f.purgeRepo != nil {
		return f.purgeRepo(ctx, repoID)
	}
	return nil
}
func (f *fakeGitManager) CreateBranch(ctx context.Context, req codevaldgit.CreateBranchRequest) (codevaldgit.Branch, error) {
	if f.createBranch != nil {
		return f.createBranch(ctx, req)
	}
	return codevaldgit.Branch{ID: "branch-1", Name: req.Name}, nil
}
func (f *fakeGitManager) GetBranch(ctx context.Context, branchID string) (codevaldgit.Branch, error) {
	if f.getBranch != nil {
		return f.getBranch(ctx, branchID)
	}
	return codevaldgit.Branch{ID: branchID, Name: "main", IsDefault: true}, nil
}
func (f *fakeGitManager) ListBranches(ctx context.Context, repoID string) ([]codevaldgit.Branch, error) {
	if f.listBranches != nil {
		return f.listBranches(ctx, repoID)
	}
	return []codevaldgit.Branch{{ID: "branch-1", Name: "main", IsDefault: true}}, nil
}
func (f *fakeGitManager) DeleteBranch(ctx context.Context, branchID string) error {
	if f.deleteBranch != nil {
		return f.deleteBranch(ctx, branchID)
	}
	return nil
}
func (f *fakeGitManager) MergeBranch(ctx context.Context, branchID string) (codevaldgit.Branch, error) {
	if f.mergeBranch != nil {
		return f.mergeBranch(ctx, branchID)
	}
	return codevaldgit.Branch{ID: "branch-1", Name: "main", IsDefault: true}, nil
}
func (f *fakeGitManager) CreateTag(ctx context.Context, req codevaldgit.CreateTagRequest) (codevaldgit.Tag, error) {
	if f.createTag != nil {
		return f.createTag(ctx, req)
	}
	return codevaldgit.Tag{ID: "tag-1", Name: req.Name}, nil
}
func (f *fakeGitManager) GetTag(ctx context.Context, tagID string) (codevaldgit.Tag, error) {
	if f.getTag != nil {
		return f.getTag(ctx, tagID)
	}
	return codevaldgit.Tag{ID: tagID, Name: "v1.0.0"}, nil
}
func (f *fakeGitManager) ListTags(ctx context.Context, repoID string) ([]codevaldgit.Tag, error) {
	if f.listTags != nil {
		return f.listTags(ctx, repoID)
	}
	return []codevaldgit.Tag{{ID: "tag-1", Name: "v1.0.0"}}, nil
}
func (f *fakeGitManager) DeleteTag(ctx context.Context, tagID string) error {
	if f.deleteTag != nil {
		return f.deleteTag(ctx, tagID)
	}
	return nil
}
func (f *fakeGitManager) WriteFile(ctx context.Context, req codevaldgit.WriteFileRequest) (codevaldgit.Commit, error) {
	if f.writeFile != nil {
		return f.writeFile(ctx, req)
	}
	return codevaldgit.Commit{ID: "commit-1", SHA: "abc123", Message: req.Message}, nil
}
func (f *fakeGitManager) ReadFile(ctx context.Context, branchID, path string) (codevaldgit.Blob, error) {
	if f.readFile != nil {
		return f.readFile(ctx, branchID, path)
	}
	return codevaldgit.Blob{ID: "blob-1", Path: path, Content: "file content", Encoding: "utf-8"}, nil
}
func (f *fakeGitManager) DeleteFile(ctx context.Context, req codevaldgit.DeleteFileRequest) (codevaldgit.Commit, error) {
	if f.deleteFile != nil {
		return f.deleteFile(ctx, req)
	}
	return codevaldgit.Commit{ID: "commit-2", SHA: "def456", Message: "Delete " + req.Path}, nil
}
func (f *fakeGitManager) ListDirectory(ctx context.Context, branchID, path string) ([]codevaldgit.FileEntry, error) {
	if f.listDirectory != nil {
		return f.listDirectory(ctx, branchID, path)
	}
	return []codevaldgit.FileEntry{{Name: "README.md", Path: "README.md"}}, nil
}
func (f *fakeGitManager) Log(ctx context.Context, branchID string, filter codevaldgit.LogFilter) ([]codevaldgit.CommitEntry, error) {
	if f.log != nil {
		return f.log(ctx, branchID, filter)
	}
	return []codevaldgit.CommitEntry{{SHA: "abc123", Message: "initial commit"}}, nil
}
func (f *fakeGitManager) Diff(ctx context.Context, fromRef, toRef string) ([]codevaldgit.FileDiff, error) {
	if f.diffFunc != nil {
		return f.diffFunc(ctx, fromRef, toRef)
	}
	return []codevaldgit.FileDiff{{Path: "README.md", Operation: "added"}}, nil
}

func (f *fakeGitManager) ImportRepo(_ context.Context, _ codevaldgit.ImportRepoRequest) (codevaldgit.ImportJob, error) {
	return codevaldgit.ImportJob{ID: "fake-job-id", Status: "pending"}, nil
}

func (f *fakeGitManager) GetImportStatus(_ context.Context, _ string) (codevaldgit.ImportJob, error) {
	return codevaldgit.ImportJob{ID: "fake-job-id", Status: "pending"}, nil
}

func (f *fakeGitManager) CancelImport(_ context.Context, _ string) error {
	return nil
}

func (f *fakeGitManager) CreateKeyword(_ context.Context, req codevaldgit.CreateKeywordRequest) (codevaldgit.Keyword, error) {
	if f.createKeyword != nil {
		return f.createKeyword(context.Background(), req)
	}
	return codevaldgit.Keyword{}, nil
}

func (f *fakeGitManager) GetKeyword(_ context.Context, kwID string) (codevaldgit.Keyword, error) {
	if f.getKeyword != nil {
		return f.getKeyword(context.Background(), kwID)
	}
	return codevaldgit.Keyword{}, nil
}

func (f *fakeGitManager) ListKeywords(_ context.Context, filter codevaldgit.KeywordFilter) ([]codevaldgit.Keyword, error) {
	if f.listKeywords != nil {
		return f.listKeywords(context.Background(), filter)
	}
	return nil, nil
}

func (f *fakeGitManager) GetKeywordTree(_ context.Context, kwID string) ([]codevaldgit.KeywordTreeNode, error) {
	if f.getKeywordTree != nil {
		return f.getKeywordTree(context.Background(), kwID)
	}
	return nil, nil
}

func (f *fakeGitManager) UpdateKeyword(_ context.Context, kwID string, req codevaldgit.UpdateKeywordRequest) (codevaldgit.Keyword, error) {
	if f.updateKeyword != nil {
		return f.updateKeyword(context.Background(), kwID, req)
	}
	return codevaldgit.Keyword{}, nil
}

func (f *fakeGitManager) DeleteKeyword(_ context.Context, kwID string) error {
	if f.deleteKeyword != nil {
		return f.deleteKeyword(context.Background(), kwID)
	}
	return nil
}

func (f *fakeGitManager) CreateEdge(_ context.Context, req codevaldgit.CreateEdgeRequest) error {
	if f.createEdge != nil {
		return f.createEdge(context.Background(), req)
	}
	return nil
}

func (f *fakeGitManager) DeleteEdge(_ context.Context, req codevaldgit.DeleteEdgeRequest) error {
	if f.deleteEdge != nil {
		return f.deleteEdge(context.Background(), req)
	}
	return nil
}

func (f *fakeGitManager) GetNeighborhood(_ context.Context, branchID, entityID string, depth int) (codevaldgit.GraphResult, error) {
	if f.getNeighborhood != nil {
		return f.getNeighborhood(context.Background(), branchID, entityID, depth)
	}
	return codevaldgit.GraphResult{}, nil
}

func (f *fakeGitManager) SearchByKeywords(_ context.Context, req codevaldgit.SearchByKeywordsRequest) (codevaldgit.GraphResult, error) {
	if f.searchByKeywords != nil {
		return f.searchByKeywords(context.Background(), req)
	}
	return codevaldgit.GraphResult{}, nil
}

func (f *fakeGitManager) QueryGraph(_ context.Context, _ codevaldgit.QueryGraphRequest) (codevaldgit.GraphResult, error) {
	return codevaldgit.GraphResult{}, nil
}

func (f *fakeGitManager) FetchBranch(_ context.Context, req codevaldgit.FetchBranchRequest) (codevaldgit.FetchBranchJob, error) {
	return codevaldgit.FetchBranchJob{}, nil
}

func (f *fakeGitManager) GetFetchBranchStatus(_ context.Context, jobID string) (codevaldgit.FetchBranchJob, error) {
	return codevaldgit.FetchBranchJob{}, nil
}

func (f *fakeGitManager) GetBranchByName(_ context.Context, _ string, _ string) (codevaldgit.Branch, error) {
	return codevaldgit.Branch{}, nil
}

func (f *fakeGitManager) IndexPushedBranch(_ context.Context, _, _, _, _ string) error {
	return nil
}

// ── test server setup ─────────────────────────────────────────────────────────

const bufSize = 1024 * 1024

// newTestServer spins up a real gRPC server backed by the given manager and
// returns a client connected to it. The server and connection are cleaned up
// when t ends.
func newTestServer(t *testing.T, mgr codevaldgit.GitManager) pb.GitServiceClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	pb.RegisterGitServiceServer(srv, server.New(mgr))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewGitServiceClient(conn)
}

// grpcCode extracts the gRPC status code from an error (codes.OK if nil).
func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return codes.Unknown
}

// ── InitRepo ──────────────────────────────────────────────────────────────────

func TestServer_InitRepo_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		initRepo: func(_ context.Context, req codevaldgit.CreateRepoRequest) (codevaldgit.Repository, error) {
			return codevaldgit.Repository{
				ID:            "repo-99",
				Name:          req.Name,
				DefaultBranch: "main",
				AgencyID:      "agency-1",
			}, nil
		},
	})
	resp, err := client.InitRepo(context.Background(), &pb.InitRepoRequest{
		Name:          "my-repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if resp.GetId() != "repo-99" {
		t.Errorf("resp.Id = %q, want %q", resp.GetId(), "repo-99")
	}
	if resp.GetName() != "my-repo" {
		t.Errorf("resp.Name = %q, want %q", resp.GetName(), "my-repo")
	}
}

func TestServer_InitRepo_AlreadyExists(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		initRepo: func(_ context.Context, _ codevaldgit.CreateRepoRequest) (codevaldgit.Repository, error) {
			return codevaldgit.Repository{}, codevaldgit.ErrRepoAlreadyExists
		},
	})
	_, err := client.InitRepo(context.Background(), &pb.InitRepoRequest{Name: "x"})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("got code %v, want AlreadyExists", grpcCode(err))
	}
}

// ── GetRepository ─────────────────────────────────────────────────────────────

func TestServer_GetRepository_NotInitialised(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		getRepository: func(_ context.Context, _ string) (codevaldgit.Repository, error) {
			return codevaldgit.Repository{}, codevaldgit.ErrRepoNotInitialised
		},
	})
	_, err := client.GetRepository(context.Background(), &pb.GetRepositoryRequest{})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── DeleteRepo ────────────────────────────────────────────────────────────────

func TestServer_DeleteRepo_Success(t *testing.T) {
	called := false
	client := newTestServer(t, &fakeGitManager{
		deleteRepo: func(_ context.Context, _ string) error {
			called = true
			return nil
		},
	})
	_, err := client.DeleteRepo(context.Background(), &pb.DeleteRepoRequest{})
	if err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if !called {
		t.Error("DeleteRepo handler not called")
	}
}

// ── CreateBranch ──────────────────────────────────────────────────────────────

func TestServer_CreateBranch_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		createBranch: func(_ context.Context, req codevaldgit.CreateBranchRequest) (codevaldgit.Branch, error) {
			return codevaldgit.Branch{ID: "br-1", Name: req.Name}, nil
		},
	})
	resp, err := client.CreateBranch(context.Background(), &pb.CreateBranchRequest{Name: "task/feature"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if resp.GetName() != "task/feature" {
		t.Errorf("resp.Name = %q, want %q", resp.GetName(), "task/feature")
	}
}

func TestServer_CreateBranch_AlreadyExists(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		createBranch: func(_ context.Context, _ codevaldgit.CreateBranchRequest) (codevaldgit.Branch, error) {
			return codevaldgit.Branch{}, codevaldgit.ErrBranchExists
		},
	})
	_, err := client.CreateBranch(context.Background(), &pb.CreateBranchRequest{Name: "dupe"})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("got code %v, want AlreadyExists", grpcCode(err))
	}
}

// ── GetBranch ─────────────────────────────────────────────────────────────────

func TestServer_GetBranch_NotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		getBranch: func(_ context.Context, _ string) (codevaldgit.Branch, error) {
			return codevaldgit.Branch{}, codevaldgit.ErrBranchNotFound
		},
	})
	_, err := client.GetBranch(context.Background(), &pb.GetBranchRequest{BranchId: "ghost"})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── DeleteBranch ──────────────────────────────────────────────────────────────

func TestServer_DeleteBranch_DefaultForbidden(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		deleteBranch: func(_ context.Context, _ string) error {
			return codevaldgit.ErrDefaultBranchDeleteForbidden
		},
	})
	_, err := client.DeleteBranch(context.Background(), &pb.DeleteBranchRequest{BranchId: "main-id"})
	if grpcCode(err) != codes.FailedPrecondition {
		t.Errorf("got code %v, want FailedPrecondition", grpcCode(err))
	}
}

// ── MergeBranch ───────────────────────────────────────────────────────────────

func TestServer_MergeBranch_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		mergeBranch: func(_ context.Context, branchID string) (codevaldgit.Branch, error) {
			return codevaldgit.Branch{ID: "main-id", Name: "main", IsDefault: true, HeadCommitID: "commit-99"}, nil
		},
	})
	resp, err := client.MergeBranch(context.Background(), &pb.MergeBranchRequest{BranchId: "feature-id"})
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	if !resp.GetIsDefault() {
		t.Error("merged branch IsDefault = false, want true")
	}
	if resp.GetHeadCommitId() != "commit-99" {
		t.Errorf("HeadCommitId = %q, want %q", resp.GetHeadCommitId(), "commit-99")
	}
}

func TestServer_MergeBranch_Conflict(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		mergeBranch: func(_ context.Context, _ string) (codevaldgit.Branch, error) {
			return codevaldgit.Branch{}, &codevaldgit.ErrMergeConflict{
				TaskID:           "task-branch",
				ConflictingFiles: []string{"file.txt"},
			}
		},
	})
	_, err := client.MergeBranch(context.Background(), &pb.MergeBranchRequest{BranchId: "conflict-branch"})
	if grpcCode(err) != codes.Aborted {
		t.Errorf("got code %v, want Aborted", grpcCode(err))
	}
}

// ── WriteFile ─────────────────────────────────────────────────────────────────

func TestServer_WriteFile_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		writeFile: func(_ context.Context, req codevaldgit.WriteFileRequest) (codevaldgit.Commit, error) {
			return codevaldgit.Commit{ID: "c1", SHA: "abc", Message: req.Message}, nil
		},
	})
	resp, err := client.WriteFile(context.Background(), &pb.WriteFileRequest{
		BranchId: "branch-1",
		Path:     "README.md",
		Content:  "# Hello",
		Message:  "Add README",
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if resp.GetSha() != "abc" {
		t.Errorf("commit.SHA = %q, want %q", resp.GetSha(), "abc")
	}
	if resp.GetMessage() != "Add README" {
		t.Errorf("commit.Message = %q, want %q", resp.GetMessage(), "Add README")
	}
}

func TestServer_WriteFile_BranchNotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		writeFile: func(_ context.Context, _ codevaldgit.WriteFileRequest) (codevaldgit.Commit, error) {
			return codevaldgit.Commit{}, codevaldgit.ErrBranchNotFound
		},
	})
	_, err := client.WriteFile(context.Background(), &pb.WriteFileRequest{
		BranchId: "ghost-branch",
		Path:     "file.txt",
		Content:  "content",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── ReadFile ──────────────────────────────────────────────────────────────────

func TestServer_ReadFile_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		readFile: func(_ context.Context, _, path string) (codevaldgit.Blob, error) {
			return codevaldgit.Blob{ID: "blob-1", Path: path, Content: "file content"}, nil
		},
	})
	resp, err := client.ReadFile(context.Background(), &pb.ReadFileRequest{
		BranchId: "branch-1",
		Path:     "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if resp.GetContent() != "file content" {
		t.Errorf("blob.Content = %q, want %q", resp.GetContent(), "file content")
	}
}

func TestServer_ReadFile_FileNotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		readFile: func(_ context.Context, _, _ string) (codevaldgit.Blob, error) {
			return codevaldgit.Blob{}, codevaldgit.ErrFileNotFound
		},
	})
	_, err := client.ReadFile(context.Background(), &pb.ReadFileRequest{
		BranchId: "branch-1",
		Path:     "ghost.txt",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── ListDirectory ─────────────────────────────────────────────────────────────

func TestServer_ListDirectory_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		listDirectory: func(_ context.Context, _, _ string) ([]codevaldgit.FileEntry, error) {
			return []codevaldgit.FileEntry{
				{Name: "main.go", Path: "main.go", IsDir: false, Size: 42},
				{Name: "src", Path: "src", IsDir: true},
			}, nil
		},
	})
	resp, err := client.ListDirectory(context.Background(), &pb.ListDirectoryRequest{
		BranchId: "branch-1",
		Path:     "",
	})
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if len(resp.GetEntries()) != 2 {
		t.Fatalf("entries count = %d, want 2", len(resp.GetEntries()))
	}
}

// ── Log ───────────────────────────────────────────────────────────────────────

func TestServer_Log_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		log: func(_ context.Context, _ string, filter codevaldgit.LogFilter) ([]codevaldgit.CommitEntry, error) {
			return []codevaldgit.CommitEntry{
				{SHA: "sha1", Author: "alice", Message: "first commit"},
				{SHA: "sha2", Author: "bob", Message: "second commit"},
			}, nil
		},
	})
	resp, err := client.Log(context.Background(), &pb.LogRequest{BranchId: "branch-1"})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(resp.GetCommits()) != 2 {
		t.Fatalf("commits count = %d, want 2", len(resp.GetCommits()))
	}
}

// ── Diff ──────────────────────────────────────────────────────────────────────

func TestServer_Diff_Success(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		diffFunc: func(_ context.Context, fromRef, _ string) ([]codevaldgit.FileDiff, error) {
			return []codevaldgit.FileDiff{
				{Path: "added.txt", Operation: "added"},
				{Path: "removed.txt", Operation: "deleted"},
			}, nil
		},
	})
	resp, err := client.Diff(context.Background(), &pb.DiffRequest{FromRef: "main", ToRef: "feature"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(resp.GetDiffs()) != 2 {
		t.Fatalf("diffs count = %d, want 2", len(resp.GetDiffs()))
	}
}

func TestServer_Diff_RefNotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		diffFunc: func(_ context.Context, _, _ string) ([]codevaldgit.FileDiff, error) {
			return nil, codevaldgit.ErrRefNotFound
		},
	})
	_, err := client.Diff(context.Background(), &pb.DiffRequest{FromRef: "ghost", ToRef: "main"})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── Tag management ────────────────────────────────────────────────────────────

func TestServer_CreateTag_AlreadyExists(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		createTag: func(_ context.Context, _ codevaldgit.CreateTagRequest) (codevaldgit.Tag, error) {
			return codevaldgit.Tag{}, codevaldgit.ErrTagAlreadyExists
		},
	})
	_, err := client.CreateTag(context.Background(), &pb.CreateTagRequest{
		Name:     "v1.0.0",
		CommitId: "commit-1",
	})
	if grpcCode(err) != codes.AlreadyExists {
		t.Errorf("got code %v, want AlreadyExists", grpcCode(err))
	}
}

func TestServer_GetTag_NotFound(t *testing.T) {
	client := newTestServer(t, &fakeGitManager{
		getTag: func(_ context.Context, _ string) (codevaldgit.Tag, error) {
			return codevaldgit.Tag{}, codevaldgit.ErrTagNotFound
		},
	})
	_, err := client.GetTag(context.Background(), &pb.GetTagRequest{TagId: "ghost-tag"})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", grpcCode(err))
	}
}

// ── Error mapping completeness ────────────────────────────────────────────────

// TestServer_ErrorMapping verifies the complete error-to-gRPC-code table
// without requiring a real network round-trip (uses the server directly).
func TestServer_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"ErrRepoNotFound", codevaldgit.ErrRepoNotFound, codes.NotFound},
		{"ErrRepoNotInitialised", codevaldgit.ErrRepoNotInitialised, codes.NotFound},
		{"ErrRepoAlreadyExists", codevaldgit.ErrRepoAlreadyExists, codes.AlreadyExists},
		{"ErrBranchNotFound", codevaldgit.ErrBranchNotFound, codes.NotFound},
		{"ErrBranchExists", codevaldgit.ErrBranchExists, codes.AlreadyExists},
		{"ErrTagNotFound", codevaldgit.ErrTagNotFound, codes.NotFound},
		{"ErrTagAlreadyExists", codevaldgit.ErrTagAlreadyExists, codes.AlreadyExists},
		{"ErrFileNotFound", codevaldgit.ErrFileNotFound, codes.NotFound},
		{"ErrRefNotFound", codevaldgit.ErrRefNotFound, codes.NotFound},
		{"ErrDefaultBranchDeleteForbidden", codevaldgit.ErrDefaultBranchDeleteForbidden, codes.FailedPrecondition},
		{"ErrMergeConflict", &codevaldgit.ErrMergeConflict{TaskID: "t", ConflictingFiles: []string{"f"}}, codes.Aborted},
		{"unknown", errors.New("mystery"), codes.Internal},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			client := newTestServer(t, &fakeGitManager{
				getRepository: func(_ context.Context, _ string) (codevaldgit.Repository, error) {
					return codevaldgit.Repository{}, tc.err
				},
			})
			_, err := client.GetRepository(context.Background(), &pb.GetRepositoryRequest{})
			if grpcCode(err) != tc.want {
				t.Errorf("error %v: got gRPC code %v, want %v", tc.err, grpcCode(err), tc.want)
			}
		})
	}
}
