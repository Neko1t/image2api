# Boogu-Image Deployment and image2api Integration Plan

Version: v0.5
Last reviewed: 2026-07-07
Status: implementation-ready design with final implementation clarifications
Supersedes: v0.4 (2026-07-07), v0.3 (2026-07-07), v0.2 (2026-07-07), v0.1 archived at `docs/archive/BOOGU_IMAGE_DEPLOYMENT_PLAN_v0.1_2026-07-07.md`

## 1. Decision Summary

`image2api` should manage Boogu-Image through the existing custom/upstream provider path. Boogu itself should run as a **standalone Docker service**, decoupled from the `image2api` compose stack, to allow independent lifecycle management, upgrades, and scaling.

```text
Canvas client
  -> image2api /v1/images/generations or /v1/images/edits
  -> existing custom/upstream OpenAI adapter
  -> boogu-openai standalone Docker service (same host, internal network or host bridge)
  -> Boogu-Image Python pipeline on GPU
```

Decisions made:

- CUDA version: **12.6.3** (official Boogu baseline)
- Python version: **stable CPython 3.11.x** for the wrapper runtime.
- Deployment mode: **standalone Docker service**, not a sidecar in the image2api compose stack
- Download source: **ModelScope** as primary (domestic server); Hugging Face as fallback
- Phase 3 access: **all users** after wrapper validation passes; capacity remains protected by account concurrency `1`
- `/v1` output: base64-only, matching current image2api behavior
- Torch compile: disabled for phase 1 to avoid black-image risk on first deployment
- GPU profile and offload mode: configured via `BOOGU_GPU_PROFILE` and `BOOGU_OFFLOAD_MODE` to separate the 8 GB RTX 4060 test environment from high-VRAM production GPUs
- Boogu source: pinned via `BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba`
- Timeout policy: a 7-minute inference timeout returns 504, marks the wrapper not ready, and exits the process so Docker restarts with a clean CUDA state

The architectural decision from v0.1 remains unchanged: do not add a native Boogu provider to the Go backend for the first rollout.

## 2. Goals and Non-Goals

Goals:

- Deploy Boogu-Image on the same server or Docker host as `image2api` as an **independent standalone Docker service**.
- Expose Boogu through an OpenAI-compatible local wrapper reachable from the `image2api` backend container.
- Let `image2api` continue to own authentication, model catalog, billing, logs, user concurrency, account concurrency, and failover.
- Keep Go backend changes at zero for phase 1.
- Make first deployment reproducible enough for operations review.
- Decouple Boogu lifecycle (start, stop, upgrade, rebuild) from the `image2api` compose stack.

Non-goals for phase 1:

- No native Go provider for Boogu.
- No public exposure of the Boogu wrapper.
- No multi-GPU scheduling inside one wrapper process.
- No Base/Edit 2K public offering until latency and timeout data are collected.
- No attempt to bake model weights into the Docker image.
- No torch compile; disabled to avoid black-image risk on first deployment.

## 3. Required Repository Additions

The deployment package should be added under:

```text
deploy/
  boogu-openai/
    app.py
    Dockerfile
    requirements.txt
    README.md
    docker-compose.boogu.yml
    .dockerignore
    download_models.sh
```

`app.py` is mandatory. It is the largest missing piece in v0.1.

`download_models.sh` is a host-level prerequisite script for downloading model weights from ModelScope or Hugging Face.

`README.md` must not be left empty. It should contain a short service description, quick-start commands, and a pointer to `IMPLEMENTATION_STEPS.md`.

Minimum wrapper behavior:

- Use FastAPI with structured JSON logging.
- Load the Boogu pipeline once during application startup.
- Set `os.environ["device"]` before importing/loading Boogu pipeline code.
- Expose `/livez` for process liveness.
- Expose `/readyz` for model readiness.
- Expose `/health` as a compatibility alias, returning ready status.
- Expose `GET /v1/models`.
- Expose `POST /v1/images/generations`.
- Later expose `POST /v1/images/edits`.
- Enforce `Authorization: Bearer $BOOGU_API_KEY` on generation endpoints.
- Protect `GET /v1/models` by default. During implementation, verify whether the current image2api account-test path calls upstream `/v1/models` and whether it sends the configured key. If that probe is unauthenticated, make only `/v1/models` optional/no-auth or update the probe to send auth; do not relax generation endpoint auth.
- Parse OpenAI `size` strings such as `1024x1024`.
- Clamp or reject unsupported sizes.
- Serialize generation with an `asyncio.Lock`; return 503 immediately if model is busy.
- Return OpenAI-style `data[0].b64_json`.
- Return 503 until the model is loaded and warm.
- Apply inference timeout (7 minutes) to prevent wrapper hang.
- Validate generation output (non-zero size, not all-black) before returning.

For phase 1, the wrapper should support one model id:

```text
boogu-image-0.1-turbo
```

