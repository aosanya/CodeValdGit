// git_impl_import.go implements [GitManager.ImportRepo], [GitManager.GetImportStatus],
// and [GitManager.CancelImport].
//
// GIT-023c — Lazy Import v2 (Phase 1):
//
// ImportRepo begins an async background goroutine that:
//  1. Creates an ImportJob entity (status=pending) and returns immediately.
//  2. Performs a bare shallow clone (Depth=1, Bare=true, NoTags) into a
//     persistent directory under the agency's clone root.  The directory is
//     NOT cleaned up — FetchBranch reuses it for on-demand deepening.
//  3. Iterates remote refs to discover branches.
//  4. Writes one Repository entity (carrying bare_clone_path) and one stub
//     Branch entity per ref (status="stub"; no commits, trees, or blobs).
//  5. Transitions the job to completed.
//
// walkBranchCommits is retained for use by FetchBranch (GIT-023d).  It now
// accepts a seenSHAs set so shared commit history across branches is processed
// only once.
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

// branchStatus values for the Branch entity "status" property (lazy import v2).
const (
	branchStatusStub        = "stub"
	branchStatusFetching    = "fetching"
	branchStatusFetched     = "fetched"
	branchStatusFetchFailed = "fetch_failed"
)

// cloneRootDir returns the persistent directory that holds the bare clone for
// this import job.  If the directory already exists (e.g., from a previous
// failed run) it is removed and recreated so that PlainClone always starts
// with an empty target.
func cloneRootDir(agencyID, jobID string) (string, error) {
	base := filepath.Join(os.TempDir(), "codevaldgit-clones", agencyID, jobID)
	if err := os.RemoveAll(base); err != nil {
		return "", fmt.Errorf("cloneRootDir remove stale %s: %w", base, err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("cloneRootDir %s: %w", base, err)
	}
	return base, nil
}

// importCancelEntry holds the cancel function and in-memory progress log for an
// in-flight import goroutine. A pointer is stored in importJobs so that step
// messages can be appended without replacing the map entry.
type importCancelEntry struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	steps  []string
}

// appendStep adds a progress message to the entry.
func (e *importCancelEntry) appendStep(msg string) {
	e.mu.Lock()
	e.steps = append(e.steps, msg)
	e.mu.Unlock()
}

// getSteps returns a copy of the current progress steps.
func (e *importCancelEntry) getSteps() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.steps))
	copy(out, e.steps)
	return out
}

// importJobsMu guards importJobs.
var importJobsMu sync.Mutex

// importJobs maps jobID → cancel entry for all active (pending/running) import goroutines.
// Goroutines remove their entry on completion, failure, or cancellation.
var importJobs = make(map[string]*importCancelEntry)

// appendImportStep appends a progress message for the given job (no-op if terminal).
func appendImportStep(jobID, msg string) {
	importJobsMu.Lock()
	entry, ok := importJobs[jobID]
	importJobsMu.Unlock()
	if ok {
		entry.appendStep(msg)
	}
}

