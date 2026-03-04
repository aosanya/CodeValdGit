// Command server starts the CodeValdGit gRPC microservice.
//
// Configuration is via environment variables:
//
//	CODEVALDGIT_PORT           gRPC listener port (required, set in .env)
//	CODEVALDGIT_BACKEND        storage backend: "filesystem" (default) or "arangodb"
//	CODEVALDCROSS_ADDR         CodeValdCross gRPC address for service registration
//	                            heartbeats (optional; omit to disable registration)
//	CODEVALDGIT_PING_INTERVAL  heartbeat cadence in seconds sent to CodeValdCross (default 10s)
//	CODEVALDGIT_PING_TIMEOUT   per-RPC timeout in seconds for each Register call (default 5s)
//
// Filesystem backend:
//
//	CODEVALDGIT_FS_BASE     base path for live repos (default /data/repos)
//	CODEVALDGIT_FS_ARCHIVE  archive path for deleted repos (default /data/archive)
//
// ArangoDB backend:
//
//	ARANGODB_URL   ArangoDB endpoint URL (required)
//	ARANGODB_USER  ArangoDB username (default root)
//	ARANGODB_PASS  ArangoDB password
//	ARANGODB_DB    ArangoDB database name (default cortex)
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
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/grpcserver"
	"github.com/aosanya/CodeValdGit/internal/registrar"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
	"github.com/aosanya/CodeValdSharedLib/serverutil"
)

func main() {
	agencyID := os.Getenv("CODEVALDGIT_AGENCY_ID")
	if agencyID == "" {
		log.Fatal("CODEVALDGIT_AGENCY_ID must be set")
	}

	port := os.Getenv("CODEVALDGIT_PORT")
	if port == "" {
		log.Fatal("CODEVALDGIT_PORT must be set")
	}
	backendName := serverutil.EnvOrDefault("CODEVALDGIT_BACKEND", "filesystem")

	backend, err := initBackend(backendName)
	if err != nil {
		log.Fatalf("failed to initialise backend %q: %v", backendName, err)
	}

	mgr, err := codevaldgit.NewRepoManager(backend)
	if err != nil {
		log.Fatalf("failed to create RepoManager: %v", err)
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen on :%s: %v", port, err)
	}

	grpcServer, _ := serverutil.NewGRPCServer()

	// Register RepoService.
	pb.RegisterRepoServiceServer(grpcServer, grpcserver.New(mgr))

	// Cancellable context — cancelled on shutdown to stop background goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start CodeValdCross registration heartbeat if an address is configured.
	crossAddr := serverutil.EnvOrDefault("CROSS_GRPC_ADDR", serverutil.EnvOrDefault("CODEVALDCROSS_ADDR", ""))
	if crossAddr != "" {
		pingInterval := serverutil.ParseDurationSeconds("CODEVALDGIT_PING_INTERVAL", 10*time.Second)
		pingTimeout := serverutil.ParseDurationSeconds("CODEVALDGIT_PING_TIMEOUT", 5*time.Second)
		advertiseAddr := serverutil.EnvOrDefault("GIT_GRPC_ADVERTISE_ADDR", serverutil.EnvOrDefault("GIT_GRPC_LISTEN_ADDR", ":"+port))
		reg, err := registrar.New(crossAddr, advertiseAddr, agencyID, pingInterval, pingTimeout)
		if err != nil {
			log.Printf("registrar: failed to create: %v — continuing without registration", err)
		} else {
			defer reg.Close()
			go reg.Run(ctx)
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		log.Println("shutdown signal received — draining in-flight RPCs (up to 30 s)")
		cancel()
	}()

	log.Printf("CodeValdGit gRPC server listening on :%s (backend: %s)", port, backendName)
	serverutil.RunWithGracefulShutdown(ctx, grpcServer, lis, 30*time.Second)
}

// initBackend constructs the storage backend selected by CODEVALDGIT_BACKEND.
func initBackend(name string) (codevaldgit.Backend, error) {
	switch name {
	case "filesystem", "":
		basePath := serverutil.EnvOrDefault("CODEVALDGIT_FS_BASE", "/data/repos")
		archivePath := serverutil.EnvOrDefault("CODEVALDGIT_FS_ARCHIVE", "/data/archive")
		return filesystem.NewFilesystemBackend(filesystem.FilesystemConfig{
			BasePath:    basePath,
			ArchivePath: archivePath,
		})

	case "arangodb":
		return initArangoBackend()

	default:
		return nil, fmt.Errorf("unknown backend %q: must be \"filesystem\" or \"arangodb\"", name)
	}
}

// initArangoBackend reads ARANGODB_* env vars and constructs an ArangoDB backend.
func initArangoBackend() (codevaldgit.Backend, error) {
	url := os.Getenv("ARANGODB_URL")
	if url == "" {
		return nil, fmt.Errorf("ARANGODB_URL is required for the arangodb backend")
	}
	return arangodb.NewArangoBackend(arangodb.ArangoConfig{
		Endpoint: url,
		User:     serverutil.EnvOrDefault("ARANGODB_USER", "root"),
		Password: os.Getenv("ARANGODB_PASS"),
		Database: serverutil.EnvOrDefault("ARANGODB_DB", "cortex"),
	})
}

