# CodeValdGit — Documentation Layer Requirements

## 1. Purpose

Introduce a **documentation layer** to CodeValdGit that enables keyword-based
discovery across Git objects (Blobs, Branches, Commits). AI agents can query
the graph by keyword and receive all related files — documentation and code
alike — to build rich working context for tasks.

---

## 2. Design Decisions (Resolved)

### DR-001: Storage Model — ArangoDB Graph Alongside Git Data

The documentation layer lives as an **ArangoDB graph layer** alongside the
existing Git entity graph. It is **not** embedded inside the Git repo as
metadata files. New TypeDefinitions will be added to the existing
`schema.go` (`DefaultGitSchema`), keeping the same schema ID `git-schema-v1`.

### DR-002: Scope — Per-Repo, Not a Separate Service

Documents are **normal files committed into a repo** (e.g., files under a
`documentation/` path). They participate in the standard branch-per-task
workflow — committed on task branches, merged to `main`, versioned like any
other file. There is **no separate Document entity type**; documentation
files are simply Blobs with documentation edges.

### DR-003: Primary Consumer — AI Agents

The primary consumer is **AI agents** building context for tasks. When an
agent receives a task, it queries "give me all files related to keyword X"
and receives a set of Blobs (both documentation markdown and code files),
Branches, and Commits.

Human users (via CodeValdHi / CodeValdGitFrontend) are a secondary
beneficiary. API should prioritise bulk retrieval and machine-readable
responses.

### DR-004: Node Types — Keyword Only (v1)

No new Document entity type. The only **new entity type** is:

- **Keyword** — a hierarchical discovery label node (e.g.,
  `"authentication"`, `"grpc"`, `"merge-conflict"`, `"pull-flow"`)

All other nodes already exist in the schema: Blob, Branch, Commit, Tag,
Repository.

**Explicitly out of scope for v1:**
- Function/Symbol-level nodes (no code parsing or AST extraction)

### DR-005: Edges Point to Existing Schema Types

Documentation edges point directly to **existing entity types** in the
schema — Blob, Branch, and Commit. No new lightweight reference nodes.

### DR-006: Doc↔Code Mapping via Direct Blob Edges

A documentation Blob can have `documents` edges pointing to the code Blobs
it describes. For example:

```
architecture-pull-flow.md ──documents──► git_impl_repo.go
architecture-pull-flow.md ──documents──► server.go
git_impl_repo.go ──documented_by──► architecture-pull-flow.md  (inverse)
```

This enables graph traversal: "given this doc file, show me the code it
documents" and vice versa.

### DR-007: Edge Creation — Explicit API Only

Edges (both `tagged_with` and `documents`) are created via **explicit API
calls**. No frontmatter parsing, no auto-extraction from file content.
An agent or human calls the API to create doc↔code edges and keyword tags.

### DR-008: Cross-Repo — Keyword-Mediated Only

`documents`/`documented_by` edges are strictly **within the same repo**.
Cross-repo discovery is achieved through **shared Keywords** — both repos
tag their files with the same Keyword, and the agent finds them via
keyword query.

### DR-009: Keyword Taxonomy — Free-Form with Hierarchy

Keywords are **free-form strings** — no controlled vocabulary. Any agent
or human can create any keyword.

Keywords support **parent-child nesting** (taxonomy tree):

```
backend
├── grpc
│   ├── pull-flow
│   └── push-flow
├── authentication
└── storage
    ├── arangodb
    └── filesystem
```

**Cascading search is the default**: querying keyword `"backend"` returns
all entities tagged with `"backend"` AND all entities tagged with any
descendant keyword (`"grpc"`, `"pull-flow"`, etc.).

### DR-010: Edges Follow Git Lifecycle

Documentation edges are **soft state** — they follow the same lifecycle as
the Git objects they're attached to. They are **not** permanent metadata.

| Scenario | Edge behaviour |
|---|---|
| Branch merged to `main` | Edges on branch Blobs are **replicated** to corresponding `main` Blobs (matched by `path`), additive on top of existing `main` edges |
| Branch deleted without merge | Edges on branch Blobs are **deleted** — they never reach `main` |
| Merge reverted (revert commit) | Edges that were replicated from the branch are **removed** from `main` Blobs |
| File deleted in a commit | `tagged_with` and `documents` edges on that Blob are **removed** |
| File renamed/moved | Edges on old-path Blob are **migrated** to new-path Blob |

