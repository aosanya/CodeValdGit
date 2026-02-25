package filesystem_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
)

// newTestBackend creates a filesystemBackend backed by two temp directories
// and returns the backend, basePath, and archivePath.
func newTestBackend(t *testing.T) (codevaldgit.Backend, string, string) {
	t.Helper()
	base := t.TempDir()
	archive := t.TempDir()
	b, err := filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
		BasePath:    base,
		ArchivePath: archive,
	})
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	return b, base, archive
}

// TestInitRepo_CreatesGitDir verifies that InitRepo creates a valid .git
// directory with a HEAD file inside {BasePath}/{agencyID}/.
func TestInitRepo_CreatesGitDir(t *testing.T) {
	t.Parallel()
	b, base, _ := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-001"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	gitDir := filepath.Join(base, agency, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		t.Fatalf(".git directory does not exist: %v", err)
	}
	headFile := filepath.Join(gitDir, "HEAD")
	if _, err := os.Stat(headFile); err != nil {
		t.Fatalf("HEAD file does not exist: %v", err)
	}
}

// TestInitRepo_HeadPointsToMain verifies that HEAD references refs/heads/main
// (not the go-git default of refs/heads/master) after InitRepo.
func TestInitRepo_HeadPointsToMain(t *testing.T) {
	t.Parallel()
	b, base, _ := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-head"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	headBytes, err := os.ReadFile(filepath.Join(base, agency, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read HEAD: %v", err)
	}
	const want = "ref: refs/heads/main\n"
	if string(headBytes) != want {
		t.Errorf("HEAD = %q, want %q", string(headBytes), want)
	}
}

// TestInitRepo_AlreadyExists verifies that a second InitRepo for the same
// agency returns ErrRepoAlreadyExists.
func TestInitRepo_AlreadyExists(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-dup"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("first InitRepo: %v", err)
	}
	if err := b.InitRepo(ctx, agency); !errors.Is(err, codevaldgit.ErrRepoAlreadyExists) {
		t.Fatalf("second InitRepo: got %v, want ErrRepoAlreadyExists", err)
	}
}

// TestOpenRepo_Success verifies that OpenStorer returns non-nil storer and
// filesystem for a freshly initialized agency repository.
func TestOpenRepo_Success(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-open"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	storer, wt, err := b.OpenStorer(ctx, agency)
	if err != nil {
		t.Fatalf("OpenStorer: %v", err)
	}
	if storer == nil {
		t.Fatal("OpenStorer returned nil storer")
	}
	if wt == nil {
		t.Fatal("OpenStorer returned nil filesystem")
	}
}

// TestOpenRepo_NotFound verifies that OpenStorer returns ErrRepoNotFound for
// an agency ID that was never initialized.
func TestOpenRepo_NotFound(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()

	_, _, err := b.OpenStorer(ctx, "nonexistent-agency")
	if !errors.Is(err, codevaldgit.ErrRepoNotFound) {
		t.Fatalf("OpenStorer: got %v, want ErrRepoNotFound", err)
	}
}

// TestDeleteRepo_Archives verifies that DeleteRepo moves the live repo to the
// archive path (source gone, destination has .git).
func TestDeleteRepo_Archives(t *testing.T) {
	t.Parallel()
	b, base, archive := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-del"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if err := b.DeleteRepo(ctx, agency); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	// Source must be gone.
	if _, err := os.Stat(filepath.Join(base, agency)); !os.IsNotExist(err) {
		t.Fatal("source path still exists after DeleteRepo")
	}
	// Destination must contain a .git directory.
	if _, err := os.Stat(filepath.Join(archive, agency, ".git")); err != nil {
		t.Fatalf("archive .git not found: %v", err)
	}
}

// TestDeleteRepo_AlreadyArchived verifies that when {ArchivePath}/{agencyID}/
// already exists, DeleteRepo creates a timestamped variant instead of failing.
func TestDeleteRepo_AlreadyArchived(t *testing.T) {
	t.Parallel()
	b, base, archive := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-collision"

	// Pre-create the archive destination to force a collision.
	if err := os.MkdirAll(filepath.Join(archive, agency), 0o755); err != nil {
		t.Fatalf("setup: pre-create archive: %v", err)
	}
	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if err := b.DeleteRepo(ctx, agency); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	// Source must be gone.
	if _, err := os.Stat(filepath.Join(base, agency)); !os.IsNotExist(err) {
		t.Fatal("source path still exists after DeleteRepo")
	}
	// A timestamped directory (longer than agencyID) must exist under archive.
	entries, err := os.ReadDir(archive)
	if err != nil {
		t.Fatalf("ReadDir archive: %v", err)
	}
	found := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && len(name) > len(agency) && name[:len(agency)] == agency {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no timestamped archive directory found after collision")
	}
}

// TestDeleteRepo_NotFound verifies that DeleteRepo returns ErrRepoNotFound for
// an agency ID with no live repository.
func TestDeleteRepo_NotFound(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()

	if err := b.DeleteRepo(ctx, "nonexistent-agency"); !errors.Is(err, codevaldgit.ErrRepoNotFound) {
		t.Fatalf("DeleteRepo: got %v, want ErrRepoNotFound", err)
	}
}

// TestPurgeRepo_HardDeletes verifies that PurgeRepo removes the archive
// directory entirely after a prior DeleteRepo.
func TestPurgeRepo_HardDeletes(t *testing.T) {
	t.Parallel()
	b, _, archive := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-purge"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if err := b.DeleteRepo(ctx, agency); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if err := b.PurgeRepo(ctx, agency); err != nil {
		t.Fatalf("PurgeRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archive, agency)); !os.IsNotExist(err) {
		t.Fatal("archive path still exists after PurgeRepo")
	}
}

// TestPurgeRepo_NotFound verifies that PurgeRepo returns ErrRepoNotFound when
// no archived repository exists for the given agency ID.
func TestPurgeRepo_NotFound(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()

	if err := b.PurgeRepo(ctx, "nonexistent-agency"); !errors.Is(err, codevaldgit.ErrRepoNotFound) {
		t.Fatalf("PurgeRepo: got %v, want ErrRepoNotFound", err)
	}
}

// TestConcurrentOpen verifies that 10 goroutines can call OpenStorer on the
// same agency simultaneously without data races or errors.
func TestConcurrentOpen(t *testing.T) {
	t.Parallel()
	b, _, _ := newTestBackend(t)
	ctx := context.Background()
	const agency = "agency-concurrent"

	if err := b.InitRepo(ctx, agency); err != nil {
		t.Fatalf("InitRepo: %v", err)
	}

	const goroutines = 10
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := b.OpenStorer(ctx, agency)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: OpenStorer: %v", i, err)
		}
	}
}
