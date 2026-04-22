package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// repo is the concrete backend-agnostic implementation of [Repo].
// It wraps a go-git repository constructed from a [Backend]-supplied
// storage.Storer and billy.Filesystem. The same implementation is used
// regardless of whether the filesystem or ArangoDB backend is active.
//
// Branch and file operations are implemented in MVP-GIT-003 and MVP-GIT-004.
// Merge is implemented in MVP-GIT-005 and MVP-GIT-006.
// History and diff are implemented in MVP-GIT-007.
type repo struct {
	git *gogit.Repository
}

// newRepo opens a go-git repository from the given storer and working tree,
// returning a [Repo] ready for use. Called by repoManager.OpenRepo.
func newRepo(storer storage.Storer, wt billy.Filesystem) (Repo, error) {
	r, err := gogit.Open(storer, wt)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	return &repo{git: r}, nil
}

// taskBranchName returns the full branch name for a task ID.
func taskBranchName(taskID string) string {
	return "task/" + taskID
}

// CreateBranch creates refs/heads/task/{taskID} pointing at the current HEAD
// of main. Returns [ErrBranchExists] if the branch already exists.
// Returns an error if taskID is empty or main cannot be resolved.
func (r *repo) CreateBranch(_ context.Context, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("CreateBranch: taskID must not be empty")
	}

	mainRefName := plumbing.NewBranchReferenceName("main")
	mainRef, err := r.git.Reference(mainRefName, true)
	if err != nil {
		return fmt.Errorf("CreateBranch: resolve main: %w", err)
	}

	branchRefName := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if _, err := r.git.Reference(branchRefName, false); err == nil {
		// Reference already exists.
		return ErrBranchExists
	}

	newRef := plumbing.NewHashReference(branchRefName, mainRef.Hash())
	if err := r.git.Storer.SetReference(newRef); err != nil {
		return fmt.Errorf("CreateBranch %q: set reference: %w", taskID, err)
	}
	return nil
}

// MergeBranch merges task/{taskID} into main.
//
// If main HEAD is an ancestor of the task branch HEAD (fast-forward possible),
// main is advanced to the task branch tip with no new merge commit.
//
// If main has advanced since the task branch was created (fast-forward not
// possible), an auto-rebase is attempted by cherry-picking task commits onto
// the latest main (MVP-GIT-006). On rebase conflict, [*ErrMergeConflict] is
// returned and the task branch is left in its original pre-rebase state.
//
// The operation is idempotent: if main and task/{taskID} already point at the
// same commit, nil is returned immediately.
// Returns [ErrBranchNotFound] if task/{taskID} does not exist.
func (r *repo) MergeBranch(ctx context.Context, taskID string) error {
	taskRef, err := r.git.Reference(
		plumbing.NewBranchReferenceName(taskBranchName(taskID)), true)
	if err != nil {
		return ErrBranchNotFound
	}

	mainRef, err := r.git.Reference(
		plumbing.NewBranchReferenceName("main"), true)
	if err != nil {
		return fmt.Errorf("MergeBranch %q: resolve main: %w", taskID, err)
	}

	// Idempotent: already merged.
	if mainRef.Hash() == taskRef.Hash() {
		return nil
	}

	// Fast-forward check: is main HEAD an ancestor of task HEAD?
	ok, err := r.isAncestor(mainRef.Hash(), taskRef.Hash())
	if err != nil {
		return fmt.Errorf("MergeBranch %q: ancestor check: %w", taskID, err)
	}
	if ok {
		// Fast-forward: advance main HEAD to the task branch tip.
		newMain := plumbing.NewHashReference(
			plumbing.NewBranchReferenceName("main"), taskRef.Hash())
		if err := r.git.Storer.SetReference(newMain); err != nil {
			return fmt.Errorf("MergeBranch %q: set main reference: %w", taskID, err)
		}
		return nil
	}

	// Main has advanced — auto-rebase required (MVP-GIT-006).
	return r.rebaseAndMerge(ctx, taskID, taskRef, mainRef)
}

