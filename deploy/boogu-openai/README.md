# boogu-openai

Standalone Docker service wrapping Boogu-Image as an OpenAI-compatible image generation API for image2api's existing custom/upstream provider.

## Quick start

1. Copy `.env.example` to `.env` and set a strong `BOOGU_API_KEY`.
2. Download model weights: `bash download_models.sh`.
3. Build and start: `docker compose -f docker-compose.boogu.yml up -d --build`.
4. Wait for readiness: `curl http://127.0.0.1:8008/readyz`.

Use `BOOGU_GPU_PROFILE=test-8gb` with `BOOGU_OFFLOAD_MODE=sequential` on RTX 4060 test hosts. If WSL2/Triton crashes during the first denoise step in `boogu/ops/triton/layer_norm.py`, set `BOOGU_DISABLE_TRITON_LAYER_NORM=true` to use Boogu's PyTorch reference layer norm fallback. Use `BOOGU_GPU_PROFILE=production-high-vram` with `BOOGU_OFFLOAD_MODE=none` on RTX 5090 or higher VRAM production hosts.

For local RTX 4060 flash-attn validation, use `docker-compose.boogu.local.yml`. It builds Python 3.11.13 from source instead of Ubuntu 22.04's `python3.11.0rc1`, then compiles `flash-attn` from source for compute capability 8.9:

```powershell
docker compose `
  -f deploy/boogu-openai/docker-compose.boogu.yml `
  -f deploy/boogu-openai/docker-compose.boogu.local.yml `
  build --no-cache
```

The default Boogu dependency set is `BOOGU_TORCH_REQUIREMENTS=torch2.7-cu126.txt`. If Python 3.11.13 still reproduces a native crash in `flash_attn.ops.activations.swiglu`, the next matrix to test from the same pinned Boogu commit is `torch2.11-cu126.txt`.

Before starting the service, verify the native extension path inside a clean image:

```powershell
docker compose `
  -f deploy/boogu-openai/docker-compose.boogu.yml `
  -f deploy/boogu-openai/docker-compose.boogu.local.yml `
  run --rm --no-deps boogu-openai `
  python3.11 -X faulthandler -c "import sys, torch; import flash_attn, flash_attn_2_cuda; print(sys.version); print(torch.__version__); print(flash_attn.__version__)"

docker compose `
  -f deploy/boogu-openai/docker-compose.boogu.yml `
  -f deploy/boogu-openai/docker-compose.boogu.local.yml `
  run --rm --no-deps boogu-openai `
  python3.11 -X faulthandler -c "import torch; from flash_attn.ops.activations import swiglu; x=torch.randn(2,128,device='cuda',dtype=torch.bfloat16); y=torch.randn(2,128,device='cuda',dtype=torch.bfloat16); z=swiglu(x,y); torch.cuda.synchronize(); print(z.shape, z.dtype)"
```

Configure image2api with a custom upstream account:

- `base_url`: `http://boogu-openai:8008`
- `key`: same value as `BOOGU_API_KEY`
- `served models`: `boogu-image-0.1-turbo`
- `concurrency`: `1`

Do not include `/v1` in the upstream `base_url`; image2api appends `/v1/images/...` itself.

## Reference

See `IMPLEMENTATION_STEPS.md` for the full step-by-step guide and deployment validation checklist.
