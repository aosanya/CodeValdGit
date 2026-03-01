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
	"syscall"
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
	gitv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/grpcserver"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
	crossv1 "github.com/aosanya/CodeValdSharedLib/gen/go/codevaldcross/v1"
	sharedregistrar "github.com/aosanya/CodeValdSharedLib/registrar"
	"github.com/aosanya/CodeValdSharedLib/serverutil"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	agencyID := os.Getenv("CODEVALDGIT_AGENCY_ID")
	if agencyID == "" {
		return fmt.Errorf("CODEVALDGIT_AGENCY_ID must be set")
	}

	listenAddr := serverutil.EnvOrDefault("GIT_GRPC_LISTEN_ADDR", ":"+serverutil.EnvOrDefault("CODEVALDGIT_PORT", "50053"))

	backend, err := buildBackend()
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	manager, err := codevaldgit.NewRepoManager(backend)
	if err != nil {
		return fmt.Errorf("repo manager: %w", err)
	}

	srv := grpcserver.New(manager)

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	grpcServer, _ := serverutil.NewGRPCServer()
	gitv1.RegisterRepoServiceServer(grpcServer, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		log.Println("codevaldgit: shutdown signal received")
		cancel()
	}()

	crossAddr := serverutil.EnvOrDefault("CROSS_GRPC_ADDR", "")
	advertiseAddr := serverutil.EnvOrDefault("GIT_GRPC_ADVERTISE_ADDR", listenAddr)
	pingInterval := serverutil.ParseDurationString("CROSS_PING_INTERVAL", 30*time.Second)
	pingTimeout := serverutil.ParseDurationString("CROSS_PING_TIMEOUT", 5*time.Second)

	if crossAddr != "" {
		reg, err := sharedregistrar.New(
			crossAddr, advertiseAddr, agencyID,
			"codevaldgit",
			[]string{"git.repo.created", "git.branch.merged", "git.conflict.detected"},
			[]string{},
			gitRoutes(),
			pingInterval, pingTimeout,
		)
		if err != nil {
			log.Printf("codevaldgit: registrar: failed to create: %v — continuing without registration", err)
		} else {
			defer reg.Close()
			go reg.Run(ctx)
		}
	} else {
		log.Println("codevaldgit: CROSS_GRPC_ADDR not set — skipping registration with CodeValdCross")
	}

	log.Printf("codevaldgit: listening on %s", listenAddr)
	serverutil.RunWithGracefulShutdown(ctx, grpcServer, lis, 30*time.Second)
	return nil
}

// buildBackend selects and constructs a storage backend from environment variables.
// Uses the ArangoDB backend when GIT_ARANGO_ENDPOINT is set; falls back to filesystem.
func buildBackend() (codevaldgit.Backend, error) {
	if endpoint := os.Getenv("GIT_ARANGO_ENDPOINT"); endpoint != "" {
		cfg := arangodb.ArangoConfig{
			Endpoint:     endpoint,
			Database:     serverutil.EnvOrDefault("GIT_ARANGO_DATABASE", "codevaldgit"),
			User:         serverutil.EnvOrDefault("GIT_ARANGO_USER", "root"),
			Password:     os.Getenv("GIT_ARANGO_PASSWORD"),
			WorktreePath: os.Getenv("GIT_ARANGO_WORKTREE_PATH"),
		}
		log.Printf("codevaldgit: using ArangoDB backend (endpoint=%s db=%s)", cfg.Endpoint, cfg.Database)
		return arangodb.NewArangoBackend(cfg)
	}

	basePath := serverutil.EnvOrDefault("GIT_REPOS_BASE_PATH", "/tmp/codevaldgit/repos")
	archivePath := serverutil.EnvOrDefault("GIT_REPOS_ARCHIVE_PATH", "/tmp/codevaldgit/archive")
	cfg := filesystem.FilesystemConfig{
		BasePath:    basePath,
		ArchivePath: archivePath,
	}
	log.Printf("codevaldgit: using filesystem backend (base=%s archive=%s)", cfg.BasePath, cfg.ArchivePath)
	return filesystem.NewFilesystemBackend(cfg)
}

// gitRoutes returns the HTTP routes that CodeValdGit declares to CodeValdCross.
func gitRoutes() []*crossv1.RouteDeclaration {
	return []*crossv1.RouteDeclaration{
		{
			Method:     "GET",
			Pattern:    "/{agencyId}/tasks/{taskId}/files",
			Capability: "list_task_files",
			GrpcMethod: "/codevaldgit.v1.RepoService/ListDirectory",
			PathBindings: []*crossv1.PathBinding{
				{UrlParam: "agencyId", Field: "agency_id"},
				{UrlParam: "taskId", Field: "ref"},
			},
		},
		{
			Method:     "POST",
			Pattern:    "/{agencyId}/repositories",
			Capability: "init_repo",
			GrpcMethod: "/codevaldgit.v1.RepoService/InitRepo",
			PathBindings: []*crossv1.PathBinding{
				{UrlParam: "agencyId", Field: "agency_id"},
			},
		},
	}
}
