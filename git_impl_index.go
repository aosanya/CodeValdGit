// git_impl_index.go — syncGitGraph: reads .git-graph/ files from a pushed
// commit tree and applies keyword + edge sync via the internal gitgraph package.
package codevaldgit

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"

	"github.com/aosanya/CodeValdGit/internal/gitgraph"
)

// syncGitGraph reads all .git-graph/*.json files at the pushed commit tip,
// parses them with the active signal vocabulary, and applies keyword + edge
// updates via [gitgraph.Syncer].
//
// The caller logs any returned error and does not propagate it — a malformed
// .git-graph/ file must never block the push.
func (m *gitManager) syncGitGraph(ctx context.Context, repoName, branchRef, newSHA string) error {
	// 1. Open the storer and resolve the commit tree at newSHA.
	sto, fs, err := m.backend.OpenStorer(ctx, m.agencyID, repoName)
	if err != nil {
		return fmt.Errorf("syncGitGraph: open storer: %w", err)
	}
	repo, err := gogit.Open(sto, fs)
	if err != nil {
		return fmt.Errorf("syncGitGraph: open repo: %w", err)
	}
	tipCommit, err := repo.CommitObject(gogitplumbing.NewHash(newSHA))
	if err != nil {
		return fmt.Errorf("syncGitGraph: resolve commit %s: %w", newSHA[:8], err)
	}
	tipTree, err := tipCommit.Tree()
	if err != nil {
		return fmt.Errorf("syncGitGraph: resolve tip tree: %w", err)
	}

	// 2. Locate the .git-graph/ subtree; absent means nothing to sync.
	gitGraphTree, err := tipTree.Tree(".git-graph")
	if err != nil {
		// .git-graph/ not present in this commit — silently skip.
		return nil
	}

	// 3. Read .signals.json first (spec: parse before other mapping files).
	//    If absent or malformed, DefaultSignals is used; no push failure.
	vocab := gitgraph.DefaultSignals
	for _, e := range gitGraphTree.Entries {
		if e.Mode == filemode.Dir || e.Name != ".signals.json" {
			continue
		}
		data, readErr := readBlobFromRepo(repo, e.Hash)
		if readErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: read .signals.json: %v", m.agencyID, repoName, readErr)
			break
		}
		parsed, parseErr := gitgraph.ParseSignalVocab(data)
		if parseErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: parse .signals.json: %v", m.agencyID, repoName, parseErr)
			// vocab stays DefaultSignals
		} else {
			vocab = parsed
		}
		break
	}

	// 4. Parse each .git-graph/*.json mapping file.
	//    Sub-directories and .signals.json are skipped.
	//    Parse errors are logged per-file; the rest are still processed.
	var mappingFiles []gitgraph.MappingFile
	for _, e := range gitGraphTree.Entries {
		if e.Mode == filemode.Dir || e.Name == ".signals.json" {
			continue
		}
		if !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		data, readErr := readBlobFromRepo(repo, e.Hash)
		if readErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: read %s: %v", m.agencyID, repoName, e.Name, readErr)
			continue
		}
		mf, parseErr := gitgraph.ParseMappingFile(data, vocab)
		if parseErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: parse %s: %v", m.agencyID, repoName, e.Name, parseErr)
			continue
		}
		mappingFiles = append(mappingFiles, mf)
	}

	if len(mappingFiles) == 0 {
		return nil
	}

	// 5. Resolve the branch entity ID for branch-scoped edge writes.
	branchName := strings.TrimPrefix(branchRef, "refs/heads/")
	branchID, err := m.findBranchIDForRepo(ctx, repoName, branchName)
	if err != nil {
		return fmt.Errorf("syncGitGraph: resolve branch %q: %w", branchName, err)
	}

	// 6. Apply keyword upsert + edge hard-sync.
	syncer := gitgraph.NewSyncer(m.dm, m.agencyID, vocab)
	return syncer.Sync(ctx, branchID, mappingFiles)
}

// readBlobFromRepo reads all bytes of the blob identified by hash from repo.
func readBlobFromRepo(repo *gogit.Repository, hash gogitplumbing.Hash) ([]byte, error) {
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("BlobObject %s: %w", hash, err)
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("blob reader: %w", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("io.ReadAll: %w", err)
	}
	return data, nil
}
