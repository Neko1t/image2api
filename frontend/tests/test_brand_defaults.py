import pathlib
import struct


ROOT = pathlib.Path(__file__).resolve().parents[2]
FRONTEND = ROOT / "frontend"
BACKEND = ROOT / "backend"


def read(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8")


def test_frontend_runtime_defaults_are_lunixai():
    assert "title: 'Lunixai'" in read(FRONTEND / "src/site.js")
    assert "site.title || 'Lunixai'" in read(FRONTEND / "src/main.js")
    assert "加入 Lunixai,开始 AI 生图" in read(FRONTEND / "src/components/LoginModal.vue")
    assert '/lunixai-logo.png' in read(FRONTEND / "src/components/Logo.vue")
    assert '/lunixai-logo.png' in read(FRONTEND / "index.html")
    assert "Lunixai 首页" in read(FRONTEND / "index.html")


def test_logo_asset_is_square_rgba_png():
    data = (FRONTEND / "public/lunixai-logo.png").read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n"

    length = struct.unpack(">I", data[8:12])[0]
    assert data[12:16] == b"IHDR"
    assert length == 13
    width, height, bit_depth, color_type, _, _, _ = struct.unpack(">IIBBBBB", data[16:29])

    assert (width, height) == (512, 512)
    assert bit_depth == 8
    assert color_type == 6  # RGBA


def test_backend_runtime_defaults_and_legacy_migration_are_lunixai():
    seed = read(BACKEND / "internal/bootstrap/seed.go")
    assert '{Key: "site.title", Value: "Lunixai"}' in seed
    assert 'Where("key = ? AND value IN ?", "site.title", []string{"Vivid", "Vivid AI"})' in seed
    assert 'Update("value", "Lunixai")' in seed

    config = read(BACKEND / "internal/config/config.go")
    assert 'envString("APP_TITLE", "Lunixai")' in config
    assert "APP_TITLE=Lunixai" in read(BACKEND / ".env.example")
    assert "APP_TITLE: Lunixai" in read(ROOT / "docker-compose.yml")

    app_settings = read(BACKEND / "internal/service/app_settings.go")
    assert 'title = "Lunixai"' in app_settings

    smtp = read(BACKEND / "internal/service/smtp.go")
    assert 'subject = "Lunixai 邮箱验证码"' in smtp