Additional ids can be added after the first model is stable.

## 4. Wrapper API Contract

### 4.1 Liveness

```http
GET /livez
```

Returns 200 when the HTTP process is alive, even if the model is still loading.

Example:

```json
{"ok":true,"status":"alive"}
```

### 4.2 Readiness

```http
GET /readyz
```

Returns:

- 200 after model load and optional warmup succeed.
- 503 while loading, after load failure, or when the wrapper is draining.

Example ready response:

```json
{
  "ok": true,
  "status": "ready",
  "model": "boogu-image-0.1-turbo",
  "device": "cuda:0"
}
```

Example not-ready response:

```json
{
  "ok": false,
  "status": "loading"
}
```

### 4.3 Models

```http
GET /v1/models
Authorization: Bearer sk-local-boogu-change-me
```

Default behavior: authenticated. This matches the OpenAI-style bearer-token model. Before enabling the upstream account, verify the current image2api account-test/probe path: if it calls `/v1/models` without `Authorization`, either make this endpoint optional/no-auth in the wrapper or adjust the probe to send the upstream account key. The image generation endpoints must remain authenticated.

Response:

```json
{
  "object": "list",
  "data": [
    {
      "id": "boogu-image-0.1-turbo",
      "object": "model",
      "owned_by": "boogu-local"
    }
  ]
}
```

### 4.4 Image Generation

```http
POST /v1/images/generations
Authorization: Bearer sk-local-boogu-change-me
Content-Type: application/json
```

Request from current `image2api` custom client:

```json
{
  "model": "boogu-image-0.1-turbo",
  "prompt": "A cinematic photo of a mountain lake at sunrise",
  "n": 1,
  "size": "1024x1024",
  "quality": "standard"
}
```

Response required by current `image2api` custom client:

```json
{
  "created": 1783420800,
  "data": [
    {
      "b64_json": "<base64-png>"
    }
  ]
}
```

Validation rules:

- `model` must be a configured Boogu wrapper model.
- `prompt` must be non-empty.
- `n` should be accepted but only `1` is supported in phase 1.
- `size` must parse as `<width>x<height>`.
- `quality` is accepted for OpenAI compatibility but ignored in phase 1. Output dimensions are controlled by `size` plus `BOOGU_MAX_SIZE`, not by `quality`.
- Width and height must be multiples of 16 after normalization.
- Turbo phase 1 should allow only 1K-class sizes. Recommended hard cap: `max(width, height) <= 1024`.

### 4.5 Image Edits, Later

The existing Go custom client sends multipart fields to `/v1/images/edits`:

- `model`
- `prompt`
- `size`
- files under `image[]`

Boogu Edit/Edit-Turbo support should be implemented only after T2I is stable. The first edit model should support exactly one reference image per request, because Boogu Edit currently focuses on one reference image per sample.

## 5. Reference Wrapper Implementation Shape

The actual wrapper should live in `deploy/boogu-openai/app.py`. This document captures the required shape, not every line of final production code.

Key implementation points:

```python
import asyncio
import base64
import io
import json
import logging
import os
import threading
import time
from contextlib import asynccontextmanager

import torch
from fastapi import FastAPI, Header, HTTPException
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(message)s")
logger = logging.getLogger("boogu")

MODEL_ID = os.getenv("BOOGU_MODEL_ID", "boogu-image-0.1-turbo")
MODEL_PATH = os.getenv("BOOGU_MODEL_PATH", "/models/Boogu-Image-0.1-Turbo")
MODEL_KIND = os.getenv("BOOGU_MODEL_KIND", "turbo")
DEVICE = os.getenv("BOOGU_DEVICE", "cuda:0")
API_KEY = os.getenv("BOOGU_API_KEY", "")
WARMUP = os.getenv("BOOGU_WARMUP", "true").lower() == "true"
GPU_PROFILE = os.getenv("BOOGU_GPU_PROFILE", "production-high-vram")

os.environ["device"] = DEVICE

state = {
    "ready": False,
    "status": "loading",
    "pipe": None,
    "load_error": "",
    "busy": False,
}
inference_lock = asyncio.Lock()


class ImageGenerationRequest(BaseModel):
    model: str
    prompt: str
    n: int = 1
    size: str = "1024x1024"
    quality: str | None = None
    seed: int | None = None


def require_auth(auth_header: str | None) -> None:
    if not API_KEY:
        raise HTTPException(status_code=500, detail="BOOGU_API_KEY is not configured")
    if auth_header != f"Bearer {API_KEY}":
        raise HTTPException(status_code=401, detail="unauthorized")


def parse_size(size: str) -> tuple[int, int]:
    try:
        w_raw, h_raw = size.lower().split("x", 1)
        width = int(w_raw)
        height = int(h_raw)
    except Exception:
        raise HTTPException(status_code=400, detail="size must be like 1024x1024")
    if width <= 0 or height <= 0:
        raise HTTPException(status_code=400, detail="size must be positive")
    if max(width, height) > 1024:
        raise HTTPException(status_code=400, detail="turbo phase 1 supports up to 1K")
    width = (width // 16) * 16
    height = (height // 16) * 16
    return width, height


def load_pipeline():
    OFFLOAD = os.getenv("BOOGU_OFFLOAD_MODE", "none")  # none | model | sequential

    if MODEL_KIND == "turbo":
        from boogu.pipelines.boogu.pipeline_boogu_turbo import BooguImageTurboPipeline
        pipe = BooguImageTurboPipeline.from_pretrained(
            MODEL_PATH,
            torch_dtype=torch.bfloat16,
            trust_remote_code=True,
        )
    else:
        from boogu.pipelines.boogu.pipeline_boogu import BooguImagePipeline
        pipe = BooguImagePipeline.from_pretrained(
            MODEL_PATH,
            torch_dtype=torch.bfloat16,
            trust_remote_code=True,
        )

    if OFFLOAD == "sequential":
        pipe.enable_sequential_cpu_offload(device=DEVICE)   # ~10-12 GB VRAM, slowest
    elif OFFLOAD == "model":
        pipe.enable_model_cpu_offload(device=DEVICE)        # ~16 GB VRAM, moderate
    else:
        pipe.to(DEVICE)                        # full VRAM, fastest (24 GB+)

    return pipe
```

