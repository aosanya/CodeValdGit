// git_impl_fetchbranch.go implements the lazy import v2 on-demand branch fetch
// methods on [gitManager] (GIT-023d):
//
//   - [GitManager.FetchBranch] — creates a FetchBranchJob entity, transitions
//     the Branch status to "fetching", and launches a background goroutine that
//     deepens (or re-clones) the bare repository, walks the full commit history
//     and the tip-commit tree (blob metadata only), then transitions the branch
//     to "fetched" or "fetch_failed".
//   - [GitManager.GetFetchBranchStatus] — returns the current state of a fetch job.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"
	gogittransport "github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// fetchJobStatus values for the FetchBranchJob entity "status" property.
const (
	fetchJobStatusPending   = "pending"
	fetchJobStatusRunning   = "running"
	fetchJobStatusCompleted = "completed"
	fetchJobStatusFailed    = "failed"
)

// fetchCancelEntry holds the cancel function for an in-flight fetch goroutine.
type fetchCancelEntry struct {
	cancel context.CancelFunc
}

// fetchJobsMu guards fetchJobs.
var fetchJobsMu sync.Mutex

// fetchJobs maps jobID → cancel entry for all active fetch goroutines.
var fetchJobs = make(map[string]fetchCancelEntry)

// FetchBranch triggers an async on-demand fetch of the full commit history
// and tip-commit file tree for a branch that is currently in "stub" status.
// Returns immediately with a [FetchBranchJob]. Returns [ErrBranchAlreadyFetched]
// if the branch status is "fetching" or "fetched".
func (m *gitManager) FetchBranch(ctx context.Context, req FetchBranchRequest) (FetchBranchJob, error) {
	log.Printf("[FetchBranch] agencyID=%q repoID=%q branchID=%q", m.agencyID, req.RepoID, req.BranchID)

	// 1. Fetch the Branch entity by ID.
	branchEntity, err := m.dm.GetEntity(ctx, m.agencyID, req.BranchID)
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return FetchBranchJob{}, fmt.Errorf("FetchBranch: branch %s not found", req.BranchID)
		}
		return FetchBranchJob{}, fmt.Errorf("FetchBranch %s: get branch entity: %w", req.BranchID, err)
	}
	branchName, _ := branchEntity.Properties["name"].(string)
	status, _ := branchEntity.Properties["status"].(string)

	// 2. Guard: reject if already fetching or fetched.
	if status == branchStatusFetching || status == branchStatusFetched {
		return FetchBranchJob{}, ErrBranchAlreadyFetched
	}

	// 3. Create the FetchBranchJob entity.
	now := time.Now().UTC().Format(time.RFC3339)
	jobEntity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "FetchBranchJob",
		Properties: map[string]any{
			"agency_id":     m.agencyID,
			"repo_id":       req.RepoID,
			"branch_name":   branchName,
			"status":        fetchJobStatusPending,
			"error_message": "",
			"created_at":    now,
			"updated_at":    now,
		},
	})
	if err != nil {
		return FetchBranchJob{}, fmt.Errorf("FetchBranch %s: create job entity: %w", req.BranchID, err)
	}
	jobID := jobEntity.ID
	job := fetchJobFromEntity(jobEntity)

	// 4. Transition Branch status to "fetching".
	if _, err := m.dm.UpdateEntity(ctx, m.agencyID, req.BranchID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"status":     branchStatusFetching,
			"updated_at": now,
		},
	}); err != nil {
		return FetchBranchJob{}, fmt.Errorf("FetchBranch %s: transition to fetching: %w", req.BranchID, err)
	}

	// 5. Launch background goroutine.
	jobCtx, cancel := context.WithCancel(context.Background())
	fetchJobsMu.Lock()
	fetchJobs[jobID] = fetchCancelEntry{cancel: cancel}
	fetchJobsMu.Unlock()

	go m.runFetchBranch(jobCtx, jobID, req.RepoID, req.BranchID, branchName)

	return job, nil
}

// GetFetchBranchStatus returns the current state of a fetch job.
// Returns [ErrImportJobNotFound] if no job with the given ID exists.
func (m *gitManager) GetFetchBranchStatus(ctx context.Context, jobID string) (FetchBranchJob, error) {
	log.Printf("[GetFetchBranchStatus] agencyID=%q jobID=%q", m.agencyID, jobID)
	entity, err := m.dm.GetEntity(ctx, m.agencyID, jobID)
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return FetchBranchJob{}, ErrImportJobNotFound
		}
		return FetchBranchJob{}, fmt.Errorf("GetFetchBranchStatus %s: %w", jobID, err)
	}
	return fetchJobFromEntity(entity), nil
}

