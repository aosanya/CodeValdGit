// Package arangodb implements the ArangoDB backend for CodeValdGit.
// All implementation logic lives in
// [github.com/aosanya/CodeValdSharedLib/entitygraph/arangodb]; this package
// is a thin service-scoped adapter that fixes the collection and graph names
// to their CodeValdGit-specific values.
//
// Entity collections:
//   - git_entities  — mutable refs: Agency, Repository, Branch, Tag
//   - git_objects   — immutable content-addressed objects: Commit, Tree, Blob
//
// Infrastructure collections:
//   - git_relationships    — ArangoDB edge collection for all directed graph edges
//   - git_schemas_draft    — one mutable draft schema document per agency
//   - git_schemas_published — immutable append-only published schema snapshots
//
// Named graph: git_graph
//
// Use [New] to obtain a (DataManager, SchemaManager) pair from an open database.
// Use [NewBackend] to connect and construct in a single call.
// Use [NewBackendFromDB] in tests that manage their own database lifecycle.
package arangodb

import (
	"context"
	"fmt"

	driver "github.com/arangodb/go-driver"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/arangoutil"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
	sharedadb "github.com/aosanya/CodeValdSharedLib/entitygraph/arangodb"
	"github.com/aosanya/CodeValdSharedLib/types"
)

// Backend is a type alias for the shared ArangoDB Backend.
// Callers holding *Backend references continue to compile unchanged.
type Backend = sharedadb.Backend

// Config is the connection parameters for the CodeValdGit ArangoDB backend.
// It is an alias of [sharedadb.ConnConfig]; see that type for field docs.
// NewBackend requires Database to be set (e.g. "codevaldgit").
type Config = sharedadb.ConnConfig

// toSharedConfig expands a CodeValdGit Config into a full SharedLib Config,
// filling in the fixed CodeValdGit-specific collection and graph names.
// Does not check whether Database is set — that check belongs in NewBackend,
// which dials the server. New and NewBackendFromDB receive an already-open
// database and do not use the Database field.
func toSharedConfig(cfg Config) sharedadb.Config {
	return sharedadb.Config{
		Endpoint:            cfg.Endpoint,
		Username:            cfg.Username,
		Password:            cfg.Password,
		Database:            cfg.Database,
		Schema:              cfg.Schema,
		EntityCollection:    "git_entities",
		RelCollection:       "git_relationships",
		SchemasDraftCol:     "git_schemas_draft",
		SchemasPublishedCol: "git_schemas_published",
		GraphName:           "git_graph",
	}
}

// New constructs a Backend from an already-open driver.Database using the
// provided schema, ensures all collections and the named graph exist, and
// returns the Backend as both a DataManager and a SchemaManager.
func New(db driver.Database, schema types.Schema) (entitygraph.DataManager, entitygraph.SchemaManager, error) {
	if db == nil {
		return nil, nil, fmt.Errorf("arangodb: New: database must not be nil")
	}
	scfg := toSharedConfig(Config{Schema: schema})
	return sharedadb.New(db, scfg)
}

// NewBackend connects to ArangoDB using cfg, ensures all collections exist,
// and returns a ready-to-use Backend. cfg.Database is required.
func NewBackend(cfg Config) (*Backend, error) {
	if cfg.Database == "" {
		return nil, fmt.Errorf("arangodb: NewBackend: Database must be set (e.g. \"codevaldgit\")")
	}
	scfg := toSharedConfig(cfg)
	return sharedadb.NewBackend(scfg)
}

// NewBackendFromDB constructs a Backend from an already-open driver.Database
// using the provided schema. Intended for tests that manage their own database
// lifecycle.
func NewBackendFromDB(db driver.Database, schema types.Schema) (*Backend, error) {
	if db == nil {
		return nil, fmt.Errorf("arangodb: NewBackendFromDB: database must not be nil")
	}
	scfg := toSharedConfig(Config{Schema: schema})
	return sharedadb.NewBackendFromDB(db, scfg)
}

// NewArangoStorerBackend constructs a codevaldgit.Backend from an existing
// entitygraph.DataManager. Use this in cmd/main.go to share the same
// DataManager between the gRPC GitManager and the git Smart HTTP handler
// without maintaining a separate driver.Database reference.
func NewArangoStorerBackend(dm entitygraph.DataManager) codevaldgit.Backend {
	return &arangoBackend{dm: dm}
}

// gitObjectCollections lists the ArangoDB collection names that hold
// immutable, content-addressed git objects.  A persistent index on
// [agency_id, properties.sha] is required on each so that
// arangoStorer.EncodedObject can look up an object by SHA in O(1) time
// instead of scanning the entire collection.
var gitObjectCollections = []string{"git_blobs", "git_trees", "git_commits"}

// EnsureGitObjectIndexes adds a persistent ArangoDB index on
// [agency_id, properties.sha] to each git object collection
// (git_blobs, git_trees, git_commits).  The call is idempotent — ArangoDB
// returns the existing index when one with the same fields already exists.
//
// Call this once at startup after [NewBackend] succeeds.
// cfg.Database is required; other fields default to their zero-value
// equivalents (same as NewBackend).
func EnsureGitObjectIndexes(ctx context.Context, cfg Config) error {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:8529"
	}
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	db, err := arangoutil.Connect(ctx, arangoutil.Config{
		Endpoint: cfg.Endpoint,
		Username: cfg.Username,
		Password: cfg.Password,
		Database: cfg.Database,
	})
	if err != nil {
		return fmt.Errorf("EnsureGitObjectIndexes: connect: %w", err)
	}
	for _, name := range gitObjectCollections {
		exists, err := db.CollectionExists(ctx, name)
		if err != nil {
			return fmt.Errorf("EnsureGitObjectIndexes %s: check collection: %w", name, err)
		}
		if !exists {
			// Collection not yet created (first-run before schema seed).
			// Skip and let the index be added on the next startup.
			continue
		}
		col, err := db.Collection(ctx, name)
		if err != nil {
			return fmt.Errorf("EnsureGitObjectIndexes %s: open collection: %w", name, err)
		}
		// Sparse index: documents without properties.sha (e.g. GitInternalState)
		// are excluded, keeping the index small.
		_, _, err = col.EnsurePersistentIndex(ctx,
			[]string{"agency_id", "properties.sha"},
			&driver.EnsurePersistentIndexOptions{
				Name:         "idx_agency_sha",
				Sparse:       true,
				InBackground: true,
			},
		)
		if err != nil {
			return fmt.Errorf("EnsureGitObjectIndexes %s: ensure index: %w", name, err)
		}
	}
	return nil
}
