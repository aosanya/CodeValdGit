// File operations and commit history implementations for [gitManager].
//
// WriteFile creates Commit, Tree, and Blob entities, wires the graph edges,
// and advances the branch HEAD pointer. ReadFile, DeleteFile, and
// ListDirectory traverse the commit + tree graph to locate blobs.
// Log walks the has_parent chain; Diff compares two commit trees.
package codevaldgit

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 matches Git's content-addressing convention.
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── File Operations ───────────────────────────────────────────────────────────

// WriteFile commits a single file to the specified branch.
// It creates Blob, Tree, and Commit entities, wires the graph edges, and
// advances the branch HEAD pointer to the new commit.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) WriteFile(ctx context.Context, req WriteFileRequest) (Commit, error) {
	branch, err := m.GetBranch(ctx, req.BranchID)
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: %w", err)
	}
	repo, err := m.GetRepository(ctx)
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: %w", err)
	}

	encoding := req.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}
	message := req.Message
	if message == "" {
		message = "Update " + req.Path
	}
	now := time.Now().UTC().Format(time.RFC3339)

	blobSHA := contentSHA(req.Content)
	treeDir := dirPath(req.Path)
	treeSHA := contentSHA(treeDir + ":" + req.Path + ":" + blobSHA)

	// 1. Create the Blob entity.
	blobEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"sha":        blobSHA,
			"path":       req.Path,
			"size":       int64(len(req.Content)),
			"encoding":   encoding,
			"content":    req.Content,
			"created_at": now,
		},
	})
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: create blob: %w", err)
	}

	// 2. Create a Tree entity for the directory containing the file.
	treeEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Tree",
		Properties: map[string]any{
			"sha":        treeSHA,
			"path":       treeDir,
			"created_at": now,
		},
	})
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: create tree: %w", err)
	}

	// Tree → has_blob → Blob (also creates inverse belongs_to_tree).
	if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "has_blob",
		FromID:   treeEntity.ID,
		ToID:     blobEntity.ID,
	}); err != nil {
		return Commit{}, fmt.Errorf("WriteFile: link blob to tree: %w", err)
	}

	// 3. Compute commit SHA and create the Commit entity.
	var parentIDs []string
	if branch.HeadCommitID != "" {
		parentIDs = []string{branch.HeadCommitID}
	}
	commitSHA := commitSHA(treeSHA, branch.HeadCommitID, req.Message, now)

	commitProps := map[string]any{
		"sha":             commitSHA,
		"message":         message,
		"author_name":     req.AuthorName,
		"author_email":    req.AuthorEmail,
		"author_at":       now,
		"committer_name":  req.AuthorName,
		"committer_email": req.AuthorEmail,
		"committed_at":    now,
		"created_at":      now,
	}

	commitRels := []entitygraph.EntityRelationshipRequest{
		{Name: "belongs_to_repository", ToID: repo.ID},
		{Name: "has_tree", ToID: treeEntity.ID},
	}
	if len(parentIDs) > 0 {
		commitRels = append(commitRels, entitygraph.EntityRelationshipRequest{
			Name: "has_parent",
			ToID: parentIDs[0],
		})
	}

	commitEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID:      m.agencyID,
		TypeID:        "Commit",
		Properties:    commitProps,
		Relationships: commitRels,
	})
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: create commit: %w", err)
	}

	// Tree → belongs_to_commit (root tree).
	if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     "belongs_to_commit",
		FromID:   treeEntity.ID,
		ToID:     commitEntity.ID,
	}); err != nil {
		return Commit{}, fmt.Errorf("WriteFile: link tree to commit: %w", err)
	}

	// 4. Advance branch HEAD.
	if _, err := m.advanceBranchHead(ctx, branch.ID, commitEntity.ID); err != nil {
		return Commit{}, fmt.Errorf("WriteFile: advance branch head: %w", err)
	}

	return entityToCommit(commitEntity, repo.ID, parentIDs), nil
}

// ReadFile retrieves the Blob entity for a file at the branch's current HEAD.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path does not exist on the branch.
func (m *gitManager) ReadFile(ctx context.Context, branchID, path string) (Blob, error) {
	branch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		return Blob{}, fmt.Errorf("ReadFile: %w", err)
	}
	if branch.HeadCommitID == "" {
		return Blob{}, ErrFileNotFound
	}
	blob, err := m.findBlobAtCommit(ctx, branch.HeadCommitID, path)
	if err != nil {
		return Blob{}, fmt.Errorf("ReadFile: %w", err)
	}
	return blob, nil
}