// runFetchBranch is the background goroutine started by [FetchBranch].
// Steps:
//  1. Transition job to "running".
//  2. Retrieve bare_clone_path and source_url from the Repository entity.
//  3. Open or re-clone the bare repository.
//  4. Deepen the clone for this branch (unshallow).
//  5. Walk full commit history; upsert Commit entities with seenSHAs dedup.
//  6. Walk tip-commit tree; upsert Tree + Blob entities (metadata only, no content).
//  7. Advance branch HEAD pointer.
//  8. Transition Branch status to "fetched" (or "fetch_failed" on error).
//  9. Transition job to "completed" (or "failed" on error).
//  10. Publish cross.git.{agencyID}.branch.fetched.
func (m *gitManager) runFetchBranch(ctx context.Context, jobID, repoID, branchID, branchName string) {
	defer func() {
		fetchJobsMu.Lock()
		delete(fetchJobs, jobID)
		fetchJobsMu.Unlock()
	}()

	fail := func(msg string) {
		log.Printf("[ERROR] FetchBranchJob %s failed: %s", jobID, msg)
		bg := context.Background()
		_ = m.updateFetchJobStatus(bg, jobID, fetchJobStatusFailed, msg)
		_ = m.updateBranchFetchStatus(bg, branchID, branchStatusFetchFailed, msg)
	}

	if err := m.updateFetchJobStatus(ctx, jobID, fetchJobStatusRunning, ""); err != nil {
		log.Printf("[ERROR] FetchBranchJob %s: transition to running: %v", jobID, err)
		return
	}

	repoEntity, err := m.dm.GetEntity(ctx, m.agencyID, repoID)
	if err != nil {
		fail(fmt.Sprintf("get repo entity %s: %v", repoID, err))
		return
	}
	bareClonePath, _ := repoEntity.Properties["bare_clone_path"].(string)
	sourceURL, _ := repoEntity.Properties["source_url"].(string)

	if sourceURL == "" {
		branchEnt, berr := m.dm.GetEntity(ctx, m.agencyID, branchID)
		if berr == nil {
			sourceURL, _ = branchEnt.Properties["source_url"].(string)
		}
	}

	repo, newClonePath, err := m.openOrRecloneBare(ctx, bareClonePath, sourceURL, jobID)
	if err != nil {
		fail(fmt.Sprintf("open/re-clone bare repo: %v", err))
		return
	}

	if newClonePath != bareClonePath && newClonePath != "" {
		if _, uerr := m.dm.UpdateEntity(ctx, m.agencyID, repoID, entitygraph.UpdateEntityRequest{
			Properties: map[string]any{
				"bare_clone_path": newClonePath,
				"updated_at":      time.Now().UTC().Format(time.RFC3339),
			},
		}); uerr != nil {
			log.Printf("[runFetchBranch] update bare_clone_path: %v (continuing)", uerr)
		}
	}

	if err := m.deepenClone(ctx, repo, branchName, sourceURL); err != nil {
		fail(fmt.Sprintf("deepen clone branch=%q: %v", branchName, err))
		return
	}

	ref, err := findBranchRef(repo, branchName)
	if err != nil {
		fail(fmt.Sprintf("find ref for branch=%q: %v", branchName, err))
		return
	}

	seenSHAs := make(map[string]bool)
	if err := m.walkCommitsOnly(ctx, repo, ref, seenSHAs); err != nil {
		if ctx.Err() != nil {
			bg := context.Background()
			_ = m.updateFetchJobStatus(bg, jobID, fetchJobStatusFailed, "context cancelled")
			_ = m.updateBranchFetchStatus(bg, branchID, branchStatusFetchFailed, "context cancelled")
			return
		}
		fail(fmt.Sprintf("walk commits branch=%q: %v", branchName, err))
		return
	}

	tipCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		fail(fmt.Sprintf("resolve tip commit branch=%q sha=%q: %v", branchName, ref.Hash().String(), err))
		return
	}
	tipTree, err := tipCommit.Tree()
	if err != nil {
		fail(fmt.Sprintf("resolve tip tree branch=%q: %v", branchName, err))
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := m.upsertTreeMetadata(ctx, repo, tipTree, "", now); err != nil {
		fail(fmt.Sprintf("walk tip tree branch=%q: %v", branchName, err))
		return
	}

	tipSHA := ref.Hash().String()
	headCommits, _ := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Commit",
		Properties: map[string]any{"sha": tipSHA},
	})
	if len(headCommits) > 0 {
		if _, advErr := m.advanceBranchHead(ctx, branchID, headCommits[0].ID); advErr != nil {
			log.Printf("[runFetchBranch] advanceBranchHead branch=%q: %v (continuing)", branchName, advErr)
		}
	}

	if err := m.updateBranchFetchStatus(ctx, branchID, branchStatusFetched, ""); err != nil {
		log.Printf("[ERROR] runFetchBranch: transition branch to fetched: %v", err)
	}
	if err := m.updateFetchJobStatus(ctx, jobID, fetchJobStatusCompleted, ""); err != nil {
		log.Printf("[ERROR] runFetchBranch: transition job to completed: %v", err)
	}

	if m.publisher != nil {
		topic := fmt.Sprintf("cross.git.%s.branch.fetched", m.agencyID)
		if pubErr := m.publisher.Publish(ctx, topic, m.agencyID); pubErr != nil {
			log.Printf("[runFetchBranch] publish branch.fetched: %v (continuing)", pubErr)
		}
	}
	log.Printf("[runFetchBranch] done agencyID=%q branch=%q jobID=%q", m.agencyID, branchName, jobID)
}

