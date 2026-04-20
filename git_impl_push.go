// git_impl_push.go — IndexPushedBranch implementation.
//
// After a successful git-receive-pack, this method walks the newly pushed
// commits and materialises Commit, Tree, and Blob entities in the entity
// graph, then advances the branch HEAD pointer to the new tip SHA.
package codevaldgit

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// IndexPushedBranch walks the newly pushed commits reachable from newSHA and
// materialises Commit, Tree, and Blob entities in the entity graph, then
// advances the branch HEAD pointer to the new tip SHA.
//
// It opens the git object store directly from the Backend storer — no network
// clone is required because receive-pack has already stored all objects.
func (m *gitManager) IndexPushedBranch(ctx context.Context, repoName, branchRef, newSHA string) error {
	log.Printf("[push-index][%s] repo=%s ref=%s sha=%s: start", m.agencyID, repoName, branchRef, newSHA[:8])
	start := time.Now()

	// ── 1. Open the go-git repository via the ArangoDB storer ────────────────
	sto, fs, err := m.backend.OpenStorer(ctx, m.agencyID, repoName)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: open storer: %w", repoName, branchRef, err)
	}
	// Use gogit.NewRepository to bypass the config-existence check that
	// gogit.Open performs — the ArangoDB storer has no on-disk .git/config.
	repo, err := gogit.Open(sto, fs)
	if err != nil {
		// Fallback: construct repo directly from storer without config validation.
		repo = gogit.NewRepository(sto, fs)
		log.Printf("[push-index][%s] repo=%s: gogit.Open failed (%v), using NewRepository", m.agencyID, repoName, err)
	}

	newHash := gogitplumbing.NewHash(newSHA)

	// ── 2. Walk and upsert all commits reachable from newSHA ─────────────────
	dummyRef := gogitplumbing.NewHashReference(gogitplumbing.ReferenceName(branchRef), newHash)
	seenSHAs := make(map[string]bool)
	if err := m.walkCommitsOnly(ctx, repo, dummyRef, seenSHAs); err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: walk commits: %w", repoName, branchRef, err)
	}
	log.Printf("[push-index][%s] repo=%s ref=%s: walked %d commit(s)", m.agencyID, repoName, branchRef, len(seenSHAs))

	// ── 3. Walk the tip tree and upsert Tree + Blob entities ─────────────────
	tipCommit, err := repo.CommitObject(newHash)
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: resolve tip commit %s: %w", repoName, branchRef, newSHA[:8], err)
	}
	tipTree, err := tipCommit.Tree()
	if err != nil {
		return fmt.Errorf("IndexPushedBranch %s/%s: resolve tip tree: %w", repoName, branchRef, err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
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

	// ── 5. Advance the branch HEAD pointer ───────────────────────────────────
	branchName := strings.TrimPrefix(branchRef, "refs/heads/")
	branchID, err := m.findBranchIDForRepo(ctx, repoName, branchName)
	if err != nil {
		log.Printf("[push-index][%s] repo=%s ref=%s: WARNING find branch: %v", m.agencyID, repoName, branchRef, err)
	} else if branchID != "" {
		if _, advErr := m.advanceBranchHead(ctx, branchID, commitID); advErr != nil {
			log.Printf("[push-index][%s] repo=%s ref=%s: WARNING advance branch head: %v", m.agencyID, repoName, branchRef, advErr)
		}
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