// isAncestor reports whether candidateAncestor is reachable by walking
// backwards from tip through the commit graph. Returns (true, nil) when
// candidateAncestor appears in the history of tip, (false, nil) when it does
// not, or (false, error) on graph traversal failure.
func (r *repo) isAncestor(candidateAncestor, tip plumbing.Hash) (bool, error) {
	iter, err := r.git.Log(&gogit.LogOptions{From: tip})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for {
		c, err := iter.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if c.Hash == candidateAncestor {
			return true, nil
		}
	}
}

// rebaseAndMerge cherry-picks each task branch commit (not yet in main) onto
// the current main tip in oldest-first order, then fast-forwards main to the
// rebased task HEAD.
//
// If any cherry-pick encounters a content conflict, [*ErrMergeConflict] is
// returned immediately and the task branch ref is left at its original hash
// (clean state for agent retry). Respects context cancellation.
//
// go-git v5 has no native rebase — the cherry-pick loop is manual per FR-006.
func (r *repo) rebaseAndMerge(ctx context.Context, taskID string,
	taskRef, mainRef *plumbing.Reference) error {

	// 1. Collect task-branch commits not yet in main, oldest-first.
	taskCommits, err := r.commitsSinceAncestor(taskRef.Hash(), mainRef.Hash())
	if err != nil {
		return fmt.Errorf("MergeBranch %q: collect task commits: %w", taskID, err)
	}
	if len(taskCommits) == 0 {
		// All task commits are already in main — idempotent no-op.
		return nil
	}

	// 2. Cherry-pick each commit onto the current main tip.
	currentBase := mainRef.Hash()
	for _, commit := range taskCommits {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("MergeBranch %q: rebase cancelled: %w", taskID, err)
		}
		newHash, conflictFiles, err := r.cherryPick(ctx, currentBase, commit)
		if err != nil {
			return fmt.Errorf("MergeBranch %q: cherry-pick %s: %w", taskID, commit.Hash, err)
		}
		if len(conflictFiles) > 0 {
			// Task branch ref is untouched — return conflict for agent retry.
			return &ErrMergeConflict{
				TaskID:           taskID,
				ConflictingFiles: conflictFiles,
			}
		}
		currentBase = newHash
	}

	// 3. All cherry-picks succeeded.
	// Update task branch ref to rebased tip, then fast-forward main.
	rebasedTaskRef := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName(taskBranchName(taskID)), currentBase)
	if err := r.git.Storer.SetReference(rebasedTaskRef); err != nil {
		return fmt.Errorf("MergeBranch %q: update task ref after rebase: %w", taskID, err)
	}
	newMainRef := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("main"), currentBase)
	if err := r.git.Storer.SetReference(newMainRef); err != nil {
		return fmt.Errorf("MergeBranch %q: fast-forward main after rebase: %w", taskID, err)
	}
	return nil
}

// commitsSinceAncestor walks the commit graph backwards from tip and collects
// all commits up to (but not including) ancestor. The returned slice is
// ordered oldest-first, ready for cherry-pick replay.
func (r *repo) commitsSinceAncestor(tip, ancestor plumbing.Hash) ([]*object.Commit, error) {
	iter, err := r.git.Log(&gogit.LogOptions{From: tip})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []*object.Commit
	for {
		c, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if c.Hash == ancestor {
			break
		}
		commits = append(commits, c)
	}
	// Log returns newest-first; reverse for oldest-first replay order.
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits, nil
}

// repoFileEntry holds a blob hash and file mode for tree construction.
type repoFileEntry struct {
	hash plumbing.Hash
	mode filemode.FileMode
}