The `BOOGU_OFFLOAD_MODE` environment variable decouples VRAM strategy from the image build, allowing the same Docker image to run on different hardware tiers:

| `BOOGU_OFFLOAD_MODE` | Minimum VRAM | Notes |
|---|---|---|
| `none` (default) | ~24 GB | Full model on GPU; fastest inference |
| `model` | ~16 GB | CPU offload per model component |
| `sequential` | ~10-12 GB | Sequential CPU offload; slowest, use as last resort |

RTX 4060 (8 GB VRAM) note: Turbo 1K at bfloat16 may require `sequential` or FP8 quantization. Confirm at first run by checking `nvidia-smi` memory usage during load.

`BOOGU_GPU_PROFILE` is an operational label for logs and deployment profiles. It should not auto-select behavior by itself; `BOOGU_OFFLOAD_MODE` remains the explicit runtime control.

Recommended profiles:

```env
# Test environment: RTX 4060 8 GB
BOOGU_GPU_PROFILE=test-8gb
BOOGU_OFFLOAD_MODE=sequential
BOOGU_MAX_SIZE=1024x1024

# Production: RTX 5090 or better
BOOGU_GPU_PROFILE=production-high-vram
BOOGU_OFFLOAD_MODE=none
BOOGU_MAX_SIZE=1024x1024
```

Concurrent request handling — reject immediately if busy, never block the event loop:

```python
# Reject immediately instead of queuing; image2api failover handles retries.
if state["busy"] or inference_lock.locked():
    raise HTTPException(status_code=503, detail="model busy, please retry")
```

Generation must run in a thread pool to avoid blocking the asyncio event loop. This is required so that `/livez`, `/readyz`, and the busy-503 check above remain responsive during inference:

```python
import asyncio
from concurrent.futures import ThreadPoolExecutor

# Module-level executor — max_workers=1 enforces single-GPU serialization
inference_executor = ThreadPoolExecutor(max_workers=1)


def _sync_generate(prompt, width, height, seed, model_kind, pipe, device):
    """Pure synchronous inference function — runs in thread pool."""
    generator = torch.Generator(device).manual_seed(seed)
    kwargs = dict(
        height=height, width=width, generator=generator,
        device=device,
        negative_instruction="", empty_instruction="",
        image_guidance_scale=1.0,
        empty_instruction_guidance_scale=0.0,
    )
    if model_kind == "turbo":
        result = pipe(
            instruction=[prompt],
            num_inference_steps=4,
            text_guidance_scale=1.0,
            use_dmd_student_inference=True,
            dmd_conditioning_sigma=0.001,
            **kwargs,
        )
    else:
        result = pipe(
            instruction=prompt,
            num_inference_steps=50,
            text_guidance_scale=4.0,
            **kwargs,
        )
    return result.images[0]


# Inside the async generation endpoint:
async with inference_lock:
    state["busy"] = True
    loop = asyncio.get_running_loop()
    future = loop.run_in_executor(
        inference_executor,
        _sync_generate,
        prompt, width, height, seed, MODEL_KIND, state["pipe"], DEVICE,
    )
    try:
        image = await asyncio.wait_for(asyncio.shield(future), timeout=420)
    except asyncio.TimeoutError:
        state["ready"] = False
        state["status"] = "timed_out_restarting"
        logger.error(json.dumps({"event": "inference_timeout_restart"}))
        threading.Timer(1.0, lambda: os._exit(124)).start()
        raise HTTPException(status_code=504, detail="inference timeout; wrapper restarting")
    finally:
        if state["status"] != "timed_out_restarting":
            state["busy"] = False
```

