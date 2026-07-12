import pathlib


ROOT = pathlib.Path(__file__).resolve().parents[2]
FRONTEND = ROOT / "frontend"
BACKEND = ROOT / "backend"


def read(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8")


def test_ycy_import_route_is_registered():
    router = read(BACKEND / "internal/http/router/router.go")

    assert 'authed.POST("/tokens/import-ycy-account", handlers.ProviderAdmin.ImportYCYAccount)' in router


def test_upstream_modal_uses_selected_ycy_format_for_submit():
    modal = read(FRONTEND / "src/components/UpstreamModal.vue")

    assert "const isYCYFormat = computed(() => adapterType.value === 'ycy')" in modal
    assert "isYCYFormat.value ? '/tokens/import-ycy-account' : '/tokens/import-custom-account'" in modal
    assert "adapter_type: adapterType.value" in modal


def test_accounts_view_passes_mode_and_edits_ycy_as_upstream():
    view = read(FRONTEND / "src/views/AccountsView.vue")

    assert "const upstreamMode = ref('custom')" in view
    assert "function isUpstreamAccount(a)" in view
    assert "a?.type === 'ycy'" in view
    assert ':mode="upstreamMode"' in view


def test_account_rows_expose_adapter_type():
    tokens = read(BACKEND / "internal/service/tokens.go")

    assert '"adapter_type":' in tokens
    assert 'stringValue(item.Meta["adapter_type"])' in tokens