// cherryPick applies the file changes introduced by src onto the commit at
// base. Returns (newCommitHash, nil, nil) on success, or
// (plumbing.ZeroHash, conflictPaths, nil) when content conflicts are detected.
// The repository object store is not mutated on conflict.
//
// Conflict rules:
//   - Insert: conflict when base already contains the file at a different hash
//     (both sides independently added the same path with different content).
//   - Modify: conflict when base changed the file since src's parent
//     (both sides independently edited the same file).
//   - Delete: conflict when base changed the file since src's parent
//     (base edited a file that src is trying to delete).
func (r *repo) cherryPick(_ context.Context, base plumbing.Hash, src *object.Commit) (plumbing.Hash, []string, error) {
	baseCommit, err := r.git.CommitObject(base)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: get base commit: %w", err)
	}
	baseTree, err := baseCommit.Tree()
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: get base tree: %w", err)
	}

	// Get src's parent tree (what src was built on top of).
	var srcParentTree *object.Tree
	if src.NumParents() > 0 {
		srcParent, err := src.Parent(0)
		if err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: get src parent: %w", err)
		}
		srcParentTree, err = srcParent.Tree()
		if err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: get src parent tree: %w", err)
		}
	} else {
		srcParentTree = &object.Tree{}
	}

	srcTree, err := src.Tree()
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: get src tree: %w", err)
	}

	// Diff srcParent → src to see what this commit changed.
	srcChanges, err := object.DiffTree(srcParentTree, srcTree)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: diff src: %w", err)
	}

	if len(srcChanges) == 0 {
		// Empty commit — replay with the base tree unchanged.
		h, err := r.writeNewCommit(base, baseTree.Hash, src)
		return h, nil, err
	}

	// Flatten base and srcParent trees for O(1) lookup.
	baseFiles, err := r.treeToFileMap(baseTree)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: flatten base: %w", err)
	}
	srcParentFiles, err := r.treeToFileMap(srcParentTree)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: flatten srcParent: %w", err)
	}

	// Apply changes to a mutable copy of baseFiles.
	merged := make(map[string]repoFileEntry, len(baseFiles))
	for k, v := range baseFiles {
		merged[k] = v
	}

	var conflictFiles []string
	for _, ch := range srcChanges {
		action, err := ch.Action()
		if err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: change action: %w", err)
		}
		switch action {
		case merkletrie.Insert:
			path := ch.To.Name
			if existing, exists := baseFiles[path]; exists {
				if existing.hash != ch.To.TreeEntry.Hash {
					// Both sides independently added this path with different content.
					conflictFiles = append(conflictFiles, path)
					continue
				}
				// Same content added independently — no conflict.
			}
			merged[path] = repoFileEntry{hash: ch.To.TreeEntry.Hash, mode: ch.To.TreeEntry.Mode}

		case merkletrie.Modify:
			path := ch.From.Name
			srcParentEntry, inSrcParent := srcParentFiles[path]
			baseEntry, inBase := baseFiles[path]
			if !inBase {
				// File was deleted in base — conflict.
				conflictFiles = append(conflictFiles, path)
				continue
			}
			if inSrcParent && baseEntry.hash != srcParentEntry.hash {
				// Base changed the file since the common ancestor — conflict.
				conflictFiles = append(conflictFiles, path)
				continue
			}
			merged[path] = repoFileEntry{hash: ch.To.TreeEntry.Hash, mode: ch.To.TreeEntry.Mode}

		case merkletrie.Delete:
			path := ch.From.Name
			srcParentEntry, inSrcParent := srcParentFiles[path]
			baseEntry, inBase := baseFiles[path]
			if inBase && inSrcParent && baseEntry.hash != srcParentEntry.hash {
				// Base changed the file that src is deleting — conflict.
				conflictFiles = append(conflictFiles, path)
				continue
			}
			delete(merged, path)
		}
	}

	if len(conflictFiles) > 0 {
		return plumbing.ZeroHash, conflictFiles, nil
	}

	// Write the new tree and commit.
	newTreeHash, err := r.buildTree(merged, "")
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("cherryPick: write tree: %w", err)
	}
	h, err := r.writeNewCommit(base, newTreeHash, src)
	return h, nil, err
}

// treeToFileMap recursively flattens a git tree into a path → repoFileEntry
// map. Only file blobs are included; directory tree nodes are expanded.
func (r *repo) treeToFileMap(tree *object.Tree) (map[string]repoFileEntry, error) {
	result := make(map[string]repoFileEntry)
	files := tree.Files()
	defer files.Close()
	err := files.ForEach(func(f *object.File) error {
		result[f.Name] = repoFileEntry{hash: f.Blob.Hash, mode: f.Mode}
		return nil
	})
	return result, err
}

