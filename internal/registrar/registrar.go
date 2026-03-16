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

	crossv1 "github.com/aosanya/CodeValdSharedLib/gen/go/codevaldcross/v1"
	sharedregistrar "github.com/aosanya/CodeValdSharedLib/registrar"
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

// gitRoutes returns the HTTP routes that CodeValdGit exposes via Cross.
// All routes are prefixed with /git/{agencyId} to disambiguate git endpoints
// from other services.
func gitRoutes() []*crossv1.RouteDeclaration {
	return []*crossv1.RouteDeclaration{
		{
			Method:     "GET",
			Pattern:    "/git/{agencyId}/tasks/{taskId}/files",
			Capability: "list_task_files",
			GrpcMethod: "/codevaldgit.v1.RepoService/ListDirectory",
			PathBindings: []*crossv1.PathBinding{
				{UrlParam: "agencyId", Field: "agency_id"},
				{UrlParam: "taskId", Field: "ref"},
			},
		},
		{
			Method:     "POST",
			Pattern:    "/git/{agencyId}/repositories",
			Capability: "init_repo",
			GrpcMethod: "/codevaldgit.v1.RepoService/InitRepo",
			PathBindings: []*crossv1.PathBinding{
				{UrlParam: "agencyId", Field: "agency_id"},
			},
		},
	}
}
