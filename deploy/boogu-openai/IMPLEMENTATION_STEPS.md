# Boogu-Image Wrapper Implementation Steps

Version: 1.3
Last updated: 2026-07-07
Reference: BOOGU_IMAGE_DEPLOYMENT_PLAN.md v0.5

Changes in v1.3: added required README content, changed Dockerfile to install wrapper dependencies
from copied `requirements.txt`, clarified `/v1/models` auth verification against image2api probes,
and documented that `quality` is accepted but ignored by the Boogu wrapper in phase 1.

This document is the step-by-step guide for implementing and deploying Boogu-Image as a standalone Docker service integrated with image2api.

## Prerequisites

Before starting, confirm:

- GPU-capable Linux host, NVIDIA Driver 535+
- Docker and Docker Compose v2 installed
- NVIDIA Container Toolkit installed and configured
- VRAM requirements by offload mode (set via `BOOGU_OFFLOAD_MODE`):
  - `none` — 24 GB+ (full model on GPU, fastest)
  - `model` — ~16 GB (model CPU offload)
  - `sequential` - ~10-12 GB (sequential CPU offload, slowest)
  - RTX 4060 (8 GB): use `sequential` or FP8; confirm at first run with `nvidia-smi`
- Test environment profile: `BOOGU_GPU_PROFILE=test-8gb`, `BOOGU_OFFLOAD_MODE=sequential`
- Production profile for RTX 5090 or better: `BOOGU_GPU_PROFILE=production-high-vram`, `BOOGU_OFFLOAD_MODE=none`
- Network access to ModelScope (primary) or Hugging Face (fallback)
- `image2api` already deployed and running

---

## Overview

| Phase | Description |
|---|---|
| 0 | Create file structure |
| 1 | Implement wrapper service (app.py) |
| 2 | Write Dockerfile |
| 3 | Write dependency and README files |
| 4 | Write Docker Compose |
| 5 | Write model download script |
| 6 | Write .dockerignore |
| 7 | Host prerequisite checks |
| 8 | Build and isolated validation |
| 9 | image2api integration |
| 10 | Full validation checklist |

---

## Phase 0: Create File Structure

### Step 0.1 — Create directory and empty files

```bash
cd /path/to/image2api
mkdir -p deploy/boogu-openai
cd deploy/boogu-openai
touch app.py Dockerfile requirements.txt docker-compose.boogu.yml
touch download_models.sh README.md .dockerignore .env.example
```

Expected result:

```text
deploy/
  boogu-openai/
    app.py
    Dockerfile
    requirements.txt
    docker-compose.boogu.yml
    download_models.sh
    README.md
    .dockerignore
    .env.example
```

---

## Phase 1: Implement Wrapper Service (app.py)

### Step 1.1 — Imports, logging, and environment variables

Write the top of `app.py`. Two critical constraints:
- `os.environ["device"] = DEVICE` must appear before any Boogu module import
- The `config_loaded` log must be emitted at startup (required by Phase 8.3 verification)

```python
#!/usr/bin/env python3
"""
Boogu-Image OpenAI-compatible API Wrapper
"""

import asyncio
import base64
import io
import json
import logging
import os
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager
from typing import Optional

import numpy as np
import torch
from fastapi import FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(message)s")
logger = logging.getLogger("boogu")

MODEL_ID     = os.getenv("BOOGU_MODEL_ID",     "boogu-image-0.1-turbo")
MODEL_PATH   = os.getenv("BOOGU_MODEL_PATH",   "/models/Boogu-Image-0.1-Turbo")
MODEL_KIND   = os.getenv("BOOGU_MODEL_KIND",   "turbo")
DEVICE       = os.getenv("BOOGU_DEVICE",       "cuda:0")
API_KEY      = os.getenv("BOOGU_API_KEY",      "")
WARMUP       = os.getenv("BOOGU_WARMUP",       "true").lower() == "true"
OFFLOAD_MODE = os.getenv("BOOGU_OFFLOAD_MODE", "none")  # none | model | sequential
GPU_PROFILE  = os.getenv("BOOGU_GPU_PROFILE",  "production-high-vram")

# CRITICAL: must be set before any Boogu import
os.environ["device"] = DEVICE

# Emit config_loaded immediately — Phase 8.3 verification expects this line
logger.info(json.dumps({
    "event":        "config_loaded",
    "model_id":     MODEL_ID,
    "model_kind":   MODEL_KIND,
    "device":       DEVICE,
    "gpu_profile":  GPU_PROFILE,
    "offload_mode": OFFLOAD_MODE,
    "warmup":       WARMUP,
}))
```

### Step 1.2 — Global state, inference lock, and thread pool executor

The executor (`ThreadPoolExecutor(max_workers=1)`) is module-level. It ensures synchronous GPU inference runs in a background thread so the asyncio event loop stays responsive for `/livez`, `/readyz`, and busy-503 checks.

```python
state = {
    "ready":      False,
    "status":     "loading",
    "pipe":       None,
    "load_error": "",
    "busy":       False,
}

# Lock prevents two request handlers from entering the generation path simultaneously.
# state["busy"] remains true until synchronous inference actually completes or
# the wrapper exits after timeout; do not rely only on asyncio.Lock after timeout.
# max_workers=1 on the executor enforces single-GPU serialization at the thread level.
inference_lock    = asyncio.Lock()
inference_executor = ThreadPoolExecutor(max_workers=1)
```