// openOrRecloneBare opens the existing bare clone at bareClonePath. If the
// path is missing or gone, it performs a fresh bare shallow clone from sourceURL.
// Returns the Repository and the (possibly new) bare clone path.
func (m *gitManager) openOrRecloneBare(ctx context.Context, bareClonePath, sourceURL, jobID string) (*gogit.Repository, string, error) {
	if bareClonePath != "" {
		if _, statErr := os.Stat(bareClonePath); statErr == nil {
			repo, openErr := gogit.PlainOpen(bareClonePath)
			if openErr == nil {
				return repo, bareClonePath, nil
			}
			log.Printf("[openOrRecloneBare] PlainOpen %q failed (%v); will re-clone", bareClonePath, openErr)
		}
	}
	if sourceURL == "" {
		return nil, "", fmt.Errorf("bare_clone_path missing or gone and source_url is empty")
	}
	dir, err := cloneRootDir(m.agencyID, jobID+"-refetch")
	if err != nil {
		return nil, "", err
	}
	repo, err := gogit.PlainCloneContext(ctx, dir, true, &gogit.CloneOptions{
		URL:          sourceURL,
		Depth:        1,
		SingleBranch: false,
		Tags:         gogit.NoTags,
	})
	if err != nil {
		return nil, "", fmt.Errorf("re-clone %s: %w", sourceURL, err)
	}
	return repo, dir, nil
}

// deepenClone fetches the full history for branchName from the remote.
// Non-fatal errors (already-up-to-date, empty remote) are logged and ignored.
func (m *gitManager) deepenClone(ctx context.Context, repo *gogit.Repository, branchName, sourceURL string) error {
	refSpec := fmt.Sprintf("refs/heads/%s:refs/heads/%s", branchName, branchName)
	fetchOpts := &gogit.FetchOptions{
		RemoteName: "origin",
		RefSpecs:   []gogitconfig.RefSpec{gogitconfig.RefSpec(refSpec)},
		Depth:      0,
		Force:      true,
	}
	if sourceURL != "" {
		fetchOpts.RemoteURL = sourceURL
	}
	err := repo.FetchContext(ctx, fetchOpts)
	if err != nil &&
		!errors.Is(err, gogit.NoErrAlreadyUpToDate) &&
		!errors.Is(err, gogittransport.ErrEmptyRemoteRepository) {
		log.Printf("[deepenClone] FetchContext branch=%q: %v (continuing)", branchName, err)
	}
	return nil
}