`asyncio.wait_for` with `run_in_executor` keeps the event loop responsive. `asyncio.shield` prevents the wrapper future from being cancelled in a way that obscures state handling. If timeout fires, the process exits intentionally after returning 504; Docker `restart: unless-stopped` brings the wrapper back with a clean CUDA state. Do not release capacity and continue serving new requests after a timed-out GPU job.

Startup structured log — must be emitted at config load time so the Phase 8.3 log verification passes:

```python
logger.info(json.dumps({
    "event": "config_loaded",
    "model_id": MODEL_ID,
    "model_kind": MODEL_KIND,
    "device": DEVICE,
    "gpu_profile": GPU_PROFILE,
    "offload": os.getenv("BOOGU_OFFLOAD_MODE", "none"),
    "warmup": WARMUP,
}))
```

Output validation before returning — check that the generated image is not trivially broken:

```python
buf = io.BytesIO()
image.save(buf, format="PNG")
png_bytes = buf.getvalue()

# Sanity check: image must have expected dimensions and not be all-black
if image.width != width or image.height != height:
    raise HTTPException(status_code=500, detail="generated image has unexpected dimensions")

import numpy as np
arr = np.array(image)
if arr.max() == 0:
    raise HTTPException(status_code=500, detail="generated image is blank (all-black); check model weights and CUDA setup")

b64 = base64.b64encode(png_bytes).decode("ascii")
```

Structured logging — emit JSON logs for each inference so latency can be tracked without an external metrics system:

```python
import logging, json, time
logger = logging.getLogger("boogu")

# At startup
logging.basicConfig(
    level=logging.INFO,
    format="%(message)s",
)

# After each generation
t_elapsed = time.monotonic() - t_start
logger.info(json.dumps({
    "event": "generation_done",
    "model": MODEL_ID,
    "size": f"{width}x{height}",
    "elapsed_s": round(t_elapsed, 2),
}))
```

The final code must handle Boogu pipeline argument differences carefully. Turbo and non-Turbo paths should be tested separately because they use different pipeline classes and recommended inference settings. Torch compile must remain disabled for phase 1.

## 6. Dockerfile Requirements

The first production Dockerfile should optimize for build reliability over image size.

Recommended first base image:

```dockerfile
FROM nvidia/cuda:12.6.3-cudnn-devel-ubuntu22.04
```

Rationale:

- Boogu official baseline targets CUDA 12.6.
- `devel` includes build tooling needed if Flash Attention falls back to compilation.
- The image will be large, but failure risk is lower than starting with a runtime-only image.

Expected Dockerfile responsibilities:

- Install stable CPython 3.11.x and build basics.
- Clone Boogu-Image source at a pinned commit via `ARG BOOGU_GIT_REF`.
- Install Boogu requirements via `BOOGU_TORCH_REQUIREMENTS`, defaulting to `requirements/torch2.7-cu126.txt`.
- Install Boogu package with `pip install -e`.
- Copy `requirements.txt` into the image and install wrapper dependencies from it. `requirements.txt` is the single source of truth for wrapper dependencies.
- Install Flash Attention via Boogu's own helper script by default, with a source-build path for local native-extension debugging (see below).
- Set `HF_HOME` for HuggingFace cache to persist across container restarts.
- Do not copy model weights into the image.
- Run `uvicorn app:app --host 0.0.0.0 --port 8008`.

Pinning Boogu source version:

```dockerfile
ARG BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba
RUN git clone https://github.com/boogu-project/Boogu-Image.git /opt/Boogu-Image \
    && cd /opt/Boogu-Image \
    && git checkout ${BOOGU_GIT_REF}
```

Production builds should use the reviewed commit SHA:

```text
df9a219fccd8954df7cf16e71453c19f7a72dbba
```

Flash Attention installation strategy — use Boogu's helper script by default for reviewed production builds. For local RTX 4060 native-extension debugging, compile flash-attn from source against the exact Python/Torch/CUDA/SM combination:

```dockerfile
ARG BOOGU_INSTALL_FLASH_ATTN=true
ARG FLASH_ATTN_VERSION=2.8.3
ARG TORCH_CUDA_ARCH_LIST=8.9
ARG MAX_JOBS=1
ENV TORCH_CUDA_ARCH_LIST=${TORCH_CUDA_ARCH_LIST} \
    MAX_JOBS=${MAX_JOBS}
RUN case "$BOOGU_INSTALL_FLASH_ATTN" in \
        true|helper) \
            python3.11 /opt/Boogu-Image/utils/get_flash_attn.py \
            || python3.11 -m pip install "flash-attn==${FLASH_ATTN_VERSION}" --no-build-isolation; \
            ;; \
        source) \
            python3.11 -m pip install ninja packaging \
            && python3.11 -m pip install -v "flash-attn==${FLASH_ATTN_VERSION}" --no-build-isolation --no-binary=flash-attn; \
            ;; \
        false|0|no) \
            echo "Skipping flash-attn install"; \
            ;; \
        *) \
            exit 1; \
            ;; \
    esac
```

