# 4 — QA

## Overview

This section covers testing strategy, acceptance criteria, and quality assurance for CodeValdGit.

---

## Index

| Document | Description |
|---|---|
| _(none yet)_ | Test plans and QA artifacts will be added as tasks are implemented |

---

## Testing Standards

All contributions must satisfy:

| Check | Command | Requirement |
|---|---|---|
| Build | `go build ./...` | Must succeed — no compilation errors |
| Unit tests | `go test -v -race ./...` | All tests green; no data races |
| Static analysis | `go vet ./...` | 0 issues |
| Linting | `golangci-lint run ./...` | Must pass |
| Coverage | `go test -coverprofile=coverage.out ./...` | Target ≥ 80% on exported functions |

---

## Test Structure Convention

Tests live alongside source files using Go's standard `_test.go` convention:

```
manager_test.go       ← RepoManager lifecycle tests
repo_test.go          ← Repo operation tests
storage/
  arangodb/
    objects_test.go   ← ArangoDB object storer tests
    refs_test.go      ← ArangoDB ref storer tests
internal/
  rebase/
    rebase_test.go    ← Cherry-pick rebase tests
```

Integration tests that require external services (ArangoDB) must use `t.Skip()` when `ARANGODB_URL` is not set.

---

## Acceptance Criteria per Task

See the `### Tests` section of each task file in [../3-SofwareDevelopment/mvp-details/](../3-SofwareDevelopment/mvp-details/README.md) for the full test matrix per MVP task.
