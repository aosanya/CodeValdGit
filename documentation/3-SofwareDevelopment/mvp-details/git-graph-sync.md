# GIT-025 — `.git-graph/` Push Sync

## Overview

Developers (and AI agents) can declare keyword taxonomy and file mappings by
committing JSON files under `.git-graph/` in a repository and pushing them.
`IndexPushedBranch` parses the files after every push and hard-syncs the
agency-wide keyword graph.

---

## Motivation

The existing keyword and edge CRUD APIs require individual API calls per
keyword and per edge. For large repositories or AI-generated mappings, authoring
via files is far more practical — the full mapping for a domain can be reviewed,
diffed, and merged using standard git workflows before it touches the graph.

---

## File Convention

### Location

```
{repo-root}/
└── .git-graph/
    ├── auth.json
    ├── crops.json
    └── payments.json
```

- Any `*.json` file directly inside `.git-graph/` is a mapping file.
- Subdirectories are ignored (reserved for future use).
- Files outside `.git-graph/` are never parsed by the sync.

### Schema

```json
{
  "keywords": [
    {
      "name": "authentication",
      "description": "Login, session, and token management",
      "scope": "agency",
      "parent": null
    },
    {
      "name": "login-screen",
      "description": "Flutter widget for the login UI",
      "scope": "repo",
      "parent": "authentication"
    }
  ],
  "mappings": [
    {
      "file": "lib/features/auth/login_screen.dart",
      "keywords": ["authentication", "login-screen"],
      "depths": [
        {
          "keyword": "authentication",
          "signal": "authority",
          "note": "Canonical login implementation — referenced by dashboard and profile"
        },
        {
          "keyword": "login-screen",
          "signal": "contributor",
          "note": "Defines the login widget structure"
        }
      ],
      "tested_by": [
        {
          "file": "test/features/auth/login_screen_test.dart"
        }
      ],
      "references": [
        {
          "file": "lib/features/auth/auth_provider.dart",
          "descriptor": "depends_on"
        },
        {
          "file": "docs/auth-flow.md",
          "descriptor": "documents"
        }
      ]
    },
    {
      "file": "test/features/auth/login_screen_test.dart",
      "keywords": ["authentication"],
      "depths": [
        {
          "keyword": "authentication",
          "signal": "structural",
          "note": "Test coverage for the authentication flow"
        }
      ],
      "references": [
        {
          "file": "lib/features/auth/login_screen.dart",
          "descriptor": "test_for"
        }
      ]
    }
  ]
}
```

### Field Reference

#### Top-level

| Field      | Type    | Required | Description |
|---|---|---|---|
| `keywords` | array   | No       | Keyword definitions to upsert into the agency taxonomy |
| `mappings` | array   | No       | File→keyword and file→file edge declarations |

#### `keywords[]` entry

| Field         | Type   | Required | Description |
|---|---|---|---|
| `name`        | string | ✅        | Unique keyword label within the agency. Used as the lookup key during sync. |
| `description` | string | No       | Human-readable explanation of what the keyword covers |
| `scope`       | string | No       | `"agency"` (default) or `"repo"`. Agency-scoped keywords are visible across all repos; repo-scoped are local. |
| `parent`      | string | No       | Name of the parent keyword. Resolved by name during sync. Null or omitted = root keyword. |

#### `mappings[]` entry

| Field        | Type            | Required | Description |
|---|---|---|---|
| `file`       | string          | ✅        | Repo-relative path to the file being mapped. Must match the path used in the entity graph. |
| `keywords`   | string[]        | No       | Names of keywords to attach via `tagged_with` edges (signal defaults to `surface` if no matching `depths[]` entry). |
| `depths`     | depth[]         | No       | Signal depth and note for each keyword attachment. Each entry enriches the `tagged_with` edge. |
| `tested_by`  | tested_by[]     | No       | Files that verify the claims in this file. Creates `references {descriptor:"tested_by"}` edges. |
| `references` | reference[]     | No       | File→file edges to create. |

#### `depths[]` entry

| Field     | Type   | Required | Description |
|---|---|---|---|
| `keyword` | string | ✅        | Name of the keyword this depth entry applies to. Must appear in the same mapping's `keywords[]` list. |
| `signal`  | string | ✅        | Coverage depth signal. Must be a value from `.git-graph/.signals.json` or the built-in vocabulary: `surface`, `index`, `structural`, `contributor`, `authority`. |
| `note`    | string | No       | Plain-text explanation of why this file covers the keyword at this depth. |

If a keyword in `keywords[]` has no matching `depths[]` entry, the `tagged_with` edge is created with `signal: "surface"`.

#### `tested_by[]` entry

