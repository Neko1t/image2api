# Lunixai Default Brand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all runtime Vivid defaults with Lunixai, ship the supplied standalone transparent logo, and migrate only persisted legacy default titles.

**Architecture:** Keep the existing `/admin/api/site` request and reactive `site` store. The shared `Logo.vue` becomes the single default-image adapter, while uploaded logos remain higher-priority layout overrides. Bootstrap changes defaults and applies a targeted idempotent database update for legacy title values.

**Tech Stack:** Vue 3 SFC, Vite, Go, GORM/PostgreSQL, Python/pytest static contract tests, PNG/RGBA asset processing.

---

### Task 1: Add the failing runtime-brand contract

**Files:**
- Create: `frontend/tests/test_brand_defaults.py`
- Test: `frontend/tests/test_brand_defaults.py`

- [ ] **Step 1: Write the failing contract test**

Create tests that read runtime files and assert:

```python
def test_frontend_runtime_defaults_are_lunixai():
    assert "title: 'Lunixai'" in read("src/site.js")
    assert "site.title || 'Lunixai'" in read("src/main.js")
    assert "加入 Lunixai,开始 AI 生图" in read("src/components/LoginModal.vue")
    assert '/lunixai-logo.png' in read("src/components/Logo.vue")
    assert '/lunixai-logo.png' in read("index.html")


def test_backend_runtime_defaults_and_migration_are_lunixai():
    seed = read_repo("backend/internal/bootstrap/seed.go")
    assert '{Key: "site.title", Value: "Lunixai"}' in seed
    assert 'WHERE key = ? AND value IN ?' in seed
    assert '[]string{"Vivid", "Vivid AI"}' in seed
    assert 'envString("APP_TITLE", "Lunixai")' in read_repo("backend/internal/config/config.go")
    assert 'title = "Lunixai"' in read_repo("backend/internal/service/app_settings.go")
    assert 'subject = "Lunixai 邮箱验证码"' in read_repo("backend/internal/service/smtp.go")
```

Also parse the PNG IHDR with Python's `struct` module and assert it is square RGBA.

- [ ] **Step 2: Run the contract to verify it fails**

Run:

```powershell
python -m pytest frontend/tests/test_brand_defaults.py -q
```

Expected: FAIL because Lunixai defaults and `frontend/public/lunixai-logo.png` do not exist yet.

- [ ] **Step 3: Commit the red contract with the later implementation**

Do not commit a permanently failing test alone; keep it in the working tree through Tasks 2-4.

### Task 2: Produce and integrate the default logo asset

**Files:**
- Create: `frontend/public/lunixai-logo.png`
- Modify: `frontend/src/components/Logo.vue`
- Modify: `frontend/index.html`
- Modify: `frontend/src/site.js`

- [ ] **Step 1: Crop the supplied RGBA source deterministically**

Use Pillow to crop `(302, 85, 722, 505)` from `C:/Users/20895/Downloads/20260711-141014-VFYRBSDI.png`, resize the 420x420 crop to 512x512 with Lanczos resampling, and save it losslessly as `frontend/public/lunixai-logo.png`. This centered crop contains the upper standalone mark and excludes the lower lockup.

- [ ] **Step 2: Validate the asset before consuming it**

Check that the output is 512x512 RGBA, has transparent corners, contains opaque subject pixels, and has no nontransparent pixels caused by the lower lockup.

- [ ] **Step 3: Replace the shared code-native default Logo**

Change `Logo.vue` to retain its `size` prop and render:

```vue
<img
  src="/lunixai-logo.png"
  alt=""
  :width="size"
  :height="size"
  class="shrink-0 object-contain"
/>
```

- [ ] **Step 4: Switch favicon defaults**

Change `index.html` to use `/lunixai-logo.png`, and change `applyFavicon()` in `site.js` so an empty custom logo falls back to the same asset.

### Task 3: Replace frontend runtime brand defaults

**Files:**
- Modify: `frontend/src/site.js`
- Modify: `frontend/src/main.js`
- Modify: `frontend/src/components/LoginModal.vue`
- Modify: `frontend/src/views/ConfigView.vue`
- Modify: `frontend/src/layouts/PublicLayout.vue`

- [ ] **Step 1: Change synchronous frontend fallbacks**

Set `site.title` and the `main.js` browser-title fallback to `Lunixai`; set the initial HTML title to `Lunixai 首页`.

- [ ] **Step 2: Update visible authentication copy**

Change the register subtitle to `加入 Lunixai,开始 AI 生图`. Keep login and password-reset copy unchanged.

- [ ] **Step 3: Update admin hints and stale code comments**

Change the site-title hint and placeholder in `ConfigView.vue` from Vivid to Lunixai. Update comments that describe the old Vivid wordmark without changing behavior.

- [ ] **Step 4: Run the frontend portion of the contract**

Run:

```powershell
python -m pytest frontend/tests/test_brand_defaults.py -q -k "frontend or logo_asset"
```

Expected: frontend and PNG assertions PASS; backend assertions are deselected until Task 4.

### Task 4: Replace backend defaults and migrate legacy persisted titles

**Files:**
- Modify: `backend/internal/bootstrap/seed.go`
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/service/app_settings.go`
- Modify: `backend/internal/service/smtp.go`

- [ ] **Step 1: Update new-install defaults and empty-value fallbacks**

Change `site.title`, `APP_TITLE`, site-title email fallback, and SMTP fallback subject to Lunixai.

- [ ] **Step 2: Add the targeted idempotent migration**

After inserting missing defaults, execute:

```go
if err := db.WithContext(ctx).Model(&model.SiteSetting{}).
    Where("key = ? AND value IN ?", "site.title", []string{"Vivid", "Vivid AI"}).
    Update("value", "Lunixai").Error; err != nil {
    return err
}
```

This updates only legacy default values and preserves every other title.

- [ ] **Step 3: Format and run contract tests**

Run:

```powershell
gofmt -w backend/internal/bootstrap/seed.go backend/internal/config/config.go backend/internal/service/app_settings.go backend/internal/service/smtp.go
python -m pytest frontend/tests/test_brand_defaults.py -q
```

Expected: all brand contract tests PASS.

### Task 5: Full verification and delivery

**Files:**
- Verify all files changed in Tasks 1-4.

- [ ] **Step 1: Build the frontend**

Run `npm run build` in `frontend` and expect exit code 0.

- [ ] **Step 2: Run backend tests**

Run `go test ./...` in `backend` and expect exit code 0.

- [ ] **Step 3: Scan runtime source for old visible defaults**

Run an `rg` scan for `Vivid|Vivid AI` across runtime frontend/backend files. Remaining matches must be limited to migration literals, compatibility identifiers, contact details, or intentionally unchanged repository documentation.

- [ ] **Step 4: Inspect and render the asset**

Open `frontend/public/lunixai-logo.png` and verify the crop visually. Start the frontend dev server and use browser screenshots at desktop and mobile widths to verify the login modal, public rail, admin rail, and config Logo preview in light and dark themes.

- [ ] **Step 5: Check diff quality**

Run `git diff --check` and inspect `git diff --stat` and `git status --short`.

- [ ] **Step 6: Commit the implementation**

Stage only the brand contract, Lunixai asset, and runtime frontend/backend changes. Commit with:

```powershell
git commit -m "feat: replace default brand with Lunixai"
```