### Step 1.3 — Pydantic request/response models

```python
class ImageGenerationRequest(BaseModel):
    model: str
    prompt: str
    n: int = 1
    size: str = "1024x1024"
    # Accepted for OpenAI compatibility. Phase 1 ignores it; size controls output dimensions.
    quality: Optional[str] = None
    seed: Optional[int] = None


class ImageGenerationResponse(BaseModel):
    created: int
    data: list[dict]   # [{"b64_json": "..."}]
```

### Step 1.4 — Authentication and size validation helpers

```python
def require_auth(authorization: Optional[str] = Header(None)) -> None:
    if not API_KEY:
        raise HTTPException(status_code=500, detail="BOOGU_API_KEY is not configured")
    if authorization != f"Bearer {API_KEY}":
        raise HTTPException(status_code=401, detail="unauthorized")


def parse_size(size: str) -> tuple[int, int]:
    try:
        w_raw, h_raw = size.lower().split("x", 1)
        width, height = int(w_raw), int(h_raw)
    except Exception:
        raise HTTPException(status_code=400, detail="size must be like 1024x1024")
    if width <= 0 or height <= 0:
        raise HTTPException(status_code=400, detail="size must be positive")
    if max(width, height) > 1024:
        raise HTTPException(status_code=400, detail="turbo phase 1 supports up to 1K")
    # align to multiple of 16
    width  = (width  // 16) * 16
    height = (height // 16) * 16
    return width, height
```

### Step 1.5 — Model loading and warmup

Apply `OFFLOAD_MODE` after loading, before the pipeline is used. This is what makes the same image work on different VRAM tiers.

```python
def load_pipeline():
    logger.info(json.dumps({"event": "model_load_start", "path": MODEL_PATH, "kind": MODEL_KIND}))

    if MODEL_KIND == "turbo":
        from boogu.pipelines.boogu.pipeline_boogu_turbo import BooguImageTurboPipeline
        pipe = BooguImageTurboPipeline.from_pretrained(
            MODEL_PATH, torch_dtype=torch.bfloat16, trust_remote_code=True,
        )
    else:
        from boogu.pipelines.boogu.pipeline_boogu import BooguImagePipeline
        pipe = BooguImagePipeline.from_pretrained(
            MODEL_PATH, torch_dtype=torch.bfloat16, trust_remote_code=True,
        )

    if OFFLOAD_MODE == "sequential":
        pipe.enable_sequential_cpu_offload(device=DEVICE)   # ~10-12 GB VRAM (e.g. RTX 4060)
        logger.info(json.dumps({"event": "offload_mode", "mode": "sequential"}))
    elif OFFLOAD_MODE == "model":
        pipe.enable_model_cpu_offload(device=DEVICE)        # ~16 GB VRAM
        logger.info(json.dumps({"event": "offload_mode", "mode": "model"}))
    else:
        pipe.to(DEVICE)                        # full VRAM, 24 GB+
        logger.info(json.dumps({"event": "offload_mode", "mode": "none"}))

    logger.info(json.dumps({"event": "model_load_done"}))
    return pipe


def run_warmup(pipe):
    logger.info(json.dumps({"event": "warmup_start"}))
    generator = torch.Generator(DEVICE).manual_seed(42)
    kwargs = dict(
        height=512, width=512, generator=generator,
        negative_instruction="", empty_instruction="",
        image_guidance_scale=1.0, empty_instruction_guidance_scale=0.0,
    )
    if MODEL_KIND == "turbo":
        pipe(instruction=["warmup"], num_inference_steps=4,
             text_guidance_scale=1.0, use_dmd_student_inference=True,
             dmd_conditioning_sigma=0.001, **kwargs)
    else:
        pipe(instruction="warmup", num_inference_steps=50,
             text_guidance_scale=4.0, **kwargs)
    logger.info(json.dumps({"event": "warmup_done"}))
```

### Step 1.6 — Application lifespan (startup)

The lifespan runs model loading in a thread so the HTTP server stays responsive during startup. `/livez` returns 200 immediately; `/readyz` returns 503 until the thread finishes.

```python
@asynccontextmanager
async def lifespan(app: FastAPI):
    import threading

    def _load():
        try:
            pipe = load_pipeline()
            if WARMUP:
                run_warmup(pipe)
            state["pipe"]   = pipe
            state["ready"]  = True
            state["status"] = "ready"
        except Exception as e:
            state["status"]     = "failed"
            state["load_error"] = str(e)

    threading.Thread(target=_load, daemon=True).start()
    yield  # server runs here


app = FastAPI(title="Boogu-Image OpenAI API", lifespan=lifespan)
```

### Step 1.7 — Probe and models endpoints

```python
@app.get("/livez")
async def livez():
    return {"ok": True, "status": "alive"}


@app.get("/readyz")
async def readyz():
    if state["ready"]:
        return {"ok": True, "status": "ready", "model": MODEL_ID, "device": DEVICE}
    if state["status"] == "failed":
        return JSONResponse(
            status_code=503,
            content={"ok": False, "status": "failed", "error": state["load_error"]},
        )
    return JSONResponse(status_code=503, content={"ok": False, "status": "loading"})


@app.get("/health")
async def health():
    return await readyz()


@app.get("/v1/models")
async def list_models(authorization: Optional[str] = Header(None)):
    require_auth(authorization)
    return {
        "object": "list",
        "data": [{"id": MODEL_ID, "object": "model", "owned_by": "boogu-local"}],
    }
```