The default helper path avoids unnecessary version pinning. The source path is intentionally explicit so RTX 4060 debugging can validate `flash_attn.ops.activations.swiglu` without relying on a prebuilt wheel.

Wrapper dependency installation should be driven by `deploy/boogu-openai/requirements.txt`:

```dockerfile
WORKDIR /app
COPY requirements.txt /app/
RUN python3.11 -m pip install -r /app/requirements.txt
COPY app.py /app/
```

Image-size policy:

- Large image size is accepted for v0.2.
- Keep model weights outside the image.
- Revisit multi-stage or prebuilt base images after the first working deployment.

## 7. Model Weights Strategy

Model weights are a host-level prerequisite. They must be downloaded before starting the standalone Docker service.

Recommended host directory:

```text
/opt/boogu/models/
  Boogu-Image-0.1-Turbo/
    model_index.json
    mllm/
    processor/
    scheduler/
    transformer/
    vae/
```

Download on the host — **use ModelScope as primary source for domestic servers**:

```bash
sudo mkdir -p /opt/boogu/models
sudo chown -R "$USER":"$USER" /opt/boogu

# Install modelscope CLI
pip install modelscope

# Download from ModelScope (primary for China mainland)
modelscope download --model Boogu/Boogu-Image-0.1-Turbo \
  --local_dir /opt/boogu/models/Boogu-Image-0.1-Turbo
```

Alternative: HuggingFace (for servers with direct access):

```bash
python3 -m pip install -U "huggingface_hub[cli]"

huggingface-cli download Boogu/Boogu-Image-0.1-Turbo \
  --local-dir /opt/boogu/models/Boogu-Image-0.1-Turbo
```

The chosen source must produce the same local directory structure expected by Boogu. Verify the download succeeded:

```bash
ls -la /opt/boogu/models/Boogu-Image-0.1-Turbo/model_index.json
```

The standalone Docker service should mount weights read-only:

```yaml
volumes:
  - /opt/boogu/models:/models:ro
```

The wrapper's `BOOGU_MODEL_PATH` environment variable should then point to:

```text
/models/Boogu-Image-0.1-Turbo
```

## 8. GPU Container Prerequisites

Before running the Docker sidecar, the host must pass both checks.

Host GPU check:

```bash
nvidia-smi
```

Container GPU check:

```bash
docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu22.04 nvidia-smi
```

If the second command fails, install or fix NVIDIA Container Toolkit before deploying the wrapper.

Typical Ubuntu setup outline:

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
  | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

Use the current NVIDIA documentation if distribution-specific commands differ.

## 9. Standalone Docker Service

Boogu-Image is deployed as a **standalone Docker service**, independent of the `image2api` compose stack. This allows the model service to be started, stopped, rebuilt, and upgraded without touching the main gateway.

### 9.1 docker-compose.boogu.yml

The standalone compose file lives at `deploy/boogu-openai/docker-compose.boogu.yml`. It is operated separately from the main `docker-compose.yml`.

```yaml
services:
  boogu-openai:
    build:
      context: .
      args:
        BOOGU_GIT_REF: df9a219fccd8954df7cf16e71453c19f7a72dbba
    env_file:
      - .env                  # provides BOOGU_API_KEY
    environment:
      BOOGU_MODEL_ID: boogu-image-0.1-turbo
      BOOGU_MODEL_PATH: /models/Boogu-Image-0.1-Turbo
      BOOGU_MODEL_KIND: turbo
      BOOGU_DEVICE: cuda:0
      BOOGU_GPU_PROFILE: production-high-vram
      BOOGU_OFFLOAD_MODE: none   # none | model | sequential — set per hardware tier
      BOOGU_MAX_SIZE: 1024x1024
      BOOGU_WARMUP: "true"
      CUDA_VISIBLE_DEVICES: "0"
      HF_HOME: /cache/hf
    volumes:
      - /opt/boogu/models:/models:ro
      - boogu-cache:/cache
    ports:
      - "127.0.0.1:8008:8008"
    networks:
      boogu-net: {}
      image2api-net:
        aliases:
          - boogu-openai       # explicit alias ensures stable DNS regardless of compose project name
    healthcheck:
      test: ["CMD", "python3", "-c",
             "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8008/readyz', timeout=5).read()"]
      interval: 15s
      timeout: 10s
      retries: 20
      start_period: 300s
    # GPU option A — Compose v2 standard (preferred)
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
    # GPU option B — fallback for older Compose environments
    # Uncomment the line below and remove the deploy block above if option A fails:
    # gpus: all
    restart: unless-stopped

volumes:
  boogu-cache:

networks:
  boogu-net:
    driver: bridge
  image2api-net:
    external: true
    name: image2api_default   # update if your image2api network name differs
```

