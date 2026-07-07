# Boogu-Image Deployment and image2api Integration Plan

Last reviewed: 2026-07-07
Status: evaluation draft
Scope: deploy `boogu-project/Boogu-Image` on the same server as `image2api` and manage it through the existing model gateway with minimal backend changes.

## 1. Executive Summary

The recommended integration is:

```text
Downstream canvas client
  -> image2api /v1/images/generations or /v1/images/edits
  -> existing image2api custom/upstream adapter
  -> local Boogu OpenAI-compatible wrapper
  -> Boogu-Image pipeline on GPU
```

Do not add Boogu as a new native Go provider at the first stage. Boogu-Image currently provides inference scripts and Python pipelines, not a production REST API. The lowest-risk path is to deploy it as an isolated local inference service that speaks the OpenAI image API shape already supported by `image2api`.

This keeps the current platform responsible for:

- model catalog and visibility
- API key authentication for downstream clients
- user credit billing
- generation logs
- user and account concurrency
- account weighting and failover
- generated media storage for the UI path

The Boogu sidecar service is responsible only for:

- loading Boogu model weights
- running text-to-image and optional image-edit inference
- returning generated image bytes as OpenAI-compatible `b64_json`

## 2. Current Project Fit

According to `docs/backend-architecture.md`, `image2api` already has a generic OpenAI-compatible upstream path under the `custom` provider. Current local code also has an `upstreamAdapters` registry that maps an account `adapter_type` such as `openai` to an implementation.

Important existing behavior:

- `backend/internal/provider/custom/client.go` posts text-to-image requests to `{base_url}/v1/images/generations`.
- With reference images, it posts multipart requests to `{base_url}/v1/images/edits`.
- It expects OpenAI-style responses with `data[0].b64_json` or `data[0].url`.
- `ModelConfig.Provider` can remain `custom`; runtime dispatch can route to the configured upstream account when the account declares that it serves the model id.
- The custom/upstream account stores `base_url`, API key, served model ids, weight, and per-account concurrency.

Therefore, Boogu only needs an OpenAI-compatible wrapper. The Go backend does not need Boogu-specific model loading, Python process management, or GPU logic.

## 3. Boogu-Image Quick Deployment

Official repository: https://github.com/boogu-project/Boogu-Image

Officially tested baseline:

- Linux x86_64
- Python 3.10
- CUDA 12.6
- PyTorch 2.7.1

Bootstrap:

```bash
git clone https://github.com/boogu-project/Boogu-Image.git /opt/boogu/Boogu-Image
cd /opt/boogu/Boogu-Image

bash quick_start.sh
conda activate boogu
```

Download checkpoints:

```bash
pip install -U "huggingface_hub[cli]"

huggingface-cli download Boogu/Boogu-Image-0.1-Turbo \
  --local-dir models/Boogu-Image-0.1-Turbo
```

First smoke test:

```bash
export CUDA_VISIBLE_DEVICES=0
export device="cuda:0"

python inference_turbo_simple.py
```

Recommended first production candidate:

- `Boogu-Image-0.1-Turbo`
- text-to-image only
- 1K output
- account concurrency `1`

Reasoning: Turbo is a four-step model and is the quickest way to validate the full gateway path. Base/Edit models are heavier and should be added after latency and VRAM are measured.

## 4. Required Wrapper Service

Boogu-Image should run behind a small persistent Python service, not by spawning `inference.py` per request.

Minimum wrapper responsibilities:

- Load the selected Boogu pipeline once at process startup.
- Keep `os.environ["device"]` aligned with the configured CUDA device before loading the pipeline.
- Expose `GET /health`.
- Expose `GET /v1/models`.
- Expose `POST /v1/images/generations`.
- Optionally expose `POST /v1/images/edits` for Edit/Edit-Turbo.
- Check `Authorization: Bearer <local-key>`.
- Convert OpenAI `size` such as `1024x1024` into Boogu `height` and `width`.
- Return `{"data":[{"b64_json":"<base64-png>"}]}`.
- Serialize inference with an internal lock unless multiple GPUs or multiple model replicas are explicitly configured.

OpenAI-compatible generation request expected from `image2api`:

```json
{
  "model": "boogu-image-0.1-turbo",
  "prompt": "A cinematic photo...",
  "n": 1,
  "size": "1024x1024",
  "quality": "standard"
}
```

OpenAI-compatible generation response required by `image2api`:

```json
{
  "created": 1783420800,
  "data": [
    {
      "b64_json": "..."
    }
  ]
}
```

