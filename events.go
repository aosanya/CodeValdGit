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
	// Published by CodeValdAI when the LLM emits a git.branch.create action.
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

	// TopicMergeRequested fires when a new [MergeRequest] is opened.
	// Payload: [MergeRequestRequestedPayload].
	TopicMergeRequested = "git.merge.requested"

	// TopicMergeCompleted fires when a [MergeRequest] is successfully merged
	// into its target branch. Payload: [MergeRequestCompletedPayload].
	TopicMergeCompleted = "git.merge.completed"

	// TopicMergeFailed fires when a [MergeRequest] terminates in the failed
	// state. Payload: [MergeRequestFailedPayload].
	TopicMergeFailed = "git.merge.failed"

	// TopicFileWrite is consumed by CodeValdGit to write (or update) a file on
	// a branch. Published by CodeValdAI when the LLM emits a git.file.write action.
	// Each write creates a real commit in the ArangoDB-backed git repository.
	// Payload: [FileWritePayload].
	TopicFileWrite = "git.file.write"

	// TopicFileWritten fires after CodeValdGit successfully writes a file via
	// [TopicFileWrite]. Consumed by CodeValdAI to update the run debrief.
	// Payload: [FileWrittenPayload].
	TopicFileWritten = "git.file.written"
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
		TopicMergeRequested,
		TopicMergeCompleted,
		TopicMergeFailed,
		TopicFileWritten,
	}
}

// ConsumedTopics is the closed list of topics this service subscribes to.
func ConsumedTopics() []string {
	return []string{TopicBranchCreate, TopicFileWrite}
}

