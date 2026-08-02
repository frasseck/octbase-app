// Octbase SPA — boot-time environment constants.
//
// Split out of config.js on 2026-07-30 (37b stage 2). Same load-order reason as
// delegation.js, and the same fix: api.js and http.js read BASE_PATH AT LOAD
// TIME, while config.js imports api.js (loadFeatureConfig calls it). That is a
// cycle — config.js -> api.js -> config.js — so under ESM's depth-first
// evaluation, entering config.js runs api.js's body first and BASE_PATH is
// still in its temporal dead zone when api.js reads it. A runtime
// ReferenceError at boot, invisible to every build-time check.
//
// THIS FILE IMPORTS NOTHING and must stay that way. Everything here derives
// from window.location alone, so it can always be evaluated first; config.js
// keeps what genuinely needs the API (FEATURES, LIMITS, loadFeatureConfig).
// The split is also the honest one: these are facts about where the page is
// running, not configuration fetched from a server.

const URL_PARAMS = new URLSearchParams(window.location.search);
// DEV_CONTEXT: the page runs from disk (standalone demo, Playwright) or a
// loopback host (local stacks/previews). URL overrides that redirect traffic
// (?apiBase=…) are honored only here — on a deployed origin a crafted link
// could otherwise point the app (and the login form's credentials) at a
// foreign server.
const DEV_CONTEXT = window.location.protocol === 'file:'
  || ['localhost', '127.0.0.1', '[::1]', '::1'].includes(window.location.hostname);
const API_BASE = (DEV_CONTEXT && URL_PARAMS.get('apiBase')) || (window.location.protocol === 'file:'
  ? 'http://127.0.0.1:8000'
  : window.location.origin);
const BASE_PATH = '/api/v1';
// Standalone demo mode: when the SPA is opened directly from disk (file://),
// it auto-signs-in as the seeded demo user to obtain a real JWT. The backend is
// JWT-only, so this replaces the legacy X-User-Id header approach.
const USE_STANDALONE_DEMO_AUTH = window.location.protocol === 'file:';
const DEMO_EMAIL = 'demo@octbase.dev';
const DEMO_PASSWORD = 'demopass1234';

// STATUS_META / PRIORITY_META / TYPE_META and the derived STATUSES / PRIORITIES /
// TASK_TYPES live in @octbase/shared/meta.js, which the files that need them
// import directly (37b stage 3).

export { API_BASE, BASE_PATH, DEMO_EMAIL, DEMO_PASSWORD, URL_PARAMS, USE_STANDALONE_DEMO_AUTH };