For image edits, the current `image2api` custom client sends multipart fields:

- `model`
- `prompt`
- `size`
- one or more files under `image[]`

Boogu Edit currently focuses on one reference image per sample, so the first supported edit model should set `max_reference_images = 1`.

## 5. Deployment Topology Options

### Option A: External Local Service, Recommended First

Run Boogu outside the main `docker-compose.yml`, using conda and systemd on the GPU host.

Pros:

- Does not rebuild the existing image2api deployment.
- Easier to debug CUDA, PyTorch, Flash Attention, and model files.
- Keeps the large ML dependency stack out of the Go/Vue gateway stack.
- Easy rollback: disable the upstream account in the image2api admin panel.

Cons:

- Requires host-level process supervision.
- Requires local firewall or bind address discipline.

Recommended binding:

```text
127.0.0.1:8008
```

If `image2api` backend runs in Docker and Boogu runs on the host, the backend container must reach the host service. On Linux, configure Docker host networking or use the Docker bridge host gateway. Alternatively, run the wrapper as a compose sidecar in the same network.

### Option B: Docker Compose Sidecar

Add a `boogu-openai` service to the compose stack after the wrapper is stable.

Pros:

- Single deployment graph.
- Service discovery by container name, for example `http://boogu-openai:8008`.
- Cleaner production operations once the image build is proven.

Cons:

- Requires NVIDIA Container Toolkit.
- Docker image will be large.
- CUDA/PyTorch/Flash Attention build issues may slow deployment.

Sketch only:

```yaml
boogu-openai:
  build:
    context: ./deploy/boogu-openai
  environment:
    BOOGU_MODEL_PATH: /models/Boogu-Image-0.1-Turbo
    BOOGU_MODEL_KIND: turbo
    BOOGU_DEVICE: cuda:0
    BOOGU_API_KEY: sk-local-boogu-change-me
  volumes:
    - /opt/boogu/models:/models:ro
  ports:
    - "127.0.0.1:8008:8008"
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: 1
            capabilities: [gpu]
  restart: unless-stopped
```

For compose-internal access from `backend`, the admin upstream base URL should be:

```text
http://boogu-openai:8008
```

Do not expose the Boogu service publicly unless a separate gateway, rate limit, auth, and abuse policy are added.

## 6. image2api Admin Configuration

### 6.1 Add Managed Model

In admin model management, add a custom image model:

```text
id: boogu-image-0.1-turbo
name: Boogu Image 0.1 Turbo
type: image
provider: custom
upstream_model: boogu-image-0.1-turbo
enabled: true
```

Suggested first capabilities:

```text
ratios: 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3, 2:1
resolutions: 1K only at first
max_reference_images: 0
reference_mode: none
```

Pricing should be set locally according to GPU cost and target margin. Start with a conservative price because local GPU jobs occupy scarce capacity.

### 6.2 Add Upstream Account

In admin accounts/upstreams, add:

```text
protocol / adapter_type: OpenAI Compatible / openai
base_url: http://boogu-openai:8008
key: sk-local-boogu-change-me
served models: boogu-image-0.1-turbo
weight: 0 or higher if preferred over other upstreams
concurrency: 1
status: active
```

Important:

- The base URL should not include `/v1`; the current custom client appends `/v1/images/...`.
- If served models is left empty, the account may be treated as serving all custom models. Prefer explicit model ids for safety.
- Keep concurrency at `1` until GPU memory and latency are measured under load.

### 6.3 Add Edit Model Later

After text-to-image is stable, add:

```text
id: boogu-image-0.1-edit-turbo
type: image
provider: custom
upstream_model: boogu-image-0.1-edit-turbo
max_reference_images: 1
reference_mode: image
image_to_image: true
resolutions: 1K first
```

The wrapper must route this model id to the Edit-Turbo pipeline and implement `/v1/images/edits`.

## 7. Runtime Limits and Performance Notes

Known Boogu model family guidance from the official README/Inference Guide:

- Turbo supports fast text-to-image generation at 1K.
- Base supports 1K, 1.5K, and 2K, but is heavier.
- Edit supports image-to-image and is more stable at 1K.
- The model family maximum native resolution is approximately 2K.
- Offload modes can reduce VRAM but increase latency.
- Torch compile should be enabled only after baseline outputs are stable; it may produce black images on some setups.

Gateway-specific constraints:

- The current custom HTTP client timeout is 10 minutes.
- The core generation context is around 8 minutes.
- Slow 2K Base/Edit jobs may exceed this budget.
- If Boogu jobs regularly exceed this, either keep public offerings to Turbo/1K or evaluate a code change to extend the timeout for selected local upstream models.

