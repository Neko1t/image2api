import ast
import pathlib
import re


ROOT = pathlib.Path(__file__).resolve().parents[1]
APP = ROOT / "app.py"
DOCKERFILE = ROOT / "Dockerfile"
COMPOSE = ROOT / "docker-compose.boogu.yml"
README = ROOT / "README.md"
ENV_EXAMPLE = ROOT / ".env.example"


def read(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8")


def test_app_exposes_openai_compatible_endpoints_and_contract():
    source = read(APP)
    assert '@app.get("/livez")' in source
    assert '@app.get("/readyz")' in source
    assert '@app.get("/health")' in source
    assert '@app.get("/v1/models")' in source
    assert re.search(r'@app\.post\("/v1/images/generations"', source)
    assert '@app.post("/v1/images/edits")' in source
    assert '"b64_json"' in source
    assert '"created"' in source
    assert "quality" in source


def test_app_serializes_inference_and_timeout_restart_contract():
    source = read(APP)
    assert "ThreadPoolExecutor(max_workers=1)" in source
    assert "asyncio.Lock()" in source
    assert 'state["busy"]' in source
    assert "run_in_executor" in source
    assert re.search(r"asyncio\.wait_for\(\s*asyncio\.shield\(", source, re.DOTALL)
    assert "timeout=420" in source
    assert "inference_timeout_restart" in source
    assert "os._exit(124)" in source


def test_app_sets_device_before_boogu_import_and_passes_device_to_offload():
    source = read(APP)
    device_pos = source.index('os.environ["device"] = DEVICE')
    boogu_pos = source.index("from boogu.pipelines.boogu")
    assert device_pos < boogu_pos
    assert "enable_sequential_cpu_offload(device=DEVICE)" in source
    assert "enable_model_cpu_offload(device=DEVICE)" in source


def test_size_limits_are_controlled_by_boogu_max_size_not_quality():
    source = read(APP)
    tree = ast.parse(source)
    parse_size = next(
        node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "parse_size"
    )
    parse_source = ast.get_source_segment(source, parse_size)
    assert "BOOGU_MAX_SIZE" in source
    assert "MAX_WIDTH" in parse_source
    assert "MAX_HEIGHT" in parse_source
    assert "quality" not in parse_source


def test_dockerfile_pins_cuda_python_boogu_and_installs_wrapper_requirements():
    source = read(DOCKERFILE)
    assert "FROM nvidia/cuda:12.6.3-cudnn-devel-ubuntu22.04" in source
    assert "python3.11" in source
    assert "ARG BOOGU_GIT_REF=df9a219fccd8954df7cf16e71453c19f7a72dbba" in source
    assert "ARG BOOGU_INSTALL_FLASH_ATTN=true" in source
    assert "COPY requirements.txt /app/" in source
    assert "pip install -r /app/requirements.txt" in source
    assert "/opt/Boogu-Image/utils/get_flash_attn.py" in source


def test_compose_keeps_weights_external_and_documents_gpu_profiles():
    source = read(COMPOSE)
    assert "/opt/boogu/models:/models:ro" in source
    assert "BOOGU_GPU_PROFILE: production-high-vram" in source
    assert "BOOGU_OFFLOAD_MODE: none" in source
    assert "127.0.0.1:8008:8008" in source
    assert "boogu-openai" in source


def test_readme_and_env_example_are_not_placeholders():
    assert "Quick start" in read(README)
    assert "IMPLEMENTATION_STEPS.md" in read(README)
    assert "BOOGU_API_KEY=" in read(ENV_EXAMPLE)