| Field  | Type   | Required | Description |
|---|---|---|---|
| `file` | string | ✅        | Repo-relative path to the file that verifies this file. Creates a `references {descriptor:"tested_by"}` edge from this mapping's `file` to the target. |

#### `references[]` entry

| Field        | Type   | Required | Description |
|---|---|---|---|
| `file`       | string | ✅        | Repo-relative target file path. |
| `descriptor` | string | ✅        | Semantic label for the edge. See valid values below. |

#### Valid descriptors

| Descriptor    | Meaning |
|---|---|
| `depends_on`  | Source file depends on target at runtime |
| `test_for`    | Source is a test file that directly tests target |
| `tested_by`   | Source is a design/content file; target is the QA/acceptance file that verifies it |
| `documents`   | Source is a doc/markdown that explains target |
| `obsoletes`   | Source supersedes or replaces target |
| `contradicts` | Source and target define conflicting behaviour |
| `references`  | Generic cross-reference (fallback) |

---

### `.signals.json` — Signal Vocabulary File

```
{repo-root}/.git-graph/.signals.json
```

This file is the machine-readable signal vocabulary for the repository. It
defines the allowed signal names and their integer layer values. It is parsed
before any other `.git-graph/*.json` file during sync.

```json
{
  "signals": [
    { "name": "surface",     "layer": 2,  "description": "Keyword appears but file does not own the concept" },
    { "name": "index",       "layer": 5,  "description": "File lists or links to other files on this topic" },
    { "name": "structural",  "layer": 8,  "description": "File defines schema, format, status model, or process" },
    { "name": "contributor", "layer": 12, "description": "File adds content other files depend on" },
    { "name": "authority",   "layer": 18, "description": "Canonical source — other files reference this one" }
  ]
}
```

If `.signals.json` is absent, the syncer falls back to the built-in
vocabulary above. If `.signals.json` is present but malformed, the syncer
logs `ErrInvalidMappingFile` and continues using the built-in vocabulary.

---

## Sync Behaviour

### Trigger

`GitManager.IndexPushedBranch` is called by the Smart HTTP receive-pack
handler after every successful push. It already exists for commit/blob
indexing. The `.git-graph/` sync is added as an additional phase within this
function.

### Signal Vocabulary DB Sync (insert-only — existing records untouched)

After `.signals.json` is parsed (or `DefaultSignals` is chosen as fallback),
each signal definition is persisted to the agency's entity graph as a
`Signal` entity:

1. Look up the signal by `name` in the agency's `Signal` collection.
2. If **not found** → `CreateEntity` with `TypeID: "Signal"` and properties
   `name`, `layer`, `description`.
3. If **found** → **leave it untouched**. No update is applied. What is
   already in the database is the authoritative record.

Signals are **never deleted or updated by the sync**. Removing a signal from
`.signals.json` or changing its `layer` value has no effect on existing DB
records — changes to persisted signals are an explicit operator action.

### Keyword Sync (upsert — never delete)

For every keyword entry across all `.git-graph/*.json` files on the pushed
branch tip:

1. Look up the keyword by `name` in the agency's keyword collection.
2. If **not found** → `CreateKeyword` with the supplied fields.
3. If **found** → `UpdateKeyword` to apply any changed `description` or `scope`.
4. Resolve `parent` by name → set `belongs_to_parent` / `has_child` edges.

Keywords are **never deleted by the sync** even if removed from files — deletion
is an explicit operator action via the API or UI to prevent accidental data loss.

### Edge Sync (hard sync — removals honoured)

The sync computes the **desired edge set** from the current branch tip's
`.git-graph/` files and the **actual edge set** in the DB for the edges that
originate from files touched by this push.

Scope: only edges whose `fromId` is a Blob entity for a file path declared in
any `.git-graph/` mapping entry are considered. Edges created manually via the
UI/API for files *not* mentioned in any mapping file are left untouched.

Algorithm:
```
desired  = edges declared in .git-graph/ files
actual   = edges in DB whose fromId is in desired.files
to_add   = desired − actual
to_remove = actual − desired

CreateEdge for each edge in to_add
DeleteEdge for each edge in to_remove
```

### Branch vs Agency Scope

- **Keywords** go directly to the agency-wide taxonomy — they are not
  branch-scoped. A push to any branch (including `main`, feature branches, or
  task branches) can define or update keywords.
- **Edges** (`tagged_with`, `references`) follow the existing branch-scoped
  edge rules:
  - On a task branch push: edges are written to the task branch scope.
  - On a `main` branch push (e.g. after merge): edges are written to main.

---

> **Implementation plan, error handling, dependencies, and acceptance criteria**:
> see [`git-graph-sync-impl.md`](./git-graph-sync-impl.md)

