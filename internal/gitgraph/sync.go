// sync.go — Syncer: keyword upsert, signal persistence, and edge hard-sync
// for .git-graph/ push sync.
//
// [Syncer.Sync] is the entry point called after every successful push by
// syncGitGraph in git_impl_index.go. All individual operation errors are
// logged and never returned — a malformed .git-graph/ file must not block the push.
package gitgraph

import (
	"context"
	"log"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// edgeKey uniquely identifies a documentation edge for hard-sync comparison.
// Two edges with identical fields are treated as the same edge.
type edgeKey struct {
	relName    string
	toEntityID string
	descriptor string // "" for tagged_with; actual descriptor for references edges
}

// desiredEdge describes a single edge that Sync wants to exist in the DB.
type desiredEdge struct {
	relName    string
	toEntityID string
	properties map[string]any
}

func keyOfDesired(e desiredEdge) edgeKey {
	descriptor, _ := e.properties["descriptor"].(string)
	return edgeKey{relName: e.relName, toEntityID: e.toEntityID, descriptor: descriptor}
}

// Syncer applies a parsed set of [MappingFile]s to the entity graph.
// All write errors are logged and never cause [Syncer.Sync] to return a
// non-nil error — sync failures must never block the git push.
type Syncer struct {
	dm       entitygraph.DataManager
	agencyID string
	vocab    SignalVocab // active signal vocabulary; defaults to DefaultSignals
}

// NewSyncer constructs a Syncer for the given agency.
// vocab is the active signal vocabulary; pass [DefaultSignals] when no
// .signals.json is present in the pushed commit tree.
func NewSyncer(dm entitygraph.DataManager, agencyID string, vocab SignalVocab) *Syncer {
	return &Syncer{dm: dm, agencyID: agencyID, vocab: vocab}
}

// Sync performs a full keyword upsert + edge hard-sync for the supplied files.
// branchID is the entity ID of the branch that scopes all edge operations.
//
// tagged_with edges carry signal and note from the matching depths[] entry, or
// signal "surface" when no depths[] entry exists for the keyword.
// tested_by[] entries are written as references {descriptor:"tested_by"} edges.
func (s *Syncer) Sync(ctx context.Context, branchID string, files []MappingFile) error {
	// 1. Signal vocabulary persistence (insert-only — no update, never delete).
	s.persistSignals(ctx, s.vocab)

	// 2. Keyword upsert: collect across all files, deduplicate by name.
	kwIDByName := s.upsertAllKeywords(ctx, files)

	// 3. Wire parent edges for keywords that declare a non-empty parent name.
	s.resolveParentEdges(ctx, files, kwIDByName)

	// 4. Hard-sync edges for every file declared in any mapping entry.
	s.syncEdgesForFiles(ctx, branchID, files, kwIDByName)

	return nil
}

// persistSignals writes Signal entities for each entry in vocab.
// Signals that already exist in the DB are left completely untouched.
// Errors from CreateEntity are logged and do not abort the sync.
func (s *Syncer) persistSignals(ctx context.Context, vocab SignalVocab) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sig := range vocab.Signals {
		existing, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID:   s.agencyID,
			TypeID:     "Signal",
			Properties: map[string]any{"name": sig.Name},
		})
		if err != nil {
			log.Printf("[gitgraph-sync][%s] persistSignals: list signal %q: %v", s.agencyID, sig.Name, err)
			continue
		}
		if len(existing) > 0 {
			continue // already exists — leave untouched per spec
		}
		if _, err := s.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: s.agencyID,
			TypeID:   "Signal",
			Properties: map[string]any{
				"name":        sig.Name,
				"layer":       sig.Layer,
				"description": sig.Description,
				"created_at":  now,
			},
		}); err != nil {
			log.Printf("[gitgraph-sync][%s] persistSignals: create signal %q: %v", s.agencyID, sig.Name, err)
		}
	}
}

// upsertAllKeywords collects all keyword definitions from all mapping files,
// deduplicates by name (last definition wins), upserts each one, and returns
// a name→entityID map. Keywords that fail to upsert are omitted from the map.
func (s *Syncer) upsertAllKeywords(ctx context.Context, files []MappingFile) map[string]string {
	byName := make(map[string]KeywordDef)
	for _, f := range files {
		for _, kw := range f.Keywords {
			if kw.Name != "" {
				byName[kw.Name] = kw
			}
		}
	}
	kwIDByName := make(map[string]string, len(byName))
	for name, kw := range byName {
		if id := s.upsertOneKeyword(ctx, kw); id != "" {
			kwIDByName[name] = id
		}
	}
	return kwIDByName
}

