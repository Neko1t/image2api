#!/usr/bin/env python3
"""
Boogu-Image OpenAI-compatible API wrapper.
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
from typing import Any

import numpy as np
import torch
from fastapi import FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(message)s")
logger = logging.getLogger("boogu")

MODEL_ID = os.getenv("BOOGU_MODEL_ID", "boogu-image-0.1-turbo")
MODEL_PATH = os.getenv("BOOGU_MODEL_PATH", "/models/Boogu-Image-0.1-Turbo")
MODEL_KIND = os.getenv("BOOGU_MODEL_KIND", "turbo").lower()
DEVICE = os.getenv("BOOGU_DEVICE", "cuda:0")
API_KEY = os.getenv("BOOGU_API_KEY", "")
WARMUP = os.getenv("BOOGU_WARMUP", "true").lower() == "true"
OFFLOAD_MODE = os.getenv("BOOGU_OFFLOAD_MODE", "none").lower()
GPU_PROFILE = os.getenv("BOOGU_GPU_PROFILE", "production-high-vram")
MAX_SIZE_RAW = os.getenv("BOOGU_MAX_SIZE", "1024x1024")
DISABLE_TRITON_LAYER_NORM = (
    os.getenv("BOOGU_DISABLE_TRITON_LAYER_NORM", "false").lower() == "true"
)

# Boogu modules read this environment variable at import/use time.
os.environ["device"] = DEVICE


def _parse_max_size(raw: str) -> tuple[int, int]:
    try:
        w_raw, h_raw = raw.lower().split("x", 1)
        width = int(w_raw)
        height = int(h_raw)
    except Exception:
        logger.warning(
            json.dumps(
                {
                    "event": "invalid_max_size",
                    "value": raw,
                    "fallback": "1024x1024",
                }
            )
        )
        return 1024, 1024
    if width <= 0 or height <= 0:
        logger.warning(
            json.dumps(
                {
                    "event": "invalid_max_size",
                    "value": raw,
                    "fallback": "1024x1024",
                }
            )
        )
        return 1024, 1024
    return width, height


MAX_WIDTH, MAX_HEIGHT = _parse_max_size(MAX_SIZE_RAW)

logger.info(
    json.dumps(
        {
            "event": "config_loaded",
            "model_id": MODEL_ID,
            "model_kind": MODEL_KIND,
            "model_path": MODEL_PATH,
            "device": DEVICE,
            "gpu_profile": GPU_PROFILE,
            "offload_mode": OFFLOAD_MODE,
            "max_size": f"{MAX_WIDTH}x{MAX_HEIGHT}",
            "warmup": WARMUP,
            "disable_triton_layer_norm": DISABLE_TRITON_LAYER_NORM,
            "timeout_s": 420,
        }
    )
)

state: dict[str, Any] = {
    "ready": False,
    "status": "loading",
    "pipe": None,
    "load_error": "",
    "busy": False,
}

inference_lock = asyncio.Lock()
inference_executor = ThreadPoolExecutor(max_workers=1)


class ImageGenerationRequest(BaseModel):
    model: str
    prompt: str
    n: int = 1
    size: str = "1024x1024"
    quality: str | None = None
    seed: int | None = None


class ImageGenerationResponse(BaseModel):
    created: int
    data: list[dict[str, str]]


def require_auth(authorization: str | None = Header(None)) -> None:
    if not API_KEY:
        raise HTTPException(status_code=500, detail="BOOGU_API_KEY is not configured")
    if authorization != f"Bearer {API_KEY}":
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
    if width > MAX_WIDTH or height > MAX_HEIGHT:
        raise HTTPException(
            status_code=400,
            detail=f"phase 1 supports sizes up to {MAX_WIDTH}x{MAX_HEIGHT}",
        )

    width = (width // 16) * 16
    height = (height // 16) * 16
    if width <= 0 or height <= 0:
        raise HTTPException(status_code=400, detail="size must be at least 16x16")
    return width, height


def patch_boogu_triton_layer_norm() -> None:
    if not DISABLE_TRITON_LAYER_NORM:
        return

    from boogu.ops.triton import layer_norm

    def _check_fallback_args(return_dropout_mask, out, residual_out) -> None:
        if return_dropout_mask:
            raise RuntimeError(
                "BOOGU_DISABLE_TRITON_LAYER_NORM does not support dropout masks"
            )
        if out is not None or residual_out is not None:
            raise RuntimeError(
                "BOOGU_DISABLE_TRITON_LAYER_NORM does not support explicit output buffers"
            )

    def layer_norm_fn_fallback(
        x,
        weight,
        bias,
        residual=None,
        x1=None,
        weight1=None,
        bias1=None,
        eps=1e-6,
        dropout_p=0.0,
        rowscale=None,
        prenorm=False,
        residual_in_fp32=False,
        zero_centered_weight=False,
        is_rms_norm=False,
        return_dropout_mask=False,
        out=None,
        residual_out=None,
    ):
        _check_fallback_args(return_dropout_mask, out, residual_out)
        ref = layer_norm.rms_norm_ref if is_rms_norm else layer_norm.layer_norm_ref
        return ref(
            x,
            weight,
            bias,
            residual=residual,
            x1=x1,
            weight1=weight1,
            bias1=bias1,
            eps=eps,
            dropout_p=dropout_p,
            rowscale=rowscale,
            prenorm=prenorm,
            zero_centered_weight=zero_centered_weight,
            upcast=residual_in_fp32,
        )

    def rms_norm_fn_fallback(
        x,
        weight,
        bias,
        residual=None,
        x1=None,
        weight1=None,
        bias1=None,
        eps=1e-6,
        dropout_p=0.0,
        rowscale=None,
        prenorm=False,
        residual_in_fp32=False,
        zero_centered_weight=False,
        return_dropout_mask=False,
        out=None,
        residual_out=None,
    ):
        _check_fallback_args(return_dropout_mask, out, residual_out)
        return layer_norm.rms_norm_ref(
            x,
            weight,
            bias,
            residual=residual,
            x1=x1,
            weight1=weight1,
            bias1=bias1,
            eps=eps,
            dropout_p=dropout_p,
            rowscale=rowscale,
            prenorm=prenorm,
            zero_centered_weight=zero_centered_weight,
            upcast=residual_in_fp32,
        )

    layer_norm.layer_norm_fn = layer_norm_fn_fallback
    layer_norm.rms_norm_fn = rms_norm_fn_fallback
    logger.info(json.dumps({"event": "boogu_triton_layer_norm_disabled"}))


def load_pipeline():
    logger.info(
        json.dumps({"event": "model_load_start", "path": MODEL_PATH, "kind": MODEL_KIND})
    )

    patch_boogu_triton_layer_norm()

    if MODEL_KIND == "turbo":
        from boogu.pipelines.boogu.pipeline_boogu_turbo import BooguImageTurboPipeline

        pipe = BooguImageTurboPipeline.from_pretrained(
            MODEL_PATH,
            torch_dtype=torch.bfloat16,
            trust_remote_code=True,
        )
    elif MODEL_KIND in {"base", "standard"}:
        from boogu.pipelines.boogu.pipeline_boogu import BooguImagePipeline

        pipe = BooguImagePipeline.from_pretrained(
            MODEL_PATH,
            torch_dtype=torch.bfloat16,
            trust_remote_code=True,
        )
    else:
        raise ValueError(f"unsupported BOOGU_MODEL_KIND: {MODEL_KIND}")

    if OFFLOAD_MODE == "sequential":
        pipe.enable_sequential_cpu_offload_flag = True
        pipe.enable_sequential_cpu_offload(device=DEVICE)
        logger.info(json.dumps({"event": "offload_mode", "mode": "sequential"}))
    elif OFFLOAD_MODE == "model":
        pipe.enable_model_cpu_offload_flag = True
        pipe.enable_model_cpu_offload(device=DEVICE)
        logger.info(json.dumps({"event": "offload_mode", "mode": "model"}))
    elif OFFLOAD_MODE == "none":
        pipe.to(DEVICE)
        logger.info(json.dumps({"event": "offload_mode", "mode": "none"}))
    else:
        raise ValueError(f"unsupported BOOGU_OFFLOAD_MODE: {OFFLOAD_MODE}")

    logger.info(json.dumps({"event": "model_load_done"}))
    return pipe


def _sync_generate(prompt, width, height, seed, model_kind, pipe, device):
    generator = torch.Generator(device).manual_seed(seed)
    kwargs = dict(
        height=height,
        width=width,
        generator=generator,
        negative_instruction="",
        empty_instruction="",
        image_guidance_scale=1.0,
        empty_instruction_guidance_scale=0.0,
        device=device,
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


def run_warmup(pipe) -> None:
    logger.info(json.dumps({"event": "warmup_start"}))
    image = _sync_generate(
        "warmup",
        min(512, MAX_WIDTH),
        min(512, MAX_HEIGHT),
        42,
        MODEL_KIND,
        pipe,
        DEVICE,
    )
    if np.array(image).max() == 0:
        raise RuntimeError("warmup produced an all-black image")
    logger.info(json.dumps({"event": "warmup_done"}))


@asynccontextmanager
async def lifespan(app: FastAPI):
    def _load() -> None:
        try:
            pipe = load_pipeline()
            if WARMUP:
                run_warmup(pipe)
            state["pipe"] = pipe
            state["ready"] = True
            state["status"] = "ready"
        except Exception as exc:
            state["ready"] = False
            state["status"] = "failed"
            state["load_error"] = str(exc)
            logger.error(
                json.dumps({"event": "model_load_failed", "error": str(exc)})
            )

    threading.Thread(target=_load, daemon=True).start()
    yield
    inference_executor.shutdown(wait=False, cancel_futures=True)


app = FastAPI(title="Boogu-Image OpenAI API", lifespan=lifespan)


@app.get("/livez")
async def livez():
    return {"ok": True, "status": "alive"}


@app.get("/readyz")
async def readyz():
    if state["ready"]:
        return {
            "ok": True,
            "status": "ready",
            "model": MODEL_ID,
            "device": DEVICE,
        }
    if state["status"] == "failed":
        return JSONResponse(
            status_code=503,
            content={
                "ok": False,
                "status": "failed",
                "error": state["load_error"],
            },
        )
    return JSONResponse(
        status_code=503,
        content={"ok": False, "status": state["status"]},
    )


@app.get("/health")
async def health():
    return await readyz()


@app.get("/v1/models")
async def list_models(authorization: str | None = Header(None)):
    require_auth(authorization)
    return {
        "object": "list",
        "data": [{"id": MODEL_ID, "object": "model", "owned_by": "boogu-local"}],
    }


@app.post("/v1/images/generations", response_model=ImageGenerationResponse)
async def create_image(
    request: ImageGenerationRequest,
    authorization: str | None = Header(None),
):
    require_auth(authorization)

    if not state["ready"]:
        raise HTTPException(status_code=503, detail="model not ready")
    if request.model != MODEL_ID:
        raise HTTPException(status_code=404, detail=f"model {request.model!r} not found")
    if not request.prompt.strip():
        raise HTTPException(status_code=400, detail="prompt must not be empty")
    if request.n != 1:
        raise HTTPException(status_code=400, detail="phase 1 supports n=1 only")
    if state["busy"] or inference_lock.locked():
        raise HTTPException(status_code=503, detail="model busy, please retry")

    width, height = parse_size(request.size)
    seed = request.seed if request.seed is not None else int(time.time())
    t_start = time.monotonic()

    async with inference_lock:
        state["busy"] = True
        loop = asyncio.get_running_loop()
        future = loop.run_in_executor(
            inference_executor,
            _sync_generate,
            request.prompt,
            width,
            height,
            seed,
            MODEL_KIND,
            state["pipe"],
            DEVICE,
        )
        try:
            image = await asyncio.wait_for(
                asyncio.shield(future),
                timeout=420,
            )
        except asyncio.TimeoutError:
            state["ready"] = False
            state["status"] = "timed_out_restarting"
            logger.error(
                json.dumps(
                    {
                        "event": "inference_timeout_restart",
                        "model": MODEL_ID,
                        "size": f"{width}x{height}",
                        "elapsed_s": round(time.monotonic() - t_start, 2),
                    }
                )
            )
            threading.Timer(1.0, lambda: os._exit(124)).start()
            raise HTTPException(
                status_code=504,
                detail="inference timeout; wrapper restarting",
            )
        except Exception as exc:
            logger.error(
                json.dumps({"event": "inference_error", "error": str(exc)})
            )
            raise HTTPException(status_code=500, detail=f"inference failed: {exc}")
        finally:
            if state["status"] != "timed_out_restarting":
                state["busy"] = False

    if image.width != width or image.height != height:
        raise HTTPException(
            status_code=500,
            detail=f"generated image has unexpected dimensions {image.width}x{image.height}",
        )

    arr = np.array(image)
    if arr.max() == 0:
        raise HTTPException(
            status_code=500,
            detail="generated image is blank (all-black); check model weights and CUDA setup",
        )

    buf = io.BytesIO()
    image.save(buf, format="PNG")
    b64 = base64.b64encode(buf.getvalue()).decode("ascii")

    elapsed = time.monotonic() - t_start
    logger.info(
        json.dumps(
            {
                "event": "generation_done",
                "model": MODEL_ID,
                "size": f"{width}x{height}",
                "elapsed_s": round(elapsed, 2),
            }
        )
    )

    return {"created": int(time.time()), "data": [{"b64_json": b64}]}


@app.post("/v1/images/edits")
async def create_image_edit(authorization: str | None = Header(None)):
    require_auth(authorization)
    raise HTTPException(
        status_code=501,
        detail="image edits not implemented in phase 1; use /v1/images/generations",
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8008, log_level="info")
