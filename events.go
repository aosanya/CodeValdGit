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

	// TopicBranchCreate is consumed by CodeValdGit to create a branch on demand.
	// Published by CodeValdAI when the LLM instructs a branch to be created.
	// Payload: [BranchCreatePayload].
	TopicBranchCreate = "git.branch.create"

	// TopicBranchFetched fires when an async FetchBranch job completes successfully,
	// or when a branch is directly created via [TopicBranchCreate].
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

// ConsumedTopics is the closed list of topics this service subscribes to.
func ConsumedTopics() []string {
	return []string{TopicBranchCreate}
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

// BranchCreatePayload is the [eventbus.Event.Payload] for [TopicBranchCreate].
// Published by CodeValdAI; consumed by CodeValdGit to create the branch and
// emit [TopicBranchFetched] on completion.
type BranchCreatePayload struct {
	// Repository is the human-readable name of the target repository.
	Repository string `json:"repository"`
	// Name is the branch name to create (e.g. "feature/UTIL-001-widget").
	// Also accepted as "branch_name" for LLM compatibility.
	Name string `json:"name"`
	// BranchName is an LLM-emitted alias for Name; used when the model writes
	// "branch_name" instead of "name". Resolved in UnmarshalJSON.
	BranchName string `json:"branch_name,omitempty"`
	// FromBranch is the source branch name; defaults to the repo default branch.
	// Also accepted as "base_branch".
	FromBranch string `json:"from_branch,omitempty"`
	// BaseBranch is an LLM-emitted alias for FromBranch.
	BaseBranch string `json:"base_branch,omitempty"`
}

// Resolve merges alias fields into canonical fields so callers only check Name/FromBranch.
func (p *BranchCreatePayload) Resolve() {
	if p.Name == "" && p.BranchName != "" {
		p.Name = p.BranchName
	}
	if p.FromBranch == "" && p.BaseBranch != "" {
		p.FromBranch = p.BaseBranch
	}
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