Add `from fastapi.responses import JSONResponse` to the import block at the top.

Default behavior is authenticated `/v1/models`, matching OpenAI-style bearer-token access. During image2api integration, verify whether the current account-test/probe path calls upstream `/v1/models` and whether it sends the configured key. If the probe is unauthenticated, either update that probe to send auth or relax only this endpoint; never relax `/v1/images/generations` or `/v1/images/edits`.

### Step 1.8 — Image generation endpoint (core logic)

This is the most critical section. Synchronous GPU inference runs inside `run_in_executor` so the event loop stays responsive for `/livez`, `/readyz`, and busy-503 checks during inference.

First define the synchronous inference function (pure function, no async, called from thread pool):

```python
def _sync_generate(prompt, width, height, seed, model_kind, pipe, device):
    """
    Pure synchronous inference. Runs in ThreadPoolExecutor — never call directly
    from an async context without run_in_executor.
    """
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
```

Then the async endpoint:

```python
@app.post("/v1/images/generations")
async def create_image(
    request: ImageGenerationRequest,
    authorization: Optional[str] = Header(None),
):
    require_auth(authorization)

    # 1. Readiness check
    if not state["ready"]:
        raise HTTPException(status_code=503, detail="model not ready")

    # 2. Validate request
    if request.model != MODEL_ID:
        raise HTTPException(status_code=404, detail=f"model {request.model!r} not found")
    if not request.prompt.strip():
        raise HTTPException(status_code=400, detail="prompt must not be empty")

    width, height = parse_size(request.size)
    seed = request.seed if request.seed is not None else int(time.time())

    if request.n != 1:
        raise HTTPException(status_code=400, detail="phase 1 supports n=1 only")

    # 3. Reject immediately if busy — never block the event loop waiting for the lock
    if state["busy"] or inference_lock.locked():
        raise HTTPException(status_code=503, detail="model busy, please retry")

    # OpenAI compatibility only: phase 1 accepts but ignores quality.
    # Do not map quality to higher resolutions; size is the output-dimension source.

    t_start = time.monotonic()

    async with inference_lock:
        state["busy"] = True
        loop = asyncio.get_running_loop()
        future = loop.run_in_executor(
            inference_executor,
            _sync_generate,
            request.prompt, width, height, seed,
            MODEL_KIND, state["pipe"], DEVICE,
        )
        try:
            # run_in_executor offloads synchronous GPU work to the thread pool.
            # shield keeps timeout state handling explicit. If timeout fires,
            # the thread may still own CUDA work, so the wrapper exits and Docker restarts it.
            image = await asyncio.wait_for(
                asyncio.shield(future),
                timeout=420,  # 7 minutes — shorter than gateway's 8-minute context
            )
        except asyncio.TimeoutError:
            state["ready"] = False
            state["status"] = "timed_out_restarting"
            logger.error(json.dumps({
                "event": "inference_timeout_restart",
                "model": MODEL_ID,
                "size": f"{width}x{height}",
                "elapsed_s": round(time.monotonic() - t_start, 2),
            }))
            threading.Timer(1.0, lambda: os._exit(124)).start()
            raise HTTPException(status_code=504, detail="inference timeout; wrapper restarting")
        except Exception as e:
            logger.error(json.dumps({"event": "inference_error", "error": str(e)}))
            raise HTTPException(status_code=500, detail=f"inference failed: {e}")
        finally:
            if state["status"] != "timed_out_restarting":
                state["busy"] = False

    # 4. Output validation
    if image.width != width or image.height != height:
        raise HTTPException(
            status_code=500,
            detail=f"generated image has unexpected dimensions {image.width}x{image.height}"
        )
    arr = np.array(image)
    if arr.max() == 0:
        raise HTTPException(
            status_code=500,
            detail="generated image is blank (all-black); check model weights and CUDA setup"
        )

    # 5. Encode to base64 PNG
    buf = io.BytesIO()
    image.save(buf, format="PNG")
    b64 = base64.b64encode(buf.getvalue()).decode("ascii")

    elapsed = time.monotonic() - t_start
    logger.info(json.dumps({
        "event":     "generation_done",
        "model":     MODEL_ID,
        "size":      f"{width}x{height}",
        "elapsed_s": round(elapsed, 2),
    }))

    return {"created": int(time.time()), "data": [{"b64_json": b64}]}
```

### Step 1.9 — /v1/images/edits placeholder

Phase 1 implements T2I only. Add a 501 stub so the endpoint exists but clearly refuses requests:

```python
@app.post("/v1/images/edits")
async def create_image_edit(authorization: Optional[str] = Header(None)):
    require_auth(authorization)
    raise HTTPException(
        status_code=501,
        detail="image edits not implemented in phase 1; use /v1/images/generations"
    )
```

### Step 1.10 — Startup entry point

Add at the bottom of `app.py`:

```python
if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8008, log_level="info")
```

