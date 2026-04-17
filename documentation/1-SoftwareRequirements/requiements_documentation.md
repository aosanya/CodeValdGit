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

### `documents` / `documented_by` — Doc↔Code Mapping

Direct edges between Blob entities **within the same repo** linking
documentation files to the code files they describe.

| From | Edge | To | Description |
|---|---|---|---|
| Blob (doc) | `documents` | Blob (code) | "This doc describes this code file" |
| Blob (code) | `documented_by` | Blob (doc) | Inverse — auto-created |

### `depends_on` / `imported_by` — File Dependencies

Direct edges between Blob entities **within the same repo** declaring
that one file depends on (imports, references, or uses) another.

| From | Edge | To | Description |
|---|---|---|---|
| Blob (source) | `depends_on` | Blob (dependency) | "This file depends on / imports this file" |
| Blob (dependency) | `imported_by` | Blob (source) | Inverse — auto-created |

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

## 6. Open Questions (Research Gaps)

All questions resolved. ✅

| # | Question | Status |
|---|---|---|
| ~~OQ-001~~ | ~~Cross-repo edges~~ | ✅ **Resolved** — no cross-repo edges; keyword-mediated only (DR-008) |
| ~~OQ-002~~ | ~~Keyword taxonomy~~ | ✅ **Resolved** — free-form with parent-child hierarchy (DR-009) |
| ~~OQ-003~~ | ~~Keyword node properties~~ | ✅ **Resolved** — name, description, scope, timestamps (Section 3) |
| ~~OQ-004~~ | ~~Query API design~~ | ✅ **Resolved** — SearchByKeywords with AND/OR match_mode (Section 5, DR-011) |
| ~~OQ-005~~ | ~~Blob identity across versions~~ | ✅ **Resolved** — edges replicated on merge by path; follow Git lifecycle (DR-010) |
| ~~OQ-006~~ | ~~Schema version~~ | ✅ **Resolved** — no bump; add to existing git-schema-v1 (DR-012) |