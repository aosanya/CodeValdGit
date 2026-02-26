package main

import (
	"context"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	gitv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// repoServer implements gitv1.RepoServiceServer.
type repoServer struct {
	gitv1.UnimplementedRepoServiceServer
	manager codevaldgit.RepoManager
}

// newRepoServer constructs a repoServer backed by the given RepoManager.
func newRepoServer(manager codevaldgit.RepoManager) *repoServer {
	return &repoServer{manager: manager}
}

// ListDirectory implements gitv1.RepoServiceServer.
func (s *repoServer) ListDirectory(ctx context.Context, req *gitv1.ListDirectoryRequest) (*gitv1.ListDirectoryResponse, error) {
	repo, err := s.manager.OpenRepo(ctx, req.GetAgencyId())
	if err != nil {
		log.Printf("ListDirectory: OpenRepo agencyID=%s: %v", req.GetAgencyId(), err)
		return nil, err
	}

	files, err := repo.ListDirectory(ctx, req.GetRef(), req.GetPath())
	if err != nil {
		log.Printf("ListDirectory: ListFiles agencyID=%s ref=%s path=%s: %v",
			req.GetAgencyId(), req.GetRef(), req.GetPath(), err)
		return nil, err
	}

	entries := make([]*gitv1.DirectoryEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, &gitv1.DirectoryEntry{
			Name:  f.Name,
			Path:  f.Path,
			IsDir: f.IsDir,
			Size:  f.Size,
		})
	}

	return &gitv1.ListDirectoryResponse{Entries: entries}, nil
}
