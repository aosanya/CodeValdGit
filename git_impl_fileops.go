// File operations and commit history implementations for [gitManager].
//
// WriteFile creates Commit, Tree, and Blob entities, wires the graph edges,
// and advances the branch HEAD pointer. ReadFile, DeleteFile, and
// ListDirectory traverse the commit + tree graph to locate blobs.
// Log walks the has_parent chain; Diff compares two commit trees.
package codevaldgit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ── File Operations ───────────────────────────────────────────────────────────

// treeRecord holds the git object data for one tree node built by buildNestedTrees.
type treeRecord struct {
	path    string // directory path: "" = root, "lib", "lib/providers", …
	sha     string
	rawData []byte
	size    int64
	entries string // JSON [{name,mode,sha}] for entity graph queries
}

// buildNestedTrees constructs a complete, properly-nested git tree from a flat
// map of full file paths to blob hashes. It processes directories bottom-up so
// subtree hashes are known before parent trees are encoded.
// Returns the root tree hash and one treeRecord per directory.
func buildNestedTrees(files map[string]plumbing.Hash) (plumbing.Hash, []treeRecord, error) {
	// Collect every directory that appears as an ancestor of any file path.
	dirSet := map[string]bool{"": true}
	for p := range files {
		parts := strings.Split(p, "/")
		for i := 1; i < len(parts); i++ {
			dirSet[strings.Join(parts[:i], "/")] = true
		}
	}

	// Sort directories deepest-first so subtrees are built before their parents.
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		di := strings.Count(dirs[i], "/")
		dj := strings.Count(dirs[j], "/")
		if di != dj {
			return di > dj
		}
		return dirs[i] > dirs[j]
	})

	subtreeHashes := map[string]plumbing.Hash{} // dir path → encoded tree hash
	var records []treeRecord

	for _, dir := range dirs {
		var entries []object.TreeEntry

		// File entries directly inside this directory.
		for filePath, blobHash := range files {
			if dirPath(filePath) == dir {
				entries = append(entries, object.TreeEntry{
					Name: fileName(filePath),
					Mode: filemode.Regular,
					Hash: blobHash,
				})
			}
		}

		// Immediate subdirectory entries (already encoded in earlier iterations).
		for subPath, subHash := range subtreeHashes {
			if dirPath(subPath) == dir {
				entries = append(entries, object.TreeEntry{
					Name: fileName(subPath),
					Mode: filemode.Dir,
					Hash: subHash,
				})
			}
		}

		// Git requires tree entries sorted by name.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name < entries[j].Name
		})

		treeObj := &object.Tree{Entries: entries}
		memObj := &plumbing.MemoryObject{}
		if err := treeObj.Encode(memObj); err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("buildNestedTrees: encode %q: %w", dir, err)
		}
		r, _ := memObj.Reader()
		raw, _ := io.ReadAll(r)
		r.Close()

		h := memObj.Hash()
		subtreeHashes[dir] = h

		entryMaps := make([]map[string]string, len(entries))
		for i, e := range entries {
			mode := "100644"
			if e.Mode == filemode.Dir {
				mode = "040000"
			}
			entryMaps[i] = map[string]string{"name": e.Name, "mode": mode, "sha": e.Hash.String()}
		}
		entriesJSON, _ := json.Marshal(entryMaps)

		records = append(records, treeRecord{
			path:    dir,
			sha:     h.String(),
			rawData: raw,
			size:    memObj.Size(),
			entries: string(entriesJSON),
		})
	}

	rootHash, ok := subtreeHashes[""]
	if !ok {
		return plumbing.ZeroHash, nil, fmt.Errorf("buildNestedTrees: root tree missing")
	}
	return rootHash, records, nil
}