**Merge edge replication strategy**: after merge, scan branch Blobs for
`tagged_with` and `documents` edges, find the corresponding `main` Blob by
**path** (same `path` property), and create new edges from the `main` Blob
to the same Keyword/target entities.

### DR-011: Query Match Mode — Caller's Choice

When searching by multiple keywords, the caller specifies a `match_mode`:

- **AND** — return only entities tagged with ALL specified keywords
- **OR** — return entities tagged with ANY specified keyword

### DR-012: Schema Version — No Bump

New types are added directly to the existing `git-schema-v1`. No deployed
instances exist, so no migration or version bump is needed.

### DR-013: File Dependency Edges — Manual, Blob-to-Blob

Blobs can declare **dependency relationships** to other Blobs within the
same repo via `depends_on` / `imported_by` edges. For example:

```
repo.go ──depends_on──► errors.go
errors.go ──imported_by──► repo.go  (inverse, auto-created)
```

These edges follow the **same rules** as `documents`/`documented_by`:

- **Branch-scoped**: created on task-branch Blobs, replicated to `main`
  on merge via path-based matching (DR-010)
- **Same-repo only**: no cross-repo dependency edges (DR-008 applies)
- **Manual creation**: edges are created via explicit API calls (DR-007
  applies). No automatic import parsing in v1 — an agent or human
  declares the dependency
- **Git lifecycle**: deleted on branch delete, removed on revert,
  migrated on rename (DR-010 applies)

### DR-023: Blob→Blob Edges — Collapsed to `references` / `referenced_by` with `descriptor` Property

The original four named edge types (`documents`, `documented_by`, `depends_on`,
`imported_by`) are **replaced** by a single generic Blob→Blob pair:

| Edge name | Direction | Inverse |
|---|---|---|
| `references` | from → to | `referenced_by` |
| `referenced_by` | to → from (auto) | `references` |

Both edges carry a `descriptor` string property (required) that names the
semantic role of the relationship. The vocabulary is **open** — agents may
invent new descriptors — but should **reuse existing labels before coining
new ones**. Well-known values:

| Descriptor | Meaning |
|---|---|
| `documents` | This doc file describes the target code file |
| `depends_on` | This file imports / depends on the target file |
| `contradicts` | This file's content conflicts with the target |
| `references` | General cross-reference without a stronger semantic |
| `test_for` | This test file covers the target implementation file |
| `obsoletes` | This file supersedes the target file |

**Rationale**: The descriptor is data, not schema. Adding a new relationship
type previously required a schema change; now agents simply pass a new string.
The `RelationshipDefinition.Properties` field added to SharedLib carries this
declaration so tooling can surface it.

### DR-024: Inverse Edge Copies `descriptor` Property

When `entitygraph.DataManager.CreateRelationship` auto-creates the inverse
(`referenced_by`) edge, it **copies the full `Properties` map** from the
originating `references` edge. This ensures that inbound graph traversal
("who references this file, and how?") returns the same descriptor context
as outbound traversal ("what does this file reference, and how?").

The `referenced_by` `RelationshipDefinition` explicitly declares the same
`descriptor` property so schema tooling documents it correctly.

---

## 3. Entity Types (New)

### Keyword

A hierarchical discovery label used for keyword-based search across Git
objects. Keywords can be nested in parent-child trees for taxonomy.

| Property | Type | Required | Description |
|---|---|---|---|
| `name` | string | ✅ | The keyword label, e.g. `"authentication"`, `"pull-flow"` |
| `description` | string | | Optional explanation of what this keyword covers |
| `scope` | string | | `"agency"` (spans all repos in the agency) or `"repo"` (repo-local) |
| `created_at` | string | | ISO 8601 timestamp |
| `updated_at` | string | | ISO 8601 timestamp |

**Storage collection**: `git_keywords`

**Relationships**:

| Relationship | To | ToMany | Inverse | Description |
|---|---|---|---|---|
| `has_child` | Keyword | true | `belongs_to_parent` | Child keywords in the taxonomy tree |
| `belongs_to_parent` | Keyword | false | `has_child` | Parent keyword (optional; root keywords have none) |

