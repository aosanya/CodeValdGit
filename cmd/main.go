// Command codevaldgit is the binary entry point for the CodeValdGit gRPC
// service. It wires the configured storage backend and starts the server.
// No business logic lives here — only construction and startup.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
	gitv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	listenAddr := getEnv("GIT_GRPC_LISTEN_ADDR", ":"+getEnv("CODEVALDGIT_PORT", "50053"))

	backend, err := buildBackend()
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	manager, err := codevaldgit.NewRepoManager(backend)
	if err != nil {
		return fmt.Errorf("repo manager: %w", err)
	}

	srv := newRepoServer(manager)

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	grpcServer := grpc.NewServer()
	gitv1.RegisterRepoServiceServer(grpcServer, srv)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("codevaldgit: listening on %s", listenAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	crossAddr := getEnv("CROSS_GRPC_ADDR", "")
	advertiseAddr := getEnv("GIT_GRPC_ADVERTISE_ADDR", listenAddr)
	pingInterval := parseDuration(getEnv("CROSS_PING_INTERVAL", "30s"))

	crossCtx, crossCancel := context.WithCancel(context.Background())
	defer crossCancel()
	go registerWithCross(crossCtx, crossAddr, advertiseAddr, pingInterval)

	<-quit
	log.Println("codevaldgit: shutdown signal received — draining in-flight requests (up to 30 s)")

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-done:
		log.Println("codevaldgit: server stopped cleanly")
	case <-shutdownCtx.Done():
		log.Println("codevaldgit: drain timeout exceeded — forcing stop")
		grpcServer.Stop()
	}

	return nil
}

// buildBackend selects and constructs a storage backend from environment variables.
// Uses the ArangoDB backend when GIT_ARANGO_ENDPOINT is set; falls back to filesystem.
func buildBackend() (codevaldgit.Backend, error) {
	if endpoint := os.Getenv("GIT_ARANGO_ENDPOINT"); endpoint != "" {
		cfg := arangodb.ArangoConfig{
			Endpoint:     endpoint,
			Database:     getEnv("GIT_ARANGO_DATABASE", "codevaldgit"),
			User:         getEnv("GIT_ARANGO_USER", "root"),
			Password:     os.Getenv("GIT_ARANGO_PASSWORD"),
			WorktreePath: os.Getenv("GIT_ARANGO_WORKTREE_PATH"),
		}
		log.Printf("codevaldgit: using ArangoDB backend (endpoint=%s db=%s)", cfg.Endpoint, cfg.Database)
		return arangodb.NewArangoBackend(cfg)
	}

	basePath := getEnv("GIT_REPOS_BASE_PATH", "/tmp/codevaldgit/repos")
	archivePath := getEnv("GIT_REPOS_ARCHIVE_PATH", "/tmp/codevaldgit/archive")
	cfg := filesystem.FilesystemConfig{
		BasePath:    basePath,
		ArchivePath: archivePath,
	}
	log.Printf("codevaldgit: using filesystem backend (base=%s archive=%s)", cfg.BasePath, cfg.ArchivePath)
	return filesystem.NewFilesystemBackend(cfg)
}

// parseDuration parses a duration string (e.g. "30s", "1m"). Falls back to
// 30 s on any parse error. A value of 0 disables the ping loop.
func parseDuration(s string) time.Duration {
	if s == "0" {
		return 0
	}
	// Allow plain integer seconds for convenience (e.g. "30").
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Printf("codevaldgit: invalid CROSS_PING_INTERVAL %q — using 30s", s)
		return 30 * time.Second
	}
	return d
}

// getEnv returns the value of key, or fallback if unset or empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
