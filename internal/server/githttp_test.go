package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	filesystem "github.com/aosanya/CodeValdGit/storage/filesystem"

	"github.com/aosanya/CodeValdGit/internal/server"
)

// newTestHandler creates a GitHTTPHandler backed by a temp filesystem backend
// and initialises a repository for the given agencyID.
func newTestHandler(t *testing.T, agencyID string) *server.GitHTTPHandler {
	t.Helper()

	base := t.TempDir()
	archive := filepath.Join(base, "archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}

	b, err := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
		BasePath:    base,
		ArchivePath: archive,
	})
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}

	if err := b.InitRepo(t.Context(), agencyID, testRepoName); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}

	return server.NewGitHTTPHandler(b)
}

// testRepoName is the repository name used across all githttp tests.
const testRepoName = "repo"

func TestInfoRefs_UploadPack(t *testing.T) {
	h := newTestHandler(t, "agency-1")

	req := httptest.NewRequest(http.MethodGet, "/agency-1/"+testRepoName+"/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-git-upload-pack-advertisement" {
		t.Errorf("unexpected Content-Type: %q", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "# service=git-upload-pack") {
		t.Errorf("response body missing service header; got: %q", body[:min(len(body), 200)])
	}
}

func TestInfoRefs_ReceivePack(t *testing.T) {
	h := newTestHandler(t, "agency-2")

	req := httptest.NewRequest(http.MethodGet, "/agency-2/"+testRepoName+"/info/refs?service=git-receive-pack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-git-receive-pack-advertisement" {
		t.Errorf("unexpected Content-Type: %q", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "# service=git-receive-pack") {
		t.Errorf("response body missing service header; got: %q", body[:min(len(body), 200)])
	}
}

func TestInfoRefs_UnknownRepo_AutoCreates(t *testing.T) {
	// backendLoader auto-creates a repository on first access, so a previously
	// unknown repo returns 200 (not 404) — the repo is initialised on the fly.
	base := t.TempDir()
	archive := filepath.Join(base, "archive")
	_ = os.MkdirAll(archive, 0o755)

	b, err := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
		BasePath:    base,
		ArchivePath: archive,
	})
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}

	h := server.NewGitHTTPHandler(b)

	req := httptest.NewRequest(http.MethodGet, "/no-such-agency/no-such-repo/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (auto-created), got %d", w.Result().StatusCode)
	}
}

func TestInfoRefs_UnsupportedService(t *testing.T) {
	h := newTestHandler(t, "agency-3")

	req := httptest.NewRequest(http.MethodGet, "/agency-3/"+testRepoName+"/info/refs?service=git-daemon-export-ok", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestServeHTTP_InvalidPath(t *testing.T) {
	h := newTestHandler(t, "agency-4")

	cases := []struct {
		path string
		desc string
	}{
		{"/", "root path"},
		{"/agency-only", "agency only, no repo segment"},
		{"/agency/repo", "agency+repo but no trailing git endpoint"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Result().StatusCode == http.StatusOK {
			t.Errorf("%s: expected non-200, got 200", tc.desc)
		}
	}
}

func TestNoCacheHeaders(t *testing.T) {
	h := newTestHandler(t, "agency-5")

	req := httptest.NewRequest(http.MethodGet, "/agency-5/"+testRepoName+"/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	cc := w.Result().Header.Get("Cache-Control")
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("expected Cache-Control: no-cache, got %q", cc)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