`HF_HOME: /cache/hf` replaces the narrower `HF_MODULES_CACHE` from v0.2. This ensures HuggingFace tokenizer files, model cards, and module caches all persist in the same named volume and are not re-downloaded on every container restart.

### 9.2 Network Connectivity

Because `boogu-openai` is a standalone service, it must join the same Docker network as the `image2api` backend to be reachable by service name.

The main `image2api` compose stack creates a default network named `image2api_default`. The standalone compose file attaches to this network as an external network (see `image2api-net` above).

Verify the network name on your host before deploying:

```bash
docker network ls | grep image2api
```

If the network name differs (e.g. `gateway_default`), update the `name:` field accordingly.

Once connected, the `image2api` admin upstream `base_url` should be:

```text
http://boogu-openai:8008
```

### 9.3 Operating the Standalone Service

```bash
# Build and start
cd deploy/boogu-openai
docker compose -f docker-compose.boogu.yml up -d --build

# Check readiness
docker compose -f docker-compose.boogu.yml ps
curl http://127.0.0.1:8008/readyz

# View logs
docker compose -f docker-compose.boogu.yml logs -f boogu-openai

# Stop without removing
docker compose -f docker-compose.boogu.yml stop

# Rebuild after code change
docker compose -f docker-compose.boogu.yml up -d --build --no-deps boogu-openai
```

### 9.4 Compatibility Note

Compose v2 supports the `deploy.resources.reservations.devices` GPU form in modern environments. Some deployments may prefer `gpus: all` at the top-level service key. Validate GPU visibility with:

```bash
docker compose -f docker-compose.boogu.yml run --rm boogu-openai nvidia-smi
```

## 10. image2api Admin Configuration

### 10.1 Managed Model

Add a custom image model:

```text
id: boogu-image-0.1-turbo
name: Boogu Image 0.1 Turbo
type: image
provider: custom
upstream_model: boogu-image-0.1-turbo
enabled: true
```

Phase 1 capabilities:

```text
ratios: 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3, 2:1
resolutions: 1K only
max_reference_images: 0
reference_mode: none
image_to_image: false
```

Do not enable 2K or 4K for Turbo phase 1.

### 10.2 Upstream Account

Add an OpenAI-compatible custom upstream:

```text
adapter_type: openai
base_url: http://boogu-openai:8008
key: sk-local-boogu-change-me
served models: boogu-image-0.1-turbo
weight: 0
concurrency: 1
status: active only after wrapper /readyz is healthy
```

Important:

- Do not include `/v1` in `base_url`; current Go code appends `/v1/images/...`.
- Do not leave served models empty; empty may mean all custom models.
- Keep concurrency at `1` until load testing proves otherwise.

## 11. Cold Start, Warmup, and Timeout Policy

Boogu cold start has three separate costs:

- container startup
- pipeline weight load from mounted volume into CPU/GPU memory
- first inference warmup, including CUDA kernel and attention path initialization

The wrapper should load the model before reporting ready. If `BOOGU_WARMUP=true`, it should also run a tiny or normal 1K warmup generation before `/readyz` returns 200.

Current gateway constraints:

- `backend/internal/provider/custom/client.go` uses a 10-minute HTTP client timeout.
- The main generation context in `V1Service` is shorter, around 8 minutes.

Phase 1 policy:

- Only expose Turbo 1K.
- Warm the wrapper before enabling the upstream account.
- Record first request, p50, p95, and worst-case latency.
- Do not change Go backend timeouts until there is measured evidence that a desired model tier cannot fit.

If Base/Edit 2K becomes a requirement, review timeout changes as a separate backend design.

## 12. Validation Checklist

Host:

- `nvidia-smi` works.
- `docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu22.04 nvidia-smi` works.
- `/opt/boogu/models/Boogu-Image-0.1-Turbo/model_index.json` exists.
- `docker network ls | grep image2api` shows the expected network name.

Wrapper container:

- Image builds successfully.
- Container sees GPU (`docker compose run --rm boogu-openai nvidia-smi`).
- `/livez` returns 200 during startup.
- `/readyz` returns 503 while loading.
- `/readyz` returns 200 after load and warmup.
- `/v1/models` auth behavior has been verified against the current image2api account-test/probe path. Default: requires auth and returns configured model id with the correct key.
- `POST /v1/images/generations` returns `data[0].b64_json`.
- Response image decodes to a valid PNG with expected dimensions.
- Response image is not all-black.
- `quality` values such as `standard`, `hd`, `low`, `medium`, or `high` do not alter phase 1 output size; only `size` does.
- Invalid API key returns 401.
- Invalid size returns 400.
- Second concurrent request returns 503 (model busy) rather than blocking.
- Wrapper JSON logs show generation time in `elapsed_s`.