// ImportRepo begins an async import of a public Git repository into this
// agency's entity graph. It returns immediately with an ImportJob whose
// ID can be used to poll [GitManager.GetImportStatus].
//
// If a Repository with the same name already exists it will be overwritten
// (upsert semantics).
// Returns [ErrImportInProgress] if a pending or running import already exists.
func (m *gitManager) ImportRepo(ctx context.Context, req ImportRepoRequest) (ImportJob, error) {

	// 1. (Upsert) — no duplicate-repo check; reimporting overwrites entities.

	// 2. (Upsert) — no active-job check; a new import overwrites any prior state.

	// 3. Create the ImportJob entity; capture the auto-assigned ID as jobID.
	now := time.Now().UTC().Format(time.RFC3339)
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	jobEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "ImportJob",
		Properties: map[string]any{
			"agency_id":      m.agencyID,
			"name":           req.Name,
			"source_url":     req.SourceURL,
			"default_branch": req.DefaultBranch,
			"status":         importStatusPending,
			"error_message":  "",
			"created_at":     now,
			"updated_at":     now,
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
	entry := &importCancelEntry{cancel: cancel}
	importJobsMu.Lock()
	importJobs[jobID] = entry
	importJobsMu.Unlock()

	go m.runImport(jobCtx, jobID, req, req.DefaultBranch)

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
	job := importJobFromEntity(entity)
	// Attach in-memory progress steps (only present while goroutine is active).
	importJobsMu.Lock()
	entry, ok := importJobs[jobID]
	importJobsMu.Unlock()
	if ok {
		job.ProgressSteps = entry.getSteps()
	}
	return job, nil
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

// runImport is the background goroutine started by [ImportRepo].
//
// GIT-023c — Phase 1 (lazy import v2):
//
//  1. Bare shallow clone (Depth=1, Bare=true, NoTags) into a persistent directory.
//  2. Iterate remote refs to discover branch names and tip SHAs.
//  3. Create one Repository entity (with bare_clone_path) and one stub Branch
//     entity per discovered branch ref (status="stub").
//  4. Wire has_branch / belongs_to_repository edges.
//  5. Automatically trigger [FetchBranch] for the default branch so it is
//     fully populated (commits, trees, blobs) without user interaction.
//  6. Transition job to completed.
func (m *gitManager) runImport(ctx context.Context, jobID string, req ImportRepoRequest, defaultBranch string) {
	defer func() {
		importJobsMu.Lock()
		delete(importJobs, jobID)
		importJobsMu.Unlock()
	}()

	runStart := time.Now()
	log.Printf("[import][%s] job=%s url=%s: starting", m.agencyID, jobID, req.SourceURL)
	appendImportStep(jobID, "Starting import…")

	// Transition to running.
	if err := m.updateImportJobStatus(ctx, jobID, importStatusRunning, ""); err != nil {
		return
	}

	// 1. Allocate a persistent directory for the bare clone.
	// This directory survives the import and is reused by FetchBranch (GIT-023d).
	t0 := time.Now()
	cloneDir, err := cloneRootDir(m.agencyID, jobID)
	if err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("allocate clone dir: %v", err))
		return
	}
	log.Printf("[import][%s] job=%s: clone dir allocated in %s → %s", m.agencyID, jobID, time.Since(t0), cloneDir)

	// 2. Bare shallow clone — one tip commit per branch, no tags, no blobs yet.
	appendImportStep(jobID, fmt.Sprintf("Cloning %s (shallow, all branches)…", req.SourceURL))
	t0 = time.Now()
	log.Printf("[import][%s] job=%s: starting bare shallow clone of %s", m.agencyID, jobID, req.SourceURL)
	cloneOpts := &gogit.CloneOptions{
		URL:          req.SourceURL,
		Depth:        1,
		SingleBranch: false,
		Tags:         gogit.NoTags,
	}
	repo, err := gogit.PlainCloneContext(ctx, cloneDir, true /* isBare */, cloneOpts)
	if err != nil {
		if ctx.Err() != nil {
			_ = m.updateImportJobStatus(context.Background(), jobID, importStatusCancelled, "")
			_ = m.publishImportEvent(context.Background(), "cross.git.%s.repo.import.cancelled")
			return
		}
		m.failImportJob(ctx, jobID, fmt.Sprintf("bare shallow clone %s: %v", req.SourceURL, err))
		return
	}
	log.Printf("[import][%s] job=%s: bare shallow clone done in %s", m.agencyID, jobID, time.Since(t0))
	appendImportStep(jobID, "Clone complete. Discovering branches…")

	// 3. Create the Repository entity with bare_clone_path so FetchBranch can
	// reuse the local clone without re-downloading.
	now := time.Now().UTC().Format(time.RFC3339)
	t0 = time.Now()
	repoEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":            req.Name,
			"description":     req.Description,
			"default_branch":  defaultBranch,
			"bare_clone_path": cloneDir,
			"source_url":      req.SourceURL,
			"created_at":      now,
			"updated_at":      now,
		},
	})
	if err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("create Repository entity: %v", err))
		return
	}
	repoID := repoEntity.ID
	log.Printf("[import][%s] job=%s: Repository entity created (id=%s) in %s", m.agencyID, jobID, repoID, time.Since(t0))
	appendImportStep(jobID, fmt.Sprintf("Repository entity created (id=%s).", repoID))

	// 4. Iterate remote refs and write a stub Branch entity for each branch ref.
	t0 = time.Now()
	refs, err := repo.References()
	if err != nil {
		m.failImportJob(ctx, jobID, fmt.Sprintf("list refs: %v", err))
		return
	}

	var branchCount int
	seenBranches := make(map[string]bool)
	if err := refs.ForEach(func(ref *gogitplumbing.Reference) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Resolve a human-readable branch name from this ref.
		//
		// A bare go-git clone stores remote branches as refs/remotes/origin/<name>
		// (IsBranch() == false, IsRemote() == true) rather than under refs/heads/.
		// We accept both forms and normalise to a plain branch name in both cases.
		var branchName string
		switch {
		case ref.Name().IsBranch():
			// refs/heads/<name> → "name"
			branchName = ref.Name().Short()
		case ref.Name().IsRemote():
			// refs/remotes/origin/<name> → strip "origin/" prefix
			short := ref.Name().Short() // e.g. "origin/main"
			const prefix = "origin/"
			if !strings.HasPrefix(short, prefix) {
				return nil // unexpected remote name — skip
			}
			branchName = strings.TrimPrefix(short, prefix)
			if branchName == "HEAD" || branchName == "" {
				return nil // synthetic HEAD ref — skip
			}
		default:
			return nil
		}

		// Deduplicate: refs/heads/main and refs/remotes/origin/main both
		// resolve to "main". Only process the first occurrence.
		if seenBranches[branchName] {
			return nil
		}
		seenBranches[branchName] = true

		branchCount++
		appendImportStep(jobID, fmt.Sprintf("Creating stub branch: %s", branchName))
		t1 := time.Now()
		err := m.upsertStubBranchNamed(ctx, branchName, ref.Hash().String(), repoID, req.SourceURL, now)
		log.Printf("[import][%s] job=%s: upsertStubBranch %q done in %s err=%v", m.agencyID, jobID, branchName, time.Since(t1), err)
		return err
	}); err != nil {
		if ctx.Err() != nil {
			_ = m.updateImportJobStatus(context.Background(), jobID, importStatusCancelled, "")
			_ = m.publishImportEvent(context.Background(), "cross.git.%s.repo.import.cancelled")
			return
		}
		m.failImportJob(ctx, jobID, fmt.Sprintf("walk refs: %v", err))
		return
	}
	log.Printf("[import][%s] job=%s: refs walk done in %s — %d branch stub(s) discovered", m.agencyID, jobID, time.Since(t0), branchCount)
	appendImportStep(jobID, fmt.Sprintf("%d branch stub(s) discovered.", branchCount))

	// 5. Automatically fetch the default branch so it is immediately usable.
	appendImportStep(jobID, fmt.Sprintf("Auto-fetching default branch %q…", defaultBranch))
	t0 = time.Now()
	defaultBranchEntities, listErr := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Branch",
		Properties: map[string]any{"name": defaultBranch},
	})
	log.Printf("[import][%s] job=%s: ListEntities for default branch %q took %s (found=%d err=%v)", m.agencyID, jobID, defaultBranch, time.Since(t0), len(defaultBranchEntities), listErr)
	if listErr == nil && len(defaultBranchEntities) > 0 {
		t0 = time.Now()
		_, fetchErr := m.FetchBranch(ctx, FetchBranchRequest{
			RepoID:   repoID,
			BranchID: defaultBranchEntities[0].ID,
		})
		log.Printf("[import][%s] job=%s: FetchBranch(%q) returned in %s err=%v (runs in background)", m.agencyID, jobID, defaultBranch, time.Since(t0), fetchErr)
		if fetchErr != nil {
			appendImportStep(jobID, fmt.Sprintf("Auto-fetch for %q skipped: %v", defaultBranch, fetchErr))
		} else {
			appendImportStep(jobID, fmt.Sprintf("Default branch %q fetch started in background.", defaultBranch))
		}
	} else {
		appendImportStep(jobID, fmt.Sprintf("Default branch %q not found in stubs; skipping auto-fetch.", defaultBranch))
	}

	// 6. Publish success event and mark completed.
	_ = m.publishImportEvent(ctx, "cross.git.%s.repo.imported")
	if err := m.updateImportJobStatus(context.Background(), jobID, importStatusCompleted, ""); err != nil {
		log.Printf("[import][%s] job=%s: WARNING failed to mark import completed: %v", m.agencyID, jobID, err)
	}
	log.Printf("[import][%s] job=%s: import phase done (stub+auto-fetch triggered) — total elapsed %s", m.agencyID, jobID, time.Since(runStart))
}

