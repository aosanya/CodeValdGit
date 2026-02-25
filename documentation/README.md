# CodeValdGit — Documentation

## Overview

**CodeValdGit** is a Go library that provides Git-based artifact management for [CodeValdCortex](../CodeValdCortex/README.md) — the enterprise multi-agent AI orchestration platform.

It replaces the custom hand-rolled Git engine (`internal/git/`) in CodeValdCortex with a proper Git implementation backed by [go-git](https://github.com/go-git/go-git).

---

## Documentation Index

| Document | Description |
|---|---|
| [requirements.md](requirements.md) | What the library must do — scope, constraints, open questions |
| [architecture.md](architecture.md) | Design decisions, repo structure, branching model |

---

## Quick Summary

- **Language**: Go
- **Core dependency**: [go-git](https://github.com/go-git/go-git)
- **Consumer**: CodeValdCortex (replaces `internal/git/`)
- **Unit of repo**: 1 Git repository per Agency
- **Branching model**: Agents always work on `task/{task-id}` branches; auto-merged to `main` on task completion
- **Artifact types**: Any file — code, Markdown, YAML configs, reports, etc.
