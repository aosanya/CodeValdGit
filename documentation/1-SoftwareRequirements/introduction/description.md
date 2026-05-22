**CodeValdGit** is an embedded Git engine for AI-driven development workflows, built in Go. It gives each AI agency its own isolated repository and enforces a **branch-per-task** model — agents always work on dedicated branches and never commit directly to main. The library handles the full lifecycle: repository creation, file reads/writes, auto-rebase with conflict detection, commit history, and diffs. It supports both filesystem and ArangoDB storage backends, making it container-friendly without requiring mounted volumes.

Part of the **CodeVald** platform — infrastructure for AI agents that write real code, on real branches, with real version control.

GitHub: https://github.com/aosanya/CodeValdGit