// buildTree recursively constructs git tree objects from a flat path→entry map
// and writes them to the repository's object store. prefix selects the subtree
// currently being built ("" for the root). Returns the hash of the written
// tree object.
//
// O(n·d) in the number of files n and maximum directory depth d.
func (r *repo) buildTree(files map[string]repoFileEntry, prefix string) (plumbing.Hash, error) {
	dirFiles := make(map[string]map[string]repoFileEntry)
	var entries []object.TreeEntry

	for path, fe := range files {
		rel := path
		if prefix != "" {
			if !strings.HasPrefix(path, prefix+"/") {
				continue
			}
			rel = path[len(prefix)+1:]
		}
		slash := strings.IndexByte(rel, '/')
		if slash == -1 {
			entries = append(entries, object.TreeEntry{
				Name: rel,
				Mode: fe.mode,
				Hash: fe.hash,
			})
		} else {
			dirName := rel[:slash]
			if dirFiles[dirName] == nil {
				dirFiles[dirName] = make(map[string]repoFileEntry)
			}
			dirFiles[dirName][path] = fe
		}
	}

	// Recursively build sub-trees and add them as Dir entries.
	for dirName, subFiles := range dirFiles {
		subPrefix := dirName
		if prefix != "" {
			subPrefix = prefix + "/" + dirName
		}
		subHash, err := r.buildTree(subFiles, subPrefix)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{
			Name: dirName,
			Mode: filemode.Dir,
			Hash: subHash,
		})
	}

	// Git requires tree entries sorted by name.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	tree := object.Tree{Entries: entries}
	obj := r.git.Storer.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("buildTree %q: encode: %w", prefix, err)
	}
	return r.git.Storer.SetEncodedObject(obj)
}

// writeNewCommit writes a new commit object to the repository's object store.
// The commit has parent as its sole parent, treeHash as its content tree, and
// copies the author name/email and message from src. Committer timestamp is
// set to the current UTC time.
func (r *repo) writeNewCommit(parent, treeHash plumbing.Hash, src *object.Commit) (plumbing.Hash, error) {
	commit := &object.Commit{
		Author:       src.Author,
		Committer:    object.Signature{Name: src.Author.Name, Email: src.Author.Email, When: time.Now().UTC()},
		Message:      src.Message,
		TreeHash:     treeHash,
		ParentHashes: []plumbing.Hash{parent},
	}
	obj := r.git.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writeNewCommit: encode: %w", err)
	}
	return r.git.Storer.SetEncodedObject(obj)
}

// DeleteBranch removes refs/heads/task/{taskID}.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns an error if taskID is empty or equals "main" (protected).
func (r *repo) DeleteBranch(_ context.Context, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("DeleteBranch: taskID must not be empty")
	}
	if taskID == "main" {
		return fmt.Errorf("DeleteBranch: cannot delete the main branch")
	}

	branchRefName := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if _, err := r.git.Reference(branchRefName, false); err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return ErrBranchNotFound
		}
		return fmt.Errorf("DeleteBranch %q: lookup: %w", taskID, err)
	}

	if err := r.git.Storer.RemoveReference(branchRefName); err != nil {
		return fmt.Errorf("DeleteBranch %q: remove reference: %w", taskID, err)
	}
	return nil
}

