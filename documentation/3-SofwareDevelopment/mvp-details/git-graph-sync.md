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

## Implementation Plan

### GIT-025a — JSON parser and validator

**File**: `internal/gitgraph/parser.go`

```go
package gitgraph

// SignalVocab holds the parsed contents of .git-graph/.signals.json.
// If the file is absent, DefaultSignals is used.
type SignalVocab struct {
    Signals []SignalDef `json:"signals"`
}

// SignalDef is a single entry in the signal vocabulary.
type SignalDef struct {
    Name        string `json:"name"`
    Layer       int    `json:"layer"`
    Description string `json:"description"`
}

// DefaultSignals is the built-in signal vocabulary used when .signals.json
// is absent or malformed.
var DefaultSignals = SignalVocab{
    Signals: []SignalDef{
        {Name: "surface",     Layer: 2},
        {Name: "index",       Layer: 5},
        {Name: "structural",  Layer: 8},
        {Name: "contributor", Layer: 12},
        {Name: "authority",   Layer: 18},
    },
}

// ParseSignalVocab parses .git-graph/.signals.json.
// Returns DefaultSignals and logs a warning if the file is absent or malformed.
func ParseSignalVocab(data []byte) (SignalVocab, error)

// MappingFile is the parsed representation of a single .git-graph/*.json file.
type MappingFile struct {
    Keywords []KeywordDef   `json:"keywords"`
    Mappings []MappingEntry `json:"mappings"`
}

// KeywordDef declares a keyword to upsert.
type KeywordDef struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Scope       string `json:"scope"`
    Parent      string `json:"parent"`
}

// MappingEntry declares edges for a single file.
type MappingEntry struct {
    File       string        `json:"file"`
    Keywords   []string      `json:"keywords"`
    Depths     []DepthEntry  `json:"depths"`
    TestedBy   []TestedByEntry `json:"tested_by"`
    References []RefEntry    `json:"references"`
}

// DepthEntry carries the signal depth and optional note for a single
// keyword attachment on a MappingEntry. It enriches the tagged_with edge
// with signal and note properties.
type DepthEntry struct {
    Keyword string `json:"keyword"` // must appear in MappingEntry.Keywords
    Signal  string `json:"signal"`  // must be in the active SignalVocab
    Note    string `json:"note"`
}

// TestedByEntry declares a references {descriptor:"tested_by"} edge from
// this mapping's file to the target file.
type TestedByEntry struct {
    File string `json:"file"`
}

// RefEntry is a single file→file reference edge declaration.
type RefEntry struct {
    File       string `json:"file"`
    Descriptor string `json:"descriptor"`
}

// ParseMappingFile parses and validates a single .git-graph JSON file.
// vocab is the active signal vocabulary used to validate signal values.
// Returns ErrInvalidMappingFile with details if validation fails.
func ParseMappingFile(data []byte, vocab SignalVocab) (MappingFile, error)

// ValidDescriptors is the set of allowed descriptor strings.
var ValidDescriptors = map[string]bool{
    "depends_on":  true,
    "test_for":    true,
    "tested_by":   true,
    "documents":   true,
    "obsoletes":   true,
    "contradicts": true,
    "references":  true,
}
```

Validation rules:
- Each `keywords[].name` must be non-empty.
- Each `mappings[].file` must be non-empty.
- Each `references[].descriptor` must be in `ValidDescriptors`.
- Each `depths[].signal` must be a name present in the active `SignalVocab`.
- Each `depths[].keyword` must appear in the same `MappingEntry.Keywords` list.
- Each `tested_by[].file` must be non-empty.
- Duplicate `keywords[].name` within the same file is an error.

**Error type** (in `errors.go`):
```go
// ErrInvalidMappingFile is returned when a .git-graph JSON file fails
// validation. File is the repo-relative path; Details lists each problem.
type ErrInvalidMappingFile struct {
    File    string
    Details []string
}
```

