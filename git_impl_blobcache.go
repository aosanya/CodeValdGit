// git_impl_blobcache.go provides the lazy blob-content hydration helpers used
// by [gitManager.ReadFile] (GIT-023e).
//
// When FetchBranch (GIT-023d) walks the tip-commit tree it writes Blob entities
// with metadata only (sha, path, name, extension, size) and leaves the content
// field empty.  The first ReadFile call for such a blob triggers
// loadBlobContentFromBareClone, which:
//
//  1. Looks up the bare_clone_path from the Repository entity.
//  2. Opens the bare clone with go-git (no network I/O).
//  3. Reads the blob object by its SHA.
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

// loadBlobContentFromBareClone reads the raw content of blob from the bare
// clone associated with branch's repository.  Returns the content string and
// the encoding ("utf-8" or "base64").
// Returns a non-nil error if the bare clone is missing, unavailable, or the
// blob SHA cannot be resolved.
func (m *gitManager) loadBlobContentFromBareClone(ctx context.Context, branch Branch, blob Blob) (content, encoding string, err error) {
	if blob.SHA == "" {
		return "", "", fmt.Errorf("blob entity %s has no SHA", blob.ID)
	}

	// Retrieve the repository entity to find the bare_clone_path.
	repoEntity, err := m.dm.GetEntity(ctx, m.agencyID, branch.RepositoryID)
	if err != nil {
		return "", "", fmt.Errorf("get repository entity %s: %w", branch.RepositoryID, err)
	}
	bareClonePath, _ := repoEntity.Properties["bare_clone_path"].(string)
	if bareClonePath == "" {
		return "", "", fmt.Errorf("repository %s has no bare_clone_path (branch not yet fetched)", branch.RepositoryID)
	}

	// Open the bare clone — no network I/O.
	repo, err := gogit.PlainOpen(bareClonePath)
	if err != nil {
		return "", "", fmt.Errorf("open bare clone %q: %w", bareClonePath, err)
	}

	hash := gogitplumbing.NewHash(blob.SHA)
	blobObj, err := repo.BlobObject(hash)
	if err != nil {
		return "", "", fmt.Errorf("resolve blob %s in bare clone: %w", blob.SHA, err)
	}

	r, err := blobObj.Reader()
	if err != nil {
		return "", "", fmt.Errorf("open blob reader %s: %w", blob.SHA, err)
	}
	defer func() { _ = r.Close() }()

	raw, err := io.ReadAll(r)
	if err != nil {
		return "", "", fmt.Errorf("read blob %s: %w", blob.SHA, err)
	}

	// Detect encoding: treat as binary if the bytes are not valid UTF-8 or
	// contain a null byte (common heuristic used by git itself).
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		return base64.StdEncoding.EncodeToString(raw), "base64", nil
	}
	return string(raw), "utf-8", nil
}

// cacheBlobContent persists content and encoding back into the Blob entity so
// that subsequent ReadFile calls are served directly from the entity graph
// without hitting the bare clone.
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
	log.Printf("[cacheBlobContent] cached content for blobID=%q encoding=%q", blobID, encoding)
	return nil
}
