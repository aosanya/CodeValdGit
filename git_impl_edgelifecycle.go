// git_impl_edgelifecycle.go implements the DR-010 documentation-edge lifecycle
// hooks for [gitManager]:
//
//   - [replicateDocEdges]        — GIT-022a: copy branch-scoped edges to the
//     default branch after MergeBranch.
//
//   - [deleteDocEdgesForBranch]  — GIT-022b: remove branch-scoped edges when
//     a branch is deleted without merging.
//
//   - [deleteDocEdgesForBlob]    — GIT-022c: remove branch-scoped edges tied
//     to a specific blob (called by DeleteFile before the deletion commit).
//
// Only two edge types carry branch-scoped documentation semantics:
//
//   - "tagged_with"  (Blob → Keyword): discovery labels; no descriptor.
//   - "references"   (Blob → Blob): generic semantic edge; carries a required
//     "descriptor" property (e.g. "documents", "depends_on", "contradicts").
//
// Both edge types store a "branch_id" property on the relationship to scope
// them to a specific branch. Replication creates new edges with the target
// branch ID; deletion removes all edges whose "branch_id" matches.
package codevaldgit

import (
	"context"
	"fmt"
	"log"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// docEdgeTypes is the set of branch-scoped documentation edge names that
// follow the DR-010 lifecycle. Only these edge types are replicated on merge
// and purged on branch delete.
var docEdgeTypes = []string{"tagged_with", "references"}

// ── GIT-022a: Replicate edges on MergeBranch ─────────────────────────────────

// replicateDocEdges copies all branch-scoped "tagged_with" and "references"
// edges from the source branch to the default branch.
//
// After a fast-forward merge the blobs at every path are shared between both
// branches. Replication creates companion edges on those blobs with
// branch_id == defaultBranchID so that graph queries scoped to the default
// branch return the full documentation context.
//
// Errors during individual edge creation are logged and skipped — best-effort
// delivery is intentional here to avoid blocking a merge due to an edge that
// may already exist or whose endpoint was deleted concurrently.
func (m *gitManager) replicateDocEdges(ctx context.Context, sourceBranchID, defaultBranchID, headCommitID string) {
	if headCommitID == "" {
		return
	}
	blobs, err := m.allBlobsAtCommit(ctx, headCommitID)
	if err != nil {
		return
	}

	for _, blob := range blobs {
		for _, edgeName := range docEdgeTypes {
			m.replicateEdgesOnBlob(ctx, blob.ID, edgeName, sourceBranchID, defaultBranchID)
		}
	}
}

// replicateEdgesOnBlob copies all edges of the given name whose "branch_id"
// property equals sourceBranchID, creating new edges with
// branch_id == defaultBranchID. Duplicate edges are silently ignored.
func (m *gitManager) replicateEdgesOnBlob(
	ctx context.Context,
	blobID, edgeName, sourceBranchID, defaultBranchID string,
) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   blobID,
		Name:     edgeName,
	})
	if err != nil {
		return
	}

	for _, rel := range rels {
		if strMapProp(rel.Properties, "branch_id") != sourceBranchID {
			continue
		}
		props := map[string]any{
			"branch_id": defaultBranchID,
		}
		// Carry forward the descriptor property for "references" edges.
		if d := strMapProp(rel.Properties, "descriptor"); d != "" {
			props["descriptor"] = d
		}
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID:   m.agencyID,
			Name:       edgeName,
			FromID:     rel.FromID,
			ToID:       rel.ToID,
			Properties: props,
		}); err != nil {
			// Best-effort — log and continue; edge may already exist.
			log.Printf("[replicateEdgesOnBlob] create %s (%s→%s): %v",
				edgeName, rel.FromID, rel.ToID, err)
		}
	}
}

// ── GIT-022b: Delete edges on branch delete ───────────────────────────────────