### Step 1.11 — Verify app.py is complete

Before moving on, review `app.py` against this checklist:

- [ ] `os.environ["device"] = DEVICE` appears before any Boogu import
- [ ] `config_loaded` JSON log is emitted at startup (before lifespan)
- [ ] `OFFLOAD_MODE` read from env; `load_pipeline` applies it after `from_pretrained`
- [ ] `GPU_PROFILE` is logged for test/production environment visibility
- [ ] `state["busy"]` exists and is checked before acquiring the lock
- [ ] `inference_executor = ThreadPoolExecutor(max_workers=1)` declared at module level
- [ ] `_sync_generate` is a plain synchronous function (no async keywords)
- [ ] `lifespan` starts model loading in a background thread
- [ ] `/livez` returns 200 without auth and without waiting for model
- [ ] `/readyz` returns 503 while loading, 200 when ready
- [ ] `/v1/models` default auth behavior is implemented and later verified against image2api account-test/probe behavior
- [ ] Generation endpoint checks `state["busy"] or inference_lock.locked()` before acquiring lock
- [ ] Generation endpoint rejects `n != 1` with 400
- [ ] Generation endpoint accepts `quality` but does not map it to 2K/HD in phase 1
- [ ] Inference uses `asyncio.wait_for(asyncio.shield(loop.run_in_executor(...)), timeout=420)`
- [ ] Timeout path sets `ready=false`, `status="timed_out_restarting"`, logs `inference_timeout_restart`, and schedules `os._exit(124)`
- [ ] `asyncio.timeout` is NOT used (Python 3.11+ only, and wait_for is the right choice)
- [ ] Output validation checks dimensions and non-black
- [ ] `generation_done` JSON log emitted with `elapsed_s` after each success
- [ ] `/v1/images/edits` returns 501
- [ ] Torch compile is NOT used anywhere in the file

---

## Phase 2: Write Dockerfile

### Step 2.1 — Create Dockerfile

Write `deploy/boogu-openai/Dockerfile`:

```dockerfile
FROM nvidia/cuda:12.6.3-cudnn-devel-ubuntu22.04

ENV DEBIAN_FRONTEND=noninteractive

ARG PYTHON_VERSION=3.11.13

# System dependencies. Ubuntu 22.04's python3.11 package is 3.11.0rc1,
# so build a stable CPython 3.11.x runtime inside the CUDA image.
RUN apt-get update && apt-get install -y \
    build-essential \
    ca-certificates \
    curl \
    git \
    libbz2-dev \
    libffi-dev \
    liblzma-dev \
    libncursesw5-dev \
    libreadline-dev \
    libsqlite3-dev \
    libssl-dev \
    tk-dev \
    uuid-dev \
    wget \
    xz-utils \
    zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSLO "https://www.python.org/ftp/python/${PYTHON_VERSION}/Python-${PYTHON_VERSION}.tgz" \
    && tar -xzf "Python-${PYTHON_VERSION}.tgz" \
    && cd "Python-${PYTHON_VERSION}" \
    && ./configure --enable-shared --with-ensurepip=install \
    && make -j"$(nproc)" \
    && make altinstall \
    && cd / \
    && rm -rf "Python-${PYTHON_VERSION}" "Python-${PYTHON_VERSION}.tgz" \
    && ldconfig

RUN ln -sf /usr/local/bin/python3.11 /usr/bin/python3.11 \
    && ln -sf /usr/local/bin/python3.11 /usr/bin/python3 \
    && ln -sf /usr/local/bin/python3.11 /usr/bin/python \
    && python3.11 -m pip install --upgrade pip setuptools wheel

# Pin Boogu source to the reviewed commit for reproducible builds.
ARG BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba
RUN git clone https://github.com/boogu-project/Boogu-Image.git /opt/Boogu-Image \
    && cd /opt/Boogu-Image \
    && git checkout ${BOOGU_GIT_REF}

# Install Boogu's PyTorch + CUDA 12.6 dependencies
ARG BOOGU_TORCH_REQUIREMENTS=torch2.7-cu126.txt
RUN python3.11 -m pip install -r "/opt/Boogu-Image/requirements/${BOOGU_TORCH_REQUIREMENTS}"

# Install Boogu package
RUN python3.11 -m pip install -e /opt/Boogu-Image

# Install wrapper dependencies from the copied source-of-truth file
WORKDIR /app
COPY requirements.txt /app/
RUN python3.11 -m pip install -r /app/requirements.txt

# Flash Attention. Default production builds use Boogu's helper. Local
# RTX 4060 validation can set BOOGU_INSTALL_FLASH_ATTN=source to compile
# against the exact Python/Torch/CUDA/SM combination in the image.
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
            echo "Skipping flash-attn install; Boogu will use fallback attention/SwiGLU paths."; \
            ;; \
        *) \
            echo "Invalid BOOGU_INSTALL_FLASH_ATTN=${BOOGU_INSTALL_FLASH_ATTN}. Use true, helper, source, or false." >&2; \
            exit 1; \
            ;; \
    esac

# HuggingFace cache — persisted via named volume at runtime
ENV HF_HOME=/cache/hf

COPY app.py /app/

EXPOSE 8008

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8008", "--log-level", "info"]
```

