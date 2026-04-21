// git_impl_blobcache.go provides the lazy blob-content hydration helpers used
// by [gitManager.ReadFile].
//
// When IndexPushedBranch walks the tip-commit tree it writes Blob entities
// with metadata only (sha, path, name, extension, size) and leaves the content
// field empty.  The first ReadFile call for such a blob triggers
// loadBlobContentFromStorer, which:
//
//  1. Looks up the repository name from the Repository entity.
//  2. Opens the backend storer for that repository (ArangoDB or filesystem).
//  3. Reads the blob object by its SHA directly from the storer.
//  4. Detects binary vs text and encodes accordingly.
//  5. Calls cacheBlobContent to persist the content back into the entity.
package codevaldgit

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"time"
	"unicode/utf8"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// loadBlobContentFromStorer reads the raw content of blob from the backend
// storer for the repository identified by branch.RepositoryID.
// Returns the content string and the encoding ("utf-8" or "base64").
func (m *gitManager) loadBlobContentFromStorer(ctx context.Context, branch Branch, blob Blob) (content, encoding string, err error) {
	log.Printf("[loadBlobContentFromStorer] blobID=%s sha=%q repoID=%s", blob.ID, blob.SHA, branch.RepositoryID)
	if blob.SHA == "" {
		return "", "", fmt.Errorf("blob entity %s has no SHA", blob.ID)
	}

	// Retrieve the repository entity to find the repo name.
	repoEntity, err := m.dm.GetEntity(ctx, m.agencyID, branch.RepositoryID)
	if err != nil {
		log.Printf("[loadBlobContentFromStorer] GetEntity repoID=%s error: %v", branch.RepositoryID, err)
		return "", "", fmt.Errorf("get repository entity %s: %w", branch.RepositoryID, err)
	}
	repoName, _ := repoEntity.Properties["name"].(string)
	log.Printf("[loadBlobContentFromStorer] repoName=%q", repoName)
	if repoName == "" {
		return "", "", fmt.Errorf("repository %s has no name property", branch.RepositoryID)
	}

	// Open the backend storer — ArangoDB or filesystem, no network I/O.
	sto, fs, err := m.backend.OpenStorer(ctx, m.agencyID, repoName)
	if err != nil {
		log.Printf("[loadBlobContentFromStorer] OpenStorer repo=%s error: %v", repoName, err)
		return "", "", fmt.Errorf("open storer for repo %s: %w", repoName, err)
	}
	log.Printf("[loadBlobContentFromStorer] storer opened OK")

	repo, err := gogit.Open(sto, fs)
	if err != nil {
		log.Printf("[loadBlobContentFromStorer] gogit.Open error: %v", err)
		return "", "", fmt.Errorf("open git repo %s: %w", repoName, err)
	}

	hash := gogitplumbing.NewHash(blob.SHA)
	blobObj, err := repo.BlobObject(hash)
	if err != nil {
		log.Printf("[loadBlobContentFromStorer] BlobObject sha=%s error: %v", blob.SHA, err)
		return "", "", fmt.Errorf("resolve blob %s in storer: %w", blob.SHA, err)
	}
	log.Printf("[loadBlobContentFromStorer] blob object resolved size=%d", blobObj.Size)

	r, err := blobObj.Reader()
	if err != nil {
		return "", "", fmt.Errorf("open blob reader %s: %w", blob.SHA, err)
	}
	defer func() { _ = r.Close() }()

	raw, err := io.ReadAll(r)
	if err != nil {
		return "", "", fmt.Errorf("read blob %s: %w", blob.SHA, err)
	}
	log.Printf("[loadBlobContentFromStorer] raw bytes read len=%d", len(raw))

	// Detect encoding: treat as binary if the bytes are not valid UTF-8 or
	// contain a null byte (common heuristic used by git itself).
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		log.Printf("[loadBlobContentFromStorer] encoding=base64")
		return base64.StdEncoding.EncodeToString(raw), "base64", nil
	}
	log.Printf("[loadBlobContentFromStorer] encoding=utf-8")
	return string(raw), "utf-8", nil
}

// cacheBlobContent persists content and encoding back into the Blob entity so
// that subsequent ReadFile calls are served directly from the entity graph
// without hitting the storer again.
func (m *gitManager) cacheBlobContent(ctx context.Context, blobID, content, encoding string) error {
	_, err := m.dm.UpdateEntity(ctx, m.agencyID, blobID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"content":    content,
			"encoding":   encoding,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("cacheBlobContent %s: %w", blobID, err)
	}
	return nil
}