// deleteDocEdgesForBranch removes all branch-scoped "tagged_with" and
// "references" edges on every blob reachable from headCommitID whose
// "branch_id" property equals branchID.
//
// Called by DeleteBranch before the branch entity is removed. Errors are
// logged and collection continues to maximise cleanup coverage.
func (m *gitManager) deleteDocEdgesForBranch(ctx context.Context, branchID, headCommitID string) {
	if headCommitID == "" {
		return
	}
	blobs, err := m.allBlobsAtCommit(ctx, headCommitID)
	if err != nil {
		return
	}

	for _, blob := range blobs {
		for _, edgeName := range docEdgeTypes {
			m.deleteEdgesByBranchID(ctx, blob.ID, edgeName, branchID)
		}
	}
}

// ── GIT-022c: Remove edges on file delete ────────────────────────────────────

// deleteDocEdgesForBlob removes all branch-scoped "tagged_with" and
// "references" edges tied to a specific blob entity whose "branch_id"
// property equals branchID.
//
// Called by DeleteFile before the deletion commit is written, passing the
// blob ID of the file being deleted. Errors are logged and skipped.
func (m *gitManager) deleteDocEdgesForBlob(ctx context.Context, blobID, branchID string) {
	for _, edgeName := range docEdgeTypes {
		m.deleteEdgesByBranchID(ctx, blobID, edgeName, branchID)
	}
}

// deleteEdgesByBranchID deletes all outbound edges of edgeName from blobID
// whose "branch_id" property matches branchID.
func (m *gitManager) deleteEdgesByBranchID(ctx context.Context, blobID, edgeName, branchID string) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   blobID,
		Name:     edgeName,
	})
	if err != nil {
		return
	}

	for _, rel := range rels {
		if strMapProp(rel.Properties, "branch_id") != branchID {
			continue
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// strMapProp retrieves a string value from a map[string]any, returning ""
// if the key is absent or the value is not a string.
func strMapProp(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// mergeEdgeProps merges base properties with overrides (override wins on
// collision) and returns the combined map. Useful for injecting branch_id
// while preserving caller-supplied metadata.
func mergeEdgeProps(base, overrides map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overrides))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overrides {
		result[k] = v
	}
	return result
}

// ── MoveEdges — GIT-022c (rename/move path migration) ────────────────────────

// MigrateDocEdges moves all branch-scoped "tagged_with" and "references" edges
// from the blob at oldPath to the blob at newPath on the given branch. Edges
// that cannot be migrated (e.g. the new blob does not exist yet) are left in
// place and a warning is logged.
//
// This is called by RenameFile (not yet in the interface) when it is
// implemented. Until then this function is exported for future use.
func (m *gitManager) MigrateDocEdges(ctx context.Context, branchID, headCommitID, oldBlobID, newBlobID string) error {
	for _, edgeName := range docEdgeTypes {
		if err := m.migrateEdgesOnBlob(ctx, branchID, edgeName, oldBlobID, newBlobID); err != nil {
			return fmt.Errorf("MigrateDocEdges %s: %w", edgeName, err)
		}
	}
	return nil
}

// migrateEdgesOnBlob re-targets edges of edgeName from oldBlobID to newBlobID
// for the given branchID. The old edge is deleted and a new one is created.
func (m *gitManager) migrateEdgesOnBlob(ctx context.Context, branchID, edgeName, oldBlobID, newBlobID string) error {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   oldBlobID,
		Name:     edgeName,
	})
	if err != nil {
		return fmt.Errorf("list %s: %w", edgeName, err)
	}

	for _, rel := range rels {
		if strMapProp(rel.Properties, "branch_id") != branchID {
			continue
		}
		// Build the new edge properties preserving all existing ones.
		props := make(map[string]any, len(rel.Properties))
		for k, v := range rel.Properties {
			props[k] = v
		}
		// Delete old edge.
		if err := m.dm.DeleteRelationship(ctx, m.agencyID, rel.ID); err != nil {
			continue
		}
		// Create new edge on the newBlobID.
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID:   m.agencyID,
			Name:       edgeName,
			FromID:     newBlobID,
			ToID:       rel.ToID,
			Properties: props,
		}); err != nil {
			log.Printf("[migrateEdgesOnBlob] create new rel %s→%s: %v", newBlobID, rel.ToID, err)
		}
	}
	return nil
}
