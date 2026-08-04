# API Media OSS Persistence And Direct Delivery Design

## Status

- Date: 2026-08-04
- Scope: API-key routes under `/v1/images/*` and `/v1/videos/*`
- State: implemented on `codex/api-media-oss`, awaiting commit and rollout
- Production changes: none; all new rollout flags default to disabled

## Executive Decision

Keep the existing API submission, polling, and content routes, but persist the
highest-value API results into the already configured private Hong Kong OSS
bucket before declaring them ready for durable delivery.

The recommended order is:

1. Persist every API video.
2. Persist account-gated API images, initially ChatGPT images.
3. Optionally normalize publicly accessible provider image URLs into OSS later.
4. Keep explicitly requested `b64_json` image responses inline and out of OSS.

For an OSS-backed result, the API returns or retains a stable application URL:

```text
https://lunixai.xyz/v1/videos/<event-id>/content
https://lunixai.xyz/v1/images/<event-id>/content
```

The content route authenticates the API key, verifies event ownership, and
responds with HTTP 307 to a short-lived signed URL on `media.lunixai.xyz`. The
client then downloads the bytes from OSS, including native Range/206 support.
The signed URL itself is never stored in the database or application response
history.

## Why This Change Is Worth Doing

The current API video path downloads the same upstream artifact through the
Bangkok backend on every client request. It has no Range forwarding, so seeking
or resuming a video can repeat large transfers. Some artifacts also stop being
readable when the original provider account disappears or its URL expires.

OSS persistence changes the data path from repeated relay to one-time ingest:

```text
Current:
provider -> Bangkok backend -> API client
                         repeated for every content request

Target:
provider -> Bangkok backend -> private Hong Kong OSS
                                      |
API client -> authenticated content route -> signed 307 -> OSS bytes
```

This provides the largest benefit for video because videos are large, clients
seek or resume them, and they are frequently downloaded more than once. It also
benefits account-gated images because the stored object no longer depends on the
original upstream account remaining active.

This change does not improve provider generation latency. The Bangkok backend
still submits and polls the provider, then relays the completed artifact to OSS
once. It removes Bangkok from subsequent client delivery and makes that delivery
stable, resumable, and independent of the provider URL lifetime.

## Goals

- Persist selected API image and video results in private OSS.
- Keep API-key authentication and per-user ownership checks in the application.
- Make large client downloads use OSS bandwidth and native Range support.
- Avoid buffering complete videos in the 2 GiB backend memory.
- Preserve existing routes and existing OpenAI-compatible response shapes.
- Keep legacy events containing upstream URLs readable.
- Separate provider completion from storage retries so an OSS failure never
  resubmits a generation task.
- Permit staged rollout and immediate feature rollback through configuration.
- Define retention, security, observability, and acceptance criteria before
  implementation.

## Non-Goals

- Replacing provider submission, polling, pricing, charging, or account routing.
- Solving duplicate API submissions or adding an API idempotency contract.
- Making the current in-process API video goroutine restart-durable.
- Uploading directly from a provider into OSS without the backend seeing bytes.
- Exposing OSS write credentials or presigned PUT URLs to API clients.
- Making the OSS bucket public.
- Migrating historical API events into OSS.
- Persisting `b64_json` responses solely for storage normalization.

API request idempotency and durable provider jobs are important follow-up work.
OSS persistence does not solve either concern. A backend restart can still lose
the current `runVideoJob` goroutine, and a retried API submission can still
create a second event unless a separate idempotency design is implemented.

## Current API Flow

### Authentication And Routing

Every `/v1` route authenticates a per-user API key. The raw key is hashed and
matched to an active user; UI session cookies are not used. The service then
performs model validation, banned-word checks, pricing, credit debit, event
creation, user/account concurrency checks, provider selection, and provider
failover. An active upstream account declaring a model ID can override the
model's native provider.

### Images

`POST /v1/images/generations` and `/v1/images/edits` currently set
`source="v1"`, which implies `noStore=true`.