// walkCommitsOnly walks all commits reachable from ref and upserts Commit entities.
// seenSHAs deduplicates across multiple FetchBranch calls.
func (m *gitManager) walkCommitsOnly(ctx context.Context, repo *gogit.Repository, ref *gogitplumbing.Reference, seenSHAs map[string]bool) error {
	iter, err := repo.Log(&gogit.LogOptions{
		From:  ref.Hash(),
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return fmt.Errorf("log %s: %w", ref.Name().Short(), err)
	}
	defer iter.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	return iter.ForEach(func(c *gogitobject.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sha := c.Hash.String()
		if seenSHAs[sha] {
			return nil
		}
		seenSHAs[sha] = true
		_, createErr := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: m.agencyID,
			TypeID:   "Commit",
			Properties: map[string]any{
				"sha":             sha,
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
		if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
			return fmt.Errorf("create commit %s: %w", sha, createErr)
		}
		return nil
	})
}

// upsertTreeMetadata upserts Tree and Blob entities (metadata only, no content).
// Recursive for subdirectories. ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertTreeMetadata(ctx context.Context, repo *gogit.Repository, tree *gogitobject.Tree, pathPrefix, now string) error {
	treeSHA := tree.Hash.String()
	if _, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Tree",
		Properties: map[string]any{
			"sha":        treeSHA,
			"path":       pathPrefix,
			"created_at": now,
		},
	}); err != nil && !errors.Is(err, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create tree %s path=%q: %w", treeSHA, pathPrefix, err)
	}

	for _, entry := range tree.Entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var entryPath string
		if pathPrefix == "" {
			entryPath = entry.Name
		} else {
			entryPath = pathPrefix + "/" + entry.Name
		}
		if entry.Mode.IsFile() {
			if err := m.upsertBlobMetadata(ctx, repo, entry, entryPath, now); err != nil {
				return err
			}
		} else {
			subTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return fmt.Errorf("resolve subtree %s path=%q: %w", entry.Hash.String(), entryPath, err)
			}
			if err := m.upsertTreeMetadata(ctx, repo, subTree, entryPath, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// upsertBlobMetadata creates a Blob entity with sha, path, name, extension,
// and size — content is omitted for lazy population by ReadFile (GIT-023e).
// ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertBlobMetadata(ctx context.Context, repo *gogit.Repository, entry gogitobject.TreeEntry, fullPath, now string) error {
	blobSHA := entry.Hash.String()
	blobObj, err := repo.BlobObject(entry.Hash)
	if err != nil {
		return fmt.Errorf("read blob object %s path=%q: %w", blobSHA, fullPath, err)
	}
	r, readErr := blobObj.Reader()
	if readErr != nil {
		return fmt.Errorf("open blob reader %s path=%q: %w", blobSHA, fullPath, readErr)
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()

	ext := strings.TrimPrefix(filepath.Ext(entry.Name), ".")
	name := filepath.Base(fullPath)

	if _, createErr := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"sha":        blobSHA,
			"path":       fullPath,
			"name":       name,
			"extension":  ext,
			"size":       blobObj.Size,
			"encoding":   "",
			"content":    "",
			"created_at": now,
		},
	}); createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
		return fmt.Errorf("create blob metadata %s path=%q: %w", blobSHA, fullPath, createErr)
	}
	return nil
}

// updateFetchJobStatus transitions a FetchBranchJob entity to the given status.
func (m *gitManager) updateFetchJobStatus(ctx context.Context, jobID, status, errMsg string) error {
	_, err := m.dm.UpdateEntity(ctx, m.agencyID, jobID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"status":        status,
			"error_message": errMsg,
			"updated_at":    time.Now().UTC().Format(time.RFC3339),
		},
	})
	return err
}

// updateBranchFetchStatus patches the Branch entity status and optional error_message.
func (m *gitManager) updateBranchFetchStatus(ctx context.Context, branchID, status, errMsg string) error {
	props := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if errMsg != "" {
		props["error_message"] = errMsg
	}
	_, err := m.dm.UpdateEntity(ctx, m.agencyID, branchID, entitygraph.UpdateEntityRequest{
		Properties: props,
	})
	return err
}

// fetchJobFromEntity converts an [entitygraph.Entity] to a [FetchBranchJob] value.
func fetchJobFromEntity(e entitygraph.Entity) FetchBranchJob {
	str := func(key string) string {
		v, _ := e.Properties[key].(string)
		return v
	}
	return FetchBranchJob{
		ID:           e.ID,
		AgencyID:     str("agency_id"),
		RepoID:       str("repo_id"),
		BranchName:   str("branch_name"),
		Status:       str("status"),
		ErrorMessage: str("error_message"),
		CreatedAt:    str("created_at"),
		UpdatedAt:    str("updated_at"),
	}
}

// findBranchRef resolves a branch name to a reference in the local bare clone.
// Checks refs/heads/<name> first, then refs/remotes/origin/<name>.
func findBranchRef(repo *gogit.Repository, branchName string) (*gogitplumbing.Reference, error) {
	candidates := []gogitplumbing.ReferenceName{
		gogitplumbing.ReferenceName("refs/heads/" + branchName),
		gogitplumbing.ReferenceName("refs/remotes/origin/" + branchName),
	}
	for _, name := range candidates {
		ref, err := repo.Reference(name, true)
		if err == nil {
			return ref, nil
		}
	}
	return nil, fmt.Errorf("branch %q not found in bare clone", branchName)
}