// upsertOneKeyword creates a new Keyword entity or updates an existing one.
// Returns the entity ID on success, or empty string on unrecoverable error.
func (s *Syncer) upsertOneKeyword(ctx context.Context, kw KeywordDef) string {
	now := time.Now().UTC().Format(time.RFC3339)

	existing, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "Keyword",
		Properties: map[string]any{"name": kw.Name},
	})
	if err != nil {
		log.Printf("[gitgraph-sync][%s] upsertKeyword: list %q: %v", s.agencyID, kw.Name, err)
		return ""
	}

	if len(existing) == 0 {
		entity, err := s.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
			AgencyID: s.agencyID,
			TypeID:   "Keyword",
			Properties: map[string]any{
				"name":        kw.Name,
				"description": kw.Description,
				"scope":       kw.Scope,
				"created_at":  now,
				"updated_at":  now,
			},
		})
		if err != nil {
			log.Printf("[gitgraph-sync][%s] upsertKeyword: create %q: %v", s.agencyID, kw.Name, err)
			return ""
		}
		return entity.ID
	}

	// Keyword already exists — update its mutable fields.
	entity, err := s.dm.UpdateEntity(ctx, s.agencyID, existing[0].ID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"description": kw.Description,
			"scope":       kw.Scope,
			"updated_at":  now,
		},
	})
	if err != nil {
		log.Printf("[gitgraph-sync][%s] upsertKeyword: update %q: %v", s.agencyID, kw.Name, err)
		return existing[0].ID // return existing ID even when the update fails
	}
	return entity.ID
}

// resolveParentEdges creates belongs_to_parent and has_child edges for all
// keywords that declare a non-empty parent name. Missing parent IDs are
// logged and skipped. Edges that already exist are not duplicated.
func (s *Syncer) resolveParentEdges(ctx context.Context, files []MappingFile, kwIDByName map[string]string) {
	for _, f := range files {
		for _, kw := range f.Keywords {
			if kw.Parent == "" || kw.Name == "" {
				continue
			}
			childID, ok := kwIDByName[kw.Name]
			if !ok {
				continue
			}
			parentID, ok := kwIDByName[kw.Parent]
			if !ok {
				log.Printf("[gitgraph-sync][%s] resolveParentEdges: parent %q not found for keyword %q", s.agencyID, kw.Parent, kw.Name)
				continue
			}
			existing, err := s.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
				AgencyID: s.agencyID,
				FromID:   childID,
				Name:     "belongs_to_parent",
			})
			if err != nil {
				log.Printf("[gitgraph-sync][%s] resolveParentEdges: list parent rels %q: %v", s.agencyID, kw.Name, err)
				continue
			}
			if len(existing) > 0 {
				continue // already parented — leave untouched
			}
			if _, err := s.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: s.agencyID,
				Name:     "belongs_to_parent",
				FromID:   childID,
				ToID:     parentID,
			}); err != nil {
				log.Printf("[gitgraph-sync][%s] resolveParentEdges: create belongs_to_parent %q→%q: %v", s.agencyID, kw.Name, kw.Parent, err)
			}
			if _, err := s.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: s.agencyID,
				Name:     "has_child",
				FromID:   parentID,
				ToID:     childID,
			}); err != nil {
				log.Printf("[gitgraph-sync][%s] resolveParentEdges: create has_child %q→%q: %v", s.agencyID, kw.Parent, kw.Name, err)
			}
		}
	}
}

// syncEdgesForFiles hard-syncs edges for every file path declared in any
// mapping entry across all supplied files.
func (s *Syncer) syncEdgesForFiles(ctx context.Context, branchID string, files []MappingFile, kwIDByName map[string]string) {
	pathSet := make(map[string]struct{})
	for _, f := range files {
		for _, entry := range f.Mappings {
			if entry.File != "" {
				pathSet[entry.File] = struct{}{}
			}
		}
	}

	blobIDByPath := make(map[string]string)
	for filePath := range pathSet {
		blobID, err := s.findBlobByPath(ctx, filePath, blobIDByPath)
		if err != nil || blobID == "" {
			log.Printf("[gitgraph-sync][%s] syncEdgesForFiles: blob not found for path %q: %v", s.agencyID, filePath, err)
			continue
		}
		desired := s.buildDesiredEdgesForFile(ctx, filePath, files, kwIDByName, blobIDByPath)
		s.syncEdgesForBlob(ctx, blobID, branchID, desired)
	}
}

// findBlobByPath looks up the entity ID of the Blob at the given file path.
// Results are cached in blobIDByPath. Returns empty string if not found.
func (s *Syncer) findBlobByPath(ctx context.Context, filePath string, cache map[string]string) (string, error) {
	if id, ok := cache[filePath]; ok {
		return id, nil
	}
	entities, err := s.dm.ListEntities(ctx, entitygraph.EntityFilter{
		AgencyID:   s.agencyID,
		TypeID:     "Blob",
		Properties: map[string]any{"path": filePath},
	})
	if err != nil {
		cache[filePath] = ""
		return "", err
	}
	if len(entities) == 0 {
		cache[filePath] = ""
		return "", nil
	}
	cache[filePath] = entities[0].ID
	return entities[0].ID, nil
}

// buildDesiredEdgesForFile returns the complete set of edges that should exist
// for the given file, derived from all mapping entries across all files.
func (s *Syncer) buildDesiredEdgesForFile(ctx context.Context, filePath string, files []MappingFile, kwIDByName map[string]string, blobIDByPath map[string]string) []desiredEdge {
	var desired []desiredEdge
	for _, f := range files {
		for _, entry := range f.Mappings {
			if entry.File != filePath {
				continue
			}
			desired = append(desired, s.buildTaggedWithEdges(entry, kwIDByName)...)
			desired = append(desired, s.buildReferencesEdges(ctx, entry, blobIDByPath)...)
		}
	}
	return desired
}

