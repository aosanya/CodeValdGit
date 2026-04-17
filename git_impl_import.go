// git_impl_import.go implements [GitManager.ImportRepo], [GitManager.GetImportStatus],
// and [GitManager.CancelImport].
//
// ImportRepo begins an async background goroutine that:
//  1. Creates an ImportJob entity (status=pending) and returns immediately.
//  2. Clones the remote URL via go-git (PlainCloneContext) into a temp directory.
//  3. Walks all remote branches and their full commit histories.
//  4. Writes Branch, Commit, Tree, and Blob entities into the entity graph.
//  5. Creates the Repository entity and transitions the job to completed.
//
// A per-job cancel function is stored in an in-process map so that
// CancelImport can interrupt a running goroutine.
package codevaldgit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// importJobStatus values used as the "status" property of the ImportJob entity.
const (
	importStatusPending   = "pending"
	importStatusRunning   = "running"
	importStatusCompleted = "completed"
	importStatusFailed    = "failed"
	importStatusCancelled = "cancelled"
)

// importCancelEntry holds the cancel function for an in-flight import goroutine.
type importCancelEntry struct {
	cancel context.CancelFunc
}

// importJobsMu guards importJobs.
var importJobsMu sync.Mutex

// importJobs maps jobID → cancel entry for all active (pending/running) import goroutines.
// Goroutines remove their entry on completion, failure, or cancellation.
var importJobs = make(map[string]importCancelEntry)

// ImportRepo begins an async import of a public Git repository into this
// agency's entity graph. It returns immediately with an ImportJob whose
// ID can be used to poll [GitManager.GetImportStatus].
//
// Returns [ErrRepoAlreadyExists] if a Repository entity already exists.
// Returns [ErrImportInProgress] if a pending or running import already exists.
func (m *gitManager) ImportRepo(ctx context.Context, req ImportRepoRequest) (ImportJob, error) {

	// 1. Reject if a Repository with the same name already exists for this agency.
	existingRepos, err := m.listRepositories(ctx)
	if err != nil {
		return ImportJob{}, fmt.Errorf("ImportRepo %s: list repos: %w", m.agencyID, err)
	}
	for _, r := range existingRepos {
		if strProp(r.Properties, "name") == req.Name {
			return ImportJob{}, ErrRepoAlreadyExists
		}
	}

	// 2. Reject if an active import job already exists.
	active, err := m.findActiveImportJob(ctx)
	if err != nil {
		return ImportJob{}, fmt.Errorf("ImportRepo %s: check active job: %w", m.agencyID, err)
	}
	if active != nil {
		return ImportJob{}, ErrImportInProgress
	}

	// 3. Create the ImportJob entity; capture the auto-assigned ID as jobID.
	now := time.Now().UTC().Format(time.RFC3339)
	jobEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "ImportJob",
		Properties: map[string]any{
			"agency_id":     m.agencyID,
			"source_url":    req.SourceURL,
			"status":        importStatusPending,
			"error_message": "",
			"created_at":    now,
			"updated_at":    now,
		},
	})
	if err != nil {
		return ImportJob{}, fmt.Errorf("ImportRepo %s: create job entity: %w", m.agencyID, err)
	}
	jobID := jobEntity.ID

	// 4. Snapshot the ImportJob from the entity BEFORE starting the goroutine.
	// importJobFromEntity reads jobEntity.Properties; the goroutine may later
	// call UpdateEntity which modifies the stored entity's property map.
	// Capturing the snapshot here avoids a concurrent map read/write race.
	job := importJobFromEntity(jobEntity)

	// 5. Start the background goroutine with its own cancellable context.
	jobCtx, cancel := context.WithCancel(context.Background())
	importJobsMu.Lock()
	importJobs[jobID] = importCancelEntry{cancel: cancel}
	importJobsMu.Unlock()

	defaultBranch := req.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	go m.runImport(jobCtx, jobID, req, defaultBranch)

	return job, nil
}

// GetImportStatus returns the current state of an import job.
// Returns [ErrImportJobNotFound] if no job with the given ID exists.
func (m *gitManager) GetImportStatus(ctx context.Context, jobID string) (ImportJob, error) {
	entity, err := m.dm.GetEntity(ctx, m.agencyID, jobID)
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return ImportJob{}, ErrImportJobNotFound
		}
		return ImportJob{}, fmt.Errorf("GetImportStatus %s: %w", jobID, err)
	}
	return importJobFromEntity(entity), nil
}