// WriteFile commits a single file to the specified branch.
// Each call builds a complete nested git tree that includes all files from
// the parent commit plus the new file, so the branch accumulates files
// correctly across successive writes.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrRepoNotInitialised] if no repository entity exists.
func (m *gitManager) WriteFile(ctx context.Context, req WriteFileRequest) (Commit, error) {
	branch, err := m.GetBranch(ctx, req.BranchID)
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: %w", err)
	}
	repo, err := m.GetRepository(ctx, branch.RepositoryID)
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: %w", err)
	}

	// Normalise the path: strip leading slashes.
	req.Path = strings.TrimLeft(req.Path, "/")

	encoding := req.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}
	message := req.Message
	if message == "" {
		message = "Update " + req.Path
	}
	commitTime := time.Now().UTC()
	now := commitTime.Format(time.RFC3339)

	// ── 1. Create the new Blob entity ─────────────────────────────────────────
	blobObj := &plumbing.MemoryObject{}
	blobObj.SetType(plumbing.BlobObject)
	blobW, _ := blobObj.Writer()
	_, _ = blobW.Write([]byte(req.Content))
	_ = blobW.Close()
	blobR, _ := blobObj.Reader()
	blobRaw, _ := io.ReadAll(blobR)
	_ = blobR.Close()
	blobDataB64 := base64.StdEncoding.EncodeToString(blobRaw)
	blobHash := blobObj.Hash()
	blobSHA := blobHash.String()

	blobEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"sha":        blobSHA,
			"path":       req.Path,
			"name":       fileName(req.Path),
			"extension":  fileExtension(req.Path),
			"size":       int64(len(req.Content)),
			"encoding":   encoding,
			"content":    req.Content,
			"data":       blobDataB64,
			"created_at": now,
		},
	})
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: create blob: %w", err)
	}

	// ── 2. Build the complete file map (parent files + new file) ──────────────
	var parentIDs []string
	var parentHashes []plumbing.Hash
	if branch.HeadCommitID != "" {
		parentIDs = []string{branch.HeadCommitID}
		parentEntity, err := m.dm.GetEntity(ctx, m.agencyID, branch.HeadCommitID)
		if err != nil {
			return Commit{}, fmt.Errorf("WriteFile: get parent commit: %w", err)
		}
		if sha := entitygraph.StringProp(parentEntity.Properties, "sha"); sha != "" {
			parentHashes = []plumbing.Hash{plumbing.NewHash(sha)}
		}
	}

	// path → blob git SHA for all files on the branch after this write.
	fileMap := map[string]plumbing.Hash{}
	// path → blob entity ID for wiring has_blob edges to the new root tree.
	blobEntityByPath := map[string]string{}

	if len(parentIDs) > 0 {
		parentBlobs, err := m.allBlobsAtCommit(ctx, parentIDs[0])
		if err != nil {
			log.Printf("WriteFile: allBlobsAtCommit parent=%s: %v (continuing with empty parent)", parentIDs[0], err)
		}
		for _, b := range parentBlobs {
			if b.Path != "" && b.SHA != "" {
				fileMap[b.Path] = plumbing.NewHash(b.SHA)
				blobEntityByPath[b.Path] = b.ID
			}
		}
	}
	// Add/replace with the file being written.
	fileMap[req.Path] = blobHash
	blobEntityByPath[req.Path] = blobEntity.ID

	// ── 3. Build nested git trees ─────────────────────────────────────────────
	rootTreeHash, treeRecords, err := buildNestedTrees(fileMap)
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: %w", err)
	}

	// ── 4. Persist all tree entities (root + subtrees) ────────────────────────
	var rootTreeEntityID string
	for _, tr := range treeRecords {
		te, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: m.agencyID,
			TypeID:   "Tree",
			Properties: map[string]any{
				"sha":        tr.sha,
				"path":       tr.path,
				"entries":    tr.entries,
				"data":       base64.StdEncoding.EncodeToString(tr.rawData),
				"size":       tr.size,
				"created_at": now,
			},
		})
		if err != nil {
			return Commit{}, fmt.Errorf("WriteFile: create tree entity path=%q: %w", tr.path, err)
		}
		if tr.sha == rootTreeHash.String() {
			rootTreeEntityID = te.ID
		}
	}

	// ── 5. Encode and persist the Commit entity ───────────────────────────────
	gitCommitObj := &object.Commit{
		TreeHash:     rootTreeHash,
		ParentHashes: parentHashes,
		Author:       object.Signature{Name: req.AuthorName, Email: req.AuthorEmail, When: commitTime},
		Committer:    object.Signature{Name: req.AuthorName, Email: req.AuthorEmail, When: commitTime},
		Message:      message,
	}
	commitMemObj := &plumbing.MemoryObject{}
	if err := gitCommitObj.Encode(commitMemObj); err != nil {
		return Commit{}, fmt.Errorf("WriteFile: encode commit: %w", err)
	}
	commitR, _ := commitMemObj.Reader()
	commitRaw, _ := io.ReadAll(commitR)
	_ = commitR.Close()
	commitDataB64 := base64.StdEncoding.EncodeToString(commitRaw)
	commitSHA := commitMemObj.Hash().String()

	commitRels := []entitygraph.EntityRelationshipRequest{
		{Name: "belongs_to_repository", ToID: repo.ID},
		{Name: "has_tree", ToID: rootTreeEntityID},
	}
	if len(parentIDs) > 0 {
		commitRels = append(commitRels, entitygraph.EntityRelationshipRequest{Name: "has_parent", ToID: parentIDs[0]})
	}

	commitEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Commit",
		Properties: map[string]any{
			"sha":             commitSHA,
			"message":         message,
			"author_name":     req.AuthorName,
			"author_email":    req.AuthorEmail,
			"author_at":       now,
			"committer_name":  req.AuthorName,
			"committer_email": req.AuthorEmail,
			"committed_at":    now,
			"created_at":      now,
			"data":            commitDataB64,
			"size":            commitMemObj.Size(),
		},
		Relationships: commitRels,
	})
	if err != nil {
		return Commit{}, fmt.Errorf("WriteFile: create commit: %w", err)
	}

	// ── 6. Wire edges ─────────────────────────────────────────────────────────
	for _, rel := range []struct{ name, from, to string }{
		{"has_tree", commitEntity.ID, rootTreeEntityID},
		{"belongs_to_commit", rootTreeEntityID, commitEntity.ID},
	} {
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID, Name: rel.name, FromID: rel.from, ToID: rel.to,
		}); err != nil {
			return Commit{}, fmt.Errorf("WriteFile: link %s: %w", rel.name, err)
		}
	}
	if len(parentIDs) > 0 {
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID, Name: "has_parent", FromID: commitEntity.ID, ToID: parentIDs[0],
		}); err != nil {
			return Commit{}, fmt.Errorf("WriteFile: link has_parent: %w", err)
		}
	}
	// Wire root tree → has_blob → every blob so allBlobsAtCommit finds them all.
	for path, blobEntityID := range blobEntityByPath {
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID, Name: "has_blob", FromID: rootTreeEntityID, ToID: blobEntityID,
		}); err != nil {
			log.Printf("WriteFile: link has_blob path=%q: %v (non-fatal)", path, err)
		}
	}

	// ── 7. Advance branch HEAD ────────────────────────────────────────────────
	if _, err := m.advanceBranchHead(ctx, branch.ID, commitEntity.ID, ""); err != nil {
		return Commit{}, fmt.Errorf("WriteFile: advance branch head: %w", err)
	}

	return entityToCommit(commitEntity, repo.ID, parentIDs), nil
}