Suggested rollout:

1. Turbo 1K T2I, single GPU, concurrency 1.
2. Add more aspect ratios after visual verification.
3. Add Edit-Turbo 1K with one reference image.
4. Evaluate Base 1K.
5. Evaluate Base/Edit 2K only after timeout and GPU cost are understood.

## 8. Operational Runbook

Health checks:

```bash
curl http://127.0.0.1:8008/health
curl -H "Authorization: Bearer sk-local-boogu-change-me" \
  http://127.0.0.1:8008/v1/models
```

Direct generation test:

```bash
curl -X POST http://127.0.0.1:8008/v1/images/generations \
  -H "Authorization: Bearer sk-local-boogu-change-me" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "boogu-image-0.1-turbo",
    "prompt": "A cinematic photo of a mountain lake at sunrise",
    "size": "1024x1024",
    "n": 1
  }'
```

Gateway generation test:

```bash
curl -X POST https://YOUR_IMAGE2API_DOMAIN/v1/images/generations \
  -H "Authorization: Bearer YOUR_IMAGE2API_USER_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "boogu-image-0.1-turbo",
    "prompt": "A cinematic photo of a mountain lake at sunrise",
    "size": "1024x1024"
  }'
```

Rollback:

1. Disable the Boogu upstream account in image2api admin.
2. Disable or hide the Boogu managed model.
3. Stop the wrapper service.

No database rollback should be required for normal disablement.

## 9. Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Boogu is research-oriented, not a hardened production API | Unexpected failures or unsafe outputs | Keep wrapper isolated, add moderation/safety upstream of public usage, start with limited access |
| GPU memory pressure | OOM, failed jobs, slow recovery | Use Turbo 1K first, concurrency 1, monitor VRAM, use FP8/offload only after baseline |
| Long latency exceeds gateway timeout | Users see failed jobs despite eventual local completion | Limit initial offerings, measure p95 latency, consider timeout changes only after data |
| Wrapper process crash | Local model unavailable | systemd or compose restart, health checks, image2api failover if another account exists |
| Served model list left empty | Boogu may receive unrelated custom models | Always configure explicit served model ids |
| Public exposure of Boogu wrapper | Abuse bypasses billing/auth | Bind to localhost or internal Docker network only |
| Edit endpoint mismatch | Image edit calls fail | Implement multipart `image[]` compatibility and set max refs to 1 |

## 10. Architecture Review Questions

The architect should decide:

1. Whether Boogu should initially run as a host-level systemd service or a Docker sidecar.
2. Which GPU and VRAM target are available.
3. Whether public users can access Boogu immediately or only admins/selected users during burn-in.
4. Whether content moderation is required before exposing the model to downstream canvas software.
5. Whether image2api generation timeout should stay unchanged for phase 1.
6. Whether model ids should mirror official names or use local gateway names.
7. Whether generated outputs from `/v1` should remain base64-only, matching current image2api behavior, or be persisted for Boogu-specific audit needs.

## 11. Proposed Future Changes

Phase 1, no Go changes:

- Build Boogu OpenAI-compatible wrapper.
- Deploy wrapper locally.
- Add custom model and custom upstream account in admin UI.
- Validate end-to-end generation.

Phase 2, optional small operational improvements:

- Add example compose sidecar under `deploy/boogu-openai`.
- Add documentation for Boogu admin setup.
- Add wrapper health and metrics endpoints.
- Add latency/error dashboarding outside the Go service or through logs.

Phase 3, only if needed:

- Add per-model upstream timeout controls.
- Add a Boogu-specific adapter type if OpenAI-compatible shape becomes insufficient.
- Add native provider integration only if wrapper overhead or protocol limitations become a real blocker.

## 12. Source References

- Project backend architecture: `docs/backend-architecture.md`
- Current custom upstream client: `backend/internal/provider/custom/client.go`
- Current upstream adapter registry: `backend/internal/bootstrap/app.go`
- Boogu official repository: https://github.com/boogu-project/Boogu-Image
- Boogu inference guide: https://github.com/boogu-project/Boogu-Image/blob/main/INFERENCE_GUIDE.md
- Boogu model README: https://github.com/boogu-project/Boogu-Image/blob/main/README.md

## 13. Change Log

| Date | Version | Change |
|---|---|---|
| 2026-07-07 | v0.1 | Initial evaluation draft for external OpenAI-compatible Boogu wrapper integration. |
