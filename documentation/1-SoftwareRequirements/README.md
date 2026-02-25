# 1 — Software Requirements

## Overview

This section captures everything **what** CodeValdGit must do and **why** — without prescribing how.

---

## Index

| Document | Description |
|---|---|
| [requirements.md](requirements.md) | Functional requirements (FR-001–FR-008), non-functional requirements, scope, and resolved open questions |
| [introduction/problem-definition.md](introduction/problem-definition.md) | Problem statement and motivation for the library |
| [introduction/high-level-features.md](introduction/high-level-features.md) | High-level capability summary |
| [introduction/stakeholders.md](introduction/stakeholders.md) | Consumers and stakeholders of the library |

---

## Summary

CodeValdGit is a **Go library** that provides Git-based artifact versioning for [CodeValdCortex](../../CodeValdCortex/README.md). It replaces the hand-rolled Git engine (`internal/git/`) with proper Git semantics backed by [go-git](https://github.com/go-git/go-git).

### Core Requirements at a Glance

| FR | Requirement |
|---|---|
| FR-001 | One Git repository per Agency |
| FR-002 | Any file type (text and binary) |
| FR-003 | Branch-per-task workflow — agents never commit to `main` |
| FR-004 | Commit attribution (agent ID + message) |
| FR-005 | File operations (read/write/delete/list at any ref) |
| FR-006 | Merge conflict resolution via auto-rebase; structured error returned |
| FR-007 | Repository archiving on Agency deletion (never hard-delete immediately) |
| FR-008 | History and diff read access for the CodeValdCortex UI |