// CancelImport cancels a pending or running import job.
// Returns [ErrImportJobNotFound] if the job does not exist.
// Returns [ErrImportJobNotCancellable] if the job is already in a terminal state.
func (m *gitManager) CancelImport(ctx context.Context, jobID string) error {
	job, err := m.GetImportStatus(ctx, jobID)
	if err != nil {
		return err // ErrImportJobNotFound propagated
	}

	switch job.Status {
	case importStatusCompleted, importStatusFailed, importStatusCancelled:
		return ErrImportJobNotCancellable
	}

	// Signal the goroutine.
	importJobsMu.Lock()
	entry, ok := importJobs[jobID]
	importJobsMu.Unlock()
	if ok {
		entry.cancel()
	}

	// Update status to cancelled immediately (goroutine will also attempt this;
	// idempotency is ensured by the UpdateEntity patch semantics).
	return m.updateImportJobStatus(context.Background(), jobID, importStatusCancelled, "")
}

// ── background goroutine ─────────────────────────────────────────────────────

// runImport is the background goroutine started by [ImportRepo].
// It clones the remote URL, walks all branches and commits, writes entity
// graph entities, creates the Repository entity, and transitions the job to
// completed, failed, or cancelled.
func (m *gitManager) runImport(ctx context.Context, jobID string, req ImportRepoRequest, defaultBranch string) {
	defer func() {
		importJobsMu.Lock()
		delete(importJobs, jobID)
		importJobsMu.Unlock()
	}()

	// Transition to running.
	if err := m.updateImportJobStatus(ctx, jobID, importStatusRunning, ""); err != nil {
		log.Printf("[ERROR] ImportJob %s: transition to running: %v", jobID, err)
		return
	}

	// Clone into a temp directory.
	tempDir, err := os.MkdirTemp("", "codevaldgit-import-*")
	if err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("create temp dir: %v", err))
		return
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	cloneOpts := &gogit.CloneOptions{
		URL:          req.SourceURL,
		Tags:         gogit.AllTags,
		SingleBranch: false,
	}

	repo, err := gogit.PlainCloneContext(ctx, tempDir, false, cloneOpts)
	if err != nil {
		if ctx.Err() != nil {
			_ = m.updateImportJobStatus(context.Background(), jobID, importStatusCancelled, "")
			_ = m.publishImportEvent(context.Background(), "cross.git.%s.repo.import.cancelled")
			return
		}

		m.failImportJob(ctx, jobID, fmt.Sprintf("clone %s: %v", req.SourceURL, err))
		return
	}

	// Walk all remote references (branches).
	refs, err := repo.References()
	if err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("list refs: %v", err))
		return
	}

	if err := refs.ForEach(func(ref *gogitplumbing.Reference) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !ref.Name().IsBranch() && !ref.Name().IsRemote() {
			return nil
		}
		return m.walkBranchCommits(ctx, repo, ref)
	}); err != nil {
		if ctx.Err() != nil {
			_ = m.updateImportJobStatus(context.Background(), jobID, importStatusCancelled, "")
			_ = m.publishImportEvent(context.Background(), "cross.git.%s.repo.import.cancelled")
			return
		}
		m.failImportJob(ctx, jobID, fmt.Sprintf("walk refs: %v", err))
		return
	}

	// Create the Repository entity using the provided name and default branch.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":           req.Name,
			"description":    req.Description,
			"default_branch": defaultBranch,
			"created_at":     now,
			"updated_at":     now,
		},
	}); err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("create Repository entity: %v", err))
		return
	}

	// Publish success event and mark completed.
	_ = m.publishImportEvent(ctx, "cross.git.%s.repo.imported")
	if err := m.updateImportJobStatus(ctx, jobID, importStatusCompleted, ""); err != nil {
		log.Printf("[ERROR] ImportJob %s: transition to completed: %v", jobID, err)
	}

}