// DeleteFile removes a file from the specified branch by creating a deletion
// commit (empty content, size=0).
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path does not exist on the branch.
func (m *gitManager) DeleteFile(ctx context.Context, req DeleteFileRequest) (Commit, error) {
	// Verify the file exists first.
	if _, err := m.ReadFile(ctx, req.BranchID, req.Path); err != nil {
		return Commit{}, err
	}
	message := req.Message
	if message == "" {
		message = "Delete " + req.Path
	}
	// A deletion commit writes empty content to the path.
	return m.WriteFile(ctx, WriteFileRequest{
		BranchID:    req.BranchID,
		Path:        req.Path,
		Content:     "",
		Encoding:    "utf-8",
		AuthorName:  req.AuthorName,
		AuthorEmail: req.AuthorEmail,
		Message:     message,
	})
}

// ListDirectory returns the immediate children (files and sub-directories)
// at the given path on the branch's HEAD commit.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path does not exist on the branch.
func (m *gitManager) ListDirectory(ctx context.Context, branchID, path string) ([]FileEntry, error) {
	branch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		return nil, fmt.Errorf("ListDirectory: %w", err)
	}
	if branch.HeadCommitID == "" {
		return nil, ErrFileNotFound
	}

	// Find all blobs reachable from the HEAD commit.
	blobs, err := m.allBlobsAtCommit(ctx, branch.HeadCommitID)
	if err != nil {
		return nil, fmt.Errorf("ListDirectory: %w", err)
	}

	// Normalise the query path: trim leading/trailing slashes.
	queryDir := strings.Trim(path, "/")

	seen := make(map[string]bool)
	var entries []FileEntry
	for _, b := range blobs {
		bPath := b.Path
		bDir := dirPath(bPath)
		bDirClean := strings.Trim(bDir, "/")

		if queryDir == "" {
			// Root listing — show immediate children only.
			rel := bPath
			// If the file is in a subdirectory, show the directory entry.
			parts := strings.SplitN(strings.Trim(rel, "/"), "/", 2)
			if len(parts) == 1 {
				// Direct file at root.
				if !seen[parts[0]] {
					seen[parts[0]] = true
					entries = append(entries, FileEntry{
						Name:  parts[0],
						Path:  parts[0],
						IsDir: false,
						Size:  b.Size,
					})
				}
			} else {
				// Subdirectory entry.
				if !seen[parts[0]] {
					seen[parts[0]] = true
					entries = append(entries, FileEntry{
						Name:  parts[0],
						Path:  parts[0],
						IsDir: true,
					})
				}
			}
		} else {
			// Subdirectory listing.
			if bDirClean == queryDir {
				name := fileName(bPath)
				if !seen[name] {
					seen[name] = true
					entries = append(entries, FileEntry{
						Name:  name,
						Path:  bPath,
						IsDir: false,
						Size:  b.Size,
					})
				}
			} else if strings.HasPrefix(bDirClean, queryDir+"/") {
				// Deeper subdirectory — show intermediate directory.
				rel := bDirClean[len(queryDir)+1:]
				topLevel := strings.SplitN(rel, "/", 2)[0]
				if !seen[topLevel] {
					seen[topLevel] = true
					entries = append(entries, FileEntry{
						Name:  topLevel,
						Path:  queryDir + "/" + topLevel,
						IsDir: true,
					})
				}
			}
		}
	}
	if len(blobs) > 0 && len(entries) == 0 && queryDir != "" {
		return nil, ErrFileNotFound
	}
	return entries, nil
}

// ── History ───────────────────────────────────────────────────────────────────

// Log returns the commit history for the branch, newest to oldest.
// Optionally filtered to a specific file path via filter.Path.
// Returns [ErrBranchNotFound] if the branch does not exist.
func (m *gitManager) Log(ctx context.Context, branchID string, filter LogFilter) ([]CommitEntry, error) {
	branch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		return nil, fmt.Errorf("Log: %w", err)
	}
	if branch.HeadCommitID == "" {
		return nil, nil
	}

	commits, err := m.walkCommitChain(ctx, branch.HeadCommitID, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("Log: %w", err)
	}

	out := make([]CommitEntry, 0, len(commits))
	for _, c := range commits {
		if filter.Path != "" {
			// Check if this commit touched the requested path.
			if !m.commitTouchesPath(ctx, c.ID, filter.Path) {
				continue
			}
		}
		out = append(out, commitToEntry(c))
	}
	return out, nil
}

