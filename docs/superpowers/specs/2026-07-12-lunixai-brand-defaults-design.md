# Lunixai Default Brand Design

## Goal

Replace the project's runtime default brand from Vivid/Vivid AI to Lunixai while preserving the existing asynchronous site-settings flow and admin override capability.

## Scope

- Crop the upper standalone mark from `C:/Users/20895/Downloads/20260711-141014-VFYRBSDI.png` into a square transparent PNG.
- Store the default asset as `frontend/public/lunixai-logo.png`.
- Make the shared `Logo.vue` component render the Lunixai PNG while retaining its existing `size` API.
- Use the Lunixai asset as the default favicon.
- Change runtime frontend fallback titles and login/register copy from Vivid to Lunixai.
- Change backend site-title seed values and email/config fallbacks from Vivid/Vivid AI to Lunixai.
- Migrate persisted `site.title` values only when they still equal the legacy defaults `Vivid` or `Vivid AI`.
- Preserve admin-configured non-legacy site titles and uploaded custom logos.

Repository documentation describing the upstream project or its historical hosted instance is out of scope. Package/database/session identifiers are also out of scope because they are internal compatibility surfaces rather than visible branding.

## Asset Processing

The source is a 1024x1024 RGBA PNG. The upper standalone mark is centered around x=345..690 and y=127..463, with a surrounding translucent glow. Create a centered square crop with enough padding to preserve that glow, retain the existing alpha channel, and avoid including any pixels from the lower horizontal lockup.

The resulting project asset must:

- remain RGBA PNG;
- have transparent corners;
- contain only the upper standalone mark;
- remain legible at 32, 40, 44, and 64 CSS pixels;
- avoid resampling more than necessary.

## Runtime Data Flow

`site.js` continues to initialize synchronously with Lunixai defaults and then calls the existing `/admin/api/site` endpoint. Vue reactivity continues to update consumers after the response arrives.

- Empty or unavailable site settings show Lunixai immediately.
- A non-legacy admin-configured title still overrides Lunixai.
- An uploaded custom logo still overrides the shared default Logo component.
- An empty logo setting falls back to `lunixai-logo.png`.

No new API request, store, or loading state is introduced.

## Persisted Settings

Changing seed values alone does not affect an existing database. Bootstrap therefore performs an idempotent targeted update:

- `site.title = Vivid` becomes `Lunixai`;
- `site.title = Vivid AI` becomes `Lunixai`;
- every other stored title remains unchanged.

The migration does not rewrite `site.logo`: the legacy default logo was code-native and the persisted default is empty. Existing uploaded custom logos remain intentional overrides.

## Frontend Surfaces

Update the following visible defaults:

- initial HTML title and favicon;
- `site.title` and browser-title fallback;
- login/register modal Logo and `Join Lunixai` copy;
- public/admin layout default Logo through `Logo.vue`;
- admin site-settings default-title hints and placeholders.

Contact information such as the existing support email is not changed without separate replacement values.

## Backend Surfaces

Update:

- bootstrap `site.title` default;
- application-title fallback;
- site-title fallback used by the site service;
- verification-email subject fallbacks;
- the targeted legacy-title migration.

## Verification

- Confirm the cropped asset has an alpha channel, transparent corners, and no lower lockup pixels.
- Run frontend production build.
- Run relevant Go tests, followed by the backend test suite if practical.
- Search runtime source for remaining visible `Vivid`/`Vivid AI` fallbacks and classify any intentional leftovers.
- Verify clean light/dark rendering at desktop and mobile widths, including login modal, public rail, admin rail, favicon, and system-config Logo preview.
- Verify an empty site setting uses Lunixai and a non-legacy custom title/logo still overrides it.