image2api:

- `image2api_default` (or equivalent) network is attached to `boogu-openai`.
- `http://boogu-openai:8008/readyz` is reachable from the `backend` container.
- Managed model is present and enabled.
- Upstream account is active and explicitly serves only `boogu-image-0.1-turbo`.
- Admin test generation succeeds.
- User playground generation succeeds.
- External `/v1/images/generations` succeeds through a user API key.
- Logs show provider/upstream account association.
- Credit billing and refunds behave as expected on success and failure.

Operational:

- Stopping the standalone service does not affect `image2api` stack availability.
- Disabling the upstream account in admin panel stops Boogu routing.
- Rollback does not require database rollback.

## 13. Rollout Plan

Phase 0, build artifacts:

- Add `deploy/boogu-openai` package with `app.py`, `Dockerfile`, `requirements.txt`, `docker-compose.boogu.yml`, `download_models.sh`, `README.md`.
- Ensure `README.md` contains the short quick start from the implementation steps.
- Verify GPU prerequisites on the target host.
- Run `download_models.sh` to download Boogu-Image-0.1-Turbo weights to `/opt/boogu/models`.
- Build Docker image: `docker compose -f deploy/boogu-openai/docker-compose.boogu.yml build`.

Phase 1, isolated wrapper verification:

- Start the standalone service: `docker compose -f deploy/boogu-openai/docker-compose.boogu.yml up -d`.
- Wait for `/readyz` to return 200.
- Generate one direct image with curl and decode the base64 output as PNG.
- Confirm image dimensions match requested size.
- Confirm image is not all-black.
- Confirm JSON logs show a reasonable `elapsed_s` value.
- Confirm second concurrent request is rejected with 503.

Phase 2, image2api integration:

- Verify `boogu-openai` container is reachable from `backend` container by name.
- Add managed model `boogu-image-0.1-turbo` in admin panel.
- Add upstream account (base_url: `http://boogu-openai:8008`), initially disabled.
- Enable only after phase 1 wrapper test passes.
- Run admin test generation through image2api.
- Confirm log entry shows correct provider and account association.

Phase 3, validation under traffic:

- Expose to all users after phase 1 wrapper validation and phase 2 image2api integration pass.
- Keep account concurrency at `1`.
- Collect first-request, p50, p95, and worst-case latency from wrapper JSON logs.
- Monitor VRAM usage during load.

Phase 4, capacity expansion (only after phase 3 data):

- Enable for downstream canvas software if not already done.
- Add wrapper replicas or additional upstream accounts to scale, not by raising concurrency blindly.

## 14. Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Wrapper implementation bugs | Boogu unavailable or returns malformed responses | Treat wrapper as a required deliverable; test direct and through gateway before enabling upstream account |
| CUDA image build instability | Slow deployment and failed builds | Start with CUDA 12.6.3 devel image; accept large size; optimize later |
| Flash Attention build time | 10-30 minute builds or failures | Prefer Boogu helper/prebuilt wheel for production; use source compilation for RTX 4060 native-extension debugging |
| Model weights missing | Container starts but cannot become ready | Run `download_models.sh` as a deployment prerequisite; validate `model_index.json` before starting service |
| GPU runtime not configured | Container cannot see GPU | Require host and container `nvidia-smi` checks before deploy |
| Cold start exceeds expectations | First requests fail or queue too long | Use `/readyz` plus warmup; enable upstream account only after ready |
| Gateway timeout | Long jobs fail through image2api | Phase 1 Turbo 1K only; measure p95 latency before enabling heavier tiers |
| Wrapper exposed publicly | Bypasses image2api billing/auth | Bind port to `127.0.0.1`; keep local API key regardless |
| Served models left empty | Boogu receives unrelated custom model requests | Always configure explicit served model ids in upstream account |
| Concurrent GPU OOM | Failed jobs and process instability | Wrapper lock with immediate 503 + image2api account concurrency `1` |
| Generated image is blank or broken | User sees black or corrupted output | Wrapper validates image dimensions and all-black condition before returning; log failures |
| Inference hangs indefinitely | Request times out while CUDA work may still be running | 7-minute `asyncio.wait_for(asyncio.shield(...))`; return 504, mark wrapper not ready, then `os._exit(124)` so Docker restarts with a clean CUDA state |
| HuggingFace cache re-downloaded on restart | Slow cold start; extra network traffic | Mount `HF_HOME` path to named volume `boogu-cache`; persists across restarts |
| Docker network name mismatch | `boogu-openai` unreachable from `backend` container | Verify network name with `docker network ls` before configuring upstream base_url |
| Torch compile black images | Silent generation failure on first deploy | Torch compile disabled for phase 1; do not enable without explicit testing |

## 15. Architecture Decisions Record

