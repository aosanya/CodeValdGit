# gRPC Microservice Integration

> CodeValdGit is a standalone gRPC microservice — not a Go library import.
> CodeValdCross communicates with it over gRPC (default port `50051`).

## Tasks

| Task | File | Description |
|---|---|---|
| MVP-GIT-009 | [grpc-proto.md](grpc-proto.md) | Proto service definition, message types, buf codegen |
| MVP-GIT-010 | [grpc-server.md](grpc-server.md) | Go server implementation, error mapping, Dockerfile |

## Why gRPC?

CodeValdGit runs as an independent process — independently scalable, isolated
from CodeValdCross failures, and usable by any gRPC client language.

See [grpc-proto.md](grpc-proto.md) for the full rationale table.
