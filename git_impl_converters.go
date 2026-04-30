// git_impl_converters.go — entity→domain converters and shared graph lookup
// utilities for [gitManager].
//
// Property helpers (StringProp, BoolProp, Int64Prop) live in
// [github.com/aosanya/CodeValdSharedLib/entitygraph] and are used directly.
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
		Name:          entitygraph.StringProp(p, "name"),
		Description:   entitygraph.StringProp(p, "description"),
		DefaultBranch: entitygraph.StringProp(p, "default_branch"),
		CreatedAt:     entitygraph.StringProp(p, "created_at"),
		UpdatedAt:     entitygraph.StringProp(p, "updated_at"),
		SourceURL:     entitygraph.StringProp(p, "source_url"),
	}
}

// entityToBranch maps an entitygraph.Entity of type "Branch" to [Branch].
func entityToBranch(e entitygraph.Entity, repositoryID string) Branch {
	p := e.Properties
	return Branch{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         entitygraph.StringProp(p, "name"),
		IsDefault:    entitygraph.BoolProp(p, "is_default"),
		HeadCommitID: entitygraph.StringProp(p, "head_commit_id"),
		CreatedAt:    entitygraph.StringProp(p, "created_at"),
		UpdatedAt:    entitygraph.StringProp(p, "updated_at"),
	}
}

// entityToTag maps an entitygraph.Entity of type "Tag" to [Tag].
func entityToTag(e entitygraph.Entity, repositoryID string) Tag {
	p := e.Properties
	return Tag{
		ID:           e.ID,
		RepositoryID: repositoryID,
		Name:         entitygraph.StringProp(p, "name"),
		SHA:          entitygraph.StringProp(p, "sha"),
		Message:      entitygraph.StringProp(p, "message"),
		TaggerName:   entitygraph.StringProp(p, "tagger_name"),
		TaggerAt:     entitygraph.StringProp(p, "tagger_at"),
		CreatedAt:    entitygraph.StringProp(p, "created_at"),
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
