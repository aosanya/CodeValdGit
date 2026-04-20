// git_impl_graph.go implements the graph query methods on [gitManager]:
//
//   - [GitManager.GetNeighborhood] — AQL-backed traversal returning a bounded
//     subgraph (depth 1-3, 100-node hard cap).
//
//   - [GitManager.SearchByKeywords] — keyword-driven entity discovery with
//     optional taxonomy cascade and AND/OR match modes.
//
// Both methods delegate to [entitygraph.DataManager.TraverseGraph] and
// [entitygraph.DataManager.ListRelationships] — no direct AQL is issued from
// this layer.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// neighborhoodMaxNodes is the hard cap on vertices returned by GetNeighborhood.
const neighborhoodMaxNodes = 100

// ── GetNeighborhood ───────────────────────────────────────────────────────────

// GetNeighborhood returns the subgraph reachable from entityID within depth
// hops, capped at [neighborhoodMaxNodes] nodes. The starting entity is always
// included as the first node in the result.
//
// depth is clamped to [1, 3]. The branch must exist (verified before traversal).
func (m *gitManager) GetNeighborhood(ctx context.Context, branchID, entityID string, depth int) (GraphResult, error) {
	if _, err := m.GetBranch(ctx, branchID); err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return GraphResult{}, ErrBranchNotFound
		}
		return GraphResult{}, fmt.Errorf("GetNeighborhood: get branch %s: %w", branchID, err)
	}

	depth = clampDepth(depth)

	// Resolve entityID: callers may pass a file path (e.g. "README.md") instead
	// of the actual entity graph ID. Try the raw ID first; on ErrEntityNotFound
	// fall back to a Blob lookup by path property.
	resolvedID, err := m.resolveEntityID(ctx, entityID)
	if err != nil {
		return GraphResult{}, fmt.Errorf("GetNeighborhood %s: resolve entity: %w", entityID, err)
	}

	result, err := m.dm.TraverseGraph(ctx, entitygraph.TraverseGraphRequest{
		AgencyID:  m.agencyID,
		StartID:   resolvedID,
		Direction: "any",
		Depth:     depth,
	})
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return GraphResult{}, entitygraph.ErrEntityNotFound
		}
		return GraphResult{}, fmt.Errorf("GetNeighborhood %s: traverse: %w", entityID, err)
	}

	return buildGraphResult(result, neighborhoodMaxNodes), nil
}

// resolveEntityID returns the canonical entity graph ID. If entityID is already
// a valid entity key it is returned as-is. Otherwise, the method attempts to
// find a Blob entity whose "path" property matches entityID and returns that
// entity's ID.
func (m *gitManager) resolveEntityID(ctx context.Context, entityID string) (string, error) {
	// Fast path: check if entityID is a direct entity key.
	if _, err := m.dm.GetEntity(ctx, m.agencyID, entityID); err == nil {
		return entityID, nil
	}

	// Slow path: look up Blob by "path" property.
	entities, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   m.agencyID,
		TypeID:     "Blob",
		Properties: map[string]any{"path": entityID},
	})
	if err != nil {
		return "", fmt.Errorf("resolveEntityID: list blobs by path %q: %w", entityID, err)
	}
	if len(entities) == 0 {
		return "", entitygraph.ErrEntityNotFound
	}
	return entities[0].ID, nil
}

// clampDepth enforces the range [1, 3] for traversal depth.
func clampDepth(d int) int {
	if d < 1 {
		return 1
	}
	if d > 3 {
		return 3
	}
	return d
}

// buildGraphResult converts a [entitygraph.TraverseGraphResult] to a
// [GraphResult], applying the given node cap. Edges whose endpoints fall
// outside the cap are dropped.
func buildGraphResult(raw entitygraph.TraverseGraphResult, cap int) GraphResult {
	// Cap vertices first.
	verts := raw.Vertices
	if len(verts) > cap {
		verts = verts[:cap]
	}

	// Build an ID set for fast membership checks on capped nodes.
	included := make(map[string]bool, len(verts))
	nodes := make([]GraphNode, 0, len(verts))
	for _, v := range verts {
		included[v.ID] = true
		nodes = append(nodes, GraphNode{
			ID:         v.ID,
			TypeID:     v.TypeID,
			Properties: v.Properties,
		})
	}

	// Include only edges whose both endpoints are within the node cap.
	edges := make([]GraphEdge, 0, len(raw.Edges))
	for _, e := range raw.Edges {
		if included[e.FromID] && included[e.ToID] {
			edges = append(edges, GraphEdge{
				ID:     e.ID,
				Name:   e.Name,
				FromID: e.FromID,
				ToID:   e.ToID,
			})
		}
	}

	return GraphResult{Nodes: nodes, Edges: edges}
}

// ── SearchByKeywords ──────────────────────────────────────────────────────────