// WriteFile commits content to path on branch task/{taskID} as a new Git commit
// attributed to author. The branch must already exist — call [Repo.CreateBranch] first.
// path must be relative (no leading "/") and must not contain "..".
// Subdirectories are created automatically. Returns [ErrBranchNotFound] if the
// task branch does not exist.
func (r *repo) WriteFile(_ context.Context, taskID, path, content, author, message string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("WriteFile: path must be relative, got: %s", path)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("WriteFile: path must not contain '..', got: %s", path)
	}

	w, err := r.git.Worktree()
	if err != nil {
		return fmt.Errorf("WriteFile: get worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if err := w.Checkout(&gogit.CheckoutOptions{Branch: branchRef}); err != nil {
		return ErrBranchNotFound
	}

	// Create parent directories if the path is nested.
	if dir := filepath.Dir(path); dir != "." {
		if dirFS, ok := w.Filesystem.(billy.Dir); ok {
			if err := dirFS.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("WriteFile: mkdir %q: %w", dir, err)
			}
		}
	}

	// Write the file content via the billy filesystem.
	f, err := w.Filesystem.Create(path)
	if err != nil {
		return fmt.Errorf("WriteFile: create %q: %w", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return fmt.Errorf("WriteFile: write %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("WriteFile: close %q: %w", path, err)
	}

	if _, err := w.Add(path); err != nil {
		return fmt.Errorf("WriteFile: stage %q: %w", path, err)
	}

	_, err = w.Commit(message, &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  author,
			Email: author + "@codevaldcortex.local",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return fmt.Errorf("WriteFile: commit: %w", err)
	}
	return nil
}

// ReadFile returns the content of path at the given ref.
// ref may be a branch name, tag name, or full commit SHA.
// Returns [ErrRefNotFound] if the ref cannot be resolved, or
// [ErrFileNotFound] if the path does not exist at that ref.
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) ReadFile(_ context.Context, ref, path string) (string, error) {
	hash, err := r.resolveRef(ref)
	if err != nil {
		return "", ErrRefNotFound
	}

	commit, err := r.git.CommitObject(hash)
	if err != nil {
		return "", ErrRefNotFound
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("ReadFile: get tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return "", ErrFileNotFound
	}

	return file.Contents()
}

// DeleteFile removes path from branch task/{taskID} as a new Git commit.
// Returns [ErrBranchNotFound] if the branch does not exist, or
// [ErrFileNotFound] if path does not exist on the branch.
func (r *repo) DeleteFile(_ context.Context, taskID, path, author, message string) error {
	w, err := r.git.Worktree()
	if err != nil {
		return fmt.Errorf("DeleteFile: get worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(taskBranchName(taskID))
	if err := w.Checkout(&gogit.CheckoutOptions{Branch: branchRef}); err != nil {
		return ErrBranchNotFound
	}

	if _, err := w.Filesystem.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ErrFileNotFound
		}
		return fmt.Errorf("DeleteFile: stat %q: %w", path, err)
	}

	if _, err := w.Remove(path); err != nil {
		return fmt.Errorf("DeleteFile: remove %q: %w", path, err)
	}

	_, err = w.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  author,
			Email: author + "@codevaldcortex.local",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return fmt.Errorf("DeleteFile: commit: %w", err)
	}
	return nil
}

// ListDirectory returns the immediate children of path at the given ref.
// An empty path ("") or "/" lists the repository root.
// Each [FileEntry] has Name, Path, IsDir, and Size populated.
// Returns [ErrRefNotFound] for unknown refs, [ErrFileNotFound] if path does
// not exist at ref (and is not the root), or an empty slice for empty dirs.
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) ListDirectory(_ context.Context, ref, path string) ([]FileEntry, error) {
	hash, err := r.resolveRef(ref)
	if err != nil {
		return nil, ErrRefNotFound
	}

	commit, err := r.git.CommitObject(hash)
	if err != nil {
		return nil, ErrRefNotFound
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("ListDirectory: get tree: %w", err)
	}

	// Normalise path: strip leading/trailing slashes.
	path = strings.Trim(path, "/")

	if path != "" {
		sub, err := tree.Tree(path)
		if err != nil {
			return nil, ErrFileNotFound
		}
		tree = sub
	}

	var entries []FileEntry
	for _, e := range tree.Entries {
		var size int64
		if e.Mode.IsFile() {
			if blob, berr := r.git.BlobObject(e.Hash); berr == nil {
				size = blob.Size
			}
		}
		entries = append(entries, FileEntry{
			Name:  e.Name,
			Path:  filepath.Join(path, e.Name),
			IsDir: !e.Mode.IsFile(),
			Size:  size,
		})
	}
	return entries, nil
}

