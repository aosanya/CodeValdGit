// Package app holds the shared runtime wiring for CodeValdGit. Both the
// production binary (cmd/server) and the local dev binary (cmd/dev) call
// Run; they differ only in which environment variables they set before
// loading config.
package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"

	codevaldgit "github.com/aosanya/CodeValdGit"
	pb "github.com/aosanya/CodeValdGit/gen/go/codevaldgit/v1"
	"github.com/aosanya/CodeValdGit/internal/config"
	"github.com/aosanya/CodeValdGit/internal/registrar"
	"github.com/aosanya/CodeValdGit/internal/server"
	gitarangodb "github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	healthpb "github.com/aosanya/CodeValdSharedLib/gen/go/codevaldhealth/v1"
	"github.com/aosanya/CodeValdSharedLib/health"
	"github.com/aosanya/CodeValdSharedLib/serverutil"
)

// Run starts all CodeValdGit subsystems (Cross registrar, ArangoDB backend,
// gRPC + git Smart HTTP via cmux) and blocks until SIGINT/SIGTERM triggers
// graceful shutdown.
func Run(cfg config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Cross registrar (optional) ───────────────────────────────────────────
	var pub codevaldgit.CrossPublisher
	if cfg.CrossGRPCAddr != "" {
		reg, err := registrar.New(
			cfg.CrossGRPCAddr,
			cfg.AdvertiseAddr,
			cfg.AgencyID,
			cfg.PingInterval,
			cfg.PingTimeout,
		)
		if err != nil {
			log.Printf("codevaldgit: registrar: failed to create: %v — continuing without registration", err)
		} else {
			defer reg.Close()
			go reg.Run(ctx)
			pub = reg
		}
	} else {
		log.Println("codevaldgit: CROSS_GRPC_ADDR not set — skipping CodeValdCross registration")
	}

	// ── ArangoDB entitygraph backend (gRPC GitManager) ───────────────────────
	arangoBackend, err := gitarangodb.NewBackend(gitarangodb.Config{
		Endpoint: cfg.ArangoEndpoint,
		Username: cfg.ArangoUser,
		Password: cfg.ArangoPassword,
		Database: cfg.ArangoDatabase,
		Schema:   codevaldgit.DefaultGitSchema(),
	})
	if err != nil {
		return fmt.Errorf("ArangoDB backend: %w", err)
	}

	// ── GIT-017b: Persistent [agency_id, properties.sha] indexes ─────────────
	// Idempotent — ArangoDB skips the operation if the index already exists.
	idxCtx, idxCancel := context.WithTimeout(ctx, 15*time.Second)
	if idxErr := gitarangodb.EnsureGitObjectIndexes(idxCtx, gitarangodb.Config{
		Endpoint: cfg.ArangoEndpoint,
		Username: cfg.ArangoUser,
		Password: cfg.ArangoPassword,
		Database: cfg.ArangoDatabase,
	}); idxErr != nil {
		log.Printf("codevaldgit: EnsureGitObjectIndexes: %v (continuing)", idxErr)
	}
	idxCancel()

	// ── Schema seed (idempotent on startup) ──────────────────────────────────
	if cfg.AgencyID != "" {
		seedCtx, seedCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := entitygraph.SeedSchema(seedCtx, arangoBackend, cfg.AgencyID, codevaldgit.DefaultGitSchema()); err != nil {
			log.Printf("codevaldgit: schema seed: %v", err)
		}
		seedCancel()
	} else {
		log.Println("codevaldgit: CODEVALDGIT_AGENCY_ID not set — skipping schema seed")
	}

	// ── Git Smart HTTP backend (shared ArangoDB DataManager) ─────────────────
	// Both the gRPC GitManager and the Smart HTTP handler share the same
	// entitygraph.DataManager so that repos created via gRPC are immediately
	// accessible for git clone/fetch/push over HTTP.
	gitBackend := gitarangodb.NewArangoStorerBackend(arangoBackend)

	// ── GitManager (gRPC service) ──────────────────────────────────────────
	mgr := codevaldgit.NewGitManager(arangoBackend, arangoBackend, pub, cfg.AgencyID, gitBackend, nil)

	gitHTTPHandler := server.NewGitHTTPHandler(gitBackend, mgr)

	// ── TCP listener ─────────────────────────────────────────────────────────
	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.ListenAddr, err)
	}

	// ── cmux — split one port into gRPC and HTTP ─────────────────────────────
	mux := cmux.New(lis)
	// gRPC connections start with the HTTP/2 client preface (PRI * HTTP/2.0).
	grpcLis := mux.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"),
	)
	httpLis := mux.Match(cmux.Any())

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcServer, _ := serverutil.NewGRPCServer()
	pb.RegisterGitServiceServer(grpcServer, server.New(mgr))
	healthpb.RegisterHealthServiceServer(grpcServer, health.New("codevaldgit"))

	// ── HTTP server (git Smart HTTP) ─────────────────────────────────────────
	httpServer := &http.Server{
		Handler:      gitHTTPHandler,
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
	}

	// ── Signal handling ───────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		log.Println("codevaldgit: shutdown signal received")
		cancel()
	}()

	// ── Start all servers ─────────────────────────────────────────────────────
	go func() {
		if err := grpcServer.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			log.Printf("codevaldgit: gRPC server error: %v", err)
		}
	}()

	go func() {
		if err := httpServer.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			log.Printf("codevaldgit: git HTTP server error: %v", err)
		}
	}()

	log.Printf("codevaldgit: listening on %s (gRPC + git Smart HTTP via cmux)", cfg.ListenAddr)

	go func() {
		if err := mux.Serve(); err != nil {
			log.Printf("codevaldgit: cmux serve error: %v", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-ctx.Done()
	log.Println("codevaldgit: shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	grpcServer.GracefulStop()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("codevaldgit: HTTP server shutdown error: %v", err)
	}
	log.Println("codevaldgit: stopped")
	return nil
}
