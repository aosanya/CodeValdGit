package codevaldgit

// Event topic constants — the closed set CodeValdGit publishes.
const (
	// TopicRepoCreated fires after a Repository entity is created by InitRepo.
	// Payload: [RepoCreatedPayload].
	TopicRepoCreated = "git.repo.created"

	// TopicRepoImported fires when an async ImportRepo job completes successfully.
	// Payload: [RepoImportedPayload].
	TopicRepoImported = "git.repo.imported"

	// TopicRepoImportFailed fires when an async ImportRepo job fails.
	// Payload: [RepoImportFailedPayload].
	TopicRepoImportFailed = "git.repo.import.failed"

	// TopicRepoImportCancelled fires when an async ImportRepo job is cancelled.
	// Payload: [RepoImportCancelledPayload].
	TopicRepoImportCancelled = "git.repo.import.cancelled"

	// TopicBranchFetched fires when an async FetchBranch job completes successfully.
	// Payload: [BranchFetchedPayload].
	TopicBranchFetched = "git.branch.fetched"

	// TopicBranchMerged fires after a branch is successfully merged into the
	// repository default branch. Payload: [BranchMergedPayload].
	TopicBranchMerged = "git.branch.merged"

	// TopicMergeConflict fires when MergeBranch encounters a conflict that
	// cannot be auto-resolved. Payload: [MergeConflictPayload].
	TopicMergeConflict = "git.conflict.detected"
)

// AllTopics is the closed list of topics this service publishes.
func AllTopics() []string {
	return []string{
		TopicRepoCreated,
		TopicRepoImported,
		TopicRepoImportFailed,
		TopicRepoImportCancelled,
		TopicBranchFetched,
		TopicBranchMerged,
		TopicMergeConflict,
	}
}

// RepoCreatedPayload is the [eventbus.Event.Payload] for [TopicRepoCreated].
type RepoCreatedPayload struct {
	RepoID string
	Name   string
}

// RepoImportedPayload is the [eventbus.Event.Payload] for [TopicRepoImported].
type RepoImportedPayload struct {
	JobID  string
	RepoID string
}

// RepoImportFailedPayload is the [eventbus.Event.Payload] for [TopicRepoImportFailed].
type RepoImportFailedPayload struct {
	JobID        string
	ErrorMessage string
}

// RepoImportCancelledPayload is the [eventbus.Event.Payload] for [TopicRepoImportCancelled].
type RepoImportCancelledPayload struct {
	JobID string
}

// BranchFetchedPayload is the [eventbus.Event.Payload] for [TopicBranchFetched].
type BranchFetchedPayload struct {
	JobID    string
	BranchID string
	RepoID   string
}

// BranchMergedPayload is the [eventbus.Event.Payload] for [TopicBranchMerged].
type BranchMergedPayload struct {
	BranchID string
	RepoID   string
}

// MergeConflictPayload is the [eventbus.Event.Payload] for [TopicMergeConflict].
type MergeConflictPayload struct {
	BranchID         string
	ConflictingFiles []string
}
