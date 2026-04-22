// git_impl_converters.go — entity→domain converters, property helpers, and
// shared graph lookup utilities for [gitManager].
package codevaldgit

import (
	"context"

	"github.com/aosanya/CodeValdSharedLib/entitygraph"
)

// ── Entity → domain converters ────────────────────────────────────────────────

// entityToRepository maps an entitygraph.Entity of type "Repository" to [Repository].
func entityToRepository(e entitygraph.Entity, agencyID string) Repository {
	p := e.Properties
	return Repository{
		ID:            e.ID,
		AgencyID:      agencyID,
		Name:          strProp(p, "name"),
		Description:   strProp(p, "description"),
		DefaultBranch: strProp(p, "default_branch"),
		CreatedAt:     strProp(p, "created_at"),
		UpdatedAt:     strProp(p, "updated_at"),
		SourceURL:     strProp(p, "source_url"),
	}
}

// entityToBranch maps an entitygraph.Entity of type "Branch" to [Branch].
func entityToBranch(e entitygraph.Entity, repositoryID string) Branch {
	p := e.Properties
	return Branch{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         strProp(p, "name"),
		IsDefault:    boolProp(p, "is_default"),
		HeadCommitID: strProp(p, "head_commit_id"),
		CreatedAt:    strProp(p, "created_at"),
		UpdatedAt:    strProp(p, "updated_at"),
	}
}

// entityToTag maps an entitygraph.Entity of type "Tag" to [Tag].
func entityToTag(e entitygraph.Entity, repositoryID string) Tag {
	p := e.Properties
	return Tag{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         strProp(p, "name"),
		SHA:          strProp(p, "sha"),
		Message:      strProp(p, "message"),
		TaggerName:   strProp(p, "tagger_name"),
		TaggerAt:     strProp(p, "tagger_at"),
		CreatedAt:    strProp(p, "created_at"),
	}
}

// ── Shared graph helpers ──────────────────────────────────────────────────────

// resolveParentID returns the first ToID for an outbound relationship with the
// given name from the entity identified by entityID. Returns "" on any error.
func (m *gitManager) resolveParentID(ctx context.Context, entityID, relName string) string {
	rels, err := m.dm.ListRelationships(ctx, entitygraph.RelationshipFilter{
		AgencyID: m.agencyID,
		Name:     relName,
		FromID:   entityID,
	})
	if err != nil || len(rels) == 0 {
		return ""
	}
	return rels[0].ToID
}

// ── Property helpers ──────────────────────────────────────────────────────────

// strProp returns the string value of key in props, or "" if absent.
func strProp(props map[string]any, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// boolProp returns the bool value of key in props, or false if absent.
func boolProp(props map[string]any, key string) bool {
	if v, ok := props[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// int64Prop returns the int64 value of key in props, or 0 if absent.
func int64Prop(props map[string]any, key string) int64 {
	if v, ok := props[key]; ok {
		switch vv := v.(type) {
		case int64:
			return vv
		case int:
			return int64(vv)
		case float64:
			return int64(vv)
		}
	}
	return 0
}
