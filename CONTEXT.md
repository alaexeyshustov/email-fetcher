# email-fetcher — Domain Glossary

## Graceful shutdown
On SIGTERM, the server calls `grpc.Server.GracefulStop()` and waits up to `SHUTDOWN_TIMEOUT` (default 30s) for active RPCs to complete before force-closing. Prevents mid-stream `ListEmails` / `SearchEmails` RPCs from being hard-reset during rolling deploys. Timeout is configurable via env var (`SHUTDOWN_TIMEOUT=30s`).

## bufconn test
An in-process gRPC integration test using `google.golang.org/grpc/test/bufconn`. A fake `Provider` is injected into `EmailServer`; the full RPC path runs without a real network. Used to test fan-out merging, partial failure, composite cursor round-trips, and `UNAUTHENTICATED` propagation.

## Fan-out
Concurrent dispatch of a single logical request to multiple email providers (Gmail, Yahoo Mail) using goroutines. The Go service owns all parallelism and synchronisation — Rails issues one gRPC call and receives one merged stream. Fan-out applies only to `ListEmails` and `SearchEmails`; all other RPCs target a single provider.

## Label (Gmail) vs Folder (Yahoo IMAP)
Gmail labels are many-to-many: a message can carry multiple labels simultaneously. IMAP folders are one-to-many: a message lives in exactly one folder. `ModifyLabels` presents a uniform interface but the guarantee differs — Gmail is atomic; Yahoo IMAP is COPY+EXPUNGE (best-effort, not atomic). The message UID changes after a Yahoo `ModifyLabels`, so callers must re-fetch. Rails should treat Yahoo label operations as best-effort.

## Yahoo Mail adapter
The Yahoo Mail provider adapter uses IMAP (`github.com/emersion/go-imap`) rather than Yahoo's REST API. OAuth2 authentication is performed via SASL XOAUTH2 using the `access_token` from `ProviderCredentials`. IMAP covers all 7 RPCs: LIST+FETCH for `ListEmails`, FETCH by UID for `GetEmail`, SEARCH for `SearchEmails`, LIST folders for `GetLabels`, STATUS for `GetUnreadCount`, STORE/COPY for `ModifyLabels`, FETCH body parts for `GetAttachmentContent`. `thread_id` is synthesised from `In-Reply-To` / `References` headers — best-effort, not guaranteed stable.

## ProviderCredentials
A pair of `(provider enum, access_token string)` passed by Rails in every request. The service is stateless — tokens are never stored. For fan-out RPCs (`ListEmails`, `SearchEmails`) the request carries `repeated ProviderCredentials`; all other RPCs carry a single `ProviderCredentials`.

## Email
The core domain object streamed in `ListEmails` and `SearchEmails` responses. Carries a `provider` field (enum) so Rails can attribute each message to the correct provider — required for fan-out streams where Gmail and Yahoo results are interleaved. The `(provider, id)` pair is the unique key for subsequent `GetEmail` and `ModifyLabels` calls.

## Composite cursor
An opaque `next_page_token` string (base64-encoded JSON) that encodes per-provider pagination state, e.g. `{"gmail":"tok_abc","yahoo":"tok_xyz"}`. Rails round-trips the token without inspecting it. Used by `ListEmails` and `SearchEmails` to resume a fan-out page.

## Auth failure
An `UNAUTHENTICATED` gRPC status returned when a provider rejects the access token (expired or invalid). In fan-out context, treated as a partial failure — healthy providers continue streaming; the auth failure surfaces in trailing metadata (`x-failed-providers`, `x-provider-errors`). Rails pattern-matches on `UNAUTHENTICATED` to trigger OAuth token refresh.

## Partial failure
When one provider fails during fan-out, the service continues streaming results from the healthy provider(s) and surfaces the failure in gRPC trailing metadata (`x-failed-providers`, `x-provider-errors`). The stream is not aborted.
