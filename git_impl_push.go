// git_impl_push.go — IndexPushedBranch implementation.
//
// After a successful git-receive-pack, this method indexes only the commits
// that were actually pushed (walking backwards from newSHA until it hits
// oldSHA or a commit whose raw object bytes are not stored — i.e. commits
// that were already indexed by the import job with metadata-only entities).
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// IndexPushedBranch indexes the commits that were just pushed and materialises
// Commit, Tree, and Blob entities in the entity graph, then advances the
// branch HEAD pointer.
//
// oldSHA is the previous branch tip (all-zeros string for a new branch).
// newSHA is the new branch tip written by the push.
//
// Only commits between oldSHA and newSHA are walked; ancestors already held by
// the server (import-job metadata entities) are skipped gracefully.
func (m *gitManager) IndexPushedBranch(ctx context.Context, repoName, branchRef, oldSHA, newSHA string) error {
	log.Printf("[push-index][%s] repo=%s ref=%s sha=%s: start", m.agencyID, repoName, branchRef, newSHA[:8])
	start := time.Now()

	// ── 1. Open the raw storer so we can read pushed objects directly ─────────
	sto, fs, err := m.backend.OpenStorer(ctx, m.agencyID, repoName)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: open storer: %w", repoName, branchRef, err)
	}

	// Set HEAD so that gogit.Open can resolve references for tree walking.
	headRef := gogitplumbing.NewSymbolicReference(gogitplumbing.HEAD, gogitplumbing.ReferenceName(branchRef))
	if setErr := sto.SetReference(headRef); setErr != nil {
		log.Printf("[push-index][%s] repo=%s: WARNING SetReference(HEAD) failed: %v", m.agencyID, repoName, setErr)
	}

	newHash := gogitplumbing.NewHash(newSHA)

	// ── 2. BFS from newSHA — stop at oldSHA or commits without raw data ───────
	//
	// receive-pack only stores raw object bytes for objects the client sent
	// (i.e. objects not already on the server). Old parent commits were
	// imported with metadata-only entities (no "data" field) so EncodedObject
	// returns ErrObjectNotFound for them. We stop traversal at that boundary.
	seenSHAs := make(map[string]bool)
	now := time.Now().UTC().Format(time.RFC3339)
	oldHash := gogitplumbing.NewHash(oldSHA)
	queue := []gogitplumbing.Hash{newHash}
	var commitCount int
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		sha := h.String()
		if seenSHAs[sha] || h == oldHash {
			continue
		}

		// Try to read the raw encoded object — fails for import-job metadata
		// entities that have no "data" field.
		encObj, objErr := sto.EncodedObject(gogitplumbing.CommitObject, h)
		if objErr != nil {
			// Commit exists as a metadata entity only — already indexed, stop here.
			continue
		}

		c, decErr := gogitobject.DecodeCommit(sto.(storer.EncodedObjectStorer), encObj)
		if decErr != nil {
			log.Printf("[push-index][%s] repo=%s: WARNING decode commit %s: %v", m.agencyID, repoName, sha[:8], decErr)
			continue
		}

		seenSHAs[sha] = true
		commitCount++

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
			return fmt.Errorf("IndexPushedBranch %s/%s: create commit %s: %w", repoName, branchRef, sha[:8], createErr)
		}

		for _, p := range c.ParentHashes {
			if !seenSHAs[p.String()] {
				queue = append(queue, p)
			}
		}
	}
	log.Printf("[push-index][%s] repo=%s ref=%s: indexed %d new commit(s)", m.agencyID, repoName, branchRef, commitCount)

	// ── 3. Walk the tip tree and upsert Tree + Blob entities ─────────────────
	repo, err := gogit.Open(sto, fs)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: open repo for tree walk: %w", repoName, branchRef, err)
	}
	tipCommit, err := repo.CommitObject(newHash)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: resolve tip commit %s: %w", repoName, branchRef, newSHA[:8], err)
	}
	tipTree, err := tipCommit.Tree()
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: resolve tip tree: %w", repoName, branchRef, err)
	}
	rootTreeID, err := m.upsertTreeMetadataWithEdges(ctx, repo, tipTree, "", now)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: upsert tree: %w", repoName, branchRef, err)
	}

	// ── 4. Wire head commit → has_tree → root tree ───────────────────────────
	headCommits, _ := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Commit",
		Properties: map[string]any{"sha": newSHA},
	})
	if len(headCommits) == 0 {
		log.Printf("[push-index][%s] repo=%s ref=%s: WARNING head commit entity not found for sha=%s", m.agencyID, repoName, branchRef, newSHA[:8])
		return nil
	}
	commitID := headCommits[0].ID

	if rootTreeID != "" {
		if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "has_tree",
			FromID:   commitID,
			ToID:     rootTreeID,
		}); relErr != nil {
			log.Printf("[push-index][%s] repo=%s ref=%s: WARNING create has_tree edge: %v", m.agencyID, repoName, branchRef, relErr)
		}
	}

	// ── 5. Advance (or create) the branch HEAD pointer ───────────────────────
	branchName := strings.TrimPrefix(branchRef, "refs/heads/")
	branchID, err := m.findBranchIDForRepo(ctx, repoName, branchName)
	if err != nil {
		// Branch entity does not exist yet — create it now (new branch pushed
		// directly via git push without going through the gRPC CreateBranch API).
		log.Printf("[push-index][%s] repo=%s ref=%s: branch entity not found, creating it", m.agencyID, repoName, branchRef)
		repos, lErr := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   m.agencyID,
			TypeID:     "Repository",
			Properties: map[string]any{"name": repoName},
		})
		if lErr != nil || len(repos) == 0 {
			log.Printf("[push-index][%s] repo=%s ref=%s: WARNING cannot find repo entity to create branch: %v", m.agencyID, repoName, branchRef, lErr)
		} else {
			repoID := repos[0].ID
			newBranch, createErr := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
				AgencyID: m.agencyID,
				TypeID:   "Branch",
				Properties: map[string]any{
					"name":       branchName,
					"repo_id":    repoID,
					"created_at": now,
					"updated_at": now,
				},
			})
			if createErr != nil && !errors.Is(createErr, entitygraph.ErrEntityAlreadyExists) {
				log.Printf("[push-index][%s] repo=%s ref=%s: WARNING create branch entity: %v", m.agencyID, repoName, branchRef, createErr)
			} else {
				branchID = newBranch.ID
				// Wire repo → has_branch → branch.
				if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
					AgencyID: m.agencyID,
					Name:     "has_branch",
					FromID:   repoID,
					ToID:     branchID,
				}); relErr != nil {
					log.Printf("[push-index][%s] repo=%s ref=%s: WARNING create has_branch edge: %v", m.agencyID, repoName, branchRef, relErr)
				}
				// Wire branch → belongs_to_repository → repo. GetBranch resolves
				// Branch.RepositoryID via this reverse edge, and ReadFile needs
				// it to open the backend storer for lazy blob hydration.
				if _, relErr := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
					AgencyID: m.agencyID,
					Name:     "belongs_to_repository",
					FromID:   branchID,
					ToID:     repoID,
				}); relErr != nil {
					log.Printf("[push-index][%s] repo=%s ref=%s: WARNING create belongs_to_repository edge: %v", m.agencyID, repoName, branchRef, relErr)
				}
			}
		}
	}
	if branchID != "" {
		if _, advErr := m.advanceBranchHead(ctx, branchID, commitID, ""); advErr != nil {
			log.Printf("[push-index][%s] repo=%s ref=%s: WARNING advance branch head: %v", m.agencyID, repoName, branchRef, advErr)
		}
	}

	// ── 6. .git-graph/ sync (Phase 2 — non-blocking) ────────────────────────
	if err := m.syncGitGraph(ctx, repoName, branchRef, newSHA); err != nil {
		log.Printf("[push-index][%s] repo=%s ref=%s: git-graph sync failed: %v", m.agencyID, repoName, branchRef, err)
	}

	log.Printf("[push-index][%s] repo=%s ref=%s sha=%s: done in %s", m.agencyID, repoName, branchRef, newSHA[:8], time.Since(start))
	return nil
}

// findBranchIDForRepo returns the entity ID of the branch named branchName
// belonging to the repository named repoName for this agency.
func (m *gitManager) findBranchIDForRepo(ctx context.Context, repoName, branchName string) (string, error) {
	// Look up the repo entity.
	repos, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Repository",
		Properties: map[string]any{"name": repoName},
	})
	if err != nil || len(repos) == 0 {
		return "", fmt.Errorf("findBranchIDForRepo: repo %q not found: %w", repoName, err)
	}
	repoID := repos[0].ID

	// List branches belonging to the repo and find by name.
	branches, err := m.listBranchesByRepo(ctx, repoID)
	if err != nil {
		return "", fmt.Errorf("findBranchIDForRepo: list branches for repo %q: %w", repoName, err)
	}
	for _, b := range branches {
		n, _ := b.Properties["name"].(string)
		if n == branchName {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("findBranchIDForRepo: branch %q not found in repo %q", branchName, repoName)
}