- A provider public or signed URL is returned directly when available.
- A ChatGPT account-gated URL is stored in `EventLog.File`; the response points
  to `/v1/images/<event-id>/content`.
- That content route reopens the URL using the original ChatGPT account and
  proxies all bytes through Bangkok.
- Providers that return no URL fall back to inline `b64_json`.
- The JSON handler parses `response_format` but does not pass it to the service;
  the multipart edits handler does not currently propagate it either.
- API image results do not enter the configured storage driver.

### Videos

`POST /v1/videos` validates, charges, creates a pending event, and returns a
queued video object. `runVideoJob` executes as an in-process goroutine, polls the
provider, and records only the provider URL in `EventLog.File`.

`GET /v1/videos/<event-id>/content` fetches that provider URL again and proxies
the complete response through Bangkok. The current handler always sends 200 and
does not forward the client's Range header or the upstream Content-Range. Grok
downloads require the original provider account. A custom upstream content URL
may also require Bearer authentication, while the generic proxy currently sends
a plain GET.

## Target Data Model

No historical migration is required. Reuse the existing event fields with the
following explicit meanings for new persisted API media:

| Field | New persisted event | Legacy event |
|---|---|---|
| `EventLog.File` | OSS object key | Provider `http(s)` URL or empty |
| `EventLog.UpstreamResultURL` | Original provider artifact URL | Usually empty |
| `EventLog.Source` | `v1` | `v1` |
| `EventLog.Status` | `success` only after upload or explicit fallback | Existing semantics |

Classify `File` values structurally:

- Absolute `http://` or `https://` values are legacy upstream URLs.
- Valid relative object keys are storage-backed results.
- Empty values are not ready, expired, or unavailable depending on event state.
- Never classify by a substring or by the current feature flags.

New object keys are deterministic and owner-scoped:

```text
api/<stable-user-id>/images/<event-id>.<validated-extension>
api/<stable-user-id>/videos/<event-id>.<validated-extension>
```

Use the immutable user ID, not a username or email. Do not include prompts,
provider URLs, account IDs, API keys, or signed query parameters in object keys
or OSS metadata.

Repository writes must make the transition explicit:

1. Store `UpstreamResultURL` as soon as the provider result is known.
2. Ingest and upload the artifact without changing the event to `success`.
3. Atomically set `File=<object-key>`, clear the error, and mark success.
4. If storage fallback is used, atomically set `File=<upstream-url>` and success.

A dedicated repository method should guard the terminal transition so success
counters are incremented once even when the storage stage is retried.

## Image Response Semantics

Add `ResponseFormat` to `V1ImageRequest` and propagate it from JSON and multipart
handlers. Normalize only these values:

- Empty or `url`: URL behavior.
- `b64_json`: inline behavior.
- Any other value: HTTP 400 before charge and provider submission.

### `b64_json`

The backend obtains the result bytes, base64-encodes them, and returns the
existing `data[].b64_json` shape. It does not upload the result to OSS. If the
provider supplies only a URL, the provider-aware fetcher downloads it once so
the requested response format can be honored.

### URL Mode

Persistence is controlled by `API_IMAGE_PERSISTENCE`:

| Mode | Account-gated URL | Public provider URL | Byte-only result |
|---|---|---|---|
| `off` | Current authenticated proxy | Return provider URL | Return base64 fallback |
| `gated` | Persist, return stable content URL | Return provider URL | Return base64 fallback |
| `all` | Persist, return stable content URL | Persist, return stable content URL | Persist, return stable content URL |

Production should start with `gated`. That changes only results already using
the authenticated Lunixai content endpoint, so it does not unexpectedly turn a
previously public provider URL into an API-key-protected URL. The `all` mode is
an optional later policy because some OpenAI-compatible clients may fetch image
URLs without forwarding their Authorization header.

For a persisted image, the normal successful response still contains
`data[0].url`; its value is the stable Lunixai content URL. The response must not
contain the temporary OSS signed URL.

## Video Ingestion Flow

All new API videos should use the following state sequence when
`API_VIDEO_PERSISTENCE=true`:

```text
queued
  -> provider submission and polling
  -> provider result URL recorded
  -> artifact download to bounded temporary file
  -> seekable upload to private OSS
  -> EventLog.File set to object key
  -> success
```

The provider stage returns a source descriptor, not merely untrusted bytes:

```text
provider
account_id
source_url
content_type hint
authenticated fetch strategy
```

The service then uses the same provider/account that created the artifact to
fetch it. ChatGPT and Grok use their existing authenticated asset openers.
Custom/upstream adapters need an artifact-fetch method capable of applying the
configured API key when their content endpoint requires Bearer authentication.
Public signed URLs continue through an authenticated-aware generic fetcher that
does not leak provider credentials to a different host.

### Bounded Temporary-File Spool

Do not read videos into `[]byte`. The ingestion stage must:

1. Acquire a small process-wide media-ingest semaphore.
2. Create a randomly named temporary file with owner-only permissions.
3. Check the configured spool filesystem has enough free space.
4. Reject a declared Content-Length larger than `API_MEDIA_MAX_BYTES`.
5. For unknown lengths, copy through a limiting reader of `max+1` bytes.
6. Detect and validate the actual media type from the first bytes.
7. Rewind the file and upload it through a seekable storage API.
8. Record byte count, content type, and checksum in structured diagnostics.
9. Close and delete the temporary file on every exit path.

Extend the storage wrapper with a seekable/streaming upload operation while
keeping the current `Put([]byte)` method for small images and thumbnails. The OSS
driver can use multipart upload for large seekable files, SDK retry behavior,
and default CRC64 validation. The RustFS implementation must provide equivalent
streaming semantics so local development and rollback remain supported.

The temporary file is intentional. Provider HTTP bodies are not seekable, while
reliable multipart retries require a seekable source. Disk spooling bounds RAM
usage and lets an upload retry without downloading or generating the artifact
again.

## Provider Artifact Fetching And SSRF Controls

Provider result URLs are external input and must not be passed to an unrestricted
generic HTTP client. The fetch layer must enforce all of the following:

- Only `https` except explicitly configured local-development endpoints.
- Provider-specific host allowlists or same-origin validation against the
  configured upstream base URL.
- Revalidation of every redirect target, with a small redirect limit.
- Rejection of loopback, link-local, private, multicast, and metadata-service
  destinations after DNS resolution.
- No forwarding of Authorization, cookies, or provider credentials across a
  host change unless that provider explicitly defines the redirect as trusted.
- Bounded connect, response-header, idle, and whole-ingest timeouts.
- Maximum response size enforcement regardless of Content-Length.
- Media-type validation before the object is accepted as complete.

Provider-specific openers should return the HTTP status and relevant headers,
not only an `io.ReadCloser`, so ingestion and legacy Range proxying can preserve
correct response semantics.

## Storage Retry And Failure Semantics

Provider submission and storage are separate stages. Once a provider task has
been accepted or a result URL has been obtained, no storage error may re-enter
account failover or call provider generation again.

Recommended bounded retry policy:

- Retry provider artifact download only for clearly transient transport or 5xx
  failures while the source URL remains valid.
- Retry OSS upload from the same temporary file with exponential backoff.
- Use at most three attempts per stage and respect the parent job deadline.
- Never retry 401/403 as another provider submission.
- Never change or double-charge the event during storage retry.

After retry exhaustion:

- If the upstream URL remains usable by the current legacy content path, mark
  the event successful with `File=<upstream-url>` and emit a fallback metric.
- If an image has bytes and its requested format is `b64_json`, return inline.
- If there is no deliverable fallback, mark failed and execute the existing
  single-refund transition.

This fallback keeps generation available during OSS incidents. It is a rollout
safety mechanism, not the steady-state path.

## Content Endpoint Behavior

Keep these routes stable:

```text
GET /v1/images/<event-id>/content
GET /v1/videos/<event-id>/content
```

The request flow is:

1. Authenticate the API key.
2. Load the event and require the expected media kind.
3. Require `event.UserID == principal.User.ID`.
4. Return the same not-found response for nonexistent and foreign events.
5. Require a successful, non-expired artifact.
6. Classify `EventLog.File` as object key or legacy URL.

For an OSS object key with direct delivery enabled:

1. Verify the object exists.
2. Generate a short-lived signed GET URL using the configured HTTPS CNAME.
3. Respond with HTTP 307 and `Location=<signed-url>`.
4. Set `Cache-Control: private, no-store` on the redirect response.
5. Do not log the Location value.

HTTP 307 preserves GET semantics and allows the client to send a Range request
to OSS, which then returns native 206, Content-Range, ETag, and Accept-Ranges.
Clients can call the stable content route again after a signed URL expires.

For an object key with API direct delivery disabled, proxy from the selected
storage driver. Forward the incoming Range header and preserve these response
fields where present:

- Status 200 or 206
- Content-Type
- Content-Length
- Content-Range
- Accept-Ranges
- ETag
- Last-Modified
- Content-Disposition

For a legacy upstream URL, retain provider-aware proxying and apply the same
Range/header rules. This fixes current video seek/resume behavior even for old
events. Reads of existing object keys must continue regardless of whether new
persistence is enabled; feature flags govern writes, not readability.

## Configuration

Keep API rollout controls independent from the existing website OSS controls:

```dotenv
# Existing shared storage selection remains authoritative.
STORAGE_DRIVER=oss

# New API persistence controls.
API_VIDEO_PERSISTENCE=false
API_IMAGE_PERSISTENCE=off
API_MEDIA_DIRECT_DELIVERY=false
API_MEDIA_RETENTION_DAYS=30
API_MEDIA_MAX_BYTES=1073741824
API_MEDIA_INGEST_CONCURRENCY=2
```

`API_IMAGE_PERSISTENCE` accepts only `off`, `gated`, or `all`; an invalid value
must fail startup. `API_MEDIA_MAX_BYTES` and ingest concurrency must have safe
upper bounds. The spool directory can default to the operating system temporary
directory; an optional `API_MEDIA_SPOOL_DIR` override may be added for hosts
with a dedicated volume.

Effective signed redirect delivery requires both the shared storage capability
and API rollout flag:

```text
storage driver supports signed GET
AND OSS_DIRECT_DELIVERY=true
AND API_MEDIA_DIRECT_DELIVERY=true
```

This permits the website and API paths to be rolled out independently. Turning
off API persistence stops only new API writes. It must not make existing API OSS
objects unreadable.

## Retention

Use the `api/` key prefix to isolate lifecycle policy from website gallery
objects. Application retention should:

1. Select `source=v1` events whose object-key-backed artifacts are older than
   `API_MEDIA_RETENTION_DAYS`.
2. Delete the OSS object.
3. Clear `EventLog.File` only after confirmed deletion or confirmed not-found.
4. Retain the event for accounting according to the independent log-retention
   policy.
5. Make subsequent content requests return an explicit expired/not-found API
   response without exposing whether another user's object exists.

Configure an OSS lifecycle rule for `api/` as a safety net several days longer
than application retention, for example 37 days when application retention is
30 days. The OSS rule must never be shorter than the application window. Failed
multipart uploads should also have a short abort lifecycle rule.

Do not persist indefinitely useful provider-signed URLs in client-visible logs.
`UpstreamResultURL` is an internal recovery/diagnostic field and should be
cleared when its operational value expires if policy requires it.

## Security Requirements

- Keep the Bucket private.
- Use a dedicated RAM identity restricted to the required Bucket/prefix actions.
- Keep AccessKey values only in the server `.env` or a secret manager.
- Authorize API key and ownership before OSS Head, Get, or Presign operations.
- Return not-found for cross-user access to avoid event enumeration.
- Treat signed URLs as bearer credentials and keep their TTL short.
- Never store, log, or return a signed URL outside the immediate 307 response.
- Redact query strings from all provider and OSS URL logs.
- Never log API keys, provider tokens, cookies, prompts, base64 payloads, or RAM
  credentials.
