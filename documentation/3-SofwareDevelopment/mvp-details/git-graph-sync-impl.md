# GIT-025 — `.git-graph/` Push Sync: Implementation Plan

> Schema, file convention, and sync behaviour: see [`git-graph-sync.md`](./git-graph-sync.md)

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
