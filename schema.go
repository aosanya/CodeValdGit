// Package codevaldgit — pre-delivered schema definition.
//
// This file exposes [DefaultGitSchema], which returns the fixed [types.Schema]
// for CodeValdGit. cmd/main.go seeds this schema idempotently on startup via
// GitSchemaManager.SetSchema.
//
// The schema declares eight TypeDefinitions:
//   - Agency     — root entity; one per agency ID (mutable)
//   - Repository — a versioned codebase owned by an Agency; an Agency can
//     have multiple Repositories (mutable)
//   - Branch     — named ref pointing to a Commit; owns the branch lifecycle (mutable)
//   - Tag        — immutable named ref pointing to a Commit (immutable)
//   - Commit     — immutable snapshot with author, message, and pointer to a Tree (immutable)
//   - Tree       — immutable directory listing at a specific point in time (immutable)
//   - Blob       — file content entity; content-addressed by SHA; carries documentation edges
//   - Keyword    — hierarchical discovery label; forms a taxonomy tree (mutable)
//
// Graph topology (Git objects):
//
//	Agency ──has_repository──► Repository ──has_branch──► Branch ──points_to──► Commit ──has_tree──► Tree ──has_blob──► Blob
//	                                       ──has_tag─────► Tag    ──points_to──► Commit              ──has_subtree──► Tree
//	                                       ──has_commit──► Commit ──has_parent──► Commit
//
// Documentation edges (branch-scoped, replicated to main on merge per DR-010):
//
//	Blob ──tagged_with──► Keyword ──has_child──► Keyword   (keyword taxonomy)
//	Blob ──documents────► Blob                             (doc → code)
//	Blob ──documented_by► Blob                             (code → doc, inverse)
//	Blob ──depends_on───► Blob                             (code → dependency)
//	Blob ──imported_by──► Blob                             (dependency → importer, inverse)
//
// Storage:
//   - Agency, Branch, Tag  → "git_entities" document collection (mutable refs / live state)
//   - Repository           → "git_repositories" document collection (one per agency; mutable)
//   - Commit, Tree, Blob   → "git_objects" document collection (immutable, content-addressed by SHA)
//   - Keyword              → "git_keywords" document collection (mutable; taxonomy labels)
//   - GitInternalState     → "git_internal" document collection (go-git internal: config, index, shallow)
//   - All edges            → "git_relationships" edge collection
//
// Inverse relationships auto-created by [entitygraph.DataManager.CreateRelationship]:
//
//	Repository ──belongs_to_agency──────► Agency
//	Branch     ──belongs_to_repository──► Repository
//	Tag        ──belongs_to_repository──► Repository
//	Commit     ──belongs_to_repository──► Repository
//	Tree       ──belongs_to_commit──────► Commit
//	Blob       ──belongs_to_tree────────► Tree
//	Tree       ──belongs_to_tree────────► Tree   (subtree inverse)
//	Keyword    ──belongs_to_parent──────► Keyword (taxonomy inverse)
//	Blob       ──documented_by──────────► Blob   (documents inverse)
//	Blob       ──imported_by────────────► Blob   (depends_on inverse)
package codevaldgit

import "github.com/aosanya/CodeValdSharedLib/types"

