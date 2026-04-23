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
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"

	"github.com/aosanya/CodeValdGit/internal/gitgraph"
)

// syncGitGraph reads all .git-graph/*.json files at the pushed commit tip,
// parses them with the active signal vocabulary, and applies keyword + edge
// updates via [gitgraph.Syncer].
//
// The caller logs any returned error and does not propagate it — a malformed
// .git-graph/ file must never block the push.
func (m *gitManager) syncGitGraph(ctx context.Context, repoName, branchRef, newSHA string) error {
	log.Printf("[gitgraph][%s] syncGitGraph: START repo=%s branch=%s tip=%s", m.agencyID, repoName, branchRef, shortSHA(newSHA))

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
		log.Printf("[gitgraph][%s] syncGitGraph: no .git-graph/ tree at tip — skipping sync", m.agencyID)
		return nil
	}
	log.Printf("[gitgraph][%s] syncGitGraph: .git-graph/ tree located — walking recursively", m.agencyID)

	// 3. Read .signals.json first (spec: parse before other mapping files).
	//    If absent or malformed, DefaultSignals is used; no push failure.
	vocab := gitgraph.DefaultSignals
	vocabSource := "default"
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
			vocabSource = ".git-graph/.signals.json"
		}
		break
	}
	log.Printf("[gitgraph][%s] syncGitGraph: signal vocab source=%s signals=%d", m.agencyID, vocabSource, len(vocab.Signals))

	// 4. Recursively parse every .git-graph/**/*.json mapping file.
	//    Top-level .signals.json is excluded (handled in step 3).
	//    Parse errors are logged per-file; the rest are still processed.
	var mappingFiles []gitgraph.MappingFile
	var seenJSON, parsedOK int
	walkErr := gitGraphTree.Files().ForEach(func(f *gogitobject.File) error {
		// f.Name is the path relative to gitGraphTree (e.g. "lib/features/auth.json").
		if f.Name == ".signals.json" {
			return nil // handled above
		}
		if !strings.HasSuffix(f.Name, ".json") {
			return nil
		}
		seenJSON++
		log.Printf("[gitgraph][%s] syncGitGraph: found .git-graph/%s", m.agencyID, f.Name)

		data, readErr := readBlobFromRepo(repo, f.Blob.Hash)
		if readErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: read %s: %v", m.agencyID, repoName, f.Name, readErr)
			return nil
		}
		mf, parseErr := gitgraph.ParseMappingFile(data, vocab)
		if parseErr != nil {
			log.Printf("[gitgraph][%s] syncGitGraph repo=%s: parse %s: %v", m.agencyID, repoName, f.Name, parseErr)
			return nil
		}
		parsedOK++
		log.Printf("[gitgraph][%s] syncGitGraph: parsed %s — keywords=%d mappings=%d", m.agencyID, f.Name, len(mf.Keywords), len(mf.Mappings))
		mappingFiles = append(mappingFiles, mf)
		return nil
	})
	if walkErr != nil {
		log.Printf("[gitgraph][%s] syncGitGraph: walk .git-graph/: %v", m.agencyID, walkErr)
	}
	log.Printf("[gitgraph][%s] syncGitGraph: walk complete — json_seen=%d parsed_ok=%d", m.agencyID, seenJSON, parsedOK)

	if len(mappingFiles) == 0 {
		log.Printf("[gitgraph][%s] syncGitGraph: no mapping files to sync — END", m.agencyID)
		return nil
	}

	// 5. Resolve the branch entity ID for branch-scoped edge writes.
	branchName := strings.TrimPrefix(branchRef, "refs/heads/")
	branchID, err := m.findBranchIDForRepo(ctx, repoName, branchName)
	if err != nil {
		return fmt.Errorf("syncGitGraph: resolve branch %q: %w", branchName, err)
	}
	log.Printf("[gitgraph][%s] syncGitGraph: resolved branch %q → id=%s", m.agencyID, branchName, branchID)

	// 6. Apply keyword upsert + edge hard-sync.
	syncer := gitgraph.NewSyncer(m.dm, m.agencyID, vocab)
	log.Printf("[gitgraph][%s] syncGitGraph: handing %d mapping file(s) to Syncer.Sync", m.agencyID, len(mappingFiles))
	syncErr := syncer.Sync(ctx, branchID, mappingFiles)
	log.Printf("[gitgraph][%s] syncGitGraph: END repo=%s err=%v", m.agencyID, repoName, syncErr)
	return syncErr
}

// shortSHA returns the first 8 chars of sha for log readability, or the full
// string if it is shorter than 8 chars.
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
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
