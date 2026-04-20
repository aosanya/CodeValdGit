// Package server implements the GitService gRPC handler.
package server

import (
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// repoToProto converts a domain Repository to its proto representation.
func repoToProto(r codevaldgit.Repository) *pb.Repository {
	return &pb.Repository{
		Id:            r.ID,
		AgencyId:      r.AgencyID,
		Name:          r.Name,
		Description:   r.Description,
		DefaultBranch: r.DefaultBranch,
		CreatedAt:     parseTimestamp(r.CreatedAt),
		UpdatedAt:     parseTimestamp(r.UpdatedAt),
		SourceUrl:     r.SourceURL,
	}
}

// branchToProto converts a domain Branch to its proto representation.
func branchToProto(b codevaldgit.Branch) *pb.Branch {
	return &pb.Branch{
		Id:           b.ID,
		RepositoryId: b.RepositoryID,
		Name:         b.Name,
		IsDefault:    b.IsDefault,
		HeadCommitId: b.HeadCommitID,
		CreatedAt:    parseTimestamp(b.CreatedAt),
		UpdatedAt:    parseTimestamp(b.UpdatedAt),
	}
}

// tagToProto converts a domain Tag to its proto representation.
func tagToProto(t codevaldgit.Tag) *pb.Tag {
	return &pb.Tag{
		Id:           t.ID,
		RepositoryId: t.RepositoryID,
		Name:         t.Name,
		Sha:          t.SHA,
		Message:      t.Message,
		TaggerName:   t.TaggerName,
		TaggerAt:     parseTimestamp(t.TaggerAt),
		CreatedAt:    parseTimestamp(t.CreatedAt),
	}
}

// commitToProto converts a domain Commit to its proto representation.
func commitToProto(c codevaldgit.Commit) *pb.Commit {
	return &pb.Commit{
		Id:             c.ID,
		RepositoryId:   c.RepositoryID,
		Sha:            c.SHA,
		Message:        c.Message,
		AuthorName:     c.AuthorName,
		AuthorEmail:    c.AuthorEmail,
		AuthorAt:       parseTimestamp(c.AuthorAt),
		CommitterName:  c.CommitterName,
		CommitterEmail: c.CommitterEmail,
		CommittedAt:    parseTimestamp(c.CommittedAt),
		TreeId:         c.TreeID,
		ParentIds:      c.ParentIDs,
		CreatedAt:      parseTimestamp(c.CreatedAt),
	}
}

// blobToProto converts a domain Blob to its proto representation.
func blobToProto(b codevaldgit.Blob) *pb.Blob {
	return &pb.Blob{
		Id:        b.ID,
		Sha:       b.SHA,
		Path:      b.Path,
		Size:      b.Size,
		Encoding:  b.Encoding,
		Content:   b.Content,
		TreeId:    b.TreeID,
		CreatedAt: parseTimestamp(b.CreatedAt),
	}
}

// fileEntryToProto converts a domain FileEntry to its proto representation.
func fileEntryToProto(e codevaldgit.FileEntry) *pb.FileEntry {
	return &pb.FileEntry{
		Name:  e.Name,
		Path:  e.Path,
		IsDir: e.IsDir,
		Size:  e.Size,
	}
}

// commitEntryToProto converts a domain CommitEntry to its proto representation.
func commitEntryToProto(e codevaldgit.CommitEntry) *pb.CommitEntry {
	return &pb.CommitEntry{
		Sha:       e.SHA,
		Author:    e.Author,
		Message:   e.Message,
		Timestamp: timestamppb.New(e.Timestamp),
	}
}

// fileDiffToProto converts a domain FileDiff to its proto representation.
func fileDiffToProto(d codevaldgit.FileDiff) *pb.FileDiff {
	return &pb.FileDiff{
		Path:      d.Path,
		Operation: d.Operation,
		Patch:     d.Patch,
	}
}

// fetchBranchJobToProto converts a domain FetchBranchJob to its proto
// representation.
func fetchBranchJobToProto(j codevaldgit.FetchBranchJob) *pb.FetchBranchJob {
	return &pb.FetchBranchJob{
		Id:           j.ID,
		AgencyId:     j.AgencyID,
		RepoId:       j.RepoID,
		BranchName:   j.BranchName,
		Status:       j.Status,
		ErrorMessage: j.ErrorMessage,
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
}

// parseTimestamp parses an ISO 8601 / RFC 3339 string into a protobuf
// Timestamp. Returns nil on empty input or parse failure.
func parseTimestamp(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(t)
}