| Question | Decision |
|---|---|
| Docker sidecar or standalone service? | **Standalone Docker service**. Decouples Boogu lifecycle from image2api. |
| Python version? | **Stable CPython 3.11.x** for the wrapper runtime. |
| GPU VRAM strategy? | **`BOOGU_GPU_PROFILE` + `BOOGU_OFFLOAD_MODE` env vars**. Test environment uses RTX 4060 8 GB with `BOOGU_GPU_PROFILE=test-8gb` and `BOOGU_OFFLOAD_MODE=sequential`. Production uses RTX 5090 or better with `BOOGU_GPU_PROFILE=production-high-vram` and usually `BOOGU_OFFLOAD_MODE=none`. |
| Boogu source version? | **Pin via `BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba`**. This was the latest HEAD at review time and had been stable for about one week. |
| HuggingFace or ModelScope as primary download source? | **ModelScope** primary for domestic servers; HuggingFace as documented fallback. |
| Limit Boogu to restricted users during burn-in? | **No for Phase 3**. After wrapper and image2api validation pass, Phase 3 exposes to all users while keeping account concurrency at `1` and collecting latency/OOM/failure data. |
| Content moderation required before canvas access? | Deferred; not required for phase 1. Revisit if public volume warrants it. |
| Keep `/v1` output as base64-only? | **Yes**, base64-only. Matches current image2api behavior. No audit persistence for phase 1. |
| Timeout approach? | **`asyncio.wait_for(asyncio.shield(run_in_executor(...)))` + wrapper self-restart**. Inference runs in `ThreadPoolExecutor(max_workers=1)`; on 7-minute timeout the endpoint returns 504, marks the wrapper not ready, then exits so Docker restarts it. |
| Implement `/v1/images/edits` in phase 1? | **No**. T2I only in phase 1. Edits endpoint returns 501 as a placeholder until T2I is stable. |
| `/v1/models` authentication? | **Authenticated by default**. Verify the current image2api probe path during implementation; if the probe is unauthenticated, relax only `/v1/models` or update the probe to send the key. |
| `quality` behavior? | **Accepted but ignored by the wrapper in phase 1**. image2api must configure Boogu as 1K-only; `size` is the only output-dimension control sent to the wrapper. |
| Timeout changes model-specific or global? | Deferred. No timeout changes for phase 1. Review only after measured evidence that a model tier cannot fit within 8 minutes. |

## 16. Source References

- Project backend architecture: `docs/backend-architecture.md`
- Current custom upstream client: `backend/internal/provider/custom/client.go`
- Current upstream adapter registry: `backend/internal/bootstrap/app.go`
- Boogu official repository: https://github.com/boogu-project/Boogu-Image
- Boogu inference guide: https://github.com/boogu-project/Boogu-Image/blob/main/INFERENCE_GUIDE.md
- Boogu model README: https://github.com/boogu-project/Boogu-Image/blob/main/README.md

## 17. Change Log

| Date | Version | Change |
|---|---|---|
| 2026-07-07 | v0.5 | Added final implementation clarifications from architecture review: README content is required, Dockerfile installs wrapper dependencies from copied `requirements.txt`, `/v1/models` auth behavior must be verified against the current image2api probe path, and `quality` is explicitly accepted but ignored by the Boogu wrapper in phase 1. |
| 2026-07-07 | v0.4 | Applied final deployment decisions: pinned `BOOGU_GIT_REF` to `df9a219fccd8954df7cf16e71453c19f7a72dbba`, added `BOOGU_GPU_PROFILE` for test/production hardware separation, set Phase 3 to all-user rollout after validation, and changed timeout behavior to return 504 then restart the wrapper for a clean CUDA state. |
| 2026-07-07 | v0.3 | Applied improvements from architecture review. Standalone Docker service replacing sidecar. Added decisions record (CUDA 12.6.3, ModelScope, no user restriction, base64-only output, torch compile disabled). Updated Section 3 wrapper requirements: busy-503, 7-min timeout, output validation, structured JSON logging. Updated Section 5 implementation shape with all new patterns. Updated Section 6 Flash Attention strategy: prefer prebuilt wheel with fallback. Updated Section 7 model weights: ModelScope as primary source. Rewrote Section 9 as standalone Docker service with network connectivity guide. Updated Section 12 validation checklist: network reachability, output validation, busy-503, log checks. Rewrote Section 13 rollout plan with standalone service commands. Expanded Section 14 risk table: 5 new risks (blank output, inference hang, HF cache, network mismatch, torch compile). Replaced Section 15 open questions with decisions record. |
| 2026-07-07 | v0.2 | Rewrote plan after architecture review. Added wrapper as required deliverable, Dockerfile requirements, model volume strategy, GPU prerequisites, health/readiness probes, cold-start policy, validation checklist, and rollout gates. |
| 2026-07-07 | v0.1 | Initial evaluation draft. Archived at `docs/archive/BOOGU_IMAGE_DEPLOYMENT_PLAN_v0.1_2026-07-07.md`. |