### GIT-025b — Sync logic

**File**: `internal/gitgraph/sync.go`

```go
// Syncer applies a parsed set of MappingFiles to the entity graph.
type Syncer struct {
    dm       entitygraph.DataManager
    agencyID string
    vocab    SignalVocab // active signal vocabulary; defaults to DefaultSignals
}

// Sync performs a full keyword upsert + edge hard-sync for the supplied files.
// branchID is the branch scope for edge operations.
//
// tagged_with edges are created with the signal and note from the matching
// depths[] entry, or signal "surface" if no depths[] entry exists for the keyword.
// tested_by[] entries are written as references {descriptor:"tested_by"} edges.
func (s *Syncer) Sync(ctx context.Context, branchID string, files []MappingFile) error
```

**Signal resolution for `tagged_with` edges:**
```
for each MappingEntry.Keywords[k]:
    depth = find DepthEntry where depth.Keyword == k
    if found:
        signal = depth.Signal
        note   = depth.Note
    else:
        signal = "surface"
        note   = ""
    CreateEdge(tagged_with, file→keyword, properties{signal, note})
```

### GIT-025e — `.signals.json` parsing and DB persistence

**Files**: `internal/gitgraph/parser.go` (parse), `internal/gitgraph/sync.go` (DB write)

`syncGitGraph` reads `.git-graph/.signals.json` from the commit tree **before** any
other `.git-graph/*.json` file. The resulting `SignalVocab` is passed to
`ParseMappingFile` and to `Syncer`.

**Parse behaviour:**
- File present and valid → use parsed vocabulary.
- File absent → use `DefaultSignals`, no warning.
- File present but malformed → log `ErrInvalidMappingFile{File: ".git-graph/.signals.json"}`, fall back to `DefaultSignals`.

**DB persistence (insert-only):**

```go
// persistSignals writes Signal entities for each entry in vocab.
// Signals that already exist in the DB are left completely untouched.
func (s *Syncer) persistSignals(ctx context.Context, vocab SignalVocab) error
```

For each `SignalDef` in `vocab.Signals`:
1. Call `dm.ListEntities` filtered by `TypeID: "Signal"` and `properties.name == def.Name`.
2. If the list is empty → `dm.CreateEntity` with:
   ```json
   { "TypeID": "Signal", "properties": { "name": "...", "layer": N, "description": "..." } }
   ```
3. If already present → skip, no update.

Errors from `CreateEntity` are logged and do not abort the sync.

### GIT-025f — Reviewer sign-off coverage query

**File**: `internal/gitgraph/coverage.go`

Exposes a `CheckCoverage` function used by the grpc layer to implement a
`CheckGraphCoverage` RPC (or surfaced via the existing `GetNeighborhood`
response metadata). It enforces the documentation coverage gate:

```go
// CoverageIssue describes a single coverage gate violation.
type CoverageIssue struct {
    KeywordID string
    Kind      string // "no_authority", "authority_untested", "surface_only"
    Detail    string
}

// CheckCoverage returns all coverage gate violations for the given branch.
// Returns nil if all conditions are satisfied.
//
// Conditions checked:
//   1. Every Keyword has at least one tagged_with edge with signal=="authority".
//   2. Every authority Blob has at least one outbound references edge with descriptor=="tested_by".
//   3. No Keyword is covered only by surface-signal Blobs (at least one structural/contributor/authority required).
func CheckCoverage(ctx context.Context, dm entitygraph.DataManager, agencyID, branchID string) ([]CoverageIssue, error)
```

### GIT-025c — Hook integration

**File**: `git_impl_index.go` (existing `IndexPushedBranch` implementation)

After the existing commit/blob indexing phase, add:

```go
// Phase 2 — .git-graph/ sync
if err := s.syncGitGraph(ctx, repoName, branchRef, newSHA); err != nil {
    // Log and continue — graph sync failures must not fail the push
    s.log.Error("git-graph sync failed", "repo", repoName, "err", err)
}
```