- Create spool files with owner-only permissions and remove them on all exits.
- Validate object keys and prevent `..`, absolute paths, encoded traversal, or
  cross-user prefixes.
- Preserve exact CORS origin `https://lunixai.xyz` for browser use; API clients
  do not rely on CORS, but the media domain still must not use wildcard origins
  with credentials.

## Observability

Add structured logs around state transitions with safe identifiers only:

```text
event_id, user_id, media_kind, provider, account_id,
stage, attempt, elapsed_ms, bytes, content_type, fallback_reason
```

Required stages include provider completion, source URL persistence, artifact
download start/finish, spool rejection, OSS upload start/retry/finish, event
completion, legacy fallback, storage proxy, and signed redirect.

Add counters/histograms for:

- API media ingest attempts, successes, failures, and fallbacks
- Artifact download bytes and duration by provider/kind
- OSS upload bytes and duration by kind
- Direct redirects and proxy deliveries
- 200/206 delivery outcomes
- Maximum-size and free-disk rejections
- Temporary files currently open and bytes currently spooled
- Objects expired by application retention

Logs and metric labels must never include full URLs or prompts. Record a URL
host and redacted path class only when necessary.

## Deployment Plan

### Phase 0: Preflight

- Confirm the current private OSS website path remains healthy.
- Confirm `media.lunixai.xyz` TLS, CORS, signed GET, and Range/206.
- Record database and `.env` backups.
- Confirm disk space for the configured maximum artifact and two concurrent
  spool files.

### Phase 1: Compatibility Release

Deploy code with all new API flags disabled. Verify current API image/video
behavior, legacy content proxying, charging, and provider routing are unchanged.
The read path must already understand both object keys and legacy URLs.

### Phase 2: Video Persistence In Proxy Mode

Set:

```dotenv
API_VIDEO_PERSISTENCE=true
API_IMAGE_PERSISTENCE=off
API_MEDIA_DIRECT_DELIVERY=false
```

Generate one small test video. Confirm the event stores an `api/...` key, the
source URL is retained internally, the object exists in OSS, and the content
endpoint proxies 200/206 without contacting the provider again.

### Phase 3: Video Direct Delivery

Set `API_MEDIA_DIRECT_DELIVERY=true`. Confirm the content endpoint returns 307,
the Location host is `media.lunixai.xyz`, the final full request returns 200,
and a separate Range request to the signed URL returns 206.

### Phase 4: Gated Image Persistence

Set `API_IMAGE_PERSISTENCE=gated`. Test a ChatGPT image in URL mode and confirm
it remains available after the provider URL/account is no longer used. Confirm
`b64_json` creates no OSS result object.

### Phase 5: Optional Public-Image Normalization

Only after client compatibility observation, consider
`API_IMAGE_PERSISTENCE=all`. This is optional; public provider URLs already keep
Bangkok out of client delivery and therefore have lower value.

## Rollback Plan

Use feature rollback before binary rollback:

1. Set `API_MEDIA_DIRECT_DELIVERY=false` to proxy existing OSS objects through
   the backend if signed delivery, CORS, TLS, or media-domain behavior regresses.
2. Set `API_VIDEO_PERSISTENCE=false` and/or `API_IMAGE_PERSISTENCE=off` to stop
   new API object writes. Existing object-key events remain readable.
3. Keep the existing private OSS configuration and objects during investigation.

Rolling back to a binary that predates object-key-aware API content routes would
break events created by the new release because that binary treats `File` as an
upstream URL. Therefore retain the compatibility release as the rollback image.
If a deeper binary rollback is unavoidable, first restore affected `File` values
from `UpstreamResultURL` in a backed-up transaction and verify each provider's
legacy content path. Do not delete OSS objects during rollback.

## Test Matrix

### Unit And Repository Tests

