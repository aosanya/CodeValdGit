// Command codevaldgit is the binary entry point for the CodeValdGit gRPC
// service. It wires the configured storage backend and starts the server.
// No business logic lives here — only construction and startup.
//
// TODO(GIT-009): Rewrite with cmux wiring (gRPC + git Smart HTTP on one port),
// entitygraph-backed GitManager, schema seeding, and Cross registrar.
// This file is a placeholder stub until GIT-009 is implemented.
package main

import "log"

func main() {
	log.Println("codevaldgit: binary entry point — pending GIT-009 implementation")
}