// Diff returns per-file change summaries between two refs (branch IDs or commit IDs).
// Returns [ErrRefNotFound] if either ref cannot be resolved.
func (m *gitManager) Diff(ctx context.Context, fromRef, toRef string) ([]FileDiff, error) {
	fromCommitID, err := m.resolveRef(ctx, fromRef)
	if err != nil {
		return nil, fmt.Errorf("Diff: fromRef %s: %w", fromRef, ErrRefNotFound)
	}
	toCommitID, err := m.resolveRef(ctx, toRef)
	if err != nil {
		return nil, fmt.Errorf("Diff: toRef %s: %w", toRef, ErrRefNotFound)
	}

	fromBlobs, err := m.allBlobsAtCommit(ctx, fromCommitID)
	if err != nil {
		return nil, fmt.Errorf("Diff: from blobs: %w", err)
	}
	toBlobs, err := m.allBlobsAtCommit(ctx, toCommitID)
	if err != nil {
		return nil, fmt.Errorf("Diff: to blobs: %w", err)
	}

	return diffBlobs(fromBlobs, toBlobs), nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// findBlobAtCommit traverses the commit's tree(s) to find a blob matching path.
func (m *gitManager) findBlobAtCommit(ctx context.Context, commitID, path string) (Blob, error) {
	blobs, err := m.allBlobsAtCommit(ctx, commitID)
	if err != nil {
		return Blob{}, err
	}
	for _, b := range blobs {
		if b.Path == path {
			return b, nil
		}
	}
	return Blob{}, ErrFileNotFound
}

// allBlobsAtCommit returns all Blob entities reachable from the commit's tree.
func (m *gitManager) allBlobsAtCommit(ctx context.Context, commitID string) ([]Blob, error) {
	// Traverse outbound from commit: has_tree → has_blob / has_subtree.
	result, err := m.dm.TraverseGraph(ctx, entitygraph.TraverseGraphRequest{
		AgencyID:  m.agencyID,
		StartID:   commitID,
		Direction: "outbound",
		Depth:     5,
		Names:     []string{"has_tree", "has_blob", "has_subtree"},
	})
	if err != nil {
		return nil, err
	}
	var blobs []Blob
	for _, v := range result.Vertices {
		if v.TypeID == "Blob" {
			blobs = append(blobs, entityToBlob(v))
		}
	}
	return blobs, nil
}

// walkCommitChain walks has_parent edges from startCommitID, returning up to
// limit commits (0 = no limit) in newest-first order.
func (m *gitManager) walkCommitChain(ctx context.Context, startCommitID string, limit int) ([]entitygraph.Entity, error) {
	visited := make(map[string]bool)
	var result []entitygraph.Entity
	queue := []string{startCommitID}

	for len(queue) > 0 {
		if limit > 0 && len(result) >= limit {
			break
		}
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		e, err := m.dm.GetEntity(ctx, m.agencyID, current)
		if err != nil {
			continue
		}
		result = append(result, e)

		parents, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: m.agencyID,
			Name:     "has_parent",
			FromID:   current,
		})
		if err != nil {
			continue
		}
		for _, p := range parents {
			if !visited[p.ToID] {
				queue = append(queue, p.ToID)
			}
		}
	}
	return result, nil
}

// commitTouchesPath reports whether the commit's tree contains a blob at path.
func (m *gitManager) commitTouchesPath(ctx context.Context, commitID, path string) bool {
	_, err := m.findBlobAtCommit(ctx, commitID, path)
	return err == nil
}

// resolveRef resolves a branchID or commitID to a commit entity ID.
// It first tries GetBranch (to read HeadCommitID), then falls back to
// treating the ref as a raw commit entity ID.
func (m *gitManager) resolveRef(ctx context.Context, ref string) (string, error) {
	// Try as a branch ID first.
	branch, err := m.GetBranch(ctx, ref)
	if err == nil {
		if branch.HeadCommitID == "" {
			return "", fmt.Errorf("branch %s has no HEAD commit", ref)
		}
		return branch.HeadCommitID, nil
	}
	// Try as a commit entity ID directly.
	if _, err := m.dm.GetEntity(ctx, m.agencyID, ref); err == nil {
		return ref, nil
	}
	// Try as a SHA — scan all commits.
	commits, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Commit",
	})
	if err != nil {
		return "", err
	}
	for _, c := range commits {
		if strProp(c.Properties, "sha") == ref {
			return c.ID, nil
		}
	}
	return "", ErrRefNotFound
}

