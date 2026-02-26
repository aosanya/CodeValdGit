package main

import (
	"context"
	"log"
	"time"

	crossv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldcross/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// registerWithCross dials CodeValdCross at crossAddr and calls Register to
// announce this service's availability. crossAddr is read from CROSS_GRPC_ADDR;
// if unset the call is skipped. selfAddr is the address CodeValdCross will use
// to dial back — set GIT_GRPC_ADVERTISE_ADDR when the listen address is not
// directly reachable (e.g. ":50053" in a multi-host deployment).
func registerWithCross(ctx context.Context, crossAddr, selfAddr string) {
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
		log.Printf("codevaldgit: register with CodeValdCross at %s: %v", crossAddr, err)
		return
	}
	log.Printf("codevaldgit: registered with CodeValdCross at %s (self=%s)", crossAddr, selfAddr)
}
