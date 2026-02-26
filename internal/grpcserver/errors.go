// Package grpcserver implements the RepoServiceServer gRPC handler that wraps
// the codevaldgit.RepoManager and codevaldgit.Repo interfaces.
package grpcserver

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// mapError converts a CodeValdGit Go error into the appropriate gRPC status
// error. This must be called on every error returned from RepoManager or Repo
// before it is returned to gRPC callers.
//
// Mapping:
//   - *ErrMergeConflict      → codes.Aborted  + MergeConflictInfo detail
//   - ErrRepoNotFound        → codes.NotFound
//   - ErrBranchNotFound      → codes.NotFound
//   - ErrFileNotFound        → codes.NotFound
//   - ErrRefNotFound         → codes.NotFound
//   - ErrRepoAlreadyExists   → codes.AlreadyExists
//   - ErrBranchExists        → codes.AlreadyExists
//   - anything else          → codes.Internal  (original logged server-side)
func mapError(err error) error {
	if err == nil {
		return nil
	}

	// Check for merge conflict first — it carries extra detail.
	var conflict *codevaldgit.ErrMergeConflict
	if errors.As(err, &conflict) {
		st, stErr := status.New(codes.Aborted, "merge conflict").WithDetails(
			&pb.MergeConflictInfo{
				TaskId:           conflict.TaskID,
				ConflictingFiles: conflict.ConflictingFiles,
			},
		)
		if stErr != nil {
			// Fall through to Internal if we can't marshal the detail.
			return status.Error(codes.Internal, "internal error")
		}
		return st.Err()
	}

	switch {
	case errors.Is(err, codevaldgit.ErrRepoNotFound),
		errors.Is(err, codevaldgit.ErrBranchNotFound),
		errors.Is(err, codevaldgit.ErrFileNotFound),
		errors.Is(err, codevaldgit.ErrRefNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, codevaldgit.ErrRepoAlreadyExists),
		errors.Is(err, codevaldgit.ErrBranchExists):
		return status.Error(codes.AlreadyExists, err.Error())

	default:
		// Log-worthy on the server side; return a generic message to the caller.
		return status.Error(codes.Internal, "internal error")
	}
}