// upsertStubBranchNamed creates (or updates) a Branch entity with status="stub" for the
// given branch name and tip SHA. The branch carries the source_url (for re-clone by
// FetchBranch if the bare clone is gone) and the stub status sentinel.
// Edges has_branch (repo→branch) and belongs_to_repository (branch→repo) are
// created; duplicate-edge errors are logged and ignored.
func (m *gitManager) upsertStubBranchNamed(ctx context.Context, branchName, tipSHA, repoID, sourceURL, now string) error {

	existing, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Branch",
		Properties: map[string]any{"name": branchName},
	})
	if err != nil {
		return fmt.Errorf("stub branch %s: list: %w", branchName, err)
	}

	var branchID string
	if len(existing) > 0 {
		branchID = existing[0].ID
		if _, err := m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
			Properties: map[string]any{
				"sha":        tipSHA,
				"status":     branchStatusStub,
				"source_url": sourceURL,
				"updated_at": now,
			},
		}); err != nil {
			return fmt.Errorf("stub branch %s: update: %w", branchName, err)
		}
	} else {
		branchEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: m.agencyID,
			TypeID:   "Branch",
			Properties: map[string]any{
				"name":       branchName,
				"sha":        tipSHA,
				"status":     branchStatusStub,
				"source_url": sourceURL,
				"created_at": now,
				"updated_at": now,
			},
		})
		if err != nil {
			return fmt.Errorf("stub branch %s: create: %w", branchName, err)
		}
		branchID = branchEntity.ID
	}

	// Wire repo ↔ branch edges (duplicate-safe).
	if repoID != "" {
		_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "has_branch",
			FromID:   repoID,
			ToID:     branchID,
		})
		_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "belongs_to_repository",
			FromID:   branchID,
			ToID:     repoID,
		})
	}
	return nil
}

