// Package registrar provides the CodeValdGit service registrar.
// It wraps the shared-library heartbeat registrar and additionally implements
// [codevaldgit.CrossPublisher] so the [GitManager] can notify
// CodeValdCross whenever a git lifecycle event occurs (repo created, branch
// merged, conflict detected).
package registrar

import (
	"context"
	"encoding/json"
	"log"
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/eventbus"
	sharedregistrar "github.com/aosanya/CodeValdSharedLib/registrar"
	"github.com/aosanya/CodeValdSharedLib/types"
)

// Registrar handles two responsibilities:
//  1. Sending periodic heartbeat registrations to CodeValdCross via the
//     shared-library registrar (Run / Close).
//  2. Implementing [codevaldgit.CrossPublisher] so that GitManager can
//     fire lifecycle events (e.g. "git.repo.created") on successful operations.
//
// Construct via [New]; start heartbeats by calling Run in a goroutine; stop
// by cancelling the context then calling Close.
type Registrar struct {
	heartbeat sharedregistrar.Registrar
}

// Compile-time assertion that *Registrar implements codevaldgit.CrossPublisher.
var _ codevaldgit.CrossPublisher = (*Registrar)(nil)

// New constructs a Registrar that heartbeats to the CodeValdCross gRPC server
// at crossAddr.
//
//   - crossAddr    — host:port of the CodeValdCross gRPC server
//   - advertiseAddr — host:port that Cross dials back on
//   - agencyID     — agency this instance serves
//   - pingInterval — heartbeat cadence; ≤ 0 means only the initial ping
//   - pingTimeout  — per-RPC timeout for each Register call
func New(
	crossAddr, advertiseAddr, agencyID string,
	pingInterval, pingTimeout time.Duration,
) (*Registrar, error) {
	hb, err := sharedregistrar.New(
		crossAddr,
		advertiseAddr,
		agencyID,
		"codevaldgit",
		codevaldgit.AllTopics(),
		codevaldgit.ConsumedTopics(),
		gitRoutes(),
		pingInterval,
		pingTimeout,
	)
	if err != nil {
		return nil, err
	}
	return &Registrar{heartbeat: hb}, nil
}

// Run starts the heartbeat loop, sending an immediate Register ping to
// CodeValdCross then repeating at the configured interval until ctx is
// cancelled. Must be called inside a goroutine.
func (r *Registrar) Run(ctx context.Context) {
	r.heartbeat.Run(ctx)
}

// Close releases the underlying gRPC connection used for heartbeats.
// Call after the context passed to Run has been cancelled.
func (r *Registrar) Close() {
	r.heartbeat.Close()
}

// Publish implements [eventbus.Publisher].
// Marshals the event payload to JSON and forwards it to CodeValdCross via the
// shared-library heartbeat registrar's Publish RPC.
// Errors are logged but not returned — the git operation is already persisted.
func (r *Registrar) Publish(ctx context.Context, e eventbus.Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		log.Printf("registrar[codevaldgit]: marshal payload for topic=%q: %v", e.Topic, err)
		payload = []byte("{}")
	}
	if err := r.heartbeat.Publish(ctx, e.AgencyID, e.Topic, "codevaldgit", string(payload)); err != nil {
		log.Printf("registrar[codevaldgit]: Publish topic=%q: %v", e.Topic, err)
	}
	return nil
}

// gitRoutes returns all HTTP routes that CodeValdGit exposes via Cross.
// See routes.go for the per-concern helper functions.
func gitRoutes() []types.RouteInfo {
	var all []types.RouteInfo
	all = append(all, repoRoutes()...)
	all = append(all, branchRoutes()...)
	all = append(all, tagRoutes()...)
	all = append(all, fileRoutes()...)
	all = append(all, historyRoutes()...)
	all = append(all, smartHTTPRoutes()...)
	all = append(all, importRoutes()...)
	all = append(all, fetchBranchRoutes()...)
	all = append(all, docsRoutes()...)
	for _, r := range all {
		log.Printf("[registrar] route: %s %s → %s", r.Method, r.Pattern, r.GrpcMethod)
	}
	return all
}
