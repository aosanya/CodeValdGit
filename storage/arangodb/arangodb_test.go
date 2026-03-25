// Package arangodb_test provides integration tests for the CodeValdGit
// ArangoDB backend.
//
// Tests in this file require a running ArangoDB instance. They connect to a
// single persistent database (GIT_ARANGO_DATABASE_TEST, default
// "codevald_tests") and use unique agency IDs per test for isolation.
//
// Tests are skipped automatically when GIT_ARANGO_ENDPOINT is not set or
// the server is unreachable.
//
// To run:
//
//	GIT_ARANGO_ENDPOINT=http://localhost:8529 go test -v -race ./storage/arangodb/
package arangodb_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver"
	driverhttp "github.com/arangodb/go-driver/http"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdGit/storage/arangodb"
	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// openTestDB connects to the ArangoDB instance at GIT_ARANGO_ENDPOINT and
// returns the test database. Skips the calling test if the server is
// unreachable or GIT_ARANGO_ENDPOINT is unset.
func openTestDB(t *testing.T) driver.Database {
	t.Helper()

	endpoint := os.Getenv("GIT_ARANGO_ENDPOINT")
	if endpoint == "" {
		t.Skip("GIT_ARANGO_ENDPOINT not set — skipping ArangoDB integration tests")
	}

	conn, err := driverhttp.NewConnection(driverhttp.ConnectionConfig{
		Endpoints: []string{endpoint},
	})
	if err != nil {
		t.Skipf("ArangoDB connection config error: %v", err)
	}

	user := envOrDefault("GIT_ARANGO_USER", "root")
	pass := os.Getenv("GIT_ARANGO_PASSWORD")

	client, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(user, pass),
	})
	if err != nil {
		t.Skipf("ArangoDB client error: %v", err)
	}

	// Quick ping — skip if unreachable (CI without ArangoDB).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Version(ctx); err != nil {
		t.Skipf("ArangoDB unreachable at %s: %v", endpoint, err)
	}

	dbName := envOrDefault("GIT_ARANGO_DATABASE_TEST", "codevald_tests")
	ctx2 := context.Background()
	exists, err := client.DatabaseExists(ctx2, dbName)
	if err != nil {
		t.Fatalf("DatabaseExists: %v", err)
	}
	var db driver.Database
	if exists {
		db, err = client.Database(ctx2, dbName)
	} else {
		db, err = client.CreateDatabase(ctx2, dbName, nil)
	}
	if err != nil {
		t.Fatalf("open/create test database %q: %v", dbName, err)
	}
	return db
}

// openTestBackend constructs a Backend from the test database using
// DefaultGitSchema.
func openTestBackend(t *testing.T) *arangodb.Backend {
	t.Helper()
	db := openTestDB(t)
	b, err := arangodb.NewBackendFromDB(db, codevaldgit.DefaultGitSchema())
	if err != nil {
		t.Fatalf("NewBackendFromDB: %v", err)
	}
	return b
}

// uniqueID returns a string that is unique within the current test run.
func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── No-connection constructor tests ───────────────────────────────────────────

// TestNewBackend_EmptyDatabase verifies that NewBackend returns an error when
// Database is empty — this requires no live ArangoDB connection.
func TestNewBackend_EmptyDatabase(t *testing.T) {
	_, err := arangodb.NewBackend(arangodb.Config{})
	if err == nil {
		t.Fatal("expected error for empty Database, got nil")
	}
}

// TestNewBackendFromDB_Nil verifies that NewBackendFromDB returns an error
// when db is nil — this requires no live ArangoDB connection.
func TestNewBackendFromDB_Nil(t *testing.T) {
	_, err := arangodb.NewBackendFromDB(nil, codevaldgit.DefaultGitSchema())
	if err == nil {
		t.Fatal("expected error for nil database, got nil")
	}
}

// TestNew_NilDB verifies that New returns an error when db is nil.
func TestNew_NilDB(t *testing.T) {
	_, _, err := arangodb.New(nil, codevaldgit.DefaultGitSchema())
	if err == nil {
		t.Fatal("expected error for nil database, got nil")
	}
}

// ── Integration tests (require GIT_ARANGO_ENDPOINT) ──────────────────────────

// TestNewBackendFromDB_ConstructsBackend verifies that NewBackendFromDB succeeds
// with a live ArangoDB connection and the default schema.
func TestNewBackendFromDB_ConstructsBackend(t *testing.T) {
	b := openTestBackend(t) // skips if no ArangoDB
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
}

// TestNew_ConstructsDataManagerAndSchemaManager verifies that New returns a
// usable DataManager and SchemaManager pair.
func TestNew_ConstructsDataManagerAndSchemaManager(t *testing.T) {
	db := openTestDB(t)
	dm, sm, err := arangodb.New(db, codevaldgit.DefaultGitSchema())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dm == nil {
		t.Fatal("DataManager must not be nil")
	}
	if sm == nil {
		t.Fatal("SchemaManager must not be nil")
	}
}