// walkBranchCommits upserts Branch, Commit, Tree, and Blob entities for every
// commit reachable from the given reference that has not already been processed.
//
// seenSHAs is a shared dedup set populated by the caller (FetchBranch, GIT-023d).
// Any commit SHA already present in seenSHAs is skipped; newly processed SHAs
// are added so that shared history across branches is only walked once.
//
// repoID is the entitygraph ID of the parent Repository entity; it is used to
// create has_branch and belongs_to_repository edges so that listBranchesByRepo
// can find the branch.
func (m *gitManager) walkBranchCommits(ctx context.Context, repo *gogit.Repository, ref *gogitplumbing.Reference, repoID string, seenSHAs map[string]bool) error {
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
	var branchID string
	if len(existing) > 0 {
		branchID = existing[0].ID
		if _, err := m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
			Properties: map[string]any{
				"sha":        ref.Hash().String(),
				"updated_at": now,
			},
		}); err != nil {
			return fmt.Errorf("upsert branch %s: update: %w", branchName, err)
		}
	} else {
		branchEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: m.agencyID,
			TypeID:   "Branch",
			Properties: map[string]any{
				"name":       branchName,
				"sha":        ref.Hash().String(),
				"created_at": now,
				"updated_at": now,
			},
		})
		if err != nil {
			return fmt.Errorf("upsert branch %s: create: %w", branchName, err)
		}
		branchID = branchEntity.ID
	}

	// Link branch to repository: repo→branch (has_branch) and branch→repo
	// (belongs_to_repository). Both edges are idempotency-safe via create;
	// duplicate-edge errors are non-fatal so that re-imports don't fail.
	if repoID != "" {
		_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "has_branch",
			FromID:   repoID,
			ToID:     branchID,
		})
		_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "belongs_to_repository",
			FromID:   branchID,
			ToID:     repoID,
		})
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
		sha := c.Hash.String()
		if seenSHAs[sha] {
			return nil // already processed via another branch — skip
		}
		seenSHAs[sha] = true
		commitCount++
		return m.upsertCommitAndTree(ctx, repo, c)
	}); err != nil {
		return err
	}

	// Link the branch to its HEAD commit so that ListDirectory, ReadFile, and
	// Log can resolve files. The HEAD commit is the one whose SHA matches
	// ref.Hash().
	headSHA := ref.Hash().String()
	headCommits, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Commit",
		Properties: map[string]any{"sha": headSHA},
	})
	if err == nil && len(headCommits) > 0 {
		_, _ = m.advanceBranchHead(ctx, branchID, headCommits[0].ID)
	}

	return nil
}