// ReadFile retrieves the Blob entity for a file at the branch's current HEAD.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path does not exist on the branch.
// ReadFile returns the content of path at the branch's HEAD commit.
// It first checks whether the blob entity already carries cached content.
// If the content field is empty (stub blob created by FetchBranch), it reads
// the content directly from the bare clone and caches it back into the entity.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path is not present on the branch.
// Returns [ErrBlobContentUnavailable] if the blob exists as a stub but the
// bare clone is unavailable; the caller should trigger [GitManager.FetchBranch]
// and retry.
func (m *gitManager) ReadFile(ctx context.Context, branchID, path string) (Blob, error) {
	log.Printf("[ReadFile] branchID=%s path=%q", branchID, path)
	branch, err := m.GetBranch(ctx, branchID)
	if err != nil {
		log.Printf("[ReadFile] GetBranch error: %v", err)
		return Blob{}, fmt.Errorf("ReadFile: %w", err)
	}
	log.Printf("[ReadFile] branch name=%q headCommitID=%q repoID=%q", branch.Name, branch.HeadCommitID, branch.RepositoryID)
	if branch.HeadCommitID == "" {
		log.Printf("[ReadFile] branch has no headCommitID — returning ErrFileNotFound")
		return Blob{}, ErrFileNotFound
	}
	blob, err := m.findBlobAtCommit(ctx, branch.HeadCommitID, path)
	if err != nil {
		log.Printf("[ReadFile] findBlobAtCommit error: %v", err)
		return Blob{}, fmt.Errorf("ReadFile: %w", err)
	}
	log.Printf("[ReadFile] found blob id=%s sha=%q contentLen=%d", blob.ID, blob.SHA, len(blob.Content))

	// Fast path: content is already cached in the entity graph.
	if blob.Content != "" {
		log.Printf("[ReadFile] fast path — content already cached")
		return blob, nil
	}

	// Lazy path: blob was written as metadata-only (no content field).
	// Hydrate from the backend storer (ArangoDB or filesystem).
	log.Printf("[ReadFile] lazy path — blob content empty, hydrating from storer (sha=%s)", blob.SHA)
	content, encoding, loadErr := m.loadBlobContentFromStorer(ctx, branch, blob)
	if loadErr != nil {
		log.Printf("[ReadFile] loadBlobContentFromStorer error: %v", loadErr)
		return Blob{}, ErrBlobContentUnavailable
	}
	log.Printf("[ReadFile] hydrated blob sha=%s encoding=%q contentLen=%d", blob.SHA, encoding, len(content))

	blob.Content = content
	blob.Encoding = encoding
	return blob, nil
}

