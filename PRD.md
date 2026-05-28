# PRD: `email-fetcher`

**Author:** Aleksey Shustov ([@alaexeyshustov](https://github.com/alaexeyshustov))  
**Status:** In Progress  
**Module:** `github.com/alaexeyshustov/email-fetcher`

---

## 1. Overview / Problem Statement

`application_pipeline` is a Rails 8 application that fetches email from Gmail and Yahoo Mail as part of its core workflow. Over time, that fetching logic has become scattered across models, service objects, and background jobs — each provider has its own integration path, error handling is inconsistent, and concurrent fan-out across providers is bolted on rather than designed in.

`email-fetcher` replaces all of that with a single, focused Go microservice. It exposes a clean gRPC interface that Rails calls via a generated client, handles concurrent fan-out internally, and is entirely stateless — Rails remains the source of truth for OAuth tokens. The service runs as a single binary alongside Rails via a `Procfile` entry, keeping the deployment footprint minimal.

This is also a portfolio project. It is designed to demonstrate idiomatic Go: gRPC/protobuf API design, goroutine-based concurrency, server-streaming RPCs, provider abstraction via interfaces, and single-binary deployment.

---

## 2. Goals & Non-Goals

### Goals

- Replace scattered Rails email-fetching logic with a well-defined gRPC service boundary.
- Support Gmail and Yahoo Mail via a shared provider interface, with clean extension points for future providers.
- Implement concurrent fan-out across providers using goroutines, surfacing partial failures gracefully.
- Expose server-streaming RPCs for list and search operations, enabling Rails to process results incrementally.
- Keep the service fully stateless — no database, no token storage, no session state.
- Demonstrate Go competency: protobuf IDL design, gRPC streaming, interface-driven provider abstraction, concurrent fan-out, env-var configuration.

### Non-Goals

- OAuth token acquisition or refresh — Rails owns the token lifecycle and passes a valid `access_token` with each request.
- Email composition or sending — read/label operations only.
- Persistent storage or caching of any kind.
- A standalone UI or CLI client.
- Real-time push (WebSocket/bidirectional streaming) — deferred to V2.

---

## 3. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    application_pipeline                      │
│                      (Rails 8 app)                          │
│                                                             │
│  ┌──────────────┐    gRPC calls     ┌─────────────────────┐ │
│  │  Controllers │ ─────────────────▶│   grpc gem client   │ │
│  │  / Services  │                   └──────────┬──────────┘ │
│  └──────────────┘                              │            │
└───────────────────────────────────────────────┼────────────┘
                                                │ gRPC (proto/email/v1)
                                                ▼
┌──────────────────────────────────────────────────────────────┐
│                       email-fetcher                          │
│                    (Go gRPC microservice)                     │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                  internal/server                      │   │
│  │            (gRPC handler implementations)             │   │
│  └───────────────────────┬──────────────────────────────┘   │
│                          │                                   │
│  ┌───────────────────────▼──────────────────────────────┐   │
│  │                  internal/fanout                      │   │
│  │          (concurrent multi-provider merge)            │   │
│  └────────────────┬───────────────────┬─────────────────┘   │
│                   │                   │                      │
│  ┌────────────────▼──┐   ┌────────────▼──────────────────┐  │
│  │  internal/provider│   │     internal/provider         │  │
│  │   Gmail adapter   │   │     Yahoo Mail adapter        │  │
│  └────────────┬──────┘   └────────────┬──────────────────┘  │
└───────────────┼───────────────────────┼─────────────────────┘
                │                       │
                ▼                       ▼
        Gmail REST API          Yahoo Mail REST API
      (googleapis.com)         (mail.yahoo.com/ws)
```

Rails holds OAuth tokens and passes one `access_token` per provider per request. `email-fetcher` uses that token to authenticate directly against the provider API. No credentials are stored in the service.

---

## 4. gRPC API Surface

The full IDL lives in `proto/email/v1/email.proto`. Generated Go code is committed under `gen/go/email/v1/`.

| RPC | Type | Request | Response | Description |
|---|---|---|---|---|
| `ListEmails` | Server-streaming | `ListEmailsRequest` | `stream ListEmailsResponse` | Streams email metadata (or full messages) from one or more providers. Final message in stream carries a `PageInfo` cursor via `oneof payload`. |
| `GetEmail` | Unary | `GetEmailRequest` | `GetEmailResponse` | Fetches a single email by ID from the specified provider. Supports `EmailFormat` selection. |
| `SearchEmails` | Server-streaming | `SearchEmailsRequest` | `stream SearchEmailsResponse` | Streams search results matching a query string. Cursor-paginated. |
| `GetLabels` | Unary | `GetLabelsRequest` | `GetLabelsResponse` | Returns all labels (system and user-defined) for a mailbox. |
| `GetUnreadCount` | Unary | `GetUnreadCountRequest` | `GetUnreadCountResponse` | Returns the count of unread messages, optionally scoped to a label. |
| `ModifyLabels` | Unary | `ModifyLabelsRequest` | `ModifyLabelsResponse` | Atomically adds and/or removes labels on a message in a single provider call. |
| `GetAttachmentContent` | Unary | `GetAttachmentContentRequest` | `GetAttachmentContentResponse` | Fetches the raw byte content of a single attachment by message ID and attachment ID. **Note:** gRPC default max message size is 4MB — callers must configure a higher `max_recv_msg_size` on the Rails client for large attachments. |

### Key Proto Messages

| Message | Fields | Notes |
|---|---|---|
| `ProviderCredentials` | `provider` (enum), `access_token` (string) | Passed in every request; service is stateless. Fan-out RPCs (`ListEmails`, `SearchEmails`) use `repeated ProviderCredentials`; all others use a single field. |
| `Email` | `id`, `thread_id`, `subject`, `from` (Address), `to` (Address[]), `date` (Timestamp), `snippet`, `body`, `attachments` (Attachment[]), `label_ids`, `provider` (enum) | `body` omitted when `EmailFormat = METADATA`. `provider` field required so Rails can attribute results in fan-out streams. `(provider, id)` is the unique key. For Yahoo IMAP, `thread_id` is synthesised from `In-Reply-To` / `References` headers — best-effort, not guaranteed stable across fetches. |
| `Address` | `name`, `email` | Structured sender/recipient |
| `Attachment` | `id`, `filename`, `mime_type`, `size` | Metadata only — use `GetAttachmentContent` to fetch bytes |
| `AttachmentContent` | `content` (bytes), `mime_type` (string) | Returned by `GetAttachmentContent`. Callers must configure `max_recv_msg_size` > 4MB on the gRPC client for large attachments. |
| `Label` | `id`, `name`, `type` (LabelType enum) | `SYSTEM` vs `USER` label types |
| `PageInfo` | `next_page_token`, `result_count` | Final message in streaming responses. For fan-out streams, `next_page_token` is a composite opaque cursor encoding per-provider state (base64 JSON). |

### Enums

| Enum | Values |
|---|---|
| `Provider` | `GMAIL`, `YAHOO` |
| `EmailFormat` | `METADATA`, `FULL`, `MINIMAL` |
| `LabelType` | `SYSTEM`, `USER` |

---

## 5. Key Design Decisions

### Stateless Service / Token-Passing Model

The service stores no state. Rails acquires, stores, and refreshes OAuth tokens; `email-fetcher` receives a valid `access_token` in each request via a `ProviderCredentials` message in the request body. This keeps the Go service simple and makes horizontal scaling trivial. Using the request body (rather than gRPC metadata headers) simplifies the Rails `grpc` gem integration. Migrating to interceptor-based metadata extraction is a noted V2 improvement.

### `Provider` Enum in Every Request

Each request carries a `Provider` enum field (GMAIL, YAHOO). This allows the Rails caller to fan out across providers by issuing parallel gRPC calls, or to target a single provider explicitly. The Go service implements fan-out internally in `internal/fanout` when multiple credentials are passed. A single service binary handles all providers — no per-provider microservice proliferation.

### `EmailFormat` Enum

The `EmailFormat` enum (`METADATA`, `FULL`, `MINIMAL`) lets Rails request only what it needs. List views request `METADATA` (headers, snippet, no body) to minimize payload. Opening a message requests `FULL`. This avoids over-fetching and keeps streaming responses lean.

### Cursor-Based Pagination

All list and search operations use cursor-based pagination via a `page_token` string rather than offset/limit. This is the correct approach for live mailboxes where the result set can change between pages. Offset pagination would produce duplicates or gaps in a mailbox receiving new mail.

### `oneof payload` in Streaming Responses

Streaming response messages use a `oneof payload { Email email = 1; PageInfo page_info = 2; }` pattern. The service streams email messages, then sends a final `PageInfo` message carrying the next cursor. This avoids a separate unary call to retrieve pagination state and is backward-compatible — adding fields to `PageInfo` is a non-breaking change.

### Fan-Out Partial Failure Handling

When fanning out across multiple providers, a failure in one provider does not abort the stream. `email-fetcher` streams results from the healthy provider(s) and surfaces the error in gRPC trailing metadata (`x-failed-providers`, `x-provider-errors`). Rails can inspect trailing metadata and decide how to present the partial result to the user. This is preferable to failing the entire request when one of two providers is temporarily unavailable.

### Committed `gen/` Directory

Generated protobuf Go code under `gen/go/email/v1/` is committed to the repository. For a service binary (as opposed to a shared library), this is the correct approach: it makes the repo self-contained, eliminates a `protoc` dependency in CI, ensures the generated code is reviewable in pull requests, and makes `go build ./...` work with no setup steps.

### `ModifyLabels` (not `AddLabels` / `RemoveLabels`)

Label modification is exposed as a single atomic RPC that accepts both labels to add and labels to remove in one call. Splitting this into two RPCs would require two round trips for a common operation (e.g., marking a message read while archiving it) and would not map cleanly to Gmail's `messages.modify` API, which accepts both `addLabelIds` and `removeLabelIds` in a single call.

### Provider-Specific Label Semantics (Gmail vs Yahoo IMAP)

Gmail labels and IMAP folders have fundamentally different semantics. This is a known constraint — the `ModifyLabels` RPC interface is uniform, but the guarantee it provides differs by provider.

**Gmail:** Labels are many-to-many. A message can simultaneously carry `INBOX`, `IMPORTANT`, and `work`. `ModifyLabels` maps to a single atomic `messages.modify` call.

**Yahoo Mail (IMAP):** Folders are one-to-many. A message lives in exactly one folder at a time. `ModifyLabels` is implemented as COPY + EXPUNGE — the message is copied to the target folder and the original is expunged. This operation is **not atomic**: a crash between COPY and EXPUNGE leaves the message in both folders. Additionally, the message UID changes after the copy, so any UID the Rails caller cached is invalidated.

Rails callers should treat Yahoo label operations as best-effort and re-fetch message state after a `ModifyLabels` call targeting Yahoo. The `Email.id` field returned after a Yahoo `ModifyLabels` reflects the new UID.

---

## 6. Testing Strategy

Three layers, all required in V1.

### Unit tests
Pure logic with no network or provider calls. Covers:
- `internal/config` — env-var defaults and overrides
- `internal/fanout` — concurrent merge logic, partial failure handling, composite cursor encode/decode
- Any pure helper functions in provider adapters

### Provider adapter tests
Each adapter tested against deterministic fake responses:
- **Gmail adapter:** `net/http/httptest` fake server returning recorded Gmail API JSON
- **Yahoo adapter:** In-process fake IMAP server (via `go-imap`'s test utilities) returning fixture messages

These tests verify that each adapter correctly maps provider responses to the `Email` proto shape, handles auth errors (`UNAUTHENTICATED`), and maps IMAP folders to the `Label` domain type.

### gRPC integration tests
Full request/response path tested in-process using `google.golang.org/grpc/test/bufconn` (no real network). A fake `Provider` implementation is injected into `EmailServer`. Tests cover:
- Each RPC happy path (correct response shape)
- Fan-out across two fake providers (results merged, both providers' emails appear in stream)
- Partial failure (one fake provider errors, other continues, trailing metadata carries error)
- Composite cursor round-trip (cursor from page 1 resumes correctly on page 2)
- `UNAUTHENTICATED` propagation from adapter to gRPC caller

---

## 7. Project Structure

```
email-fetcher/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: wires config, registers gRPC server
├── internal/
│   ├── server/                  # gRPC handler implementations (EmailServiceServer)
│   ├── provider/                # Provider interface + Gmail adapter + Yahoo adapter
│   ├── fanout/                  # Concurrent multi-provider merge logic
│   └── config/                  # Env-var config (port, log level, etc.)
├── proto/
│   └── email/
│       └── v1/
│           └── email.proto      # Canonical source of truth for the API
├── gen/
│   └── go/
│       └── email/
│           └── v1/              # Generated Go code (committed to VCS)
├── Makefile                     # proto generation, build, lint, test targets
├── go.mod
├── go.sum
└── Procfile                     # email_fetcher: ./bin/email-fetcher
```

`internal/` enforces Go's package visibility rules — nothing outside this module imports provider adapters or fanout logic directly.

---

## 8. Tech Stack

| Component | Choice | Rationale |
|---|---|---|
| Language | Go 1.22+ | Concurrency primitives, single-binary deployment, strong gRPC ecosystem |
| RPC framework | gRPC (`google.golang.org/grpc`) | Typed contracts via protobuf, server-streaming, native Rails grpc gem support |
| IDL | Protocol Buffers v3 | Language-agnostic schema, backward-compatible evolution, efficient wire format |
| Code generation | `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` | Standard toolchain; run via `make proto`, output committed |
| Gmail integration | Google Gmail REST API v1 (`google.golang.org/api/gmail/v1`) | Official Go client, OAuth2 bearer token auth |
| Yahoo integration | IMAP via `github.com/emersion/go-imap` | Yahoo's REST API is undocumented; IMAP is more battle-tested. OAuth2 via SASL XOAUTH2 using the `access_token` from `ProviderCredentials`. `thread_id` synthesised from `References` / `In-Reply-To` headers. |
| Configuration | Environment variables via `os.Getenv` / `envconfig` | Twelve-factor; no config files to manage |
| Rails integration | `grpc` gem (Ruby gRPC client) | Generated from the same `.proto`; strongly typed |

---

## 9. Deployment

`email-fetcher` ships as a single statically-linked binary and runs as a process alongside Rails via a `Procfile`:

```
web:           bundle exec puma -C config/puma.rb
email_fetcher: ./bin/email-fetcher
```

The binary listens on a configurable port (default `50051`). Rails configures its gRPC client to connect to `localhost:50051`. No service discovery, sidecar, or container orchestration is required for the initial deployment.

**Build:**
```sh
make build   # produces ./bin/email-fetcher
```

**Environment variables:**

| Variable | Default | Description |
|---|---|---|
| `GRPC_PORT` | `50051` | Port the gRPC server listens on |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |
| `ENV` | `development` | Runtime environment |
| `SHUTDOWN_TIMEOUT` | `30s` | Grace period for in-flight RPCs on SIGTERM (Go `time.Duration` format, e.g. `30s`, `1m`) |

**Notes:**
- The binary has no runtime dependencies — no protoc, no shared libraries, no external config files.
- TLS termination is handled at the load balancer/reverse proxy layer for production. The service accepts plaintext gRPC internally.
- The `gen/` directory being committed means `go build ./...` works out of the box with no code generation step required in CI.
- On SIGTERM, the server calls `grpc.Server.GracefulStop()` — stops accepting new connections and waits for active RPCs to complete. After `SHUTDOWN_TIMEOUT`, any remaining RPCs are force-closed. This prevents indefinite hangs during rolling deploys while giving in-flight `ListEmails` / `SearchEmails` streams a reasonable window to drain.

---

## 10. Future Work / V2 Ideas

### Interceptor-Based Auth

Move `ProviderCredentials` out of each request message and into gRPC metadata headers, extracted by a server-side unary/stream interceptor. This cleans up the proto schema (credentials are a cross-cutting concern, not a business field) and aligns with standard gRPC auth patterns. The Rails client would set metadata on the call rather than embedding credentials in the request body.

### Bidirectional Streaming for Real-Time Push

Add a `WatchMailbox` bidirectional-streaming RPC that maintains a long-lived connection and pushes new message notifications as they arrive. Gmail supports push via Cloud Pub/Sub; Yahoo supports IMAP IDLE. This would allow `application_pipeline` to react to new mail without polling `ListEmails` on a timer.

### Yahoo IMAP vs REST

The current design targets Yahoo's REST API for consistency with Gmail. Yahoo's IMAP support is more mature and battle-tested than its REST layer. A V2 could evaluate switching the Yahoo adapter to IMAP (via Go's `emersion/go-imap` library) for improved reliability, especially for search and flag operations.

### Additional Providers

The `provider.Provider` interface and `fanout` package are designed for extension. Adding support for Microsoft Outlook (Graph API), IMAP-generic (for self-hosted mail), or Apple iCloud Mail would each require a new adapter in `internal/provider/` with no changes to the gRPC surface or fanout logic.

### Observability

Add structured logging (zerolog or slog), Prometheus metrics (request latency, provider error rates, stream message counts), and OpenTelemetry trace propagation. Expose a `/healthz` HTTP endpoint for load balancer health checks alongside the gRPC port.

### Integration Test Harness

Add a test provider adapter backed by fixture data and a gRPC test server. This would allow end-to-end tests of the Rails grpc client against a real (but deterministic) `email-fetcher` instance in CI.