### Step 2.2 — Notes on Dockerfile

- Python 3.11 is required; build a stable CPython 3.11.x runtime because Ubuntu 22.04's `python3.11` package is `3.11.0rc1`, which is not a good baseline for native CUDA extension debugging.
- `BOOGU_GIT_REF` defaults to the reviewed commit `df9a219fccd8954df7cf16e71453c19f7a72dbba`. Override only after a new Boogu revision is reviewed.
- `BOOGU_TORCH_REQUIREMENTS` defaults to `torch2.7-cu126.txt`. If Python 3.11.13 still reproduces a native crash, the next Boogu-provided CUDA 12.6 matrix to test is `torch2.11-cu126.txt`.
- Flash Attention uses Boogu's helper by default. For local RTX 4060 debugging, set `BOOGU_INSTALL_FLASH_ATTN=source`, `FLASH_ATTN_VERSION=2.8.3`, `TORCH_CUDA_ARCH_LIST=8.9`, and `MAX_JOBS=1` so the extension is compiled against the exact local Python/Torch/CUDA/SM combination.
- Wrapper dependencies are installed from `/app/requirements.txt`. Keep this file as the source of truth instead of duplicating dependency versions in Dockerfile commands.
- Model weights are NOT in the image. They are mounted at runtime via volume.
- `HF_HOME=/cache/hf` persists tokenizer and module caches across restarts via the named volume declared in compose.

---

## Phase 3: Write Dependency and README Files

### Step 3.1 — Create requirements.txt

Write `deploy/boogu-openai/requirements.txt`. This is the source of truth for wrapper-only dependencies and is installed by the Dockerfile. Boogu's ML stack is installed directly from the cloned repo in the Dockerfile.

```text
fastapi==0.115.0
uvicorn[standard]==0.30.6
pydantic==2.9.0
numpy>=1.24.0
```

All versions are pinned to prevent unexpected behavior from floating upgrades.

### Step 3.2 — Create .env.example

Write `deploy/boogu-openai/.env.example`:

```bash
# Copy this file to .env and set a strong key before deploying
BOOGU_API_KEY=sk-local-boogu-change-me
```

Operators must copy this to `.env` and set a real key before starting the service.

### Step 3.3 — Create README.md

Write `deploy/boogu-openai/README.md`:

```markdown
# boogu-openai

Standalone Docker service wrapping Boogu-Image as an OpenAI-compatible image generation API.

## Quick start

1. Copy `.env.example` to `.env` and set `BOOGU_API_KEY`.
2. Download model weights: `bash download_models.sh`.
3. Build and start: `docker compose -f docker-compose.boogu.yml up -d --build`.
4. Wait for readiness: `curl http://127.0.0.1:8008/readyz`.

## Reference

See `IMPLEMENTATION_STEPS.md` for the full step-by-step guide.
```

---

## Phase 4: Write Docker Compose

### Step 4.1 — Confirm image2api network name

Before writing the compose file, run this on the deployment host to find the actual Docker network name created by image2api:

```bash
docker network ls | grep image2api
```

Common values: `image2api_default`, `gateway_default`. Note the exact name for the next step.

### Step 4.2 — Create docker-compose.boogu.yml

Write `deploy/boogu-openai/docker-compose.boogu.yml`. Replace `image2api_default` with the actual network name found in step 4.1.

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
      BOOGU_GPU_PROFILE: production-high-vram  # test-8gb for RTX 4060 test environment
      BOOGU_OFFLOAD_MODE: none   # none (24GB+) | model (16GB) | sequential (12GB)
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
          - boogu-openai     # explicit alias for stable DNS across compose project names
    healthcheck:
      test:
        - CMD
        - python3
        - -c
        - "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8008/readyz', timeout=5).read()"
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
    # GPU option B — fallback for older environments
    # If option A fails (docker compose config error), comment out the deploy
    # block above and uncomment the line below:
    # gpus: all
    restart: unless-stopped

volumes:
  boogu-cache:

networks:
  boogu-net:
    driver: bridge
  image2api-net:
    external: true
    name: image2api_default   # update to match your actual image2api network name
```

### Step 4.3 — Key configuration notes

- `env_file: .env` injects `BOOGU_API_KEY` from the local `.env` file without hardcoding it.
- `BOOGU_GPU_PROFILE` is an operational label. Use `test-8gb` with `BOOGU_OFFLOAD_MODE=sequential` on RTX 4060, and `production-high-vram` with `BOOGU_OFFLOAD_MODE=none` on RTX 5090 or better.
- `HF_HOME: /cache/hf` ensures HuggingFace tokenizer and module caches persist across restarts via the `boogu-cache` named volume.
- Port is bound to `127.0.0.1:8008` only — never exposed publicly.
- `start_period: 300s` gives the container 5 minutes before healthcheck failures count. Model cold start commonly takes 60-180 seconds.

---

## Phase 5: Write Model Download Script

### Step 5.1 — Create download_models.sh

Write `deploy/boogu-openai/download_models.sh`:

```bash
#!/usr/bin/env bash
set -e

MODEL_DIR="/opt/boogu/models/Boogu-Image-0.1-Turbo"

echo "=== Boogu-Image model weight download ==="

# Create directory and set ownership
sudo mkdir -p /opt/boogu/models
sudo chown -R "$USER":"$USER" /opt/boogu

# Check if already downloaded
if [ -f "$MODEL_DIR/model_index.json" ]; then
    echo "Model weights already present at $MODEL_DIR"
    exit 0
fi

echo "Downloading from ModelScope (primary for domestic servers)..."
pip install modelscope -q

modelscope download \
    --model Boogu/Boogu-Image-0.1-Turbo \
    --local_dir "$MODEL_DIR"

# Verify download
if [ ! -f "$MODEL_DIR/model_index.json" ]; then
    echo "ERROR: model_index.json not found. Download may have failed."
    echo "Try Hugging Face as fallback:"
    echo "  pip install -U 'huggingface_hub[cli]'"
    echo "  huggingface-cli download Boogu/Boogu-Image-0.1-Turbo --local-dir $MODEL_DIR"
    exit 1
fi

echo "Model weights ready at $MODEL_DIR"
```

Make the script executable:

```bash
chmod +x deploy/boogu-openai/download_models.sh
```

---

## Phase 6: Write .dockerignore

### Step 6.1 — Create .dockerignore

Write `deploy/boogu-openai/.dockerignore`:

```text
__pycache__/
*.pyc
*.pyo
*.pyd
.Python
.env
!.env.example
*.log
.git
.gitignore
README.md
IMPLEMENTATION_STEPS.md
download_models.sh
```

This keeps the Docker build context small. Model weights, logs, and secrets never enter the build context.

---

## Phase 7: Host Prerequisite Checks

These steps run on the deployment host before building the image. Do not skip them.

### Step 7.1 — Verify host GPU driver

```bash
nvidia-smi
```

Expected output: shows GPU model, driver version (535+), and CUDA version. If this fails, the NVIDIA driver is not installed correctly.

### Step 7.2 — Verify Docker GPU mapping

```bash
docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu22.04 nvidia-smi
```

Expected output: same GPU information as step 7.1, but from inside a container.

If this fails, install NVIDIA Container Toolkit:

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

Then re-run step 7.2 to confirm.

### Step 7.3 — Download model weights

```bash
cd /path/to/image2api
bash deploy/boogu-openai/download_models.sh
```

Verify:

```bash
ls -la /opt/boogu/models/Boogu-Image-0.1-Turbo/model_index.json
```

### Step 7.4 — Confirm image2api Docker network name

```bash
docker network ls | grep image2api
```

Note the exact network name. Update the `name:` field in `docker-compose.boogu.yml` Section `image2api-net` if it differs from `image2api_default`.

### Step 7.5 — Create .env file

```bash
cd deploy/boogu-openai
cp .env.example .env
# Edit .env and set a strong API key
nano .env
```

---

## Phase 8: Build and Isolated Validation

### Step 8.1 — Build the Docker image

```bash
cd deploy/boogu-openai
docker compose -f docker-compose.boogu.yml build
```

Expected duration: 20-40 minutes on first build (Flash Attention source compilation takes the most time if no prebuilt wheel is found). Subsequent builds use layer cache and are faster.

Watch for these key log lines indicating success:

```text
Successfully installed flash-attn-...
Successfully installed boogu-...
Successfully built <image-id>
```

### Step 8.2 — Start the standalone service

```bash
docker compose -f docker-compose.boogu.yml up -d
```

### Step 8.3 — Monitor startup

```bash
# Follow logs
docker compose -f docker-compose.boogu.yml logs -f boogu-openai
```

Expected log sequence:

```text
{"event":"config_loaded","model_id":"boogu-image-0.1-turbo","device":"cuda:0",...}
{"event":"model_load_start","path":"/models/Boogu-Image-0.1-Turbo","kind":"turbo"}
{"event":"model_load_done"}
{"event":"warmup_start"}
{"event":"warmup_done"}
```

### Step 8.4 — Wait for readiness

```bash
# Poll /readyz until 200 is returned
until curl -sf http://127.0.0.1:8008/readyz; do
  echo "Waiting for model to be ready..."
  sleep 10
done
echo "Model is ready"
```

### Step 8.5 — Test generation directly

Before running curl tests, export the API key from your `.env` file:

```bash
export BOOGU_API_KEY=$(grep BOOGU_API_KEY deploy/boogu-openai/.env | cut -d= -f2)
```

Then test:

```bash
curl -s -X POST http://127.0.0.1:8008/v1/images/generations \
  -H "Authorization: Bearer ${BOOGU_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "boogu-image-0.1-turbo",
    "prompt": "A cinematic photo of a mountain lake at sunrise",
    "size": "1024x1024",
    "n": 1
  }' \
  | python3 -c "
import sys, json, base64
data = json.load(sys.stdin)
b64 = data['data'][0]['b64_json']
with open('/tmp/boogu_test.png', 'wb') as f:
    f.write(base64.b64decode(b64))
print('Saved to /tmp/boogu_test.png')
print(f'Response size: {len(b64)} chars (base64)')
"
```

### Step 8.6 — Validate output image

```bash
python3 -c "
from PIL import Image
import numpy as np
img = Image.open('/tmp/boogu_test.png')
arr = np.array(img)
print(f'Dimensions: {img.width}x{img.height}')
print(f'Mode: {img.mode}')
print(f'Max pixel value: {arr.max()}')
assert img.width == 1024 and img.height == 1024, f'Wrong size: {img.width}x{img.height}'
assert arr.max() > 0, 'Image is all-black!'
print('PASS: output image is valid')
"
```

