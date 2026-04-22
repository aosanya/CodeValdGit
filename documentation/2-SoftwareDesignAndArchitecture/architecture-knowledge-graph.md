# CodeValdGit — Knowledge Graph Overlay

## Purpose

The knowledge graph overlay (stored under `.git-graph/` in each repo) is a
semantic layer on top of the file graph. It answers two questions that raw
`tagged_with` and `references` edges alone do not:

1. **How deeply does a Blob cover a Keyword?** (signal depth)
2. **Which Blob verifies the claims made by another Blob?** (`tested_by` edge)

Both are stored entirely within the existing schema — no new entity types or
relationship types are needed.

---

## 1. Signal Depth — `tagged_with` Edge Properties

The `tagged_with` relationship (Blob → Keyword) now carries two edge properties:

| Property | Type | Required | Purpose |
|---|---|---|---|
| `signal` | string | ✅ | How deeply this Blob covers the Keyword (see table below) |
| `note` | string | — | Plain-text explanation of the coverage at this depth |

### Signal Vocabulary (ordered by depth)

| Signal | Layer value | Meaning |
|---|---|---|
| `surface` | 2 | Keyword appears in the file but the file does not own or define the concept |
| `index` | 5 | File lists or links to other files on this topic — navigation only |
| `structural` | 8 | File defines a schema, format, status model, or process around the concept |
| `contributor` | 12 | File adds content that other files on this topic depend on |
| `authority` | 18 | Canonical source for the concept — other files reference this one |

Layer values are stored in `.git-graph/.signals.json` alongside the signal
names. They are used by tooling to compute coverage scores and to enforce the
reviewer sign-off condition (see §3).

### Example edge (JSON representation)

```json
{
  "from": "blobs/architecture.md-sha",
  "to":   "keywords/architecture-id",
  "signal": "authority",
  "note":   "Canonical architecture source — referenced by requirements and mvp"
}
```

---

## 2. `tested_by` — `references` Edge with Descriptor

`tested_by` is **not a new relationship type**. It is a value in the
open-vocabulary `descriptor` property on the existing `references` edge
(Blob → Blob).

```
Blob(architecture.md) ──references {descriptor:"tested_by"}──► Blob(test-strategy.md)
```

The auto-created inverse edge carries the same descriptor:

```
Blob(test-strategy.md) ──referenced_by {descriptor:"tested_by"}──► Blob(architecture.md)
```

This means:
- Querying `tested_by` outbound from an authority Blob tells you what verifies it.
- Querying `referenced_by` with `descriptor:"tested_by"` inbound to a QA Blob
  tells you what claims it is testing.

### Full `descriptor` vocabulary (well-known values)

| Descriptor | Direction | Meaning |
|---|---|---|
| `documents` | A → B | A is the design doc; B is the implementation |
| `depends_on` | A → B | A cannot be understood without B |
| `references` | A → B | A links to B for supporting context |
| `contradicts` | A → B | A presents a position that conflicts with B |
| `test_for` | A → B | A is a test file that targets the code in B |
| `tested_by` | A → B | A is a content/design doc; B is the QA/acceptance file that verifies A |
| `obsoletes` | A → B | A supersedes B; B is kept for history only |

The vocabulary is open — agents may coin new descriptors when none of the
above fit. Reuse well-known values wherever possible.

---

## 3. Reviewer Sign-Off Condition

A PR that introduces or modifies documentation Blobs can be signed off when
the knowledge graph satisfies all of the following:

| Condition | How to check |
|---|---|
| Every introduced Keyword has at least one `authority`-signal `tagged_with` edge | Query `tagged_with` edges for the Keyword; check `signal == "authority"` exists |
| Every `authority` Blob has at least one outbound `references {descriptor:"tested_by"}` edge | Query `references` outbound from the Blob; filter `descriptor == "tested_by"` |
| No Keyword is covered only by `surface`-signal Blobs | At least one Blob must hold `structural`, `contributor`, or `authority` |
| Every `references` edge points to a Blob that exists in the same repo | Referential integrity — no dangling edges |

This is the documentation equivalent of a CI coverage gate: `authority` without
`tested_by` = untested claim.

---

## 4. Mapping: `.git-graph` JSON ↔ Schema

The `.git-graph/*.json` files in each repo are the human-authored seed data
for the knowledge graph. They map directly to schema entities and edges:

| `.git-graph` JSON field | Schema entity / edge |
|---|---|
| `file` path | `Blob` entity (looked up by path within the repo) |
| `keywords[].name` | `Keyword` entity (`name` property) |
| `keywords[].scope` | `Keyword` entity (`scope` property) |
| `mappings[].file` | `Blob` entity |
| `depths[].signal` | `tagged_with` edge → `signal` property |
| `depths[].note` | `tagged_with` edge → `note` property |
| `references[].file` | Target `Blob` entity |
| `references[].descriptor` | `references` edge → `descriptor` property |
| `tested_by[].file` | Target `Blob` entity |
| `tested_by[].descriptor` | `references` edge with `descriptor: "tested_by"` |

---

## 5. Schema Change Summary

Two changes were made to `schema.go` to support this model:

### `tagged_with` — added edge properties

```go
{
    Name:        "tagged_with",
    Label:       "Keywords",
    PathSegment: "keywords",
    ToType:      "Keyword",
    ToMany:      true,
    Properties: []types.PropertyDefinition{
        {Name: "signal", Type: types.PropertyTypeString, Required: true},
        {Name: "note",   Type: types.PropertyTypeString},
    },
},
```

### `references` — `tested_by` added to descriptor vocabulary

No schema change was needed. The `descriptor` property is already defined as
an open-vocabulary string on the `references` edge. `tested_by` is a new
well-known value documented in the comment and in this file.

---

## 6. Related Documents

| Document | Relationship |
|---|---|
| [architecture.md](architecture.md) | Core schema design — `Blob`, `Keyword`, `tagged_with`, `references` |
| `schema.go` | Source of truth for all `TypeDefinition` and `RelationshipDefinition` entries |
| `.git-graph/.signals.json` | Machine-readable signal vocabulary with layer values (per repo) |
| `.git-graph/documentation/*.json` | Per-folder keyword and mapping definitions (per repo) |
