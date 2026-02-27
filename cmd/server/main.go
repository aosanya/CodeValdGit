// Command server starts the CodeValdGit gRPC microservice.
//
// Configuration is via environment variables:
//
//	CODEVALDGIT_PORT           gRPC listener port (default 50051)
//	CODEVALDGIT_BACKEND        storage backend: "filesystem" (default) or "arangodb"
//	CODEVALDCROSS_ADDR         CodeValdCross gRPC address for service registration
//	                            heartbeats (optional; omit to disable registration)
//	CODEVALDGIT_PING_INTERVAL  heartbeat cadence sent to CodeValdCross (default 10s)
//	CODEVALDGIT_PING_TIMEOUT   per-RPC timeout for each Register call (default 5s)
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
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/grpcserver"
	"github.com/aosanya/CodeValdGit/internal/registrar"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdGit/storage/filesystem"
)

func main() {
	agencyID := os.Getenv("CODEVALDGIT_AGENCY_ID")
	if agencyID == "" {
		log.Fatal("CODEVALDGIT_AGENCY_ID must be set")
	}

	port := envOrDefault("CODEVALDGIT_PORT", "50051")
	backendName := envOrDefault("CODEVALDGIT_BACKEND", "filesystem")

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

	grpcServer := grpc.NewServer()

	// Register RepoService.
	pb.RegisterRepoServiceServer(grpcServer, grpcserver.New(mgr))

	// Register gRPC health service.
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection for non-production debugging (grpcurl, grpc-client-cli).
	reflection.Register(grpcServer)

	// Cancellable context — cancelled on shutdown to stop background goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start CodeValdCross registration heartbeat if an address is configured.
	crossAddr := envOrDefault("CROSS_GRPC_ADDR", envOrDefault("CODEVALDCROSS_ADDR", ""))
	if crossAddr != "" {
		pingInterval := parseDuration("CODEVALDGIT_PING_INTERVAL", registrar.DefaultPingInterval)
		pingTimeout := parseDuration("CODEVALDGIT_PING_TIMEOUT", registrar.DefaultPingTimeout)
		advertiseAddr := envOrDefault("GIT_GRPC_ADVERTISE_ADDR", envOrDefault("GIT_GRPC_LISTEN_ADDR", ":"+port))
		reg, err := registrar.New(crossAddr, advertiseAddr, agencyID, pingInterval, pingTimeout)
		if err != nil {
			log.Printf("registrar: failed to create: %v — continuing without registration", err)
		} else {
			defer reg.Close()
			go reg.Run(ctx)
		}
	}

	// Graceful shutdown on SIGTERM / SIGINT.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("CodeValdGit gRPC server listening on :%s (backend: %s)", port, backendName)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	<-quit
	cancel() // stop registrar goroutine before draining gRPC
	log.Println("shutdown signal received — draining in-flight RPCs (up to 30 s)")

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		log.Println("server stopped cleanly")
	case <-time.After(30 * time.Second):
		log.Println("drain timeout exceeded — forcing stop")
		grpcServer.Stop()
	}
}

// initBackend constructs the storage backend selected by CODEVALDGIT_BACKEND.
func initBackend(name string) (codevaldgit.Backend, error) {
	switch name {
	case "filesystem", "":
		basePath := envOrDefault("CODEVALDGIT_FS_BASE", "/data/repos")
		archivePath := envOrDefault("CODEVALDGIT_FS_ARCHIVE", "/data/archive")
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
		User:     envOrDefault("ARANGODB_USER", "root"),
		Password: os.Getenv("ARANGODB_PASS"),
		Database: envOrDefault("ARANGODB_DB", "cortex"),
	})
}

// parseDuration reads key from the environment and parses it as a number of
// seconds (integer). Falls back to def when the variable is unset, empty, or
// unparseable.
func parseDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		log.Printf("config: %s=%q is not a positive integer — using default %s", key, v, def)
		return def
	}
	return time.Duration(secs) * time.Second
}

// envOrDefault returns the value of the environment variable key, or def if
// the variable is unset or empty.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
