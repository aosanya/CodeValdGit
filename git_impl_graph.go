// git_impl_graph.go implements the graph query methods on [gitManager]:
//
//   - [GitManager.GetNeighborhood] — AQL-backed traversal returning a bounded
//     subgraph (depth 1-3, 100-node hard cap).
//
//   - [GitManager.SearchByKeywords] — keyword-driven entity discovery with
//     optional taxonomy cascade and AND/OR match modes.
//
//   - [GitManager.QueryGraph] — multi-filter, signal-sorted Blob graph query.
//
// All methods delegate to [entitygraph.DataManager] — no direct AQL is issued
// from this layer.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// defaultSignalLayers maps the built-in signal names to their rank.
// Used when no Signal entities have been persisted to the store yet.
var defaultSignalLayers = map[string]int{
	"surface":     1,
	"index":       2,
	"structural":  3,
	"contributor": 4,
	"authority":   5,
}

const queryGraphDefaultLimit = 50

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

// ── QueryGraph ────────────────────────────────────────────────────────────────

// QueryGraph returns up to req.Limit Blob nodes filtered across five dimensions
// and sorted by descending signal layer. An empty request returns the top 50
// highest-signal Blob nodes with all their inter-node edges.
func (m *gitManager) QueryGraph(ctx context.Context, req QueryGraphRequest) (GraphResult, error) {
	if _, err := m.GetBranch(ctx, req.BranchID); err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return GraphResult{}, ErrBranchNotFound
		}
		return GraphResult{}, fmt.Errorf("QueryGraph: get branch: %w", err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = queryGraphDefaultLimit
	}

	signalLayers, err := m.loadSignalLayers(ctx)
	if err != nil {
		return GraphResult{}, fmt.Errorf("QueryGraph: load signals: %w", err)
	}

	blobs, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Blob",
	})
	if err != nil {
		return GraphResult{}, fmt.Errorf("QueryGraph: list blobs: %w", err)
	}
	blobs = filterBlobsByPath(blobs, req.FileTypes, req.Folders)

	tagEdges, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     "tagged_with",
	})
	if err != nil {
		return GraphResult{}, fmt.Errorf("QueryGraph: list tagged_with edges: %w", err)
	}

	type tagInfo struct {
		keywordID string
		signal    string
	}
	blobTags := make(map[string][]tagInfo, len(tagEdges))
	for _, e := range tagEdges {
		sig, _ := e.Properties["signal"].(string)
		blobTags[e.FromID] = append(blobTags[e.FromID], tagInfo{keywordID: e.ToID, signal: sig})
	}

	signalSet := toStringSet(req.Signals)
	kwSet := toStringSet(req.KeywordIDs)

	type scored struct {
		entity   entitygraph.Entity
		maxLayer int
	}
	var candidates []scored
	for _, blob := range blobs {
		tags := blobTags[blob.ID]
		if len(kwSet) > 0 {
			found := false
			for _, t := range tags {
				if kwSet[t.keywordID] {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		maxLayer := 0
		hasMatchingSignal := len(signalSet) == 0
		for _, t := range tags {
			if layer := signalLayers[t.signal]; layer > maxLayer {
				maxLayer = layer
			}
			if signalSet[t.signal] {
				hasMatchingSignal = true
			}
		}
		if !hasMatchingSignal {
			continue
		}
		candidates = append(candidates, scored{entity: blob, maxLayer: maxLayer})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].maxLayer > candidates[j].maxLayer
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	nodeIDs := make(map[string]bool, len(candidates))
	nodes := make([]GraphNode, 0, len(candidates))
	for _, c := range candidates {
		nodeIDs[c.entity.ID] = true
		nodes = append(nodes, GraphNode{
			ID:         c.entity.ID,
			TypeID:     c.entity.TypeID,
			Properties: c.entity.Properties,
		})
	}

	relSet := toStringSet(req.Relationships)
	edges, err := m.queryGraphEdges(ctx, nodeIDs, relSet)
	if err != nil {
		return GraphResult{}, fmt.Errorf("QueryGraph: collect edges: %w", err)
	}

	return GraphResult{Nodes: nodes, Edges: edges}, nil
}

// loadSignalLayers lists Signal entities and returns a name→layer map.
// Falls back to defaultSignalLayers when the store is empty.
func (m *gitManager) loadSignalLayers(ctx context.Context) (map[string]int, error) {
	signals, err := m.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID: m.agencyID,
		TypeID:   "Signal",
	})
	if err != nil {
		return nil, fmt.Errorf("loadSignalLayers: %w", err)
	}
	if len(signals) == 0 {
		return defaultSignalLayers, nil
	}
	layers := make(map[string]int, len(signals))
	for _, s := range signals {
		name, _ := s.Properties["name"].(string)
		var layer int
		switch v := s.Properties["layer"].(type) {
		case int:
			layer = v
		case float64:
			layer = int(v)
		}
		if name != "" {
			layers[name] = layer
		}
	}
	return layers, nil
}

// filterBlobsByPath applies file_types (suffix) and folders (prefix) filters
// in-memory, returning only blobs whose path matches all active filters.
func filterBlobsByPath(blobs []entitygraph.Entity, fileTypes, folders []string) []entitygraph.Entity {
	if len(fileTypes) == 0 && len(folders) == 0 {
		return blobs
	}
	out := blobs[:0]
	for _, b := range blobs {
		path, _ := b.Properties["path"].(string)
		if len(fileTypes) > 0 {
			matched := false
			for _, ft := range fileTypes {
				if strings.HasSuffix(path, ft) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if len(folders) > 0 {
			matched := false
			for _, f := range folders {
				if strings.HasPrefix(path, f) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

// queryGraphEdges collects all relationships between nodes in the given ID set,
// filtered by relSet (edge name or descriptor property). An empty relSet passes
// all edges through.
func (m *gitManager) queryGraphEdges(ctx context.Context, nodeIDs map[string]bool, relSet map[string]bool) ([]GraphEdge, error) {
	seen := make(map[string]bool)
	var edges []GraphEdge
	for nodeID := range nodeIDs {
		rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: m.agencyID,
			FromID:   nodeID,
		})
		if err != nil {
			return nil, fmt.Errorf("queryGraphEdges %s: %w", nodeID, err)
		}
		for _, rel := range rels {
			if seen[rel.ID] || !nodeIDs[rel.ToID] {
				continue
			}
			if len(relSet) > 0 {
				descriptor, _ := rel.Properties["descriptor"].(string)
				if !relSet[rel.Name] && !relSet[descriptor] {
					continue
				}
			}
			seen[rel.ID] = true
			edges = append(edges, GraphEdge{
				ID:     rel.ID,
				Name:   rel.Name,
				FromID: rel.FromID,
				ToID:   rel.ToID,
			})
		}
	}
	return edges, nil
}

// toStringSet converts a slice to a set map for O(1) membership tests.
func toStringSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	set := make(map[string]bool, len(ss))
	for _, s := range ss {
		set[s] = true
	}
	return set
}

// ── Set helpers ───────────────────────────────────────────────────────────────

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