// upsertCommitAndTree upserts Commit, Tree, and Blob entities for a single commit.
// Commit is immutable (content-addressed by SHA); ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertCommitAndTree(ctx context.Context, repo *gogit.Repository, c *gogitobject.Commit) error {
	now := time.Now().UTC().Format(time.RFC3339)
	commitSHA := c.Hash.String()

	// Create Commit entity; skip if this SHA was already ingested.
	commitEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
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
	})
	if err != nil && !errors.Is(err, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create commit %s: %w", commitSHA, err)
	}
	var commitID string
	if err == nil {
		commitID = commitEntity.ID
	}
	// Walk the commit tree.
	tree, err := c.Tree()
	if err != nil {
		return fmt.Errorf("tree for commit %s: %w", commitSHA, err)
	}
	treeID, err := m.upsertTreeWithEdges(ctx, repo, tree, "", now)
	if err != nil {
		return err
	}

	// Wire commit → has_tree → root tree edge.
	if commitID != "" && treeID != "" {
		_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "has_tree",
			FromID:   commitID,
			ToID:     treeID,
		})
	}
	return nil
}

// upsertTreeWithEdges upserts Tree and Blob entities for a go-git Tree and
// creates the has_blob and has_subtree edges required by allBlobsAtCommit.
// Returns the entity ID of the created/existing tree.
// pathPrefix is the directory path within the commit tree — empty string for the
// root tree, e.g. "src/handlers" for a nested subtree.
// Both Tree and Blob are treated as content-addressed (immutable by SHA);
// ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertTreeWithEdges(ctx context.Context, repo *gogit.Repository, tree *gogitobject.Tree, pathPrefix, now string) (string, error) {
	treeSHA := tree.Hash.String()

	// Create Tree entity; skip if SHA already ingested.
	treeEntity, createErr := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Tree",
		Properties: map[string]any{
			"sha":        treeSHA,
			"path":       pathPrefix,
			"created_at": now,
		},
	})
	var treeID string
	if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
		return "", fmt.Errorf("create tree %s: %w", treeSHA, createErr)
	}
	if createErr == nil {
		treeID = treeEntity.ID
	} else {
		// Tree already exists — look it up by SHA to get the ID for edges.
		existing, listErr := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   m.agencyID,
			TypeID:     "Tree",
			Properties: map[string]any{"sha": treeSHA},
		})
		if listErr == nil && len(existing) > 0 {
			treeID = existing[0].ID
		}
	}

	for _, entry := range tree.Entries {
		if ctx.Err() != nil {
			return treeID, ctx.Err()
		}

		// Build the full relative path for this entry.
		var entryPath string
		if pathPrefix == "" {
			entryPath = entry.Name
		} else {
			entryPath = pathPrefix + "/" + entry.Name
		}

		if entry.Mode.IsFile() {
			blobID, err := m.upsertBlobWithID(ctx, repo, entry, entryPath, now)
			if err != nil {
				return treeID, err
			}
			// Wire tree → has_blob → blob edge.
			if treeID != "" && blobID != "" {
				_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
					AgencyID: m.agencyID,
					Name:     "has_blob",
					FromID:   treeID,
					ToID:     blobID,
				})
			}
		} else {
			// Recurse into subdirectory trees.
			subTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return treeID, fmt.Errorf("resolve subtree %s path=%q: %w", entry.Hash.String(), entryPath, err)
			}
			subTreeID, err := m.upsertTreeWithEdges(ctx, repo, subTree, entryPath, now)
			if err != nil {
				return treeID, err
			}
			// Wire tree → has_subtree → subtree edge.
			if treeID != "" && subTreeID != "" {
				_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
					AgencyID: m.agencyID,
					Name:     "has_subtree",
					FromID:   treeID,
					ToID:     subTreeID,
				})
			}
		}
	}
	return treeID, nil
}

