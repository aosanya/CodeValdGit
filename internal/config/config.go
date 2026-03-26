// Package config loads CodeValdGit runtime configuration from environment
// variables. All values have sensible defaults so the service starts in
// standalone mode (no Cross registration, in-memory ArangoDB worktree) with
// zero environment variables set.
package config

import (
	"time"

	"github.com/aosanya/CodeValdSharedLib/serverutil"
)

// Config holds all runtime configuration for the CodeValdGit service.
type Config struct {
	// ListenAddr is the host:port the combined gRPC + git Smart HTTP server
	// listens on. Controlled by GIT_GRPC_LISTEN_ADDR; falls back to
	// ":CODEVALDGIT_PORT" (default port 50053).
	ListenAddr string

	// ArangoEndpoint is the ArangoDB HTTP endpoint (default "http://localhost:8529").
	ArangoEndpoint string

	// ArangoUser is the ArangoDB username (default "root").
	ArangoUser string

	// ArangoPassword is the ArangoDB password.
	ArangoPassword string

	// ArangoDatabase is the ArangoDB database name (default "codevaldgit").
	ArangoDatabase string

	// ArangoWorktreePath is the local path for the billy.Filesystem working
	// tree used by the ArangoDB backend. Empty string means use an in-memory
	// (memfs) working tree.
	ArangoWorktreePath string

	// ReposBasePath is the root directory for live agency repositories
	// (filesystem backend only). Default: "/tmp/codevaldgit/repos".
	ReposBasePath string

	// ReposArchivePath is the root directory for archived (soft-deleted)
	// repositories (filesystem backend only).
	// Default: "/tmp/codevaldgit/archive".
	ReposArchivePath string

	// CrossGRPCAddr is the CodeValdCross gRPC address used for heartbeat
	// registration. Empty string disables registration (standalone mode).
	CrossGRPCAddr string

	// AdvertiseAddr is the address CodeValdCross dials back on.
	// Defaults to ListenAddr when unset.
	AdvertiseAddr string

	// AgencyID is the agency this instance is scoped to, sent in every
	// Register heartbeat. Empty string means this instance serves all agencies.
	AgencyID string

	// PingInterval is the heartbeat cadence sent to CodeValdCross (default 30s).
	// Set to 0 to send only the initial registration ping.
	PingInterval time.Duration

	// PingTimeout is the per-RPC timeout for each Register call (default 5s).
	PingTimeout time.Duration
}

// Load reads runtime configuration from environment variables, falling back to
// sensible defaults for any variable that is unset or empty.
//
// Environment variables:
//
//	GIT_GRPC_LISTEN_ADDR     - full listen address (overrides CODEVALDGIT_PORT)
//	CODEVALDGIT_PORT         - port when GIT_GRPC_LISTEN_ADDR is unset (default "50053")
//	GIT_ARANGO_ENDPOINT      - ArangoDB endpoint (default "http://localhost:8529")
//	GIT_ARANGO_USER          - ArangoDB username (default "root")
//	GIT_ARANGO_PASSWORD      - ArangoDB password (default "")
//	GIT_ARANGO_DATABASE      - ArangoDB database name (default "codevaldgit")
//	GIT_ARANGO_WORKTREE_PATH - working tree root for ArangoDB backend (default ""; uses memfs)
//	GIT_REPOS_BASE_PATH      - live repos root for filesystem backend (default "/tmp/codevaldgit/repos")
//	GIT_REPOS_ARCHIVE_PATH   - archive root (default "/tmp/codevaldgit/archive")
//	CROSS_GRPC_ADDR          - CodeValdCross gRPC address (default ""; disables registration)
//	GIT_GRPC_ADVERTISE_ADDR  - address Cross dials back on (default: ListenAddr)
//	CODEVALDGIT_AGENCY_ID    - agency scope for this instance (default ""; all agencies)
//	CROSS_PING_INTERVAL      - heartbeat cadence, Go duration string (default "30s")
//	CROSS_PING_TIMEOUT       - per-RPC Register timeout, Go duration string (default "5s")
func Load() Config {
	port := serverutil.EnvOrDefault("CODEVALDGIT_PORT", "50053")
	listenAddr := serverutil.EnvOrDefault("GIT_GRPC_LISTEN_ADDR", ":"+port)
	return Config{
		ListenAddr:         listenAddr,
		ArangoEndpoint:     serverutil.EnvOrDefault("GIT_ARANGO_ENDPOINT", "http://localhost:8529"),
		ArangoUser:         serverutil.EnvOrDefault("GIT_ARANGO_USER", "root"),
		ArangoPassword:     serverutil.EnvOrDefault("GIT_ARANGO_PASSWORD", ""),
		ArangoDatabase:     serverutil.EnvOrDefault("GIT_ARANGO_DATABASE", "codevaldgit"),
		ArangoWorktreePath: serverutil.EnvOrDefault("GIT_ARANGO_WORKTREE_PATH", ""),
		ReposBasePath:      serverutil.EnvOrDefault("GIT_REPOS_BASE_PATH", "/tmp/codevaldgit/repos"),
		ReposArchivePath:   serverutil.EnvOrDefault("GIT_REPOS_ARCHIVE_PATH", "/tmp/codevaldgit/archive"),
		CrossGRPCAddr:      serverutil.EnvOrDefault("CROSS_GRPC_ADDR", ""),
		AdvertiseAddr:      serverutil.EnvOrDefault("GIT_GRPC_ADVERTISE_ADDR", listenAddr),
		AgencyID:           serverutil.EnvOrDefault("CODEVALDGIT_AGENCY_ID", ""),
		PingInterval:       serverutil.ParseDurationString("CROSS_PING_INTERVAL", 30*time.Second),
		PingTimeout:        serverutil.ParseDurationString("CROSS_PING_TIMEOUT", 5*time.Second),
	}
}