### Step 8.7 — Test error cases

```bash
# Model list with valid API key should return 200
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer ${BOOGU_API_KEY}" \
  http://127.0.0.1:8008/v1/models
# Expected: 200

# Model list with invalid API key should return 401 by default
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer wrong-key" \
  http://127.0.0.1:8008/v1/models
# Expected: 401

# Invalid API key should return 401
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://127.0.0.1:8008/v1/images/generations \
  -H "Authorization: Bearer wrong-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"boogu-image-0.1-turbo","prompt":"test","size":"1024x1024"}'
# Expected: 401

# Invalid size should return 400
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://127.0.0.1:8008/v1/images/generations \
  -H "Authorization: Bearer ${BOOGU_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"boogu-image-0.1-turbo","prompt":"test","size":"9999x9999"}'
# Expected: 400
```

### Step 8.8 — Check generation log output

```bash
docker compose -f docker-compose.boogu.yml logs boogu-openai | grep generation_done
```

Expected:

```json
{"event":"generation_done","model":"boogu-image-0.1-turbo","size":"1024x1024","elapsed_s":3.45}
```

Note the `elapsed_s` value. This is the p1 latency baseline for Turbo 1K.

---

## Phase 9: image2api Integration

### Step 9.1 — Verify network reachability from backend container

Before configuring anything in the admin panel, confirm the `boogu-openai` container is reachable from the `backend` container by service name:

```bash
# Find the backend container name
docker ps | grep backend

# Test connectivity from inside the backend container
docker exec <backend_container_name> \
  wget -qO- http://boogu-openai:8008/readyz
```

Expected output: `{"ok":true,"status":"ready",...}`

Also verify the current image2api account-test/probe implementation before enabling the account. If it calls upstream `/v1/models`, confirm it sends the configured upstream key. If it does not send auth, either adjust image2api's probe to include auth or make only the wrapper's `/v1/models` endpoint optional/no-auth.

If this fails, check that:
1. The `image2api-net` network name in `docker-compose.boogu.yml` matches the actual network name
2. Both containers are on the same network: `docker network inspect image2api_default`

### Step 9.2 — Add managed model in image2api admin panel

Navigate to admin model management and create:

```
id:                  boogu-image-0.1-turbo
name:                Boogu Image 0.1 Turbo
type:                image
provider:            custom
upstream_model:      boogu-image-0.1-turbo
enabled:             true
ratios:              1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3, 2:1
resolutions:         1K only
max_reference_images: 0
reference_mode:      none
image_to_image:      false
```

Do not enable 2K or higher for phase 1.

### Step 9.3 — Add upstream account in image2api admin panel

Navigate to admin accounts/upstreams and create:

```
adapter_type:   openai
base_url:       http://boogu-openai:8008
key:            <same value as BOOGU_API_KEY in your .env file>
served models:  boogu-image-0.1-turbo
weight:         0
concurrency:    1
status:         active
```

Two critical details:
- `base_url` must NOT include `/v1`. The Go custom client appends `/v1/images/...` automatically.
- `served models` must be explicitly set. Do not leave empty.

### Step 9.4 — Run admin test generation

In the image2api admin panel, run a test generation for model `boogu-image-0.1-turbo`.

Confirm:
- Generation succeeds and returns an image
- The generation log entry shows `provider: custom` and the boogu upstream account id
- No unexpected error or timeout

### Step 9.5 — Test via user playground

Log in as a regular user and generate an image using `boogu-image-0.1-turbo` from the playground.

Confirm:
- Generation succeeds
- Credits are deducted correctly
- Image appears in the user's media gallery

### Step 9.6 — Test via external API key

```bash
curl -X POST https://YOUR_IMAGE2API_DOMAIN/v1/images/generations \
  -H "Authorization: Bearer YOUR_USER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "boogu-image-0.1-turbo",
    "prompt": "A cinematic photo of a mountain lake at sunrise",
    "size": "1024x1024"
  }'
```

Confirm the full round-trip works through the gateway.

---

## Phase 10: Full Validation Checklist

Work through this checklist item by item. All items must pass before declaring the deployment complete.

### Host validation

- [ ] `nvidia-smi` shows GPU and driver version
- [ ] `docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu22.04 nvidia-smi` succeeds
- [ ] `/opt/boogu/models/Boogu-Image-0.1-Turbo/model_index.json` exists
- [ ] `docker network ls | grep image2api` shows expected network name
- [ ] `.env` file exists with non-default `BOOGU_API_KEY`
- [ ] `deploy/boogu-openai/README.md` contains the quick start and points to `IMPLEMENTATION_STEPS.md`

### Wrapper container validation

