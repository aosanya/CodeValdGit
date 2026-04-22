# GIT-026 — `POST /git/{agencyId}/branches/{branchId}/graph/query`

## Overview

A flexible server-side graph query endpoint that returns the top N graph nodes and
their edges, filtered across five dimensions and sorted by signal strength. Designed
to serve the city-plan Graph page in CodeValdGitFrontend (GITFE-019) as its sole
data source, replacing the combination of `SearchByKeywords` + `GetNeighborhood`
for that surface.

---

## Task

### GIT-026 — Graph Query Endpoint

| Task | Status | Depends On |
|---|---|---|
| GIT-026: `POST .../graph/query` — multi-filter, signal-sorted graph query | 📋 Not Started | GIT-025b, ~~GIT-025e~~ ✅ |

**Route**: `POST /git/{agencyId}/branches/{branchId}/graph/query`

**Request body** (all fields optional):

```json
{
  "limit":         50,
  "sort_by":       "signal",
  "signals":       ["authority", "contributor", "structural"],
  "keyword_ids":   ["kw-uuid-1", "kw-uuid-2"],
  "file_types":    [".ts", ".go", ".md"],
  "folders":       ["app/features/", "internal/"],
  "relationships": ["depends_on", "documents", "test_for"]
}
```

| Field | Default | Description |
|---|---|---|
| `limit` | 50 | Maximum number of nodes to return |
| `sort_by` | `"signal"` | Sort order; only `"signal"` supported in v1 |
| `signals` | all | Restrict to Blob nodes whose highest `tagged_with` signal is in the set |
| `keyword_ids` | all | Restrict to nodes tagged with at least one of the given keyword IDs |
| `file_types` | all | Restrict Blob nodes by file extension (suffix match on entity path) |
| `folders` | all | Restrict Blob nodes whose path starts with any of the given prefixes |
| `relationships` | all | Restrict edges to those whose `label`/`descriptor` is in the set |

**Response**: `GraphResponse` — same shape as all existing graph endpoints:
```json
{ "nodes": [...], "edges": [...] }
```
Only edges where both `source` and `target` are in the returned node set are included.

**Scope**:
- New HTTP handler registered in `internal/registrar/docs_routes.go`
- Filter logic (all dimensions compose with AND semantics):
  - `signals` — query `tagged_with` edges for each Blob; keep node if its max signal layer is in set
  - `keyword_ids` — keep Blob nodes that have a `tagged_with` edge to any of the given keyword IDs
  - `file_types` — suffix match on the Blob entity's path field
  - `folders` — prefix match on the Blob entity's path field
  - `relationships` — after node filtering, keep only edges whose label or `properties.descriptor` is in set
- Sort: when `sort_by="signal"`, nodes ordered by descending max signal layer; top `limit` returned
- Empty body → returns top 50 highest-signal Blob nodes with their edges (default behaviour)

**Acceptance criteria**:
- Empty body returns top 50 highest-signal nodes ordered by signal layer descending
- All five filter dimensions compose correctly (AND semantics)
- Response shape is identical to existing `GraphResponse`
- Edges in response only reference nodes present in the `nodes` array
- Unit tests: filter composition, empty-body default, limit enforcement
- Integration test: push `.git-graph/` file with signal data → call query → verify signal ordering
