// Package registrar provides the CodeValdGit service registrar.
// It wraps the shared-library heartbeat registrar so that cmd entry-points
// stay focused on startup wiring and contain no route declarations.
//
// Construct via [New]; start heartbeats by calling Run in a goroutine; stop
// by cancelling the context then calling Close.
package registrar

import (
	"context"
	"time"

	sharedregistrar "github.com/aosanya/CodeValdSharedLib/registrar"
	"github.com/aosanya/CodeValdSharedLib/types"
)

// Registrar sends periodic heartbeat registrations to CodeValdCross via the
// shared-library registrar.
type Registrar struct {
	heartbeat sharedregistrar.Registrar
}

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
	return all
}