// TestDataManager_RepositoryEntityLifecycle exercises create / get / update /
// delete round-trip for a "Repository" entity in git_entities.
func TestDataManager_RepositoryEntityLifecycle(t *testing.T) {
	b := openTestBackend(t)
	ctx := context.Background()

	agencyID := uniqueID("agency")

	// CreateEntity
	created, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":           "my-repo",
			"default_branch": "main",
			"description":    "Test repository",
			"created_at":     time.Now().UTC().Format(time.RFC3339),
			"updated_at":     time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Repository): %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty entity ID")
	}
	if created.TypeID != "Repository" {
		t.Errorf("TypeID: want %q, got %q", "Repository", created.TypeID)
	}

	// GetEntity
	got, err := b.GetEntity(ctx, agencyID, created.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Properties["name"] != "my-repo" {
		t.Errorf("name: want %q, got %v", "my-repo", got.Properties["name"])
	}

	// UpdateEntity — patch default_branch
	updated, err := b.UpdateEntity(ctx, agencyID, created.ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{"default_branch": "develop"},
	})
	if err != nil {
		t.Fatalf("UpdateEntity: %v", err)
	}
	if updated.Properties["default_branch"] != "develop" {
		t.Errorf("default_branch: want %q, got %v", "develop", updated.Properties["default_branch"])
	}

	// ListEntities
	list, err := b.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: agencyID,
		TypeID:   "Repository",
	})
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(list))
	}

	// DeleteEntity (soft delete)
	if err := b.DeleteEntity(ctx, agencyID, created.ID); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	// After soft-delete, ListEntities excludes it.
	list2, err := b.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: agencyID,
		TypeID:   "Repository",
	})
	if err != nil {
		t.Fatalf("ListEntities after delete: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("expected 0 entities after delete, got %d", len(list2))
	}
}

// TestDataManager_CommitEntityImmutable verifies that UpdateEntity returns
// ErrImmutableType for the "Commit" TypeDefinition (Immutable: true).
func TestDataManager_CommitEntityImmutable(t *testing.T) {
	b := openTestBackend(t)
	ctx := context.Background()

	agencyID := uniqueID("agency")

	created, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Commit",
		Properties: map[string]any{
			"sha":        "abc123def456abc123def456abc123def456abc1",
			"message":    "Initial commit",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Commit): %v", err)
	}

	_, err = b.UpdateEntity(ctx, agencyID, created.ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{"message": "tampered"},
	})
	if err == nil {
		t.Fatal("expected ErrImmutableType for Commit, got nil")
	}
}

// TestDataManager_BlobEntityImmutable verifies that UpdateEntity returns
// ErrImmutableType for the "Blob" TypeDefinition (Immutable: true).
func TestDataManager_BlobEntityImmutable(t *testing.T) {
	b := openTestBackend(t)
	ctx := context.Background()

	agencyID := uniqueID("agency")

	created, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Blob",
		Properties: map[string]any{
			"sha":        "aabbccddeeff00112233445566778899aabbccdd",
			"path":       "README.md",
			"content":    "# Hello",
			"encoding":   "utf-8",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Blob): %v", err)
	}

	_, err = b.UpdateEntity(ctx, agencyID, created.ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{"content": "tampered"},
	})
	if err == nil {
		t.Fatal("expected ErrImmutableType for Blob, got nil")
	}
}

// TestDataManager_AgencyToRepositoryRelationship verifies that a
// has_repository edge can be created from an Agency entity to a Repository
// entity, and retrieved via ListRelationships.
func TestDataManager_AgencyToRepositoryRelationship(t *testing.T) {
	b := openTestBackend(t)
	ctx := context.Background()

	agencyID := uniqueID("agency")

	agency, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID:   agencyID,
		TypeID:     "Agency",
		Properties: map[string]any{"name": "acme", "created_at": time.Now().UTC().Format(time.RFC3339)},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Agency): %v", err)
	}

	repo, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Repository",
		Properties: map[string]any{
			"name":           "acme-repo",
			"default_branch": "main",
			"created_at":     time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Repository): %v", err)
	}

	rel, err := b.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: agencyID,
		Name:     "has_repository",
		FromID:   agency.ID,
		ToID:     repo.ID,
	})
	if err != nil {
		t.Fatalf("CreateRelationship (has_repository): %v", err)
	}
	if rel.ID == "" {
		t.Fatal("expected non-empty relationship ID")
	}

	// Verify via GetRelationship.
	got, err := b.GetRelationship(ctx, agencyID, rel.ID)
	if err != nil {
		t.Fatalf("GetRelationship: %v", err)
	}
	if got.Name != "has_repository" {
		t.Errorf("Name: want %q, got %q", "has_repository", got.Name)
	}
	if got.FromID != agency.ID {
		t.Errorf("FromID: want %q, got %q", agency.ID, got.FromID)
	}
	if got.ToID != repo.ID {
		t.Errorf("ToID: want %q, got %q", repo.ID, got.ToID)
	}

	// DeleteRelationship.
	if err := b.DeleteRelationship(ctx, agencyID, rel.ID); err != nil {
		t.Fatalf("DeleteRelationship: %v", err)
	}
}

// TestDataManager_BranchEntityInGitEntities verifies that a Branch entity is
// stored in the git_entities collection (same StorageCollection as Agency and
// Repository).
func TestDataManager_BranchEntityInGitEntities(t *testing.T) {
	b := openTestBackend(t)
	ctx := context.Background()

	agencyID := uniqueID("agency")

	created, err := b.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: agencyID,
		TypeID:   "Branch",
		Properties: map[string]any{
			"name":       "main",
			"is_default": true,
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("CreateEntity (Branch): %v", err)
	}
	if created.TypeID != "Branch" {
		t.Errorf("TypeID: want %q, got %q", "Branch", created.TypeID)
	}

	got, err := b.GetEntity(ctx, agencyID, created.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Properties["name"] != "main" {
		t.Errorf("name: want %q, got %v", "main", got.Properties["name"])
	}
}
