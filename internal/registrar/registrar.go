// Package registrar provides the CodeValdGit service registrar.
// It wraps the shared-library heartbeat registrar and additionally implements
// [codevaldgit.CrossPublisher] so the [GitManager] can notify
// CodeValdCross whenever a git lifecycle event occurs (repo created, branch
// merged, conflict detected).
package registrar

import (
	"context"
	"log"
	"time"

	codevaldgit "github.com/aosanya/CodeValdGit"
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
		[]string{"git.repo.created", "git.branch.merged", "git.conflict.detected"},
		[]string{},
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

// Publish implements [codevaldgit.CrossPublisher].
// It fires a best-effort notification for the given topic and agencyID.
// Currently logs the event; a future iteration will call a Cross Publish RPC
// once CodeValdCross exposes one. Errors are always nil — the git operation
// has already been persisted and must not be rolled back.
func (r *Registrar) Publish(ctx context.Context, topic string, agencyID string) error {
	log.Printf("registrar: publish topic=%q agencyID=%q", topic, agencyID)
	// TODO(CROSS-007): call OrchestratorService.Publish RPC when available.
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
	return all
}
