package codevaldgit

import "errors"

// ErrRepoNotFound is returned when a repository does not exist at the
// expected path (live or archive).
var ErrRepoNotFound = errors.New("repository not found")

// ErrRepoAlreadyExists is returned by [RepoManager.InitRepo] when a
// repository already exists for the given agency ID.
var ErrRepoAlreadyExists = errors.New("repository already exists")

// ErrBranchNotFound is returned when an operation targets a task branch
// that does not exist in the repository.
var ErrBranchNotFound = errors.New("branch not found")

// ErrBranchExists is returned by [Repo.CreateBranch] when a branch with
// the given task ID already exists.
var ErrBranchExists = errors.New("branch already exists")

// ErrFileNotFound is returned when the requested path does not exist in
// the repository tree at the specified ref.
var ErrFileNotFound = errors.New("file not found")

// ErrRefNotFound is returned when a ref (branch name, tag name, or commit
// SHA) cannot be resolved in the repository.
var ErrRefNotFound = errors.New("ref not found (branch, tag, or SHA)")

// ── v2 GitManager errors ──────────────────────────────────────────────────────

// ErrRepoNotInitialised is returned by [GitManager] methods when no
// Repository entity has been created yet for this agency. Call
// [GitManager.InitRepo] first.
var ErrRepoNotInitialised = errors.New("repository not initialised")

// ErrTagAlreadyExists is returned by [GitManager.CreateTag] when a Tag
// entity with the given name already exists in the repository.
var ErrTagAlreadyExists = errors.New("tag already exists")

// ErrTagNotFound is returned by [GitManager.GetTag] and [GitManager.DeleteTag]
// when no Tag entity with the given ID exists.
var ErrTagNotFound = errors.New("tag not found")

// ErrDefaultBranchDeleteForbidden is returned by [GitManager.DeleteBranch]
// when the caller attempts to delete the repository's default branch.
var ErrDefaultBranchDeleteForbidden = errors.New("cannot delete the default branch")

// ── Import errors ─────────────────────────────────────────────────────────────

// ErrImportJobNotFound is returned by [GitManager.GetImportStatus] and
// [GitManager.CancelImport] when no import job with the given ID exists for
// this agency.
var ErrImportJobNotFound = errors.New("import job not found")

// ErrImportInProgress is returned by [GitManager.ImportRepo] when an import
// job with status "pending" or "running" already exists for this agency.
// Each agency supports at most one concurrent import.
var ErrImportInProgress = errors.New("import already in progress")

// ErrImportJobNotCancellable is returned by [GitManager.CancelImport] when
// the job has already reached a terminal state (completed, failed, or
// cancelled).
var ErrImportJobNotCancellable = errors.New("import job is not cancellable")