// ── Entity → domain converters ────────────────────────────────────────────────

// entityToCommit maps an entitygraph.Entity of type "Commit" to [Commit].
func entityToCommit(e entitygraph.Entity, repositoryID string, parentIDs []string) Commit {
	p := e.Properties
	return Commit{
		ID:             e.ID,
		RepositoryID:   repositoryID,
		SHA:            strProp(p, "sha"),
		Message:        strProp(p, "message"),
		AuthorName:     strProp(p, "author_name"),
		AuthorEmail:    strProp(p, "author_email"),
		AuthorAt:       strProp(p, "author_at"),
		CommitterName:  strProp(p, "committer_name"),
		CommitterEmail: strProp(p, "committer_email"),
		CommittedAt:    strProp(p, "committed_at"),
		ParentIDs:      parentIDs,
		CreatedAt:      strProp(p, "created_at"),
	}
}

// entityToBlob maps an entitygraph.Entity of type "Blob" to [Blob].
func entityToBlob(e entitygraph.Entity) Blob {
	p := e.Properties
	return Blob{
		ID:        e.ID,
		SHA:       strProp(p, "sha"),
		Path:      strProp(p, "path"),
		Size:      int64Prop(p, "size"),
		Encoding:  strProp(p, "encoding"),
		Content:   strProp(p, "content"),
		CreatedAt: strProp(p, "created_at"),
	}
}

// commitToEntry converts a Commit entity to a [CommitEntry] for Log output.
func commitToEntry(e entitygraph.Entity) CommitEntry {
	p := e.Properties
	ts, _ := time.Parse(time.RFC3339, strProp(p, "committed_at"))
	if ts.IsZero() {
		ts, _ = time.Parse(time.RFC3339, strProp(p, "author_at"))
	}
	if ts.IsZero() {
		ts = e.CreatedAt
	}
	return CommitEntry{
		SHA:       strProp(p, "sha"),
		Author:    strProp(p, "author_name"),
		Message:   strProp(p, "message"),
		Timestamp: ts,
	}
}

// diffBlobs computes added/modified/deleted file entries between two blob sets.
func diffBlobs(fromBlobs, toBlobs []Blob) []FileDiff {
	fromMap := make(map[string]Blob, len(fromBlobs))
	for _, b := range fromBlobs {
		fromMap[b.Path] = b
	}
	toMap := make(map[string]Blob, len(toBlobs))
	for _, b := range toBlobs {
		toMap[b.Path] = b
	}

	var diffs []FileDiff
	// Added or modified.
	for path, toBlob := range toMap {
		if fromBlob, ok := fromMap[path]; !ok {
			diffs = append(diffs, FileDiff{Path: path, Operation: "added"})
		} else if fromBlob.SHA != toBlob.SHA {
			diffs = append(diffs, FileDiff{Path: path, Operation: "modified"})
		}
	}
	// Deleted.
	for path := range fromMap {
		if _, ok := toMap[path]; !ok {
			diffs = append(diffs, FileDiff{Path: path, Operation: "deleted"})
		}
	}
	return diffs
}

// ── SHA helpers ───────────────────────────────────────────────────────────────

// contentSHA returns the Git-style SHA-1 hex digest for content.
// Uses SHA-1 to match Git's content-addressing convention.
//
//nolint:gosec
func contentSHA(content string) string {
	h := sha1.New() //nolint:gosec
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

// commitSHA computes a deterministic SHA-1 for a commit given its parts.
//
//nolint:gosec
func commitSHA(treeSHA, parentSHA, message, ts string) string {
	h := sha1.New() //nolint:gosec
	h.Write([]byte(treeSHA + parentSHA + message + ts))
	return hex.EncodeToString(h.Sum(nil))
}

// ── Path helpers ──────────────────────────────────────────────────────────────

// dirPath returns the directory component of a file path (empty for root files).
func dirPath(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return ""
	}
	return p[:idx]
}

// fileName returns the base name of a file path.
func fileName(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}