`syncGitGraph` reads all `.git-graph/*.json` files from the commit tree at
`newSHA`, parses them, and calls `Syncer.Sync`.

**Critical**: sync errors are logged but **never returned as push errors** —
a malformed `.git-graph/` file must not block the push.

### GIT-025d — Update `map-folder-keywords.prompt.md`

Update the AI prompt in `.github/prompts/map-folder-keywords.prompt.md` to
output `.git-graph/` JSON files matching this schema instead of calling the API
directly. The push itself triggers the sync.

---

## Error Handling

| Condition | Behaviour |
|---|---|
| Malformed JSON | Log `ErrInvalidMappingFile`, skip that file, continue |
| `.signals.json` absent | Use `DefaultSignals`, no warning |
| `.signals.json` malformed | Log `ErrInvalidMappingFile{File: ".git-graph/.signals.json"}`, use `DefaultSignals`, continue |
| Unknown keyword parent name | Log warning, create keyword without parent |
| Unknown signal value in `depths[]` | Log `ErrInvalidMappingFile`, skip that `depths[]` entry, create `tagged_with` with `signal: "surface"` |
| `depths[].keyword` not in same `mappings[].keywords[]` | Log `ErrInvalidMappingFile`, skip that `depths[]` entry |
| Unknown descriptor | Log `ErrInvalidMappingFile`, skip that reference entry |
| `tested_by[].file` is empty | Log `ErrInvalidMappingFile`, skip that entry |
| `CreateEntity(Signal)` fails | Log error, continue with remaining signals and the rest of the sync |
| `CreateKeyword` fails | Log error, continue with remaining keywords |
| `CreateEdge` / `DeleteEdge` fails | Log error, continue with remaining edges |
| All sync errors | Never propagate to the push response |

---

## Dependencies

| Task | Status |
|---|---|
| `IndexPushedBranch` hook exists | ✅ Already implemented |
| `CreateKeyword` / `UpdateKeyword` | ✅ Already implemented (GIT-019c) |
| `CreateEdge` / `DeleteEdge` | ✅ Already implemented (GIT-019e) |

---

## Acceptance Criteria

- [ ] `ParseMappingFile` rejects files with empty keyword names, duplicate names, or invalid descriptors
- [ ] `ParseMappingFile` rejects `depths[]` entries whose `signal` is not in the active `SignalVocab`
- [ ] `ParseMappingFile` rejects `depths[]` entries whose `keyword` is not in the same `MappingEntry.Keywords`
- [ ] On push, `.git-graph/.signals.json` is read first; absent file falls back to `DefaultSignals`
- [ ] Each signal in the active vocabulary is inserted as a `Signal` entity if it does not already exist in the DB
- [ ] Existing `Signal` entities are never updated or deleted by the sync
- [ ] On push, all `.git-graph/*.json` files at the new branch tip are parsed
- [ ] Keywords are upserted — existing keywords with the same name are updated, not duplicated
- [ ] `tagged_with` edges are created with `signal` and `note` from the matching `depths[]` entry, or `signal: "surface"` when no entry exists
- [ ] `references {descriptor:"tested_by"}` edges are created for every `tested_by[]` entry; removed entries are deleted
- [ ] `references` edges declared in files are created; edges removed from files are deleted
- [ ] Edges for files not mentioned in any mapping file are never touched by the sync
- [ ] A malformed `.git-graph/` file logs an error but does not fail the push
- [ ] `CheckCoverage` returns `"no_authority"` for any Keyword with no `authority`-signal `tagged_with` edge
- [ ] `CheckCoverage` returns `"authority_untested"` for any `authority` Blob with no outbound `tested_by` reference
- [ ] `CheckCoverage` returns `"surface_only"` for any Keyword covered only by `surface`-signal Blobs
- [ ] `go test -race ./internal/gitgraph/...` passes
