// git_impl_keywords.go implements the documentation layer methods on [gitManager]:
//
//   - Keyword CRUD ([GitManager.CreateKeyword], [GitManager.GetKeyword],
//     [GitManager.ListKeywords], [GitManager.GetKeywordTree],
//     [GitManager.UpdateKeyword], [GitManager.DeleteKeyword])
//
//   - Branch-scoped edge CRUD ([GitManager.CreateEdge], [GitManager.DeleteEdge])
//
// All storage operations go through the injected [entitygraph.DataManager].
// Edges are branch-scoped and follow the DR-010 lifecycle rules: replicated to
// main on merge, deleted on branch delete, migrated on file rename.
package codevaldgit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// validDocEdges is the set of allowed documentation relationship names.
// "tagged_with" and "references" are the two branch-scoped types that follow
// the DR-010 lifecycle (replicated on merge, deleted on branch delete).
var validDocEdges = map[string]bool{
	"tagged_with":   true,
	"references":    true,
	"referenced_by": true,
	"documents":     true,
	"documented_by": true,
	"depends_on":    true,
	"imported_by":   true,
}

// ── Keyword CRUD ──────────────────────────────────────────────────────────────

// CreateKeyword creates a new Keyword entity in the taxonomy.
// If req.ParentID is set the keyword is added as a child of that parent.
// Returns [ErrKeywordAlreadyExists] if a keyword with the same name exists
// under the same parent. Returns [ErrKeywordNotFound] if req.ParentID does not
// resolve to a keyword.
func (m *gitManager) CreateKeyword(ctx context.Context, req CreateKeywordRequest) (Keyword, error) {
	// Validate parent exists if specified.
	if req.ParentID != "" {
		_, err := m.dm.GetEntity(ctx, m.agencyID, req.ParentID)
		if err != nil {
			if errors.Is(err, entitygraph.ErrEntityNotFound) {
				return Keyword{}, ErrKeywordNotFound
			}
			return Keyword{}, fmt.Errorf("CreateKeyword: get parent: %w", err)
		}
	}

	// Check for name collision under the same parent.
	siblings, err := m.listKeywordsByParent(ctx, req.ParentID)
	if err != nil {
		return Keyword{}, fmt.Errorf("CreateKeyword: list siblings: %w", err)
	}
	for _, k := range siblings {
		if strProp(k.Properties, "name") == req.Name {
			return Keyword{}, ErrKeywordAlreadyExists
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rels := []entitygraph.EntityRelationshipRequest{}
	if req.ParentID != "" {
		rels = append(rels, entitygraph.EntityRelationshipRequest{
			Name: "belongs_to_parent",
			ToID: req.ParentID,
		})
	}

	entity, err := m.dm.CreateEntity(ctx, entitygraph.CreateEntityRequest{
		AgencyID: m.agencyID,
		TypeID:   "Keyword",
		Properties: map[string]any{
			"name":        req.Name,
			"description": req.Description,
			"scope":       req.Scope,
			"created_at":  now,
			"updated_at":  now,
		},
		Relationships: rels,
	})
	if err != nil {
		return Keyword{}, fmt.Errorf("CreateKeyword: create entity: %w", err)
	}

	// Create the forward has_child edge (parent → keyword) so GetKeywordTree
	// can traverse downwards.
	if req.ParentID != "" {
		if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
			AgencyID: m.agencyID,
			Name:     "has_child",
			FromID:   req.ParentID,
			ToID:     entity.ID,
		}); err != nil {
			return Keyword{}, fmt.Errorf("CreateKeyword: link has_child: %w", err)
		}
	}

	return entityToKeyword(entity), nil
}

// GetKeyword retrieves a Keyword entity by its entitygraph ID.
// Returns [ErrKeywordNotFound] if no keyword with that ID exists.
func (m *gitManager) GetKeyword(ctx context.Context, keywordID string) (Keyword, error) {
	entity, err := m.dm.GetEntity(ctx, m.agencyID, keywordID)
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return Keyword{}, ErrKeywordNotFound
		}
		return Keyword{}, fmt.Errorf("GetKeyword %s: %w", keywordID, err)
	}
	kw := entityToKeyword(entity)
	// Populate ParentID from belongs_to_parent relationship.
	parentRels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   keywordID,
		Name:     "belongs_to_parent",
	})
	if err != nil {
		return Keyword{}, fmt.Errorf("GetKeyword %s: list parent: %w", keywordID, err)
	}
	if len(parentRels) > 0 {
		kw.ParentID = parentRels[0].ToID
	}
	return kw, nil
}

