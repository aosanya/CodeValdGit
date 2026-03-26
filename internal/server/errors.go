// Package server implements the GitService gRPC handler.
package server

import (
	"errors"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// toGRPCError maps CodeValdGit domain errors to the appropriate gRPC status.
// ErrMergeConflict is mapped to codes.Aborted with a MergeConflictInfo detail
// message so clients can unpack the conflicting file list.
// Unknown errors are wrapped as codes.Internal.
func toGRPCError(err error) error {
	var mergeErr *codevaldgit.ErrMergeConflict
	switch {
	case errors.As(err, &mergeErr):
		st := status.New(codes.Aborted, err.Error())
		st, _ = st.WithDetails(&pb.MergeConflictInfo{
			BranchId:         mergeErr.TaskID,
			ConflictingFiles: mergeErr.ConflictingFiles,
		})
		return st.Err()
	case errors.Is(err, codevaldgit.ErrRepoNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrRepoNotInitialised):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrRepoAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, codevaldgit.ErrBranchNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrBranchExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, codevaldgit.ErrTagNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrTagAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, codevaldgit.ErrFileNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrRefNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, codevaldgit.ErrDefaultBranchDeleteForbidden):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}