// SearchByKeywords returns all entities tagged (via "tagged_with" edges) with
// the specified keywords. When Cascade is true each keyword is expanded to its
// full descendant set before matching. MatchMode controls AND/OR semantics.
func (m *gitManager) SearchByKeywords(ctx context.Context, req SearchByKeywordsRequest) (GraphResult, error) {
	if _, err := m.GetBranch(ctx, req.BranchID); err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return GraphResult{}, ErrBranchNotFound
		}
		return GraphResult{}, fmt.Errorf("SearchByKeywords: get branch %s: %w", req.BranchID, err)
	}

	if len(req.Keywords) == 0 {
		return GraphResult{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	mode := req.MatchMode
	if mode == "" {
		mode = KeywordMatchModeOR
	}

	// Expand each keyword to its descendant set when cascade is requested.
	expandedSets := make([]map[string]bool, len(req.Keywords))
	for i, kwID := range req.Keywords {
		set, err := m.expandKeyword(ctx, kwID, req.Cascade)
		if err != nil {
			return GraphResult{}, fmt.Errorf("SearchByKeywords: expand keyword %s: %w", kwID, err)
		}
		expandedSets[i] = set
	}

	// For each expanded keyword set, collect entities tagged with any keyword in the set.
	taggedPerSet := make([]map[string]bool, len(expandedSets))
	for i, kwSet := range expandedSets {
		tagged, err := m.entitiesTaggedWith(ctx, kwSet)
		if err != nil {
			return GraphResult{}, fmt.Errorf("SearchByKeywords: collect tagged entities: %w", err)
		}
		taggedPerSet[i] = tagged
	}

	// Merge according to match mode.
	var matchedIDs map[string]bool
	switch mode {
	case KeywordMatchModeAND:
		matchedIDs = intersectSets(taggedPerSet)
	default: // OR
		matchedIDs = unionSets(taggedPerSet)
	}

	if len(matchedIDs) == 0 {
		return GraphResult{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	// Fetch full entity details for each matched ID and build the result.
	nodes := make([]GraphNode, 0, len(matchedIDs))
	for entityID := range matchedIDs {
		e, err := m.dm.GetEntity(ctx, m.agencyID, entityID)
		if err != nil {
			continue // skip entities that have been soft-deleted since the edge scan
		}
		nodes = append(nodes, GraphNode{
			ID:         e.ID,
			TypeID:     e.TypeID,
			Properties: e.Properties,
		})
	}

	// Collect edges between matched entities.
	edges, err := m.edgesBetween(ctx, matchedIDs)
	if err != nil {
		return GraphResult{}, fmt.Errorf("SearchByKeywords: edges between results: %w", err)
	}

	return GraphResult{Nodes: nodes, Edges: edges}, nil
}

// expandKeyword returns a set containing kwID and, when cascade is true, all
// of its descendant keyword IDs.
func (m *gitManager) expandKeyword(ctx context.Context, kwID string, cascade bool) (map[string]bool, error) {
	set := map[string]bool{kwID: true}
	if !cascade {
		return set, nil
	}
	if err := m.collectDescendants(ctx, kwID, set); err != nil {
		return nil, err
	}
	return set, nil
}

// collectDescendants recursively collects all descendant keyword IDs of parent
// into the accumulator set, following has_child edges.
func (m *gitManager) collectDescendants(ctx context.Context, parentID string, acc map[string]bool) error {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   parentID,
		Name:     "has_child",
	})
	if err != nil {
		return fmt.Errorf("collectDescendants %s: %w", parentID, err)
	}
	for _, rel := range rels {
		if acc[rel.ToID] {
			continue // guard against cycles (taxonomy should be a DAG, but be safe)
		}
		acc[rel.ToID] = true
		if err := m.collectDescendants(ctx, rel.ToID, acc); err != nil {
			return err
		}
	}
	return nil
}

// entitiesTaggedWith returns the set of entity IDs that have a "tagged_with"
// edge whose ToID is in the given keyword set.
func (m *gitManager) entitiesTaggedWith(ctx context.Context, kwSet map[string]bool) (map[string]bool, error) {
	result := make(map[string]bool)
	for kwID := range kwSet {
		rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: m.agencyID,
			ToID:     kwID,
			Name:     "tagged_with",
		})
		if err != nil {
			return nil, fmt.Errorf("entitiesTaggedWith %s: %w", kwID, err)
		}
		for _, rel := range rels {
			result[rel.FromID] = true
		}
	}
	return result, nil
}

// edgesBetween returns all relationships where both FromID and ToID are members
// of the given entity ID set.
func (m *gitManager) edgesBetween(ctx context.Context, ids map[string]bool) ([]GraphEdge, error) {
	var edges []GraphEdge
	seen := make(map[string]bool) // deduplicate by relationship ID

	for entityID := range ids {
		// Outbound edges from this entity.
		rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: m.agencyID,
			FromID:   entityID,
		})
		if err != nil {
			return nil, fmt.Errorf("edgesBetween %s: %w", entityID, err)
		}
		for _, rel := range rels {
			if seen[rel.ID] {
				continue
			}
			if ids[rel.ToID] {
				seen[rel.ID] = true
				edges = append(edges, GraphEdge{
					ID:     rel.ID,
					Name:   rel.Name,
					FromID: rel.FromID,
					ToID:   rel.ToID,
				})
			}
		}
	}
	return edges, nil
}

// ── Set helpers ───────────────────────────────────────────────────────────────

// unionSets returns the union of all sets in the slice.
func unionSets(sets []map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for _, s := range sets {
		for k := range s {
			result[k] = true
		}
	}
	return result
}

// intersectSets returns the intersection of all sets in the slice.
// An empty slice returns an empty map.
func intersectSets(sets []map[string]bool) map[string]bool {
	if len(sets) == 0 {
		return map[string]bool{}
	}
	// Start with the smallest set to minimise iterations.
	smallest := sets[0]
	for _, s := range sets[1:] {
		if len(s) < len(smallest) {
			smallest = s
		}
	}

	result := make(map[string]bool, len(smallest))
	for k := range smallest {
		inAll := true
		for _, s := range sets {
			if !s[k] {
				inAll = false
				break
			}
		}
		if inAll {
			result[k] = true
		}
	}
	return result
}
