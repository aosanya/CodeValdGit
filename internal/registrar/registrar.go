// Package registrar sends periodic availability heartbeats to CodeValdCross.
// On startup it immediately registers, then repeats at the configured interval
// until the context is cancelled.  All errors are logged and retried on the
// next tick — a transient CodeValdCross outage never crashes the CodeValdGit
// server.
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
	serviceName = "codevaldgit"

	// DefaultPingInterval is the fallback heartbeat cadence when
	// CODEVALDGIT_PING_INTERVAL is not set.
	DefaultPingInterval = 10 * time.Second

	// DefaultPingTimeout is the fallback per-RPC timeout when
	// CODEVALDGIT_PING_TIMEOUT is not set.
	DefaultPingTimeout = 5 * time.Second
)

// producedTopics are the pub/sub topics that CodeValdGit events map to on
// the CodeValdCross bus.
var producedTopics = []string{
	"git.repo.created",
	"git.branch.merged",
	"git.conflict.detected",
}

// declaredRoutes are the HTTP endpoints that CodeValdGit asks CodeValdCross
// to expose on its HTTP management server. Each route maps to a stable
// capability identifier that Cross's dispatcher resolves to a handler.
// Cross mounts these at registration time — zero Cross source files name these.
var declaredRoutes = []*crossv1.RouteDeclaration{
	{
		Method:     "GET",
		Pattern:    "/{agencyId}/tasks/{taskId}/files",
		Capability: "list_task_files",
	},
	{
		Method:     "POST",
		Pattern:    "/{agencyId}/repositories",
		Capability: "init_repo",
	},
}

// Registrar holds a persistent gRPC connection to CodeValdCross and sends
// periodic Register heartbeats. Create with New; start with Run in a goroutine.
type Registrar struct {
	crossAddr    string
	listenAddr   string
	agencyID     string
	pingInterval time.Duration
	pingTimeout  time.Duration
	conn         *grpc.ClientConn
	client       crossv1.OrchestratorServiceClient
}

// New constructs a Registrar that will heartbeat to the CodeValdCross gRPC
// address at crossAddr. listenAddr is the host:port on which this service
// listens — it is sent in each Register heartbeat so CodeValdCross can dial
// back without a static config entry. agencyID is the agency this instance
// serves (read from CODEVALDGIT_AGENCY_ID); empty is valid for unscoped
// instances. pingInterval controls the heartbeat cadence; pingTimeout caps
// each Register RPC. The connection to crossAddr is lazy — no network I/O
// occurs until the first Register call.
// Returns an error if the address cannot be parsed.
func New(crossAddr, listenAddr, agencyID string, pingInterval, pingTimeout time.Duration) (*Registrar, error) {
	conn, err := grpc.NewClient(crossAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Registrar{
		crossAddr:    crossAddr,
		listenAddr:   listenAddr,
		agencyID:     agencyID,
		pingInterval: pingInterval,
		pingTimeout:  pingTimeout,
		conn:         conn,
		client:       crossv1.NewOrchestratorServiceClient(conn),
	}, nil
}

// Close releases the underlying gRPC connection. Call after the context passed
// to Run has been cancelled.
func (r *Registrar) Close() {
	if err := r.conn.Close(); err != nil {
		log.Printf("registrar: close connection: %v", err)
	}
}

// Run sends an immediate Register ping, then repeats at the configured
// interval until ctx is cancelled. It must be called inside a goroutine.
// Transient errors are logged and do not stop the loop.
func (r *Registrar) Run(ctx context.Context) {
	log.Printf("registrar: starting heartbeat to CodeValdCross at %s (interval=%s timeout=%s)",
		r.crossAddr, r.pingInterval, r.pingTimeout)
	r.ping(ctx)

	ticker := time.NewTicker(r.pingInterval)
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
// blocked beyond the configured timeout.
func (r *Registrar) ping(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, r.pingTimeout)
	defer cancel()

	_, err := r.client.Register(callCtx, &crossv1.RegisterRequest{
		ServiceName: serviceName,
		Produces:    producedTopics,
		Consumes:    []string{},
		Addr:        r.listenAddr,
		AgencyId:    r.agencyID,
		Routes:      declaredRoutes,
	})
	if err != nil {
		log.Printf("registrar: Register to CodeValdCross %s: %v", r.crossAddr, err)
		return
	}
	log.Printf("registrar: registered with CodeValdCross at %s", r.crossAddr)
}
