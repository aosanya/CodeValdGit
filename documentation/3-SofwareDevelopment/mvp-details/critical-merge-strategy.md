# GIT-012 — Squash Merge Strategy

**Status**: 📋 Not Started
**Depends on**: GIT-011
**See also**: [architecture-merge.md](../../2-SoftwareDesignAndArchitecture/architecture-merge.md)

---

## Objective

Replace the current HEAD-pointer-advance in `MergeBranch` with a proper
tree-diff squash merge that detects divergence between the task branch and the
default branch and returns `ErrMergeConflict` when the diff cannot be applied
cleanly.

---

## Files to Change

| File | Change |
|---|---|
| `models.go` — `Branch` | Add `ForkPointCommitID string` field |
| `git_impl_repo.go` — `CreateBranch` | Persist `fork_point_commit_id` on the Branch entity |
| `git_impl_repo.go` — `MergeBranch` | Replace HEAD-advance with divergence check + squash apply |
| `schema.go` | Add `fork_point_commit_id` property to `Branch` TypeDefinition |
| `errors.go` | Ensure `ErrMergeConflict` (already in `types.go`) is correctly surfaced |

---

## Acceptance Criteria

- [ ] `Branch.ForkPointCommitID` added to `models.go` with godoc
- [ ] `CreateBranch` sets `fork_point_commit_id` = source branch `head_commit_id` at creation time
- [ ] `MergeBranch` reads `fork_point_commit_id` from the source branch entity
- [ ] Fast-forward path: fork-point == default HEAD → advance pointer only, no new commit entity
- [ ] Diverged path: fork-point != default HEAD → compute tree diff and apply to default HEAD tree
- [ ] Conflict path: unapplicable diff → return `ErrMergeConflict{Files: [...]}`; default branch unchanged
- [ ] Squash commit entity created on the default branch with `source_branch_id` metadata
- [ ] Unit test: fast-forward merge (no divergence)
- [ ] Unit test: diverged merge with clean diff → squash commit created on default branch
- [ ] Unit test: diverged merge with conflicting diff → `ErrMergeConflict` returned; default HEAD unchanged
- [ ] `go vet ./...` passes; `go test -race ./...` passes

---

## Implementation Notes

### Tree Diff

The entitygraph model stores `Blob` entities linked to `Tree` entities via
`has_blob` edges, and `Tree` entities linked to `Commit` entities via
`has_tree` edges.

```
Commit ──has_tree──► Tree ──has_blob──► Blob (path, sha, content)
```

Tree diff steps:
1. Traverse fork-point commit → collect `{path: content}` map (agent's base).
2. Traverse task HEAD commit → collect `{path: content}` map (agent's result).
3. Diff: compute added, modified, deleted paths (net change set).
4. Traverse default HEAD commit → collect `{path: content}` map (current default).
5. Apply net change set to default HEAD tree:
   - Add/modify: apply if default path is unchanged from fork-point or does not exist.
   - Delete: apply if default path is unchanged from fork-point.
   - Conflict: default path was also modified relative to fork-point.
6. If clean → create new Tree + Blob entities + squash Commit entity on default branch.
7. If conflict → collect conflicting paths → return `ErrMergeConflict{Files: paths}`.

### Squash Commit Entity

```go
entitygraph.CreateEntityRequest{
    TypeID: "Commit",
    Properties: map[string]any{
        "message":          req.Message,       // e.g. "Merge task/abc-001"
        "author_name":      req.AuthorName,
        "author_email":     req.AuthorEmail,
        "authored_at":      now,
        "committed_at":     now,
        "source_branch_id": branchID,          // audit trail
    },
    Relationships: []entitygraph.EntityRelationshipRequest{
        {Name: "has_parent", ToID: defaultHeadCommitID},
        {Name: "has_tree",   ToID: newTreeID},
    },
}
```

---

## Branch

`feature/GIT-012_squash-merge-strategy`