// buildTaggedWithEdges returns tagged_with desiredEdges for a single MappingEntry.
// Signal and note are sourced from depths[]; defaults to signal "surface".
func (s *Syncer) buildTaggedWithEdges(entry MappingEntry, kwIDByName map[string]string) []desiredEdge {
	depthByKw := make(map[string]DepthEntry, len(entry.Depths))
	for _, d := range entry.Depths {
		depthByKw[d.Keyword] = d
	}
	var edges []desiredEdge
	for _, kwName := range entry.Keywords {
		kwID, ok := kwIDByName[kwName]
		if !ok {
			log.Printf("[gitgraph-sync] buildTaggedWithEdges: keyword %q not in agency, skipping edge", kwName)
			continue
		}
		signal, note := "surface", ""
		if d, ok := depthByKw[kwName]; ok {
			signal, note = d.Signal, d.Note
		}
		edges = append(edges, desiredEdge{
			relName:    "tagged_with",
			toEntityID: kwID,
			properties: map[string]any{"signal": signal, "note": note},
		})
	}
	return edges
}

// buildReferencesEdges returns references desiredEdges (tested_by + references)
// for a single MappingEntry. Target blob IDs are resolved from the cache.
func (s *Syncer) buildReferencesEdges(ctx context.Context, entry MappingEntry, blobIDByPath map[string]string) []desiredEdge {
	var edges []desiredEdge
	for _, tb := range entry.TestedBy {
		if tb.File == "" {
			continue
		}
		targetID, _ := s.findBlobByPath(ctx, tb.File, blobIDByPath)
		if targetID == "" {
			log.Printf("[gitgraph-sync] buildReferencesEdges: blob not found for tested_by path %q", tb.File)
			continue
		}
		edges = append(edges, desiredEdge{
			relName:    "references",
			toEntityID: targetID,
			properties: map[string]any{"descriptor": "tested_by"},
		})
	}
	for _, ref := range entry.References {
		if ref.File == "" {
			continue
		}
		targetID, _ := s.findBlobByPath(ctx, ref.File, blobIDByPath)
		if targetID == "" {
			log.Printf("[gitgraph-sync] buildReferencesEdges: blob not found for references path %q", ref.File)
			continue
		}
		edges = append(edges, desiredEdge{
			relName:    "references",
			toEntityID: targetID,
			properties: map[string]any{"descriptor": ref.Descriptor},
		})
	}
	return edges
}

// syncEdgesForBlob performs the hard-sync for a single blob: creates missing
// edges and deletes edges that are no longer in the desired set.
func (s *Syncer) syncEdgesForBlob(ctx context.Context, blobID, branchID string, desired []desiredEdge) {
	desiredSet := make(map[edgeKey]desiredEdge, len(desired))
	for _, e := range desired {
		desiredSet[keyOfDesired(e)] = e
	}

	actual, err := s.listActualEdges(ctx, blobID, branchID)
	if err != nil {
		log.Printf("[gitgraph-sync][%s] syncEdgesForBlob: list actual edges for blob %s: %v", s.agencyID, blobID, err)
		return
	}

	for key, rel := range actual {
		if _, ok := desiredSet[key]; !ok {
			if err := s.dm.DeleteRelationship(ctx, s.agencyID, rel.ID); err != nil {
				log.Printf("[gitgraph-sync][%s] syncEdgesForBlob: delete edge %s: %v", s.agencyID, rel.ID, err)
			}
		}
	}
	for key, e := range desiredSet {
		if _, ok := actual[key]; ok {
			continue
		}
		props := make(map[string]any, len(e.properties)+1)
		for k, v := range e.properties {
			props[k] = v
		}
		props["branch_id"] = branchID
		if _, err := s.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID:   s.agencyID,
			Name:       e.relName,
			FromID:     blobID,
			ToID:       e.toEntityID,
			Properties: props,
		}); err != nil {
			log.Printf("[gitgraph-sync][%s] syncEdgesForBlob: create %s blob→%s: %v", s.agencyID, e.relName, e.toEntityID, err)
		}
	}
}

// listActualEdges returns all branch-scoped tagged_with and references edges
// from the given blob, keyed by edgeKey for hard-sync comparison.
func (s *Syncer) listActualEdges(ctx context.Context, blobID, branchID string) (map[edgeKey]entitygraph.Relationship, error) {
	result := make(map[edgeKey]entitygraph.Relationship)
	for _, relName := range []string{"tagged_with", "references"} {
		rels, err := s.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
			AgencyID: s.agencyID,
			FromID:   blobID,
			Name:     relName,
		})
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			bid, _ := rel.Properties["branch_id"].(string)
			if bid != branchID {
				continue // different branch — do not touch
			}
			descriptor, _ := rel.Properties["descriptor"].(string)
			key := edgeKey{relName: rel.Name, toEntityID: rel.ToID, descriptor: descriptor}
			result[key] = rel
		}
	}
	return result, nil
}
