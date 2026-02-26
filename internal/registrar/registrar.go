// Package registrar sends periodic availability heartbeats to CodeValdCross.
// On startup it immediately registers, then repeats every 10 seconds until the
// context is cancelled.  All errors are logged and retried on the next tick —
// a transient CodeValdCross outage never crashes the CodeValdGit server.
package registrar

import (
	"context"
	"log"
	"time"

	crossv1 "github.com/aosanya/CodeValdGit/gen/go/codevaldcross/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	pingInterval = 10 * time.Second
	pingTimeout  = 5 * time.Second
	serviceName  = "codevaldgit"
)

// producedTopics are the pub/sub topics that CodeValdGit events map to on
// the CodeValdCross bus.
var producedTopics = []string{
	"git.repo.created",
	"git.branch.merged",
	"git.conflict.detected",
}

// Registrar holds a persistent gRPC connection to CodeValdCross and sends
// periodic Register heartbeats. Create with New; start with Run in a goroutine.
type Registrar struct {
	addr   string
	conn   *grpc.ClientConn
	client crossv1.OrchestratorServiceClient
}

// New constructs a Registrar that will heartbeat to the given CodeValdCross
// gRPC address. The connection is lazy — no network I/O occurs until the first
// Register call. Returns an error if the address cannot be parsed.
func New(addr string) (*Registrar, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Registrar{
		addr:   addr,
		conn:   conn,
		client: crossv1.NewOrchestratorServiceClient(conn),
	}, nil
}

// Close releases the underlying gRPC connection. Call after the context passed
// to Run has been cancelled.
func (r *Registrar) Close() {
	if err := r.conn.Close(); err != nil {
		log.Printf("registrar: close connection: %v", err)
	}
}

// Run sends an immediate Register ping, then repeats every 10 seconds until
// ctx is cancelled. It must be called inside a goroutine.
// Transient errors are logged and do not stop the loop.
func (r *Registrar) Run(ctx context.Context) {
	log.Printf("registrar: starting heartbeat to CodeValdCross at %s", r.addr)
	r.ping(ctx)

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("registrar: stopping heartbeat to CodeValdCross")
			return
		case <-ticker.C:
			r.ping(ctx)
		}
	}
}

// ping sends a single Register RPC. Errors are logged; the caller is not
// blocked beyond the 5-second timeout.
func (r *Registrar) ping(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	_, err := r.client.Register(callCtx, &crossv1.RegisterRequest{
		ServiceName: serviceName,
		Produces:    producedTopics,
		Consumes:    []string{},
	})
	if err != nil {
		log.Printf("registrar: Register to CodeValdCross %s: %v", r.addr, err)
		return
	}
	log.Printf("registrar: registered with CodeValdCross at %s", r.addr)
}
