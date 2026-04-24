// Command dev runs CodeValdGit locally against a local ArangoDB. Defaults
// differ from production: listens on :50052, talks to http://localhost:8529,
// and leaves CROSS_GRPC_ADDR empty so dev runs standalone (no Cross required).
// The Makefile's `make dev` target sources CodeValdGit/.env before exec so
// real passwords stay out of the source tree.
package main

import (
	"log"
	"os"

	"github.com/aosanya/CodeValdGit/internal/app"
	"github.com/aosanya/CodeValdGit/internal/config"
)

func main() {
	// Dev defaults — only applied when the env var is unset, so the shell or
	// .env can still override any of them.
	setDefault("GIT_GRPC_LISTEN_ADDR", ":50052")
	setDefault("GIT_ARANGO_ENDPOINT", "http://localhost:8529")

	log.Println("codevaldgit[dev]: starting with local-dev defaults")
	if err := app.Run(config.Load()); err != nil {
		log.Fatalf("codevaldgit[dev]: %v", err)
	}
}

func setDefault(key, val string) {
	if _, ok := os.LookupEnv(key); !ok {
		os.Setenv(key, val)
	}
}