- [ ] Docker image builds without errors
- [ ] `docker compose -f docker-compose.boogu.yml run --rm boogu-openai nvidia-smi` shows GPU
- [ ] `curl http://127.0.0.1:8008/livez` returns 200 immediately after container start
- [ ] `curl http://127.0.0.1:8008/readyz` returns 503 while loading
- [ ] `curl http://127.0.0.1:8008/readyz` returns 200 after model load completes
- [ ] `/v1/models` auth behavior has been verified against the current image2api account-test/probe path
- [ ] `GET /v1/models` with correct API key returns model id
- [ ] `GET /v1/models` with wrong API key returns 401
- [ ] `POST /v1/images/generations` with valid request returns base64 image
- [ ] Generated image decodes to PNG with correct dimensions
- [ ] Generated image is not all-black (pixel max > 0)
- [ ] `quality` values such as `standard`, `hd`, `low`, `medium`, or `high` are accepted and do not alter phase 1 output size
- [ ] Invalid size parameter returns 400
- [ ] Second concurrent request returns 503 "model busy"
- [ ] Container logs show JSON-formatted generation_done events with elapsed_s

### image2api integration validation

- [ ] `docker exec <backend> wget -qO- http://boogu-openai:8008/readyz` succeeds
- [ ] Managed model `boogu-image-0.1-turbo` is present and enabled
- [ ] Upstream account base_url is `http://boogu-openai:8008` (no /v1 suffix)
- [ ] Upstream account served_models is explicitly set to `boogu-image-0.1-turbo`
- [ ] Upstream account concurrency is `1`
- [ ] Admin test generation succeeds
- [ ] Generation log shows correct provider (custom) and account association
- [ ] User playground generation succeeds
- [ ] External API key generation via `/v1/images/generations` succeeds
- [ ] Credit billing works correctly (charge on success, refund on failure)

### Operational validation

- [ ] Stopping boogu-openai service does not affect image2api availability
- [ ] Disabling the upstream account in admin panel stops Boogu routing
- [ ] Re-enabling the account resumes Boogu routing
- [ ] Rollback (disable account + stop service) requires no database changes

---

## Quick Reference: Common Commands

```bash
# Build image
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml build

# Start service
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml up -d

# View logs
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml logs -f boogu-openai

# Check readiness
curl http://127.0.0.1:8008/readyz

# Stop service
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml stop

# Rebuild after code change (no downtime on other services)
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml up -d --build --no-deps boogu-openai

# Test GPU inside container
docker compose -f deploy/boogu-openai/docker-compose.boogu.yml run --rm boogu-openai nvidia-smi
```

---

## Critical Reminders for the Implementing Engineer

| Item | Detail |
|---|---|
| `os.environ["device"]` | Must appear before ANY Boogu module import in app.py |
| `config_loaded` log | Must be emitted at module level before lifespan — Phase 8.3 verification requires it |
| Python version | Python **3.11** — do not revert to 3.10 |
| Async inference | Use `asyncio.wait_for(asyncio.shield(loop.run_in_executor(...)), timeout=420)` — do NOT call pipeline directly inside the async endpoint |
| `asyncio.timeout` | Do NOT use — it is Python 3.11+ only and less portable than `wait_for` |
| Torch compile | Do NOT enable in phase 1 — known black-image risk |
| `BOOGU_OFFLOAD_MODE` | Set per hardware: `none` (24GB+), `model` (16GB), `sequential` (12GB). RTX 4060 needs `sequential` |
| `BOOGU_GPU_PROFILE` | Use `test-8gb` for RTX 4060 testing and `production-high-vram` for RTX 5090 or better |
| `BOOGU_GIT_REF` | Use reviewed commit `df9a219fccd8954df7cf16e71453c19f7a72dbba` unless a newer Boogu commit is explicitly reviewed |
| `/v1/models` auth | Keep authenticated by default; verify whether the current image2api probe sends the upstream key before enabling the account |
| `quality` field | Accept for OpenAI compatibility but ignore in phase 1; do not use it to select 2K/HD output |
| `requirements.txt` | Dockerfile must copy and install from this file; do not duplicate wrapper dependency versions in two places |
| `README.md` | Must contain the quick start; do not leave the file empty |
| Timeout behavior | On inference timeout, return 504, mark wrapper not ready, log `inference_timeout_restart`, then exit with `os._exit(124)` so Docker restarts |
| Flash Attention | Use Boogu's helper `utils/get_flash_attn.py` by default; for local RTX 4060 native-extension debugging, use source compilation with an explicit `FLASH_ATTN_VERSION` and `TORCH_CUDA_ARCH_LIST=8.9` |
| base_url in admin | Must be `http://boogu-openai:8008` — no trailing `/v1` |
| served_models field | Must be explicitly set to `boogu-image-0.1-turbo` — never leave empty |
| Network name | Verify with `docker network ls`; update `name:` in compose before deploying |
| Network alias | `aliases: [boogu-openai]` on `image2api-net` ensures stable DNS across compose project names |
| GPU option | Try `deploy.resources` first; fall back to `gpus: all` if compose rejects it |
| Model cold start | `start_period: 300s` is normal — do not reduce |
| Phase 3 access | Opens to all users after wrapper validation and image2api integration validation pass; keep account concurrency at `1` |
| `/v1/images/edits` | Returns 501 in phase 1 — do not implement until T2I is stable |
| API key in tests | All curl commands use `${BOOGU_API_KEY}` — never hardcode the default key |

---

*Reference: BOOGU_IMAGE_DEPLOYMENT_PLAN.md v0.5. If the design changes, update both documents accordingly.*