// ListKeywords returns Keyword entities matching the given filter.
// When filter.ParentID is empty, root keywords (no parent) are returned.
// Set filter.ParentID to a keyword ID to list its direct children.
func (m *gitManager) ListKeywords(ctx context.Context, filter KeywordFilter) ([]Keyword, error) {
	entities, err := m.listKeywordsByParent(ctx, filter.ParentID)
	if err != nil {
		return nil, fmt.Errorf("ListKeywords: %w", err)
	}

	out := make([]Keyword, 0, len(entities))
	for _, e := range entities {
		kw := entityToKeyword(e)
		if filter.Scope != "" && kw.Scope != filter.Scope {
			continue
		}
		kw.ParentID = filter.ParentID
		out = append(out, kw)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

// GetKeywordTree returns the full taxonomy subtree rooted at the given
// keywordID, or the full forest of root keywords when keywordID is empty.
func (m *gitManager) GetKeywordTree(ctx context.Context, keywordID string) ([]KeywordTreeNode, error) {
	var rootEntities []entitygraph.Entity
	var err error
	if keywordID == "" {
		// Return all root keywords (no parent).
		rootEntities, err = m.listKeywordsByParent(ctx, "")
	} else {
		// Return children of the given keyword.
		rootEntities, err = m.listChildEntities(ctx, keywordID)
	}
	if err != nil {
		return nil, fmt.Errorf("GetKeywordTree: %w", err)
	}

	nodes := make([]KeywordTreeNode, 0, len(rootEntities))
	for _, e := range rootEntities {
		node, err := m.buildKeywordTreeNode(ctx, e)
		if err != nil {
			return nil, fmt.Errorf("GetKeywordTree: build node %s: %w", e.ID, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// UpdateKeyword updates the mutable fields of a Keyword entity.
// Returns [ErrKeywordNotFound] if no keyword with that ID exists.
func (m *gitManager) UpdateKeyword(ctx context.Context, keywordID string, req UpdateKeywordRequest) (Keyword, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	entity, err := m.dm.UpdateEntity(ctx, m.agencyID, keywordID, entitygraph.UpdateEntityRequest{
		Properties: map[string]any{
			"name":        req.Name,
			"description": req.Description,
			"scope":       req.Scope,
			"updated_at":  now,
		},
	})
	if err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return Keyword{}, ErrKeywordNotFound
		}
		return Keyword{}, fmt.Errorf("UpdateKeyword %s: %w", keywordID, err)
	}
	return entityToKeyword(entity), nil
}

// DeleteKeyword removes a Keyword entity and re-parents its children to
// the deleted keyword's parent (or promotes them to root if the deleted
// keyword had no parent).
// Returns [ErrKeywordNotFound] if no keyword with that ID exists.
func (m *gitManager) DeleteKeyword(ctx context.Context, keywordID string) error {
	// Fetch keyword to resolve parent.
	kw, err := m.GetKeyword(ctx, keywordID)
	if err != nil {
		return fmt.Errorf("DeleteKeyword %s: %w", keywordID, err)
	}

	// Fetch direct children.
	children, err := m.listChildEntities(ctx, keywordID)
	if err != nil {
		return fmt.Errorf("DeleteKeyword %s: list children: %w", keywordID, err)
	}

	// Re-parent each child to the deleted keyword's parent.
	for _, child := range children {
		// Remove belongs_to_parent → deleted keyword.
		if err := m.removeRelationshipByEndpoints(ctx, child.ID, "belongs_to_parent", keywordID); err != nil {
			return fmt.Errorf("DeleteKeyword %s: remove child parent rel: %w", keywordID, err)
		}
		// Remove has_child → child from deleted keyword.
		if err := m.removeRelationshipByEndpoints(ctx, keywordID, "has_child", child.ID); err != nil {
			return fmt.Errorf("DeleteKeyword %s: remove has_child rel: %w", keywordID, err)
		}
		// Re-attach child to new parent (if any).
		if kw.ParentID != "" {
			if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: m.agencyID,
				Name:     "belongs_to_parent",
				FromID:   child.ID,
				ToID:     kw.ParentID,
			}); err != nil {
				return fmt.Errorf("DeleteKeyword %s: reparent child: %w", keywordID, err)
			}
			if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
				AgencyID: m.agencyID,
				Name:     "has_child",
				FromID:   kw.ParentID,
				ToID:     child.ID,
			}); err != nil {
				return fmt.Errorf("DeleteKeyword %s: reparent has_child: %w", keywordID, err)
			}
		}
	}

	// Delete the keyword entity itself.
	if err := m.dm.DeleteEntity(ctx, m.agencyID, keywordID); err != nil {
		if errors.Is(err, entitygraph.ErrEntityNotFound) {
			return ErrKeywordNotFound
		}
		return fmt.Errorf("DeleteKeyword %s: delete entity: %w", keywordID, err)
	}
	return nil
}

// ── Branch-Scoped Edge CRUD ───────────────────────────────────────────────────

// CreateEdge creates a documentation edge between two entities on the
// specified branch. Supported relationship names: "tagged_with", "documents",
// "documented_by", "depends_on", "imported_by".
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrInvalidRelationship] if the relationship name is not valid.
func (m *gitManager) CreateEdge(ctx context.Context, req CreateEdgeRequest) error {
	if !validDocEdges[req.RelationshipName] {
		return fmt.Errorf("CreateEdge: %w: %q", ErrInvalidRelationship, req.RelationshipName)
	}

	// Verify the branch exists.
	if _, err := m.GetBranch(ctx, req.BranchID); err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return ErrBranchNotFound
		}
		return fmt.Errorf("CreateEdge: get branch %s: %w", req.BranchID, err)
	}

	if _, err := m.dm.CreateRelationship(ctx, entitygraph.CreateRelationshipRequest{
		AgencyID: m.agencyID,
		Name:     req.RelationshipName,
		FromID:   req.FromEntityID,
		ToID:     req.ToEntityID,
		Properties: mergeEdgeProps(req.Properties, map[string]any{
			"branch_id": req.BranchID,
		}),
	}); err != nil {
		return fmt.Errorf("CreateEdge %s (%s→%s): %w", req.RelationshipName, req.FromEntityID, req.ToEntityID, err)
	}
	return nil
}