// walkBranchCommits upserts Branch, Commit, Tree, and Blob entities for every
// commit reachable from the given reference.
func (m *gitManager) walkBranchCommits(ctx context.Context, repo *gogit.Repository, ref *gogitplumbing.Reference) error {
	branchName := ref.Name().Short()
	now := time.Now().UTC().Format(time.RFC3339)

	// Upsert the Branch entity by name (Branch has no UniqueKey, so we list+create/update).
	existing, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Branch",
		Properties: map[string]any{"name": branchName},
	})
	if err != nil {
		return fmt.Errorf("upsert branch %s: list: %w", branchName, err)
	}
	if len(existing) > 0 {
		if _, err := m.dm.UpdateEntity(ctx, m.agencyID, existing[0].ID, entitygraph.UpdateEntityRequest{
			Properties: map[string]any{
				"sha":        ref.Hash().String(),
				"updated_at": now,
			},
		}); err != nil {
			return fmt.Errorf("upsert branch %s: update: %w", branchName, err)
		}
	} else {
		if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: m.agencyID,
			TypeID:   "Branch",
			Properties: map[string]any{
				"name":       branchName,
				"sha":        ref.Hash().String(),
				"created_at": now,
				"updated_at": now,
			},
		}); err != nil {
			return fmt.Errorf("upsert branch %s: create: %w", branchName, err)
		}
	}

	// Walk commits oldest-to-newest.
	logOpts := &gogit.LogOptions{
		From:  ref.Hash(),
		Order: gogit.LogOrderCommitterTime,
	}
	commitIter, err := repo.Log(logOpts)
	if err != nil {
		return fmt.Errorf("log branch %s: %w", branchName, err)
	}
	defer commitIter.Close()

	var commitCount int
	if err := commitIter.ForEach(func(c *gogitobject.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		commitCount++
		return m.upsertCommitAndTree(ctx, repo, c)
	}); err != nil {
		return err
	}
	return nil
}

// upsertCommitAndTree upserts Commit, Tree, and Blob entities for a single commit.
// Commit is immutable (content-addressed by SHA); ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertCommitAndTree(ctx context.Context, repo *gogit.Repository, c *gogitobject.Commit) error {
	now := time.Now().UTC().Format(time.RFC3339)
	commitSHA := c.Hash.String()

	// Create Commit entity; skip if this SHA was already ingested.
	if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Commit",
		Properties: map[string]any{
			"sha":             commitSHA,
			"message":         c.Message,
			"author_name":     c.Author.Name,
			"author_email":    c.Author.Email,
			"author_at":       c.Author.When.UTC().Format(time.RFC3339),
			"committer_name":  c.Committer.Name,
			"committer_email": c.Committer.Email,
			"committed_at":    c.Committer.When.UTC().Format(time.RFC3339),
			"created_at":      now,
		},
	}); err != nil && !errors.Is(err, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create commit %s: %w", commitSHA, err)
	}

	// Walk the commit tree.
	tree, err := c.Tree()
	if err != nil {
		return fmt.Errorf("tree for commit %s: %w", commitSHA, err)
	}
	return m.upsertTree(ctx, repo, tree, "", now)
}