// upsertBlobWithID creates a Blob entity for a single tree entry and returns its entity ID.
// It fetches the blob object from the repository to populate size, encoding,
// content, and extension in addition to sha, path, and name.
// ErrEntityAlreadyExists is skipped (blobs are content-addressed by SHA).
func (m *gitManager) upsertBlobWithID(ctx context.Context, repo *gogit.Repository, entry gogitobject.TreeEntry, fullPath, now string) (string, error) {
	blobSHA := entry.Hash.String()

	// Fetch the blob object so we can read its content and size.
	blobObj, err := repo.BlobObject(entry.Hash)
	if err != nil {
		return "", fmt.Errorf("read blob object %s path=%q: %w", blobSHA, fullPath, err)
	}

	reader, err := blobObj.Reader()
	if err != nil {
		return "", fmt.Errorf("open blob reader %s path=%q: %w", blobSHA, fullPath, err)
	}
	rawBytes, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return "", fmt.Errorf("read blob content %s path=%q: %w", blobSHA, fullPath, err)
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

	blobEntity, createErr := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
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
	})
	if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
		return "", fmt.Errorf("create blob %s path=%q: %w", blobSHA, fullPath, createErr)
	}
	var blobID string
	if createErr == nil {
		blobID = blobEntity.ID
	} else {
		// Already exists — look up by SHA.
		existing, listErr := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   m.agencyID,
			TypeID:     "Blob",
			Properties: map[string]any{"sha": blobSHA},
		})
		if listErr == nil && len(existing) > 0 {
			blobID = existing[0].ID
		}
	}
	return blobID, nil
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
	log.Printf("[import][%s] job=%s: FAILED — %s", m.agencyID, jobID, errMsg)
	_ = m.updateImportJobStatus(context.Background(), jobID, importStatusFailed, errMsg)
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
		ID:            e.ID,
		AgencyID:      str("agency_id"),
		Name:          str("name"),
		SourceURL:     str("source_url"),
		DefaultBranch: str("default_branch"),
		Status:        str("status"),
		ErrorMessage:  str("error_message"),
		CreatedAt:     str("created_at"),
		UpdatedAt:     str("updated_at"),
	}
}
