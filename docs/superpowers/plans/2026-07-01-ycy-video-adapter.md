# YCY Video Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dedicated YCY video adapter so the platform can create, poll, and stream YCY-backed video jobs through the existing unified `/v1/videos` API without introducing an account pool.

**Architecture:** Keep `image2api` as the mediation layer. YCY will be modeled as a single upstream video source with its own client and its own branch in the video execution path. The existing OpenAI-style video API stays stable; only the internal provider wiring changes. No database schema change is required for the first pass unless persistence of upstream task metadata becomes necessary later.

**Tech Stack:** Go, Gin, GORM, existing service/repo/provider patterns, environment-based config.

---

### Task 1: Add a YCY provider client

**Files:**
- Create: `E:/GithubCloneProject/image2api/backend/internal/provider/ycy/client.go`

- [ ] **Step 1: Define the YCY client contract**

Create a dedicated client that exposes a small set of methods for the YCY lifecycle:

```go
type Client struct{}

func NewClient() *Client
func (c *Client) CreateVideo(ctx context.Context, baseURL, apiKey, model, prompt, size string, seconds int) (taskID string, err error)
func (c *Client) GetVideo(ctx context.Context, baseURL, apiKey, taskID string) (status string, contentURL string, err error)
func (c *Client) Download(ctx context.Context, url, apiKey string) ([]byte, string, error)
```

Keep the contract narrow: one method to create tasks, one to poll status, one to fetch content if the upstream does not already return bytes.

- [ ] **Step 2: Map YCY responses into normalized errors**

Normalize YCY responses into the same operational categories already used elsewhere in the backend:

```go
var (
    ErrAuth              = errors.New("ycy upstream auth failed")
    ErrQuotaExhausted    = errors.New("ycy upstream quota exhausted")
    ErrTemporaryUpstream = errors.New("ycy upstream temporary error")
)
```

Treat 401/403 as auth failure, 429 or quota wording as quota exhaustion, and network/timeouts as temporary upstream failure.

- [ ] **Step 3: Keep all YCY-specific parsing inside the adapter**

Parse YCY task IDs, status strings, and content URLs only inside `provider/ycy`. Do not leak YCY field names into service code. The service layer should only receive normalized `(taskID, status, contentURL)` values or normalized errors.

### Task 2: Wire YCY into the video service path

**Files:**
- Modify: `E:/GithubCloneProject/image2api/backend/internal/service/v1.go`

- [ ] **Step 1: Extend `V1Service` with a YCY dependency**

Add a `ycy *ycy.Client` field, import the new provider package, and extend `NewV1Service(...)` so YCY can be injected alongside the existing providers.

```go
type V1Service struct {
    // existing fields...
    ycy *ycy.Client
}

func NewV1Service(..., customClient *custom.Client, ycyClient *ycy.Client, store *storage.Client) *V1Service
```

- [ ] **Step 2: Route the async video worker through YCY**

In `runVideoJob(...)`, add a `case "ycy":` branch next to `adobe`, `runway`, `grok`, and `custom`. Reuse the same event lifecycle:

```go
switch s.effectiveProvider(genCtx, modelItem) {
case "adobe":
case "runway":
case "grok":
case "custom":
case "ycy":
    // call YCY adapter, capture final content URL, update event
}
```

The first pass should keep the same success path:
- create pending event
- call upstream
- store returned content URL on success
- let `/v1/videos/:id/content` proxy the final asset

- [ ] **Step 3: Add a YCY-specific helper method**

Add a small helper such as `generateYCYVideo(...)` that mirrors `generateCustomVideo(...)` but uses the YCY client and YCY credentials from config. Keep the helper responsible for:
- building the upstream request
- handling retries or polling
- returning `(bytes, url, error)` in the same shape as existing video helpers

- [ ] **Step 4: Keep the existing OpenAI-style API stable**

Do not change:
- `/v1/videos`
- `/v1/videos/:id`
- `/v1/videos/:id/content`

The new provider should be invisible to consumers of the public API.

### Task 3: Add config for the YCY upstream

**Files:**
- Modify: `E:/GithubCloneProject/image2api/backend/internal/config/config.go`
- Modify: `E:/GithubCloneProject/image2api/.env` if needed for local testing

- [ ] **Step 1: Add explicit YCY config fields**

Extend `Config` with only the settings this integration needs:

```go
YCYBaseURL string
YCYAPIKey  string
```

Read them from environment variables such as:
- `YCY_BASE_URL`
- `YCY_API_KEY`

- [ ] **Step 2: Keep the first pass single-upstream**

Do not add account-pool configuration, rotation, or provider management UI for YCY. This integration is a single upstream bridge, not a pooled credential system.

- [ ] **Step 3: Validate startup behavior with missing config**

If the YCY model is configured but the env vars are empty, fail the request clearly at runtime rather than silently falling back to another provider.

### Task 4: Inject YCY during bootstrap

**Files:**
- Modify: `E:/GithubCloneProject/image2api/backend/internal/bootstrap/app.go`

- [ ] **Step 1: Construct the YCY client**

Instantiate the new client next to the other providers:

```go
ycyClient := ycy.NewClient()
```

- [ ] **Step 2: Pass it into `NewV1Service`**

Update the constructor call so the service layer can call YCY for video jobs.

- [ ] **Step 3: Keep the rest of app wiring unchanged**

Do not alter router setup, auth setup, Redis setup, or the existing maintenance service for this first pass.

### Task 5: Verify the model and event flow need no schema changes

**Files:**
- Inspect: `E:/GithubCloneProject/image2api/backend/internal/model/models.go`
- Inspect: `E:/GithubCloneProject/image2api/backend/internal/repo/event_repo.go`

- [ ] **Step 1: Confirm the existing event log can represent YCY jobs**

Check that the current `EventLog` fields already cover:
- provider name
- model name
- status
- upstream file / URL reference
- elapsed time

If that is sufficient, do not change the schema.

- [ ] **Step 2: Defer persistence changes unless the adapter proves it needs them**

Only add a new field such as an upstream task ID if the YCY flow cannot be completed with the current `EventLog.File` and `EventLog.Provider` fields.

### Task 6: Test the integration path end-to-end

**Files:**
- Modify only if the tests expose a real gap; otherwise keep source untouched
- Test command targets should be added where the repo already keeps integration or service tests

- [ ] **Step 1: Run the backend test suite**

Run the Go test suite from the backend root and capture the first failing package if any:

```powershell
cd E:\GithubCloneProject\image2api\backend
go test ./...
```

Expected: all existing tests still pass after the new provider is wired in.

- [ ] **Step 2: Exercise the video API with a YCY-backed model**

Use the existing API surface:
- create a video job
- poll `/v1/videos/:id`
- stream `/v1/videos/:id/content`

Expected:
- the job transitions from `queued` to `completed`
- the content endpoint returns a playable video response

- [ ] **Step 3: Confirm no Docker or frontend regressions**

Bring the stack up with the current compose setup and ensure the YCY changes did not alter the local deployment path already being repaired in the worktree.

---

### Coverage Check

- YCY provider client: Task 1
- Service routing: Task 2
- Config plumbing: Task 3
- App startup injection: Task 4
- Schema avoidance / validation: Task 5
- End-to-end verification: Task 6

### Out of Scope for this pass

- YCY account pooling
- Frontend UI changes
- New public endpoints
- Database migrations unless the current event model proves insufficient

