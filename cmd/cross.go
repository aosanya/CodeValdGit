package main

import (
	"context"
	"log"
	"time"

	crossv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldcross/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// pingCross sends a single Register call to CodeValdCross and returns whether
// it succeeded.
func pingCross(ctx context.Context, client crossv1.OrchestratorServiceClient, crossAddr, selfAddr string) bool {
	req := &crossv1.RegisterRequest{
		ServiceName: "codevaldgit",
		Addr:        selfAddr,
		Produces: []string{
			"git.repo.created",
			"git.branch.merged",
			"git.conflict.detected",
		},
		Consumes: []string{
			"cross.agency.created",
			"cross.task.requested",
		},
	}

	regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := client.Register(regCtx, req); err != nil {
		log.Printf("codevaldgit: ping CodeValdCross at %s: %v", crossAddr, err)
		return false
	}
	log.Printf("codevaldgit: registered with CodeValdCross at %s (self=%s)", crossAddr, selfAddr)
	return true
}

// registerWithCross dials CodeValdCross, sends an initial Register call, then
// re-registers on every pingInterval tick until ctx is cancelled.
// crossAddr is read from CROSS_GRPC_ADDR; if unset the call is skipped.
// selfAddr is the address CodeValdCross will use to dial back — set
// GIT_GRPC_ADVERTISE_ADDR when the listen address is not directly reachable
// (e.g. ":50053" in a multi-host deployment).
// pingInterval controls how often the heartbeat fires; set via
// CROSS_PING_INTERVAL (e.g. "30s"). Zero or negative disables the loop.
func registerWithCross(ctx context.Context, crossAddr, selfAddr string, pingInterval time.Duration) {
	if crossAddr == "" {
		log.Println("codevaldgit: CROSS_GRPC_ADDR not set — skipping registration with CodeValdCross")
		return
	}

	conn, err := grpc.NewClient(crossAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("codevaldgit: dial CodeValdCross %s: %v", crossAddr, err)
		return
	}
	defer conn.Close()

	client := crossv1.NewOrchestratorServiceClient(conn)

	// Initial registration.
	pingCross(ctx, client, crossAddr, selfAddr)

	if pingInterval <= 0 {
		return
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCross(ctx, client, crossAddr, selfAddr)
		}
	}
}
