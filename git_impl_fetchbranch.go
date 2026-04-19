// git_impl_fetchbranch.go implements the lazy import v2 on-demand fetch methods
// on [gitManager]:
//
//   - [GitManager.FetchBranch] — triggers an async fetch for a stub branch
//   - [GitManager.GetFetchBranchStatus] — returns the current state of a fetch job
//
// Full implementation is added in GIT-023d. These stubs satisfy the interface
// so that the module compiles while upstream sub-tasks are in progress.
package codevaldgit

import (
	"context"
	"fmt"
)

// FetchBranch triggers an async on-demand fetch of the full commit history
// and file tree for a stub branch. See [GitManager.FetchBranch] for the full
// contract.
//
// NOTE: This is a stub — the full implementation is added in GIT-023d.
func (m *gitManager) FetchBranch(ctx context.Context, req FetchBranchRequest) (FetchBranchJob, error) {
	return FetchBranchJob{}, fmt.Errorf("FetchBranch: not yet implemented (GIT-023d)")
}

// GetFetchBranchStatus returns the current state of a fetch job.
// See [GitManager.GetFetchBranchStatus] for the full contract.
//
// NOTE: This is a stub — the full implementation is added in GIT-023d.
func (m *gitManager) GetFetchBranchStatus(ctx context.Context, jobID string) (FetchBranchJob, error) {
	return FetchBranchJob{}, fmt.Errorf("GetFetchBranchStatus: not yet implemented (GIT-023d)")
}