- `response_format` propagation and validation for JSON and multipart images.
- `off`, `gated`, and `all` image policy decisions.
- Object-key generation uses stable owner/event IDs and validated extensions.
- Legacy URL versus object-key classification.
- Atomic ready transition stores object key and upstream URL once.
- Storage retry reuses the same temporary file and never calls provider submit.
- Declared and streaming size limits reject at `max+1`.
- Temporary files are removed after success, timeout, cancellation, and error.
- Free-disk and ingest-concurrency guards behave deterministically.
- Auth and owner checks occur before Exists/Get/Presign.
- Foreign event IDs return the same result as nonexistent IDs.
- Object redirect emits 307 and private/no-store without logging Location.
- Proxy mode forwards Range and preserves 206 response headers.
- Legacy ChatGPT/Grok/custom content uses the original account credentials.
- Storage failure falls back only when a deliverable upstream path exists.
- `b64_json` never creates an API result object.
- Disabling new persistence does not disable reads of prior object keys.
- Retention deletes only owner-scoped `api/` objects and clears matching rows.

### Integration Tests

- Fake provider URL -> spool -> OSS-compatible store -> stable content endpoint.
- Unknown Content-Length larger than the limit is stopped without unbounded RAM.
- Redirect chains and private-network destinations are rejected.
- Multipart upload retry preserves checksum and produces one final object.
- Simultaneous video ingests respect the configured semaphore.
- Legacy public, ChatGPT, Grok, and authenticated custom-upstream fetch paths.

### Production Acceptance

1. New API video event becomes completed only after its OSS object exists.
2. `GET /v1/videos/<id>/content` with the owner's API key returns 307.
3. The redacted Location host is `media.lunixai.xyz`.
4. A direct request to that signed URL returns 200.
5. A separate request to the signed URL with `Range: bytes=0-1048575` returns
   206 and a valid Content-Range.
6. A second API user's key cannot read the first user's image or video event.
7. A legacy event containing an upstream URL still downloads successfully.
8. A gated image remains readable from OSS without reusing the provider account.
9. `response_format=b64_json` returns inline data and creates no result object.
10. OSS upload failure does not produce a second provider task or second debit.
11. Server memory remains bounded during concurrent large video ingestion.
12. Logs contain stage/latency/size diagnostics but no credentials or signed URLs.

When testing a 307 manually, do not blindly forward the Lunixai Authorization
header to the OSS host. First request the stable content route without following
redirects, extract the temporary Location locally, then request that signed URL
without the Lunixai API key.

## Expected Implementation Files

Primary changes:

- `backend/internal/config/config.go`
- `backend/internal/bootstrap/app.go`
- `backend/internal/storage/client.go`
- `backend/internal/storage/oss.go`
- `backend/internal/storage/rustfs.go`
- `backend/internal/http/handler/v1.go`
- `backend/internal/service/v1.go`
- new `backend/internal/service/api_media.go`
- `backend/internal/repo/event_repo.go`
- `backend/internal/adapter/adapter.go`
- provider clients that need authenticated artifact openers
- `backend/internal/service/maintenance.go`
- `backend/internal/service/app_settings.go` if API retention is exposed in admin
- `backend/.env.example`
- `docker-compose.yml`

Focused tests should be added next to storage, V1 handler/service, repository,
provider artifact fetching, and maintenance code. No frontend change is required
for API-only delivery because the API contract and routes remain server-side.

## Implementation Sequence

1. Add strict configuration parsing and read-compatible file classification.
2. Add streaming/seekable storage upload and storage proxy Range support.
3. Add provider-aware artifact fetchers with SSRF and credential boundaries.
4. Add the temporary-file ingestion service and structured diagnostics.
5. Persist API videos, including upstream URL and atomic ready transition.
6. Refactor content handlers for OSS 307, proxy fallback, and legacy Range.
7. Propagate image `response_format` and add gated-image persistence.
8. Add retention and lifecycle documentation.
9. Complete unit/integration verification with all flags disabled.
10. Roll out through the five deployment phases above.

The first implementation branch should stop after `gated` image support. Public
image normalization should be a later, separately approved change because it has
the highest client-compatibility risk and the smallest bandwidth benefit.
