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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"

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
		bg := context.Background()
		_ = m.updateFetchJobStatus(bg, jobID, fetchJobStatusFailed, msg)
		_ = m.updateBranchFetchStatus(bg, branchID, branchStatusFetchFailed, msg)
	}

	if err := m.updateFetchJobStatus(ctx, jobID, fetchJobStatusRunning, ""); err != nil {
		return
	}

	repoEntity, err := m.dm.GetEntity(ctx, m.agencyID, repoID)
	if err != nil {
		fail(fmt.Sprintf("get repo entity %s: %v", repoID, err))
		return
	}
	sourceURL, _ := repoEntity.Properties["source_url"].(string)

	if sourceURL == "" {
		branchEnt, berr := m.dm.GetEntity(ctx, m.agencyID, branchID)
		if berr == nil {
			sourceURL, _ = branchEnt.Properties["source_url"].(string)
		}
	}

	// Perform a fresh full single-branch clone so we have the complete commit
	// history without shallow-object problems.
	repo, err := m.deepenClone(ctx, nil, branchName, sourceURL)
	if err != nil {
		fail(fmt.Sprintf("full clone branch=%q: %v", branchName, err))
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
	rootTreeID, err := m.upsertTreeMetadataWithEdges(ctx, repo, tipTree, "", now)
	if err != nil {
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
		commitID := headCommits[0].ID
		// Wire commit → has_tree → root tree edge.
		if rootTreeID != "" {
			_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: m.agencyID,
				Name:     "has_tree",
				FromID:   commitID,
				ToID:     rootTreeID,
			})
		}

	}

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

// deepenClone performs a fresh full (non-shallow) single-branch clone of
// branchName from sourceURL into a new temp directory and returns the opened
// repository. The caller should use this repo for all subsequent operations on
// the branch.
//
// A fresh clone is used instead of deepening the existing shallow clone because
// go-git v5 has no reliable Unshallow option: fetching into a shallow repo
// where the tip SHA already exists returns NoErrAlreadyUpToDate without
// fetching parent commits, leaving the object store incomplete.
func (m *gitManager) deepenClone(ctx context.Context, _ *gogit.Repository, branchName, sourceURL string) (*gogit.Repository, error) {
	if sourceURL == "" {
		return nil, fmt.Errorf("deepenClone: source_url is empty for branch %q", branchName)
	}
	dir, err := cloneRootDir(m.agencyID, branchName+"-full")
	if err != nil {
		return nil, fmt.Errorf("deepenClone: create temp dir: %w", err)
	}
	cloneRef := gogitplumbing.ReferenceName("refs/heads/" + branchName)
	repo, err := gogit.PlainCloneContext(ctx, dir, true, &gogit.CloneOptions{
		URL:           sourceURL,
		SingleBranch:  true,
		ReferenceName: cloneRef,
		Tags:          gogit.NoTags,
	})
	if err != nil {
		// Clean up temp dir on failure.
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("deepenClone: clone branch %q: %w", branchName, err)
	}
	return repo, nil
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

// upsertTreeMetadataWithEdges upserts Tree and Blob entities (metadata only, no content)
// and creates has_blob / has_subtree edges so that allBlobsAtCommit can traverse them.
// Returns the entity ID of the created/existing tree.
// Recursive for subdirectories. ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertTreeMetadataWithEdges(ctx context.Context, repo *gogit.Repository, tree *gogitobject.Tree, pathPrefix, now string) (string, error) {
	treeSHA := tree.Hash.String()

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
		return "", fmt.Errorf("create tree %s path=%q: %w", treeSHA, pathPrefix, createErr)
	}
	if createErr == nil {
		treeID = treeEntity.ID
	} else {
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
		var entryPath string
		if pathPrefix == "" {
			entryPath = entry.Name
		} else {
			entryPath = pathPrefix + "/" + entry.Name
		}
		if entry.Mode.IsFile() {
			blobID, err := m.upsertBlobMetadataWithID(ctx, repo, entry, entryPath, now)
			if err != nil {
				return treeID, err
			}
			if treeID != "" && blobID != "" {
				_, _ = m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
					AgencyID: m.agencyID,
					Name:     "has_blob",
					FromID:   treeID,
					ToID:     blobID,
				})
			}
		} else {
			subTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return treeID, fmt.Errorf("resolve subtree %s path=%q: %w", entry.Hash.String(), entryPath, err)
			}
			subTreeID, err := m.upsertTreeMetadataWithEdges(ctx, repo, subTree, entryPath, now)
			if err != nil {
				return treeID, err
			}
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

// upsertBlobMetadataWithID creates a Blob entity with sha, path, name, extension,
// and size — content is omitted for lazy population by ReadFile (GIT-023e).
// Returns the entity ID of the created/existing blob.
// ErrEntityAlreadyExists is skipped.
func (m *gitManager) upsertBlobMetadataWithID(ctx context.Context, repo *gogit.Repository, entry gogitobject.TreeEntry, fullPath, now string) (string, error) {
	blobSHA := entry.Hash.String()
	blobObj, err := repo.BlobObject(entry.Hash)
	if err != nil {
		return "", fmt.Errorf("read blob object %s path=%q: %w", blobSHA, fullPath, err)
	}
	r, readErr := blobObj.Reader()
	if readErr != nil {
		return "", fmt.Errorf("open blob reader %s path=%q: %w", blobSHA, fullPath, readErr)
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()

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
			"size":       blobObj.Size,
			"encoding":   "",
			"content":    "",
			"created_at": now,
		},
	})
	var blobID string
	if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
		return "", fmt.Errorf("create blob metadata %s path=%q: %w", blobSHA, fullPath, createErr)
	}
	if createErr == nil {
		blobID = blobEntity.ID
	} else {
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