// upsertTree upserts Tree and Blob entities for a go-git Tree.
// pathPrefix is the directory path within the commit tree — empty string for the
// root tree, e.g. "src/handlers" for a nested subtree.
// Both Tree and Blob are treated as content-addressed (immutable by SHA);
// ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertTree(ctx context.Context, repo *gogit.Repository, tree *gogitobject.Tree, pathPrefix, now string) error {
	treeSHA := tree.Hash.String()

	// Create Tree entity; skip if SHA already ingested.
	if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Tree",
		Properties: map[string]any{
			"sha":        treeSHA,
			"path":       pathPrefix,
			"created_at": now,
		},
	}); err != nil && !errors.Is(err, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create tree %s: %w", treeSHA, err)
	}

	for _, entry := range tree.Entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Build the full relative path for this entry.
		var entryPath string
		if pathPrefix == "" {
			entryPath = entry.Name
		} else {
			entryPath = pathPrefix + "/" + entry.Name
		}

		if entry.Mode.IsFile() {
			if err := m.upsertBlob(ctx, repo, entry, entryPath, now); err != nil {
				return err
			}
		} else {
			// Recurse into subdirectory trees.
			subTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return fmt.Errorf("resolve subtree %s path=%q: %w", entry.Hash.String(), entryPath, err)
			}
			if err := m.upsertTree(ctx, repo, subTree, entryPath, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// upsertBlob creates a Blob entity for a single tree entry.
// It fetches the blob object from the repository to populate size, encoding,
// content, and extension in addition to sha, path, and name.
// ErrEntityAlreadyExists is skipped (blobs are content-addressed by SHA).
func (m *gitManager) upsertBlob(ctx context.Context, repo *gogit.Repository, entry gogitobject.TreeEntry, fullPath, now string) error {
	blobSHA := entry.Hash.String()

	// Fetch the blob object so we can read its content and size.
	blobObj, err := repo.BlobObject(entry.Hash)
	if err != nil {
		return fmt.Errorf("read blob object %s path=%q: %w", blobSHA, fullPath, err)
	}

	reader, err := blobObj.Reader()
	if err != nil {
		return fmt.Errorf("open blob reader %s path=%q: %w", blobSHA, fullPath, err)
	}
	rawBytes, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return fmt.Errorf("read blob content %s path=%q: %w", blobSHA, fullPath, err)
	}

	size := blobObj.Size

	// Choose encoding: utf-8 for valid text, base64 for binary.
	var encoding, content string
	if utf8.Valid(rawBytes) {
		encoding = "utf-8"
		content = string(rawBytes)
	} else {
		encoding = "base64"
		content = base64.StdEncoding.EncodeToString(rawBytes)
	}

	// Derive extension from the filename (without leading dot; empty if none).
	ext := strings.TrimPrefix(filepath.Ext(entry.Name), ".")
	name := filepath.Base(fullPath)

	if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"sha":        blobSHA,
			"path":       fullPath,
			"name":       name,
			"extension":  ext,
			"size":       size,
			"encoding":   encoding,
			"content":    content,
			"created_at": now,
		},
	}); err != nil && !errors.Is(err, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create blob %s path=%q: %w", blobSHA, fullPath, err)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// findActiveImportJob returns the first ImportJob entity with status pending
// or running for this agency, or nil if none exists.
func (m *gitManager) findActiveImportJob(ctx context.Context) (*ImportJob, error) {
	for _, status := range []string{importStatusPending, importStatusRunning} {
		entities, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   m.agencyID,
			TypeID:     "ImportJob",
			Properties: map[string]any{"status": status},
		})
		if err != nil {
			return nil, err
		}
		if len(entities) > 0 {
			job := importJobFromEntity(entities[0])
			return &job, nil
		}
	}
	return nil, nil
}

// updateImportJobStatus transitions an ImportJob entity to the given status.
func (m *gitManager) updateImportJobStatus(ctx context.Context, jobID, status, errMsg string) error {
	_, err := m.dm.UpdateEntity(ctx, m.agencyID, jobID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"status":        status,
			"error_message": errMsg,
			"updated_at":    time.Now().UTC().Format(time.RFC3339),
		},
	})
	return err
}

// failImportJob transitions the job to failed, logs, and publishes the failure event.
func (m *gitManager) failImportJob(ctx context.Context, jobID, errMsg string) {
	log.Printf("[ERROR] ImportJob %s failed: %s", jobID, errMsg)
	if err := m.updateImportJobStatus(ctx, jobID, importStatusFailed, errMsg); err != nil {
		log.Printf("[ERROR] ImportJob %s: write failed status: %v", jobID, err)
	}
	_ = m.publishImportEvent(ctx, "cross.git.%s.repo.import.failed")
}

// publishImportEvent publishes a Cross event for this agency using the provided
// topic format string (must contain one %s placeholder for agencyID).
func (m *gitManager) publishImportEvent(ctx context.Context, topicFmt string) error {
	if m.publisher == nil {
		return nil
	}
	topic := fmt.Sprintf(topicFmt, m.agencyID)
	return m.publisher.Publish(ctx, topic, m.agencyID)
}

// importJobFromEntity converts an [entitygraph.Entity] to an [ImportJob] value type.
func importJobFromEntity(e entitygraph.Entity) ImportJob {
	str := func(key string) string {
		v, _ := e.Properties[key].(string)
		return v
	}
	return ImportJob{
		ID:           e.ID,
		AgencyID:     str("agency_id"),
		SourceURL:    str("source_url"),
		Status:       str("status"),
		ErrorMessage: str("error_message"),
		CreatedAt:    str("created_at"),
		UpdatedAt:    str("updated_at"),
	}
}
