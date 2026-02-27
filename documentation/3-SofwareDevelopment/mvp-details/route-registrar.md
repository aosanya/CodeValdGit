# Service-Driven Route Registration

Topics: HTTP Routing · Route Registrar · CodeValdCross Integration

---

## MVP-GIT-011 — Service-Driven Route Registration

### Overview

Move git-backed HTTP handler functions out of CodeValdCross's `internal/server/http.go`
and into `internal/clients/git/` inside CodeValdCross. Expose them through a
`Routes(orch *orchestrator.Orchestrator) []server.Route` function so that
`NewHTTPServer` never names `clients/git` directly — it mounts whatever routes
are handed to it.

This task is the CodeValdGit-side deliverable of the pattern decision made in
**CROSS-007**. It has no changes to CodeValdGit itself — all changes live in the
CodeValdCross module, specifically in `internal/clients/git/`.

---

### Dependencies

- ~~MVP-GIT-010~~ ✅ (gRPC server implementation — handlers already exist)
- **CROSS-007** must be designed and spec'd before implementation begins
  (provides the `server.Route` type that `Routes()` returns)

---

### Acceptance Criteria

#### `internal/clients/git/routes.go` (new file in CodeValdCross repo)

- [ ] Package `git` (same package as `client.go` and `mock.go`)
- [ ] Imports only `net/http`, `internal/orchestrator`, and `internal/server`
      (no circular imports)
- [ ] Exported function:
  ```go
  // Routes returns the HTTP routes backed by CodeValdGit operations.
  // Pass the result to server.NewHTTPServer alongside routes from other
  // client packages.
  func Routes(orch *orchestrator.Orchestrator) []server.Route
  ```
- [ ] Returns exactly two routes:
  | Method | Pattern | Handler |
  |--------|---------|---------|
  | `GET` | `/{agencyId}/tasks/{taskId}/files` | `handleListTaskFiles` |
  | `POST` | `/{agencyId}/repositories` | `handleCreateRepository` |
- [ ] `handleListTaskFiles` and `handleCreateRepository` moved verbatim from
  `internal/server/http.go` into this file (or a companion `handlers.go`)
- [ ] Both handlers call `server.WriteJSONError` (exported helper from
  `internal/server/http.go`) for error responses
- [ ] Godoc on `Routes` and on each handler function

#### `internal/server/http.go` (modified in CodeValdCross repo)

- [ ] `handleListTaskFiles` and `handleCreateRepository` removed
- [ ] `writeJSONError` renamed to `WriteJSONError` (exported); all call sites
  updated across `clients/git` and `clients/work` packages

---

### What Does NOT Change in CodeValdGit

This task makes no changes to the CodeValdGit repository. The proto definitions,
gRPC server, and generated stubs are untouched. The git client interface
(`GitClient`) gains no new methods.

---

### Test Impact

- Existing orchestrator tests (`orchestrator/task_test.go`) are unaffected
- Any direct HTTP handler tests should be re-homed into the `git` package
  alongside the handler functions
- `go build ./...` and `go test -race ./...` in CodeValdCross must pass

---

### Branch Naming (in CodeValdCross repo)

```
feature/CROSS-007_service_driven_route_registration
```

This is a shared branch with WORK-006 — both are part of the same refactor.
