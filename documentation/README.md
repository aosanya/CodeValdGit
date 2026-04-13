# CodeValdGit — Documentation

## Overview

**CodeValdGit** is a Go gRPC microservice that provides Git-based artifact management
for the CodeVald platform. It is accessible to other platform services via CodeValdCross’s HTTP proxy.

It replaces the custom hand-rolled Git engine (`internal/git/`) with a proper Git implementation backed by [go-git](https://github.com/go-git/go-git).

---

## Documentation Index

| Document | Description |
|---|---|
| [1-SoftwareRequirements/](1-SoftwareRequirements/README.md) | What the library must do — scope, FR, NFR, introduction |
| [2-SoftwareDesignAndArchitecture/](2-SoftwareDesignAndArchitecture/README.md) | Design decisions, storage backends, branching model, API draft |
| [3-SofwareDevelopment/](3-SofwareDevelopment/README.md) | MVP task list, implementation details per topic |
| [4-QA/](4-QA/README.md) | Testing strategy, acceptance criteria, QA standards |

### Key Files

| File | Description |
|---|---|
| [1-SoftwareRequirements/requirements.md](1-SoftwareRequirements/requirements.md) | Functional requirements (FR-001–FR-008), NFR, resolved open questions |
| [2-SoftwareDesignAndArchitecture/architecture.md](2-SoftwareDesignAndArchitecture/architecture.md) | Core design decisions, repo structure, branching model, API interfaces |
| [3-SofwareDevelopment/mvp.md](3-SofwareDevelopment/mvp.md) | MVP task list and status |
| [3-SofwareDevelopment/mvp-details/](3-SofwareDevelopment/mvp-details/README.md) | Per-topic task specifications |

---

## Quick Summary

- **Language**: Go
- **Core dependency**: [go-git](https://github.com/go-git/go-git)
- **Consumer**: Platform services via CodeValdCross HTTP proxy
- **Unit of repo**: 1 Git repository per Agency
- **Branching model**: Agents always work on `task/{task-id}` branches; auto-merged to `main` on task completion
- **Artifact types**: Any file — code, Markdown, YAML configs, reports, etc.