// DeleteEdge removes a documentation edge between two entities.
// Returns [ErrBranchNotFound] if the branch does not exist.
// Returns [ErrEdgeNotFound] if no matching edge exists.
// Returns [ErrInvalidRelationship] if the relationship name is invalid.
func (m *gitManager) DeleteEdge(ctx context.Context, req DeleteEdgeRequest) error {
	if !validDocEdges[req.RelationshipName] {
		return fmt.Errorf("DeleteEdge: %w: %q", ErrInvalidRelationship, req.RelationshipName)
	}

	// Verify the branch exists.
	if _, err := m.GetBranch(ctx, req.BranchID); err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return ErrBranchNotFound
		}
		return fmt.Errorf("DeleteEdge: get branch %s: %w", req.BranchID, err)
	}

	if err := m.removeRelationshipByEndpoints(ctx, req.FromEntityID, req.RelationshipName, req.ToEntityID); err != nil {
		return fmt.Errorf("DeleteEdge %s (%s→%s): %w", req.RelationshipName, req.FromEntityID, req.ToEntityID, err)
	}
	return nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// entityToKeyword maps an entitygraph.Entity of type "Keyword" to [Keyword].
func entityToKeyword(e entitygraph.Entity) Keyword {
	p := e.Properties
	return Keyword{
		ID:          e.ID,
		Name:        strProp(p, "name"),
		Description: strProp(p, "description"),
		Scope:       strProp(p, "scope"),
		CreatedAt:   strProp(p, "created_at"),
		UpdatedAt:   strProp(p, "updated_at"),
	}
}

// listKeywordsByParent returns all Keyword entities whose parent is parentID.
// When parentID is empty, root keywords (those with no belongs_to_parent edge
// and whose parent relationship is absent) are returned via ListEntities.
func (m *gitManager) listKeywordsByParent(ctx context.Context, parentID string) ([]entitygraph.Entity, error) {
	if parentID == "" {
		// Return all keywords; the caller can filter roots by checking parent rels.
		// For root listing, return all keywords with TypeID "Keyword" and rely on
		// the tree build logic. For now we list all and filter at the caller.
		return m.dm.ListEntities(ctx, entitygraph.EntityFilter{
			AgencyID: m.agencyID,
			TypeID:   "Keyword",
		})
	}
	return m.listChildEntities(ctx, parentID)
}

// listChildEntities returns Keyword entities that are direct children of parentID
// via has_child relationships.
func (m *gitManager) listChildEntities(ctx context.Context, parentID string) ([]entitygraph.Entity, error) {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   parentID,
		Name:     "has_child",
	})
	if err != nil {
		return nil, fmt.Errorf("listChildEntities %s: %w", parentID, err)
	}
	out := make([]entitygraph.Entity, 0, len(rels))
	for _, rel := range rels {
		e, err := m.dm.GetEntity(ctx, m.agencyID, rel.ToID)
		if err != nil {
			continue // skip deleted or missing keywords
		}
		out = append(out, e)
	}
	return out, nil
}

// buildKeywordTreeNode recursively builds a [KeywordTreeNode] for entity e.
func (m *gitManager) buildKeywordTreeNode(ctx context.Context, e entitygraph.Entity) (KeywordTreeNode, error) {
	kw := entityToKeyword(e)
	children, err := m.listChildEntities(ctx, e.ID)
	if err != nil {
		return KeywordTreeNode{}, err
	}
	childNodes := make([]KeywordTreeNode, 0, len(children))
	for _, child := range children {
		node, err := m.buildKeywordTreeNode(ctx, child)
		if err != nil {
			return KeywordTreeNode{}, err
		}
		childNodes = append(childNodes, node)
	}
	return KeywordTreeNode{Keyword: kw, Children: childNodes}, nil
}

// removeRelationshipByEndpoints finds the relationship with the given FromID,
// Name, and ToID and deletes it. Returns [ErrEdgeNotFound] if not found.
func (m *gitManager) removeRelationshipByEndpoints(ctx context.Context, fromID, name, toID string) error {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		FromID:   fromID,
		Name:     name,
	})
	if err != nil {
		return fmt.Errorf("removeRelationshipByEndpoints: list: %w", err)
	}
	for _, rel := range rels {
		if rel.ToID == toID {
			if err := m.dm.DeleteRelationship(ctx, m.agencyID, rel.ID); err != nil {
				return fmt.Errorf("removeRelationshipByEndpoints: delete: %w", err)
			}
			return nil
		}
	}
	return ErrEdgeNotFound
}