// DeleteFile removes a file from the specified branch by creating a deletion
// commit (empty content, size=0).
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrFileNotFound] if the path does not exist on the branch.
func (m *gitManager) DeleteFile(ctx context.Context, req DeleteFileRequest) (Commit, error) {
	// Verify the file exists first and capture the current blob ID for edge cleanup.
	existingBlob, err := m.ReadFile(ctx, req.BranchID, req.Path)
	if err != nil {
		return Commit{}, err
	}

	// GIT-022c: Remove branch-scoped documentation edges on the deleted blob
	// before the deletion commit is written.
	m.deleteDocEdgesForBlob(ctx, existingBlob.ID, req.BranchID)

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
		return []FileEntry{}, nil
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
	log.Printf("[findBlobAtCommit] commitID=%s path=%q", commitID, path)
	blobs, err := m.allBlobsAtCommit(ctx, commitID)
	if err != nil {
		log.Printf("[findBlobAtCommit] allBlobsAtCommit error: %v", err)
		return Blob{}, err
	}
	log.Printf("[findBlobAtCommit] %d blobs found at commit", len(blobs))
	for _, b := range blobs {
		if b.Path == path {
			log.Printf("[findBlobAtCommit] matched blob id=%s sha=%s", b.ID, b.SHA)
			return b, nil
		}
	}
	log.Printf("[findBlobAtCommit] path %q not found among blobs", path)
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
		if entitygraph.StringProp(c.Properties, "sha") == ref {
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
		SHA:            entitygraph.StringProp(p, "sha"),
		Message:        entitygraph.StringProp(p, "message"),
		AuthorName:     entitygraph.StringProp(p, "author_name"),
		AuthorEmail:    entitygraph.StringProp(p, "author_email"),
		AuthorAt:       entitygraph.StringProp(p, "author_at"),
		CommitterName:  entitygraph.StringProp(p, "committer_name"),
		CommitterEmail: entitygraph.StringProp(p, "committer_email"),
		CommittedAt:    entitygraph.StringProp(p, "committed_at"),
		ParentIDs:      parentIDs,
		CreatedAt:      entitygraph.StringProp(p, "created_at"),
	}
}

// entityToBlob maps an entitygraph.Entity of type "Blob" to [Blob].
func entityToBlob(e entitygraph.Entity) Blob {
	p := e.Properties
	return Blob{
		ID:        e.ID,
		SHA:       entitygraph.StringProp(p, "sha"),
		Path:      entitygraph.StringProp(p, "path"),
		Name:      entitygraph.StringProp(p, "name"),
		Extension: entitygraph.StringProp(p, "extension"),
		Size:      entitygraph.Int64Prop(p, "size"),
		Encoding:  entitygraph.StringProp(p, "encoding"),
		Content:   entitygraph.StringProp(p, "content"),
		CreatedAt: entitygraph.StringProp(p, "created_at"),
	}
}

// commitToEntry converts a Commit entity to a [CommitEntry] for Log output.
func commitToEntry(e entitygraph.Entity) CommitEntry {
	p := e.Properties
	ts, _ := time.Parse(time.RFC3339, entitygraph.StringProp(p, "committed_at"))
	if ts.IsZero() {
		ts, _ = time.Parse(time.RFC3339, entitygraph.StringProp(p, "author_at"))
	}
	if ts.IsZero() {
		ts = e.CreatedAt
	}
	return CommitEntry{
		SHA:       entitygraph.StringProp(p, "sha"),
		Author:    entitygraph.StringProp(p, "author_name"),
		Message:   entitygraph.StringProp(p, "message"),
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

// fileExtension returns the file extension without the leading dot, e.g. "txt".
// Returns an empty string for files with no extension or dotfiles (e.g. ".gitignore").
func fileExtension(p string) string {
	name := fileName(p)
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return ""
	}
	return name[idx+1:]
}