// DefaultGitSchema returns the pre-delivered [types.Schema] seeded by
// cmd/main.go on startup via GitSchemaManager.SetSchema. The operation is
// idempotent — calling it multiple times with the same schema ID is safe.
//
// All entities are stored in the "git_entities" fallback collection because
// no StorageCollection override is declared. All edges are stored in the
// "git_relationships" edge collection.
func DefaultGitSchema() types.Schema {
	return types.Schema{
		ID:      "git-schema-v1",
		Version: 1,
		Tag:     "v1",
		Types: []types.TypeDefinition{
			{
				Name:              "Agency",
				DisplayName:       "Agency",
				PathSegment:       "agencies",
				EntityIDParam:     "agencyId",
				StorageCollection: "git_agencies",
				Properties: []types.PropertyDefinition{
					// name is the human-readable label for the agency.
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					{Name: "description", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// has_repository links the agency to all of its repositories.
					// An agency may own zero or more repositories.
					{
						Name:        "has_repository",
						Label:       "Repositories",
						PathSegment: "repositories",
						ToType:      "Repository",
						ToMany:      true,
						Inverse:     "belongs_to_agency",
					},
				},
			},
			{
				Name:              "Repository",
				DisplayName:       "Repository",
				PathSegment:       "repositories",
				EntityIDParam:     "repositoryId",
				StorageCollection: "git_repositories",
				Properties: []types.PropertyDefinition{
					// name is the human-readable label, e.g. the agency ID used as repo key.
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					{Name: "description", Type: types.PropertyTypeString},
					// default_branch is the name of the primary branch (e.g. "main").
					{Name: "default_branch", Type: types.PropertyTypeString, Required: true},
					// head_ref is the symbolic HEAD target, e.g. "refs/heads/main".
					// Written by the go-git storage.Storer on InitRepo and updated on
					// symbolic-ref changes. Required for Smart HTTP (git clone/fetch).
					{Name: "head_ref", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// belongs_to_agency is the agency that owns this repository.
					{
						Name:        "belongs_to_agency",
						Label:       "Agency",
						PathSegment: "agency",
						ToType:      "Agency",
						ToMany:      false,
						Required:    true,
						Inverse:     "has_repository",
					},
					{
						Name:        "has_branch",
						Label:       "Branches",
						PathSegment: "branches",
						ToType:      "Branch",
						ToMany:      true,
						Inverse:     "belongs_to_repository",
					},
					{
						Name:        "has_tag",
						Label:       "Tags",
						PathSegment: "tags",
						ToType:      "Tag",
						ToMany:      true,
						Inverse:     "belongs_to_repository",
					},
					{
						Name:        "has_commit",
						Label:       "Commits",
						PathSegment: "commits",
						ToType:      "Commit",
						ToMany:      true,
						Inverse:     "belongs_to_repository",
					},
				},
			},
			{
				Name:              "Branch",
				DisplayName:       "Branch",
				PathSegment:       "branches",
				EntityIDParam:     "branchId",
				StorageCollection: "git_branches",
				Properties: []types.PropertyDefinition{
					// name is the full ref name, e.g. "main" or "task/abc-001".
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					// is_default is true for the repository's default branch.
					{Name: "is_default", Type: types.PropertyTypeBoolean},
					// sha is the target commit hash for this branch head.
					// Updated by the go-git storage.Storer on every SetReference call
					// so that Smart HTTP (git clone/fetch) can resolve refs without
					// traversing the points_to relationship.
					{Name: "sha", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// points_to is the current HEAD commit of this branch (ToMany=false;
					// updated atomically on each push/merge).
					{
						Name:        "points_to",
						Label:       "Head Commit",
						PathSegment: "head",
						ToType:      "Commit",
						ToMany:      false,
					},
					{
						Name:        "belongs_to_repository",
						Label:       "Repository",
						PathSegment: "repository",
						ToType:      "Repository",
						ToMany:      false,
						Required:    true,
						Inverse:     "has_branch",
					},
				},
			},
			{
				Name:              "Tag",
				DisplayName:       "Tag",
				PathSegment:       "tags",
				EntityIDParam:     "tagId",
				StorageCollection: "git_tags",
				// Tags are immutable once created — the target commit must never change.
				Immutable: true,
				Properties: []types.PropertyDefinition{
					// name is the human-readable tag label, e.g. "v1.0.0".
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					// sha is the commit SHA this tag resolves to.
					{Name: "sha", Type: types.PropertyTypeString, Required: true},
					// message is the annotation message for annotated tags; empty for lightweight tags.
					{Name: "message", Type: types.PropertyTypeString},
					// tagger_name is the name of the person or agent who created the tag.
					{Name: "tagger_name", Type: types.PropertyTypeString},
					// tagger_at is the ISO 8601 timestamp at which the tag was created.
					{Name: "tagger_at", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					{
						Name:        "points_to",
						Label:       "Commit",
						PathSegment: "commit",
						ToType:      "Commit",
						ToMany:      false,
						Required:    true,
					},
					{
						Name:        "belongs_to_repository",
						Label:       "Repository",
						PathSegment: "repository",
						ToType:      "Repository",
						ToMany:      false,
						Required:    true,
						Inverse:     "has_tag",
					},
				},
			},
			{
				Name:              "Commit",
				DisplayName:       "Commit",
				PathSegment:       "commits",
				EntityIDParam:     "commitId",
				StorageCollection: "git_commits",
				// Commits are content-addressed git objects — immutable once written.
				Immutable: true,
				Properties: []types.PropertyDefinition{
					// sha is the full 40-character hex Git commit hash.
					{Name: "sha", Type: types.PropertyTypeString, Required: true},
					{Name: "message", Type: types.PropertyTypeString, Required: true},
					{Name: "author_name", Type: types.PropertyTypeString},
					{Name: "author_email", Type: types.PropertyTypeString},
					// author_at is the author-timestamp in ISO 8601 format.
					{Name: "author_at", Type: types.PropertyTypeString},
					{Name: "committer_name", Type: types.PropertyTypeString},
					{Name: "committer_email", Type: types.PropertyTypeString},
					// committed_at is the committer-timestamp in ISO 8601 format.
					{Name: "committed_at", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// has_tree is the root Tree object for this commit's snapshot.
					{
						Name:        "has_tree",
						Label:       "Tree",
						PathSegment: "tree",
						ToType:      "Tree",
						ToMany:      false,
						Required:    true,
					},
					// has_parent lists parent commits (0 for the initial commit;
					// 1 for a normal commit; 2+ for merge commits).
					{
						Name:        "has_parent",
						Label:       "Parents",
						PathSegment: "parents",
						ToType:      "Commit",
						ToMany:      true,
					},
					{
						Name:        "belongs_to_repository",
						Label:       "Repository",
						PathSegment: "repository",
						ToType:      "Repository",
						ToMany:      false,
						Required:    true,
						Inverse:     "has_commit",
					},
				},
			},
			{
				Name:              "Tree",
				DisplayName:       "Tree",
				PathSegment:       "trees",
				EntityIDParam:     "treeId",
				StorageCollection: "git_trees",
				// Trees are content-addressed git objects — immutable once written.
				Immutable: true,
				Properties: []types.PropertyDefinition{
					// sha is the full 40-character hex Git tree hash.
					{Name: "sha", Type: types.PropertyTypeString, Required: true},
					// path is the directory path within the commit tree hierarchy.
					// An empty string ("") denotes the root tree of a commit.
					{Name: "path", Type: types.PropertyTypeString},
					// entries is a JSON array of child entries in the form
					// [{"name":"","mode":"100644","sha":""}] serialised at write time.
					// Consumed by the go-git storage.Storer to decode the tree object.
					{Name: "entries", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// has_blob links the tree to its direct file children.
					{
						Name:        "has_blob",
						Label:       "Blobs",
						PathSegment: "blobs",
						ToType:      "Blob",
						ToMany:      true,
						Inverse:     "belongs_to_tree",
					},
					// has_subtree links to child directory trees.
					{
						Name:        "has_subtree",
						Label:       "Subtrees",
						PathSegment: "subtrees",
						ToType:      "Tree",
						ToMany:      true,
					},
					// belongs_to_commit is the commit that owns this root tree.
					// Only set when this Tree is the root (path == "").
					{
						Name:        "belongs_to_commit",
						Label:       "Commit",
						PathSegment: "commit",
						ToType:      "Commit",
						ToMany:      false,
						Inverse:     "has_tree",
					},
				},
			},
			{
				Name:              "Blob",
				DisplayName:       "Blob",
				PathSegment:       "blobs",
				EntityIDParam:     "blobId",
				StorageCollection: "git_blobs",
				// Blobs are content-addressed by SHA — the data/sha/size fields never
				// change once written. Metadata fields (name, path, extension) are
				// backfilled after commit time via UpdateEntity, so Immutable is not set.
				Properties: []types.PropertyDefinition{
					// sha is the full 40-character hex Git blob hash.
					{Name: "sha", Type: types.PropertyTypeString, Required: true},
					// path is the file path relative to the repository root,
					// e.g. "src/handlers/server.go".
					{Name: "path", Type: types.PropertyTypeString, Required: true},
					// name is the base file name including extension, e.g. "Test.txt".
					// Derived from the last path segment.
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					// extension is the file extension without the leading dot, e.g. "txt".
					// Empty string for files with no extension.
					{Name: "extension", Type: types.PropertyTypeString},
					// size is the byte size of the file content.
					{Name: "size", Type: types.PropertyTypeInteger},
					// encoding is "utf-8" for text files or "base64" for binary files.
					{Name: "encoding", Type: types.PropertyTypeString},
					// content holds the raw file content; base64-encoded when encoding == "base64".
					{Name: "content", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					{
						Name:        "belongs_to_tree",
						Label:       "Tree",
						PathSegment: "tree",
						ToType:      "Tree",
						ToMany:      false,
						Required:    true,
						Inverse:     "has_blob",
					}, // tagged_with links a Blob to Keyword nodes for discovery.
					// Branch-scoped: created on task-branch Blobs, replicated to main on merge (DR-010).
					{
						Name:        "tagged_with",
						Label:       "Keywords",
						PathSegment: "keywords",
						ToType:      "Keyword",
						ToMany:      true,
					},
					// documents links a documentation Blob to the code Blobs it describes.
					// Inverse: documented_by (auto-created by entitygraph.DataManager).
					{
						Name:        "documents",
						Label:       "Documents",
						PathSegment: "documents",
						ToType:      "Blob",
						ToMany:      true,
						Inverse:     "documented_by",
					},
					// documented_by is the inverse of documents.
					{
						Name:        "documented_by",
						Label:       "Documented By",
						PathSegment: "documented-by",
						ToType:      "Blob",
						ToMany:      true,
						Inverse:     "documents",
					},
					// depends_on links a code Blob to the Blobs it imports or depends on.
					// Inverse: imported_by (auto-created by entitygraph.DataManager).
					{
						Name:        "depends_on",
						Label:       "Depends On",
						PathSegment: "depends-on",
						ToType:      "Blob",
						ToMany:      true,
						Inverse:     "imported_by",
					},
					// imported_by is the inverse of depends_on.
					{
						Name:        "imported_by",
						Label:       "Imported By",
						PathSegment: "imported-by",
						ToType:      "Blob",
						ToMany:      true,
						Inverse:     "depends_on",
					}},
			},
			{
				Name:              "GitInternalState",
				DisplayName:       "Git Internal State",
				PathSegment:       "internal-state",
				EntityIDParam:     "internalStateId",
				StorageCollection: "git_internal",
				// GitInternalState stores per-agency go-git internal data used exclusively
				// by the storage.Storer implementation. One document per agency per
				// state_type — enforced via UniqueKey so UpsertEntity is always safe.
				// Not exposed via gRPC or HTTP routes.
				UniqueKey: []string{"state_type"},
				Properties: []types.PropertyDefinition{
					// state_type is the discriminator: "config", "index", or "shallow".
					{Name: "state_type", Type: types.PropertyTypeString, Required: true},
					// data is the base64-encoded binary payload:
					//   config  — git config ini format
					//   index   — git index binary
					//   shallow — newline-separated shallow commit SHAs
					{Name: "data", Type: types.PropertyTypeString, Required: true},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
			},
			{
				Name:              "Keyword",
				DisplayName:       "Keyword",
				PathSegment:       "keywords",
				EntityIDParam:     "keywordId",
				StorageCollection: "git_keywords",
				// Keywords are hierarchical discovery labels used by AI agents to tag
				// Blobs, Branches, and Commits. A Keyword can have child Keywords
				// (has_child / belongs_to_parent), forming a free-form taxonomy tree.
				// Querying a parent Keyword cascades to all descendants by default.
				Properties: []types.PropertyDefinition{
					// name is the human-readable label, e.g. "authentication" or "grpc".
					{Name: "name", Type: types.PropertyTypeString, Required: true},
					// description is an optional plain-text summary of the keyword.
					{Name: "description", Type: types.PropertyTypeString},
					// scope is an optional grouping label (e.g. "domain", "layer", "technology").
					{Name: "scope", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
				Relationships: []types.RelationshipDefinition{
					// has_child links a parent keyword to its direct children in the taxonomy.
					{
						Name:        "has_child",
						Label:       "Children",
						PathSegment: "children",
						ToType:      "Keyword",
						ToMany:      true,
						Inverse:     "belongs_to_parent",
					},
					// belongs_to_parent is the inverse of has_child.
					{
						Name:        "belongs_to_parent",
						Label:       "Parent",
						PathSegment: "parent",
						ToType:      "Keyword",
						ToMany:      false,
						Inverse:     "has_child",
					},
				},
			},
			{
				Name:              "ImportJob",
				DisplayName:       "Import Job",
				PathSegment:       "import-jobs",
				EntityIDParam:     "importJobId",
				StorageCollection: "git_importjobs",
				// ImportJob tracks the lifecycle of an async repository import operation.
				// One document per import request; keyed by a UUID assigned at call time.
				// Status transitions: pending → running → completed | failed | cancelled.
				Properties: []types.PropertyDefinition{
					// agency_id scopes this job to the owning agency.
					{Name: "agency_id", Type: types.PropertyTypeString, Required: true},
					// source_url is the public HTTPS URL of the remote repository being imported.
					{Name: "source_url", Type: types.PropertyTypeString, Required: true},
					// status is one of: "pending", "running", "completed", "failed", "cancelled".
					{Name: "status", Type: types.PropertyTypeString, Required: true},
					// error_message is populated only when status == "failed".
					{Name: "error_message", Type: types.PropertyTypeString},
					{Name: "created_at", Type: types.PropertyTypeString},
					{Name: "updated_at", Type: types.PropertyTypeString},
				},
			},
		},
	}
}
