import { api } from './api.js';
import { URL_PARAMS } from './env.js';
import { S } from './state.js';

// ═══════════════════════════════════════════════════════════
// CONFIG
// ═══════════════════════════════════════════════════════════

// ── App version ──────────────────────────────────────────────────────────────
// The version string lives on S (S.appVersion, state.js) like all cross-file
// mutable app state. The backend is the source of truth: GET /api/v1/config
// carries the operator-configured OCTBASE_APP_VERSION and loadFeatureConfig
// (below) overwrites S.appVersion and the rendered "octbase X.Y" tag once it
// resolves. The pre-boot default "beta" (state.js) matches the backend's
// unstamped default, so a deployment without OCTBASE_APP_VERSION shows
// "octbase beta" rather than a version number.

// ── Optional-feature flags ─────────────────────────────────────────────────
// Which optional views/features are switched on. The backend is the source of
// truth: GET /api/v1/config (driven by OCTBASE_FEATURE_* env vars) is fetched
// once at boot and merged into this object (loadFeatureConfig, below). The values
// here are only the pre-boot defaults; everything that shows/hides a feature
// reads this object — never re-derives it. A URL param (?taskView=on|off,
// ?jiraCsvImport=on|off) lets the Playwright suite and previews force a flag
// without a backend change, mirroring the apiBase override pattern; the URL
// always wins. jiraCsvImport is edition-driven server-side (OCTBASE_EDITION:
// included in ENTERPRISE, an additional bookable option in BUSINESS via
// OCTBASE_OPTION_JIRA_IMPORT, never available in TEAM).
const FEATURES = { taskView: true, jiraCsvImport: true };

// ── Installation limits ──────────────────────────────────────────────────────
// Informational copies of the server-enforced limits from GET /api/v1/config
// (OCTBASE_MAX_USERS etc.; 0 = unlimited), merged in by loadFeatureConfig.
// Display-only — the backend rejects anything over a limit regardless.
const LIMITS = { maxUsers: 0, maxUploadMb: 0, maxUserStorageMb: 0 };

function applyFeatureOverrides() {
  for (const flag of Object.keys(FEATURES)) {
    const ov = URL_PARAMS.get(flag);
    if (ov === 'on'  || ov === 'true'  || ov === '1') FEATURES[flag] = true;
    if (ov === 'off' || ov === 'false' || ov === '0') FEATURES[flag] = false;
  }
}

// The /config response is a per-deployment constant, but boot asks for it twice
// on the login path: once for the pre-auth screens' version tag and again from
// initApp. Holding the in-flight/settled request lets the second caller reuse
// the first instead of paying another round trip. Only the fetch is shared —
// every call still applies the result to whatever DOM is currently rendered.
let _configRequest = null;

// loadFeatureConfig pulls the authoritative flags from the backend, then lets any
// URL override win. Failures keep the defaults so the app still boots offline.
async function loadFeatureConfig() {
  try {
    const cfg = await (_configRequest = _configRequest || api.config());
    for (const flag of Object.keys(FEATURES)) {
      if (cfg && cfg.features && typeof cfg.features[flag] === 'boolean') {
        FEATURES[flag] = cfg.features[flag];
      }
    }
    for (const key of Object.keys(LIMITS)) {
      if (cfg && cfg.limits && typeof cfg.limits[key] === 'number') {
        LIMITS[key] = cfg.limits[key];
      }
    }
    if (cfg && typeof cfg.version === 'string' && cfg.version) {
      S.appVersion = cfg.version;
      const versionEl = document.querySelector('.app-version');
      if (versionEl) versionEl.textContent = `octbase ${S.appVersion}`;
    }
  } catch {
    // Keep the defaults — and drop the shared request so a boot-time blip
    // doesn't pin the whole session to them; the next caller retries.
    _configRequest = null;
  }
  applyFeatureOverrides();
}

// ═══════════════════════════════════════════════════════════
// ICON SYSTEM — one coherent outline set (see docs/octbase-ui-styleguide.pdf)
// 24×24 viewBox, stroke-width 2, round caps/joins, currentColor. Emit every
// icon through icon(name) so one glyph means one concept across the whole app.
// ═══════════════════════════════════════════════════════════

export { FEATURES, LIMITS, loadFeatureConfig };
