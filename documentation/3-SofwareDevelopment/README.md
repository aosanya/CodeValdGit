# 3 — Software Development

## Overview

This section tracks the development plan, MVP task breakdown, and implementation details for CodeValdGit.

---

## Index

| Document | Description |
|---|---|
| [mvp.md](mvp.md) | Full MVP scope, task list, and completion status |
| [mvp-details/](mvp-details/README.md) | Per-topic task specifications grouped by domain |

---

## MVP Status

| Task ID | Title | Status |
|---|---|---|
| MVP-GIT-001 | Library Scaffolding | 🔲 Not Started |
| MVP-GIT-002 | Filesystem Repo Lifecycle | 🔲 Not Started |
| MVP-GIT-003 | Branch-Per-Task Workflow | 🔲 Not Started |
| MVP-GIT-004 | File Operations & Commit Attribution | 🔲 Not Started |
| MVP-GIT-005 | Fast-Forward Merge | 🔲 Not Started |
| MVP-GIT-006 | Auto-Rebase & Conflict Resolution | 🔲 Not Started |
| MVP-GIT-007 | History & Diff (UI Read Access) | 🔲 Not Started |
| MVP-GIT-008 | ArangoDB Storage Backend | 🔲 Not Started |
| MVP-GIT-009 | CodeValdCortex Integration | 🔲 Not Started |

---

## Execution Order

```
MVP-GIT-001 → MVP-GIT-002 → MVP-GIT-003 → MVP-GIT-004
                                ↓
                          MVP-GIT-005 → MVP-GIT-006
                                ↓
                          MVP-GIT-007
                                ↓
MVP-GIT-008 (parallel track)
                                ↓
                          MVP-GIT-009 (integration — last)
```

---

## Task Detail Files

| File | Tasks |
|---|---|
| [mvp-details/repo-management.md](mvp-details/repo-management.md) | MVP-GIT-001, MVP-GIT-002 |
| [mvp-details/branch-workflow.md](mvp-details/branch-workflow.md) | MVP-GIT-003, MVP-GIT-005, MVP-GIT-006 |
| [mvp-details/file-operations.md](mvp-details/file-operations.md) | MVP-GIT-004 |
| [mvp-details/history-and-diff.md](mvp-details/history-and-diff.md) | MVP-GIT-007 |
| [mvp-details/storage-backends.md](mvp-details/storage-backends.md) | MVP-GIT-008 |
| [mvp-details/integration.md](mvp-details/integration.md) | MVP-GIT-009 |