---

## 4. Edge Types (New)

### `tagged_with` — Keyword Tagging

Any existing entity can be tagged with a Keyword for discovery.

| From | Edge | To | Description |
|---|---|---|---|
| Blob | `tagged_with` | Keyword | Tag a file (doc or code) with a keyword |
| Branch | `tagged_with` | Keyword | Tag a branch with a keyword |
| Commit | `tagged_with` | Keyword | Tag a commit with a keyword |

### `references` / `referenced_by` — Generic Blob→Blob Edges

A single generic pair replaces the former `documents`/`documented_by` and
`depends_on`/`imported_by` edge types (DR-023). Both edges carry a
`descriptor` string property that names the semantic role.

| From | Edge | To | Required property | Description |
|---|---|---|---|---|
| Blob | `references` | Blob | `descriptor` | Forward edge; caller supplies the descriptor |
| Blob | `referenced_by` | Blob | `descriptor` | Inverse — auto-created; `descriptor` copied from the forward edge (DR-024) |

**Example descriptors**: `"documents"`, `"depends_on"`, `"contradicts"`,
`"references"`, `"test_for"`, `"obsoletes"`.

Agents should query existing descriptors in use before creating new ones.

---

## 5. Query API

### SearchByKeywords

Search for entities tagged with one or more keywords. Cascading search
traverses the keyword hierarchy by default.

**Input**:

| Field | Type | Required | Description |
|---|---|---|---|
| `keywords` | []string | ✅ | One or more keyword names to search for |
| `match_mode` | string | | `"AND"` (all keywords) or `"OR"` (any keyword). Default: `"OR"` |
| `repo_id` | string | | Optional filter: restrict results to a specific repository |
| `entity_types` | []string | | Optional filter: e.g. `["Blob", "Branch"]`. Default: all types |
| `cascade` | bool | | Whether to include descendant keywords. Default: `true` |

**Output**: List of matching entities (Blobs, Branches, Commits) with their
keyword tags.

### Doc→Code Traversal

```
Query: "what code does architecture-pull-flow.md document?"
Traversal: Blob{path="documentation/.../architecture-pull-flow.md"} ──documents──► Blob*
Result: List of code Blobs
```

### Code→Doc Traversal

```
Query: "what documentation exists for server.go?"
Traversal: Blob{path="internal/server/server.go"} ──documented_by──► Blob*
Result: List of documentation Blobs
```

---

## 6. Frontend Graph Navigation Decisions

### DR-014: Graph Library — react-force-graph-2d + d3-hierarchy

Use **`react-force-graph-2d`** (wraps D3 force simulation, React-friendly) for
the relationship graph and **`d3-hierarchy`** (math-only, React renders SVG) for
the keyword taxonomy tree. Avoids D3-vs-React DOM conflicts.

### DR-015: View Strategy — Sidebar + Full-Page Explorer

Two views coexist:

1. **Sidebar panel on file viewer** — shows the current file's immediate
   relationships (docs, dependencies, keywords). Interactive — supports
   inline edge creation/removal without leaving the file viewer.
2. **Full-page graph explorer** — dedicated route at
   `/agencies/:id/repositories/:repo/branches/:branch/graph`. Split view:
   keyword taxonomy tree on the left, force-directed graph on the right.
   Clicking a keyword in the tree populates the graph with its tagged entities.

### DR-016: Sidebar Edge Creation UX

- **Keywords**: autocomplete search — user types keyword name, gets suggestions,
  selects to create `tagged_with` edge.
- **File-to-file edges** (`documents`, `depends_on`): drag a file from the
  existing file tree onto the graph to create the edge.

### DR-017: Edge Lifecycle — Branch-Scoped

All documentation edges are **branch-scoped**. File navigation is always per
branch, so edge creation follows the same model. Edges created on a task branch
are replicated to `main` on merge (DR-010). The sidebar should display which
branch the user is on.

### DR-018: Neighborhood Query — Configurable Depth 1-3

A single graph endpoint accepts `?depth=N` (1-3). Depth 1 for sidebar
(immediate neighbors), depth 2-3 for the full-page explorer (transitive
relationships).

### DR-019: Keyword Management — Inline + Dedicated Page