// Log returns commits reachable from ref that touched path, ordered
// newest-first. If path is "" all commits reachable from ref are returned.
// ref may be a branch name, tag name, or full commit SHA.
// Returns [ErrRefNotFound] if ref cannot be resolved.
// A valid ref with a path that has no history returns an empty slice (not an error).
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) Log(_ context.Context, ref, path string) ([]CommitEntry, error) {
	hash, err := r.resolveRef(ref)
	if err != nil {
		return nil, ErrRefNotFound
	}

	opts := &gogit.LogOptions{From: hash}
	if path != "" {
		opts.FileName = &path
	}

	iter, err := r.git.Log(opts)
	if err != nil {
		return nil, fmt.Errorf("Log %q %q: %w", ref, path, err)
	}
	defer iter.Close()

	var commits []CommitEntry
	if err := iter.ForEach(func(c *object.Commit) error {
		commits = append(commits, CommitEntry{
			SHA:       c.Hash.String(),
			Author:    c.Author.Name,
			Message:   c.Message,
			Timestamp: c.Author.When.UTC(),
		})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("Log %q %q: iterate: %w", ref, path, err)
	}
	return commits, nil
}

// Diff returns per-file changes between fromRef and toRef.
// Each [FileDiff] has Path, Operation ("add" | "modify" | "delete"), and
// Patch (unified diff text; empty for binary files).
// Returns [ErrRefNotFound] if either ref cannot be resolved.
// Returns an empty slice when the two refs point at the same tree.
// Safe for concurrent calls; does not touch the working tree.
func (r *repo) Diff(_ context.Context, fromRef, toRef string) ([]FileDiff, error) {
	fromHash, err := r.resolveRef(fromRef)
	if err != nil {
		return nil, ErrRefNotFound
	}
	toHash, err := r.resolveRef(toRef)
	if err != nil {
		return nil, ErrRefNotFound
	}

	// Same commit — no diff.
	if fromHash == toHash {
		return nil, nil
	}

	fromCommit, err := r.git.CommitObject(fromHash)
	if err != nil {
		return nil, ErrRefNotFound
	}
	toCommit, err := r.git.CommitObject(toHash)
	if err != nil {
		return nil, ErrRefNotFound
	}

	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("Diff %q: get from-tree: %w", fromRef, err)
	}
	toTree, err := toCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("Diff %q: get to-tree: %w", toRef, err)
	}

	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return nil, fmt.Errorf("Diff %q %q: tree diff: %w", fromRef, toRef, err)
	}
	if len(changes) == 0 {
		return nil, nil
	}

	diffs := make([]FileDiff, 0, len(changes))
	for _, ch := range changes {
		action, err := ch.Action()
		if err != nil {
			return nil, fmt.Errorf("Diff: change action: %w", err)
		}

		// Determine the canonical path for this change.
		path := ch.To.Name
		if action == merkletrie.Delete {
			path = ch.From.Name
		}

		// Get the unified patch text.
		patch, err := ch.Patch()
		if err != nil {
			return nil, fmt.Errorf("Diff: patch %q: %w", path, err)
		}

		patchText := ""
		for _, fp := range patch.FilePatches() {
			if !fp.IsBinary() {
				patchText = patch.String()
			}
			break // one file per change
		}

		diffs = append(diffs, FileDiff{
			Path:      path,
			Operation: diffOperation(action),
			Patch:     patchText,
		})
	}
	return diffs, nil
}

// diffOperation maps a merkletrie.Action to the FileDiff.Operation string.
func diffOperation(a merkletrie.Action) string {
	switch a {
	case merkletrie.Insert:
		return "add"
	case merkletrie.Delete:
		return "delete"
	default:
		return "modify"
	}
}

// resolveRef resolves a branch name, tag name, or commit SHA to a plumbing.Hash.
// It tries, in order: branch ref → tag ref → raw SHA (full or abbreviated).
// Returns [ErrRefNotFound] if none of those resolve.
func (r *repo) resolveRef(ref string) (plumbing.Hash, error) {
	// Try as a branch reference (refs/heads/{ref}).
	if refObj, err := r.git.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		return refObj.Hash(), nil
	}

	// Try as a tag reference (refs/tags/{ref}).
	if refObj, err := r.git.Reference(plumbing.NewTagReferenceName(ref), true); err == nil {
		return refObj.Hash(), nil
	}

	// Try as a raw commit SHA (full 40-char or abbreviated).
	hash := plumbing.NewHash(ref)
	if hash != plumbing.ZeroHash {
		return hash, nil
	}

	return plumbing.ZeroHash, ErrRefNotFound
}
