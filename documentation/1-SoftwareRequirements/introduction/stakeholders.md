# Stakeholders

## Primary Consumer

| Stakeholder | Role | How They Use CodeValdGit |
|---|---|---|
| **CodeValdCross** | Primary and only consumer | Imports `github.com/aosanya/CodeValdGit` as a Go module; calls `RepoManager` and `Repo` at Agency and Task lifecycle points |

---

## CodeValdCross Integration Points

CodeValdCross calls CodeValdGit at these lifecycle events:

| Event | CodeValdGit Call |
|---|---|
| Agency created | `RepoManager.InitRepo(agencyID)` |
| Agency deleted | `RepoManager.DeleteRepo(agencyID)` |
| Task started | `Repo.CreateBranch(taskID)` |
| Agent writes output | `Repo.WriteFile(taskID, path, content, ...)` |
| Task completed | `Repo.MergeBranch(taskID)` → `Repo.DeleteBranch(taskID)` |
| UI file browser | `Repo.ListDirectory(ref, path)` |
| UI file view | `Repo.ReadFile(ref, path)` |
| UI history view | `Repo.Log(ref, path)` |

---

## Secondary Stakeholders

| Stakeholder | Interest |
|---|---|
| **Platform operators** | Need `PurgeRepo` for permanent data removal; need archive path configuration |
| **AI agents (indirect)** | Produce artifacts via CodeValdCross — their output is what gets committed; affected by merge conflict routing |
| **End users (indirect)** | View artifact history and diffs through the CodeValdCross UI — powered by `Log` and `Diff` operations |

---

## Library Maintainers

The library is maintained as part of the **CodeVald** platform (CodeValdCross, CodeValdGit, CodeValdWork). Development follows:
- Trunk-based development with short-lived feature branches (`feature/GIT-XXX_description`)
- Pure Go — no `git` binary dependency
- go-git v5 as the sole Git engine dependency
