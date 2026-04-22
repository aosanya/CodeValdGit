---
agent: agent
---

# Map Folder Keywords to `.git-graph/` Files

This prompt guides an AI agent to analyse a repository's file structure and produce
`.git-graph/` JSON mapping files. The files are committed and pushed; the push triggers
`IndexPushedBranch`, which syncs the keyword graph automatically — no API calls needed.

---

## Objective

Produce one or more `.git-graph/*.json` files that declare:

1. **Keywords** — the concepts that live in this repository's taxonomy
2. **Mappings** — which files cover which keywords, at what depth, and how files relate to each other

---

## Steps

### 1. Analyse the Repository Structure

Walk the repository tree and identify logical domains (e.g. `auth`, `payments`, `ui`).
Group files by:

- Feature or domain folder (e.g. `lib/features/auth/`)
- File role: implementation, test, documentation, configuration
- Cross-cutting concerns (e.g. logging, error handling)

### 2. Define Keywords

For each domain or concept, create a `KeywordDef` entry:

```json
{
  "name": "authentication",
  "description": "Login, session management, and token lifecycle",
  "scope": "agency",
  "parent": null
}
```

**Rules:**

| Field         | Guidance |
|---|---|
| `name`        | Lowercase, hyphen-separated. Must be unique within the agency. |
| `description` | One sentence describing what the keyword covers. |
| `scope`       | `"agency"` for concepts shared across repos; `"repo"` for repo-local concepts. |
| `parent`      | Name of the parent keyword, or omit/null for root keywords. |

### 3. Map Files to Keywords

For each file, declare a `MappingEntry`:

```json
{
  "file": "lib/features/auth/login_screen.dart",
  "keywords": ["authentication", "login-screen"],
  "depths": [
    {
      "keyword": "authentication",
      "signal": "authority",
      "note": "Canonical login implementation — referenced by dashboard and profile flows"
    },
    {
      "keyword": "login-screen",
      "signal": "contributor",
      "note": "Defines the login widget structure"
    }
  ],
  "tested_by": [
    { "file": "test/features/auth/login_screen_test.dart" }
  ],
  "references": [
    { "file": "lib/features/auth/auth_provider.dart", "descriptor": "depends_on" },
    { "file": "docs/auth-flow.md", "descriptor": "documents" }
  ]
}
```

#### Signal Depth — Choosing the Right Value

Pick the signal that best describes **how deeply this file covers the keyword**:

| Signal        | Layer | When to use |
|---|---|---|
| `surface`     | 2     | File mentions the keyword but does not own or implement the concept (default if omitted). |
| `index`       | 5     | File lists, routes to, or aggregates files that implement the concept. |
| `structural`  | 8     | File defines schema, format, status model, or process for the concept. |
| `contributor` | 12    | File adds content that other files in this domain depend on. |
| `authority`   | 18    | Canonical source — the file other files reference for this concept. |

**Rules:**

- Every keyword in `keywords[]` should have a matching `depths[]` entry. If omitted, `signal` defaults to `"surface"`.
- `depths[].keyword` must appear in the same entry's `keywords[]` list.
- A file should have **at most one** `authority` assignment per keyword across the whole mapping set.

#### References — Choosing the Right Descriptor

| Descriptor    | When to use |
|---|---|
| `depends_on`  | Source file calls or imports target at runtime. |
| `test_for`    | Source is a test file that directly tests target. |
| `tested_by`   | Source is a design or implementation file; target is the test that verifies it. |
| `documents`   | Source is a doc or markdown explaining target. |
| `obsoletes`   | Source supersedes or replaces target. |
| `contradicts` | Source and target define conflicting behaviour. |
| `references`  | Generic cross-reference when no other descriptor applies. |

> Use `tested_by[]` (the shorthand array) when listing test files for the current file —
> it is equivalent to a `references` entry with `descriptor: "tested_by"`.

### 4. Organise Files by Domain

Split mappings into one file per logical domain and place them all under `.git-graph/`:

```
{repo-root}/
└── .git-graph/
    ├── auth.json
    ├── payments.json
    └── ui.json
```

Each file follows the schema:

```json
{
  "keywords": [ ... ],
  "mappings":  [ ... ]
}
```

- `keywords` and `mappings` are both optional — a file may contain only keywords, only mappings, or both.
- Files in subdirectories of `.git-graph/` are ignored by the syncer.
- The filename has no semantic meaning; use it for human readability only.

### 5. (Optional) Customise the Signal Vocabulary

If the built-in five signals are not expressive enough, create `.git-graph/.signals.json`:

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

- This file is read **before** any other `.git-graph/*.json` file during sync.
- Signal names used in `depths[].signal` must appear here (or in the built-in defaults).
- If absent, the syncer falls back to the built-in vocabulary above.
- If present but malformed, the syncer logs a warning and falls back to the built-in vocabulary.

### 6. Commit and Push

```bash
git add .git-graph/
git commit -m "docs: add .git-graph/ keyword mappings for <domain>"
git push
```

The push triggers `IndexPushedBranch`, which:

1. Reads `.git-graph/.signals.json` (or uses built-in defaults).
2. Parses all `.git-graph/*.json` mapping files at the pushed branch tip.
3. Upserts keywords into the agency-wide taxonomy.
4. Hard-syncs `tagged_with` and `references` edges for all mapped files.

**No API calls are needed.** The file commit is the declaration; the push is the trigger.

---

## Validation Checklist

Before committing, verify:

- [ ] Every `keywords[].name` is non-empty and unique within the file.
- [ ] Every `mappings[].file` is a real, repo-relative path.
- [ ] Every `depths[].keyword` appears in the same entry's `keywords[]` list.
- [ ] Every `depths[].signal` is one of: `surface`, `index`, `structural`, `contributor`, `authority` (or a custom signal in `.signals.json`).
- [ ] Every `references[].descriptor` is one of the valid descriptors listed above.
- [ ] Every `tested_by[].file` is non-empty.
- [ ] No keyword has two `authority`-signal depth entries across the whole mapping set for the same keyword.

---

## Full Example

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
        { "file": "test/features/auth/login_screen_test.dart" }
      ],
      "references": [
        { "file": "lib/features/auth/auth_provider.dart", "descriptor": "depends_on" },
        { "file": "docs/auth-flow.md", "descriptor": "documents" }
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
        { "file": "lib/features/auth/login_screen.dart", "descriptor": "test_for" }
      ]
    }
  ]
}
```
