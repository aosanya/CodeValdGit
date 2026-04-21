// Package server implements the GitService gRPC handler.
// It wraps a [codevaldgit.GitManager] and translates between proto messages
// and domain types. No business logic lives here — all calls delegate to
// the injected GitManager.
//
// Files in this package:
//   - server.go        — Server struct, constructor, and shared resolve helpers
//   - server_repo.go   — Repository lifecycle handlers
//   - server_branch.go — Branch management handlers
//   - server_tag.go    — Tag management handlers
//   - server_files.go  — File operation handlers
//   - server_history.go — Commit log and diff handlers
//   - server_import.go — Async repository import handlers
//   - server_docs.go   — Documentation edge and graph handlers
//   - mappers.go       — Domain ↔ proto conversion helpers
//   - errors.go        — gRPC error mapping
package server

import (
	"context"
	"fmt"
	"log"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
)

// Server implements pb.GitServiceServer by wrapping a codevaldgit.GitManager.
// Construct via New; register with grpc.Server using
// pb.RegisterGitServiceServer.
type Server struct {
	pb.UnimplementedGitServiceServer
	mgr codevaldgit.GitManager
}

// New constructs a Server backed by the given GitManager.
func New(mgr codevaldgit.GitManager) *Server {
	return &Server{mgr: mgr}
}

// ── Shared resolve helpers ────────────────────────────────────────────────────

// resolveRepoID resolves a repository entity ID from either a direct ID or a
// human-readable name. When repoName is non-empty it tries a name-based lookup
// first, then falls back to treating the value as a UUID for backward
// compatibility with callers that have not yet migrated to name-based URLs.
func (s *Server) resolveRepoID(ctx context.Context, repoID, repoName string) (string, error) {
	if repoName != "" {
		repo, err := s.mgr.GetRepositoryByName(ctx, repoName)
		if err != nil {
			// Fallback: the value might be a UUID from a pre-migration caller.
			repo, err = s.mgr.GetRepository(ctx, repoName)
			if err != nil {
				return "", err
			}
			return repo.ID, nil
		}
		return repo.ID, nil
	}
	return repoID, nil
}

// resolveBranchID resolves a branch entity ID from either a direct ID or a
// human-readable name within the given repository. When branchName is
// non-empty it lists branches and returns the ID of the first match, then
// falls back to treating the value as a UUID for backward compatibility.
func (s *Server) resolveBranchID(ctx context.Context, repoID, branchName string) (string, error) {
	if branchName == "" {
		return repoID, nil
	}
	log.Printf("[resolveBranchID] repoID=%q branchName=%q", repoID, branchName)
	branches, err := s.mgr.ListBranches(ctx, repoID)
	if err != nil {
		log.Printf("[resolveBranchID] ListBranches error: %v", err)
		return "", fmt.Errorf("resolveBranchID: list branches: %w", err)
	}
	log.Printf("[resolveBranchID] %d branches in repo %q", len(branches), repoID)
	for _, b := range branches {
		if b.Name == branchName {
			log.Printf("[resolveBranchID] resolved %q → %q", branchName, b.ID)
			return b.ID, nil
		}
	}
	// Fallback: caller might have passed a UUID directly.
	for _, b := range branches {
		if b.ID == branchName {
			log.Printf("[resolveBranchID] fallback UUID match %q", b.ID)
			return b.ID, nil
		}
	}
	log.Printf("[resolveBranchID] no match for %q in repo %q", branchName, repoID)
	return "", codevaldgit.ErrBranchNotFound
}