- **Inline**: right-click / button controls on the graph explorer's keyword tree
  for quick create/rename/delete/reparent.
- **Dedicated page**: `/agencies/:id/repositories/:repo/keywords` for bulk
  operations and full CRUD table/tree view.

### DR-020: API Response Shape — Generic Graph Format

```json
{
  "nodes": [
    { "id": "entity-123", "type": "Blob", "label": "server.go", "properties": { "path": "internal/server/server.go" } }
  ],
  "edges": [
    { "id": "edge-456", "source": "entity-123", "target": "kw-789", "label": "tagged_with" }
  ]
}
```

Frontend maps `type` to colors/icons. Backend is graph-library agnostic.

### DR-021: Performance — Lazy Expand + Hard Cap

- **Lazy expand**: show depth-1 neighbors initially; user clicks "expand" on a
  node to fetch its neighbors (progressive disclosure).
- **Hard cap**: API caps response at 100 nodes; frontend shows "N more results"
  with option to filter.

### DR-022: Visual Encoding — Icons + Colors + Distinct Edges

**Nodes** (icon inside colored circle):

| Type | Color | Icon |
|---|---|---|
| Blob | Blue | File icon |
| Keyword | Orange | Tag icon |
| Commit | Green | Git-commit icon |
| Branch | Purple | Git-branch icon |

**Edges** (color + line style):

| Edge Type | Color | Style |
|---|---|---|
| `tagged_with` | Orange | Dashed |
| `documents` | Blue | Solid |
| `depends_on` | Red | Solid |
| `has_child` | Gray | Dotted |

Accessible for colorblind users via shape/icon + line style differentiation.

---

## 7. HTTP API Endpoints (Documentation Layer)

### Keyword CRUD

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/git/{agencyId}/repositories/{repoName}/keywords` | Create keyword |
| `GET` | `/git/{agencyId}/repositories/{repoName}/keywords` | List all keywords (flat) |
| `GET` | `/git/{agencyId}/repositories/{repoName}/keywords/tree` | Full keyword taxonomy tree |
| `GET` | `/git/{agencyId}/repositories/{repoName}/keywords/{keywordId}` | Get single keyword |
| `PUT` | `/git/{agencyId}/repositories/{repoName}/keywords/{keywordId}` | Update keyword (rename, reparent) |
| `DELETE` | `/git/{agencyId}/repositories/{repoName}/keywords/{keywordId}` | Delete keyword |

### Edge Management (Branch-Scoped)

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/git/{agencyId}/repositories/{repoName}/branches/{branchId}/edges` | Create edge (`tagged_with`, `documents`, `depends_on`) |
| `DELETE` | `/git/{agencyId}/repositories/{repoName}/branches/{branchId}/edges/{edgeId}` | Remove edge |

### Graph Queries (Branch-Scoped)

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/git/{agencyId}/repositories/{repoName}/branches/{branchId}/graph/{entityId}` | Neighborhood query (`?depth=1-3`) |
| `GET` | `/git/{agencyId}/repositories/{repoName}/branches/{branchId}/graph/search` | SearchByKeywords (`?keywords=X,Y&match_mode=AND\|OR&cascade=true`) |

---

## 8. Open Questions (Research Gaps)

All questions resolved. ✅

| # | Question | Status |
|---|---|---|
| ~~OQ-001~~ | ~~Cross-repo edges~~ | ✅ **Resolved** — no cross-repo edges; keyword-mediated only (DR-008) |
| ~~OQ-002~~ | ~~Keyword taxonomy~~ | ✅ **Resolved** — free-form with parent-child hierarchy (DR-009) |
| ~~OQ-003~~ | ~~Keyword node properties~~ | ✅ **Resolved** — name, description, scope, timestamps (Section 3) |
| ~~OQ-004~~ | ~~Query API design~~ | ✅ **Resolved** — SearchByKeywords with AND/OR match_mode (Section 5, DR-011) |
| ~~OQ-005~~ | ~~Blob identity across versions~~ | ✅ **Resolved** — edges replicated on merge by path; follow Git lifecycle (DR-010) |
| ~~OQ-006~~ | ~~Schema version~~ | ✅ **Resolved** — no bump; add to existing git-schema-v1 (DR-012) |