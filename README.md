# email-fetcher

A stateless Go gRPC microservice that fetches email from Gmail and Yahoo Mail. Designed to run alongside a Rails application via a `Procfile` entry, replacing scattered per-provider fetching logic with a single typed service boundary.

## Overview

- **gRPC server** exposing 7 RPCs (list, get, search, labels, unread count, modify labels, attachments)
- **Fan-out** — concurrent dispatch to multiple providers in a single call; partial failures surface in trailing metadata rather than aborting the stream
- **Two provider adapters** — Gmail via the REST API (`google.golang.org/api/gmail/v1`) and Yahoo Mail via IMAP (`go-imap`) with OAuth2/SASL XOAUTH2
- **Stateless** — Rails owns OAuth tokens and passes an `access_token` per request; nothing is stored in the service
- **Server-streaming** for `ListEmails` and `SearchEmails` with cursor-based pagination

## Prerequisites

- Go 1.22+
- `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` (only needed to regenerate protobuf; generated code is already committed under `gen/`)

## Building

```sh
make build                  # ./bin/email-fetcher (host platform)
make build-darwin-amd64     # ./bin/email-fetcher-darwin-amd64 (explicit cross-compile)
```

The binary has no runtime dependencies — no shared libraries, no config files required.

## Running

```sh
./bin/email-fetcher
```

Listens on `:50051` by default. The `Procfile` entry for running alongside Rails:

```
email_fetcher: ./bin/email-fetcher
```

### Environment variables

| Variable           | Default | Description                                                      |
|--------------------|---------|------------------------------------------------------------------|
| `GRPC_PORT`        | `50051` | Port the gRPC server listens on                                  |
| `LOG_LEVEL`        | `info`  | Logging verbosity (`debug`, `info`, `warn`, `error`)             |
| `ENV`              | `development` | Runtime environment                                        |
| `SHUTDOWN_TIMEOUT` | `30s`   | Grace period for in-flight RPCs on SIGTERM (`30s`, `1m`, etc.)   |

On SIGTERM the server calls `GracefulStop()` and waits up to `SHUTDOWN_TIMEOUT` before force-closing any remaining connections.

## Testing

```sh
make test     # go test -race -count=1 ./...
make vet      # go vet ./...
```

Three test layers:

- **Unit tests** — pure logic (fanout merge/cursor, config parsing)
- **Adapter tests** — each provider adapter tested against deterministic fakes (`httptest` server for Gmail, in-process IMAP server for Yahoo)
- **Integration tests** — full RPC path in-process via `bufconn` with a fake `Provider` injected; covers fan-out, partial failure, cursor round-trips, and `UNAUTHENTICATED` propagation

## Regenerating protobuf

```sh
make proto
```

Generated Go code under `gen/go/email/v1/` is committed, so `go build ./...` works without this step.

## Project structure

```
email-fetcher/
├── cmd/server/          # Entry point — wires config and starts gRPC server
├── internal/
│   ├── server/          # gRPC handler implementations
│   ├── provider/        # Provider interface, Gmail adapter, Yahoo adapter
│   ├── fanout/          # Concurrent multi-provider merge
│   └── config/          # Env-var configuration
├── proto/email/v1/      # Canonical protobuf source
├── gen/go/email/v1/     # Generated Go bindings (committed)
├── Makefile
└── Procfile
```

## gRPC API

| RPC                    | Type             | Description                                                    |
|------------------------|------------------|----------------------------------------------------------------|
| `ListEmails`           | Server-streaming | Stream email metadata/full messages; final message carries cursor |
| `GetEmail`             | Unary            | Fetch a single email by `(provider, id)`                       |
| `SearchEmails`         | Server-streaming | Stream search results; cursor-paginated                        |
| `GetLabels`            | Unary            | All labels/folders for a mailbox                               |
| `GetUnreadCount`       | Unary            | Unread message count, optionally scoped to a label             |
| `ModifyLabels`         | Unary            | Add/remove labels in a single call                             |
| `GetAttachmentContent` | Unary            | Raw attachment bytes by message and attachment ID              |

Full IDL: `proto/email/v1/email.proto`