// RepoCreatedPayload is the [eventbus.Event.Payload] for [TopicRepoCreated].
type RepoCreatedPayload struct {
	RepoID string `json:"repo_id"`
	Name   string `json:"name"`
	// WorkflowRunID links this event to its originating WorkflowRun, when one
	// exists (FEAT-20260602-001). Empty for repos created outside a run.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// RepoImportedPayload is the [eventbus.Event.Payload] for [TopicRepoImported].
type RepoImportedPayload struct {
	JobID  string `json:"job_id"`
	RepoID string `json:"repo_id"`
	// WorkflowRunID links this event to its originating WorkflowRun, when one
	// exists (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// RepoImportFailedPayload is the [eventbus.Event.Payload] for [TopicRepoImportFailed].
type RepoImportFailedPayload struct {
	JobID        string `json:"job_id"`
	ErrorMessage string `json:"error_message"`
	// WorkflowRunID links this event to its originating WorkflowRun, when one
	// exists (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// RepoImportCancelledPayload is the [eventbus.Event.Payload] for [TopicRepoImportCancelled].
type RepoImportCancelledPayload struct {
	JobID string `json:"job_id"`
	// WorkflowRunID links this event to its originating WorkflowRun, when one
	// exists (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// BranchFetchedPayload is the [eventbus.Event.Payload] for [TopicBranchFetched].
type BranchFetchedPayload struct {
	JobID    string `json:"job_id,omitempty"`
	BranchID string `json:"branch_id"`
	RepoID   string `json:"repo_id"`
	// WorkflowRunID links this event to its originating WorkflowRun
	// (FEAT-20260602-001). Empty for branches fetched outside any run.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// BranchCreatePayload is the [eventbus.Event.Payload] for [TopicBranchCreate].
// Published by CodeValdAI; consumed by CodeValdGit to create the branch and
// emit [TopicBranchFetched] on completion.
type BranchCreatePayload struct {
	// Repository is the human-readable name of the target repository.
	Repository string `json:"repository"`
	// Name is the branch name to create (e.g. "feature/UTIL-001-widget").
	// Also accepted as "branch_name" or "branch" for LLM compatibility.
	Name string `json:"name"`
	// BranchName is an LLM-emitted alias for Name.
	BranchName string `json:"branch_name,omitempty"`
	// Branch is an LLM-emitted alias for Name (e.g. when the model writes "branch": "feature/...").
	Branch string `json:"branch,omitempty"`
	// FromBranch is the source branch name; defaults to the repo default branch.
	// Also accepted as "base_branch".
	FromBranch string `json:"from_branch,omitempty"`
	// BaseBranch is an LLM-emitted alias for FromBranch.
	BaseBranch string `json:"base_branch,omitempty"`
	// WorkflowRunID links the new branch to its originating WorkflowRun
	// (FEAT-20260602-001). Copied from the inbound event payload by the chain-
	// through rule. Empty when the action originates outside any run.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// Resolve merges alias fields into canonical fields so callers only check Name/FromBranch.
func (p *BranchCreatePayload) Resolve() {
	if p.Name == "" && p.BranchName != "" {
		p.Name = p.BranchName
	}
	if p.Name == "" && p.Branch != "" {
		p.Name = p.Branch
	}
	if p.FromBranch == "" && p.BaseBranch != "" {
		p.FromBranch = p.BaseBranch
	}
}

// BranchMergedPayload is the [eventbus.Event.Payload] for [TopicBranchMerged].
type BranchMergedPayload struct {
	BranchID string `json:"branch_id"`
	RepoID   string `json:"repo_id"`
	// WorkflowRunID links this event to its originating WorkflowRun
	// (FEAT-20260602-001). Copied from the merged Branch entity.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// MergeConflictPayload is the [eventbus.Event.Payload] for [TopicMergeConflict].
type MergeConflictPayload struct {
	BranchID         string   `json:"branch_id"`
	ConflictingFiles []string `json:"conflicting_files"`
	// WorkflowRunID links this event to its originating WorkflowRun
	// (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// MergeRequestRequestedPayload is the [eventbus.Event.Payload] for [TopicMergeRequested].
type MergeRequestRequestedPayload struct {
	MergeRequestID string `json:"merge_request_id"`
	RepoID         string `json:"repo_id"`
	// SourceBranchID is the branch whose commits are being requested for merge.
	SourceBranchID string `json:"source_branch_id"`
	// TargetBranchID is the branch the source will be merged into.
	TargetBranchID string `json:"target_branch_id,omitempty"`
	Title          string `json:"title"`
	// WorkflowRunID links the MR to its originating WorkflowRun (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// MergeRequestCompletedPayload is the [eventbus.Event.Payload] for [TopicMergeCompleted].
type MergeRequestCompletedPayload struct {
	MergeRequestID  string `json:"merge_request_id"`
	RepoID          string `json:"repo_id"`
	SourceBranchID  string `json:"source_branch_id"`
	TargetBranchID  string `json:"target_branch_id,omitempty"`
	MergedCommitSHA string `json:"merged_commit_sha,omitempty"`
	// WorkflowRunID links the MR to its originating WorkflowRun (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// MergeRequestFailedPayload is the [eventbus.Event.Payload] for [TopicMergeFailed].
type MergeRequestFailedPayload struct {
	MergeRequestID string `json:"merge_request_id"`
	RepoID         string `json:"repo_id"`
	SourceBranchID string `json:"source_branch_id"`
	ErrorMessage   string `json:"error_message"`
	// WorkflowRunID links the MR to its originating WorkflowRun (FEAT-20260602-001).
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// FileWritePayload is the [eventbus.Event.Payload] for [TopicFileWrite].
// Published by CodeValdAI when the LLM emits a git.file.write action; consumed
// by CodeValdGit to create a commit on the branch via [GitManager.WriteFile].
type FileWritePayload struct {
	// RunID is the CodeValdAI AgentRun ID that emitted this action.
	// Carried through so CodeValdGit can include it in the git.file.written
	// response event, enabling CodeValdAI to update the run debrief.
	RunID string `json:"run_id,omitempty"`
	// Repository is the human-readable name of the target repository.
	Repository string `json:"repository"`
	// BranchName is the name of the branch to write to.
	// Also accepted as "branch" for LLM compatibility.
	BranchName string `json:"branch_name"`
	// Branch is an LLM-emitted alias for BranchName.
	Branch string `json:"branch,omitempty"`
	// Path is the file path relative to the repository root, e.g. "src/app/foo.ts".
	Path string `json:"path"`
	// Content is the full file content to write.
	Content string `json:"content"`
	// Message is the git commit message. Defaults to "Update <path>" when empty.
	Message string `json:"message,omitempty"`
	// AuthorName is the name recorded as the commit author. Optional.
	AuthorName string `json:"author_name,omitempty"`
	// AuthorEmail is the email recorded as the commit author. Optional.
	AuthorEmail string `json:"author_email,omitempty"`
	// Keywords are optional keyword annotations to tag the resulting blob with.
	Keywords []FileWriteKeyword `json:"keywords,omitempty"`
	// WorkflowRunID links the resulting commit to its originating WorkflowRun
	// (FEAT-20260602-001). Carried through into [FileWrittenPayload].
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

// Resolve merges the "branch" alias into BranchName so callers only check BranchName.
func (p *FileWritePayload) Resolve() {
	if p.BranchName == "" && p.Branch != "" {
		p.BranchName = p.Branch
	}
}

// FileWriteKeyword tags the written blob with a Keyword entity in the git graph.
// It mirrors the full [Keyword] entity schema so agents can specify the keyword's
// place in the taxonomy tree as well as the signal depth of the blob's coverage.
type FileWriteKeyword struct {
	// Name is the keyword label, e.g. "authentication" or "oauth". Required.
	Name string `json:"name"`
	// Description is an optional plain-text summary of what this keyword means.
	Description string `json:"description,omitempty"`
	// Scope is an optional grouping label: "domain", "layer", "technology", etc.
	// Mirrors [Keyword.scope].
	Scope string `json:"scope,omitempty"`
	// Parent is the name of the parent keyword in the taxonomy tree.
	// Empty means this is a root keyword. If the parent does not exist it is
	// created automatically before the child.
	Parent string `json:"parent,omitempty"`
	// Signal is the depth at which this Blob covers the keyword.
	// Required. Values: "surface" | "index" | "structural" | "contributor" | "authority".
	Signal string `json:"signal"`
	// Note is a plain-text explanation of how this file covers the keyword
	// at the declared signal depth.
	Note string `json:"note,omitempty"`
}

// FileWrittenPayload is the [eventbus.Event.Payload] for [TopicFileWritten].
// Published by CodeValdGit after a successful WriteFile; consumed by CodeValdAI
// to update the run debrief with the real commit SHA.
type FileWrittenPayload struct {
	// RunID is the AgentRun ID carried through from the originating FileWritePayload.
	RunID string `json:"run_id,omitempty"`
	// Repository is the repository the file was written to.
	Repository string `json:"repository"`
	// BranchName is the branch the commit was made on.
	BranchName string `json:"branch_name"`
	// Path is the file path that was written.
	Path string `json:"path"`
	// CommitSHA is the SHA of the new commit.
	CommitSHA string `json:"commit_sha"`
	// WorkflowRunID is carried through from the originating [FileWritePayload]
	// (FEAT-20260602-001). Empty when no run context was supplied.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}
