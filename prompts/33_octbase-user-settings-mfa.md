Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) modular monolith with a build-free vanilla-JS app frontend (`octbase-frontend/`) and a phone-first static SPA (`octbase-mobile/`), both served by their own Caddy front door and sharing JS byte-for-byte via `octbase-shared/`. This prompt introduces a **personal settings dashboard**: a self-service page where a logged-in user picks their preferred language and theme, and can enable/disable TOTP-based multi-factor authentication (MFA) on their own account.

The **overriding requirement is two separate backend modules, not one grab-bag "settings" package.** Preferences (language/theme) and MFA are unrelated in risk profile and lifecycle — one is a cosmetic per-user setting, the other is an auth/crypto surface. Split them:

- **`internal/dashboard`** — owns per-user preferences (language, theme) and their self-service endpoint. Cosmetic, low-risk, no crypto.
- **`internal/security/mfa`** — owns TOTP enrollment, verification, disable, and recovery codes. Nest it under a new `internal/security` umbrella (not directly under `internal/`) so future security-adjacent features (password change, session/device management, audit-grade auth events) have a natural home next to it without polluting `internal/auth` or `internal/identityaccess`.

Both are genuinely new bounded contexts (this repo's convention is one package per bounded context under `internal/`, see `docs/architecture.md`), not extensions of `identityaccess` or `auth` — but MFA has one required integration point into `internal/auth`'s login handler (see §2), and the dashboard follows the same self-service-endpoint shape `internal/notifications` already established.

Three clarifications that set the priorities:

- **MFA is greenfield — there is nothing to build on.** An exhaustive grep across the whole repo for `mfa`/`totp`/`2fa`/`otp`/`authenticator` found zero hits. Auth today is bcrypt password + JWT access token + rotating refresh token, entirely in `internal/auth`. You are adding the concept of a second factor from scratch, including choosing and vetting a TOTP dependency (there is none in `go.mod` today — `pquerna/otp` is a reasonable, widely-used choice; confirm license compatibility before adding it).
- **The login-flow MFA challenge must stay stateless, per `docs/architecture.md`'s JWT-only auth model.** Do not add a server-side "pending MFA session" table or in-memory session store. Instead, when `mfa_enabled=true`, `POST /api/v1/auth/login` returns a short-lived, narrowly-scoped signed token (same signing mechanism as the existing JWT, but with a distinct claim, e.g. `purpose: "mfa_challenge"`, a few minutes' TTL, and no access-token privileges) instead of the normal access/refresh pair. A new `POST /api/v1/auth/mfa/verify` endpoint accepts that challenge token + a TOTP/recovery code and, on success, issues the real access/refresh pair exactly like today's login does. This keeps the whole auth path stateless and horizontally scalable, consistent with the existing model — flag it explicitly in `docs/architecture.md` as an addition to the auth section rather than a silent deviation.
- **The seeded demo superuser must never have MFA turned on.** `internal/seed/seed.go` seeds a fixed superuser (ID `00000000-0000-0000-0000-000000000001`) that the frontend, mobile app, and a large share of the Playwright/pytest suite log in as directly. MFA must default to disabled for every user (`mfa_enabled=false` at the DB default level), and the seed step must set it explicitly and document *why* — so nobody "fixes" this later by flipping it on and silently breaks every dev/test login. This is a dev-environment-only concern; it does not imply anything about whether admins should be encouraged/required to use MFA in a real deployment (out of scope here — no forced-MFA policy in this pass).

## Current state (read before designing)

- **User model.** Two structs project one `users` table: `identityaccess.User` (`domain.go:5-11`: `ID, Email, DisplayName, CreatedAt, UpdatedAt`) and `usermgmt.ManagedUser` (`domain.go:8-17`, adds `GlobalRole, Status, LastLoginAt`). The table is built across `migrations/001_initial.up.sql` (base columns), `003_auth.up.sql` (`password_hash, is_active, is_admin`), `009_rbac.up.sql` (`global_role, status, last_login_at`). Latest migration is `022_search_trgm_indexes`; next number is `023`.
- **Self-service endpoint precedent — copy this shape for both new modules.** `internal/notifications` implements `GET`/`PATCH /api/v1/users/me/notification-preferences` (`handler.go:25-26`) backed by its own small per-user table (`notification_preferences`, `user_id`+`kind` composite key, `004_notifications.up.sql:14-20`), gated only by `shared.GetUserID(r)` — no admin/role check, since it's the calling user's own data. `internal/dashboard`'s preferences endpoint and `internal/security/mfa`'s endpoints should follow this exact pattern: own migration, own small table(s), handler reads `shared.GetUserID(r)`, no cross-user access.
- **Theme (frontend, client-only today).** `octbase-frontend/js/framework.js:607-639` — `THEME_KEY='octbase-theme'`, four options cycled by one topbar button: `system → light → dark → octopus` (yes, an Easter-egg fourth theme — preserve it as a real option in the new selector, don't silently drop it). `theme-init.js` duplicates the read-and-apply logic so it can run synchronously before CSS loads (flash-of-wrong-theme guard) — any change to how/where the preference is sourced must keep this pre-CSS synchronous read working. CSS keyed on `[data-theme=...]` in `app.css:102-315`. **Mobile has no theme support at all today** — this is a net-new capability there.
- **Language (frontend, client-only today).** `octbase-frontend/js/i18n.js:5-19` — `AVAILABLE_LOCALES=['en','de']`, `STORAGE_KEY='octbase.lang'`, `detectInitialLocale()` falls back to `navigator.language` then `'en'`. A working `<select>` already exists (`renderLangLinks()`, `framework.js:4-9`) used on login and in the in-app topbar — reuse this component inside the new settings page rather than building a second one. Locale strings already anticipate a settings page: a `"settings"` namespace exists in `octbase-frontend/locales/en.json:409-416` (`settings.language`, `settings.languageSelector`, `settings.languages.en/de`).
- **No settings page exists yet.** No `views-settings.js`, no `/settings` route. Per `octbase-frontend/js/README.md`'s load order, a new module slots in after `views-crud.js`. Routing precedent: the `/admin` branch in `router.js:66-67` (`if (path === '/admin') { await renderAdminPanel(); return; }`) — a `/settings` branch follows the same shape. The natural nav slot is the currently link-less `#sidebar-user` block (avatar/name/logout, `framework.js:178-183`), which has no profile/settings entry today. `octbase-mobile/js/app.js:867` has a language toggle in a bottom sheet already but nothing else — extend it rather than replacing it.
- **OpenAPI parity is enforced.** `internal/apicontract/openapi_parity_test.go`'s `TestEveryRouteIsDocumented` fails the build if any live route isn't a path key in `octbase-api/api/openapi.yaml`. Every new route from both modules needs an entry there; use the `/api/v1/users/me/notification-preferences` block (`openapi.yaml:794-805`) as the template, with a distinct `tags: [Dashboard]` / `tags: [MFA]`.
- **No password-change endpoint exists at all** — noted for context (MFA disable should require re-verifying identity somehow; since there's no password-change flow to model against, requiring either the current password or a valid TOTP/recovery code at disable-time is the two viable options — pick one and document it, see §2).

## Goal

Ship, in this order:

1. **`internal/dashboard`** — per-user preferences module (language, theme), self-service endpoint, migration.
2. **`internal/security/mfa`** — TOTP enrollment/verification/disable/recovery-codes module, migration, and the one required integration point into `internal/auth`'s login handler via a stateless challenge token.
3. **Dev-environment default**: seeded superuser has MFA off by default, explicitly set and commented in `internal/seed/seed.go`.
4. **Frontend settings page** (`octbase-frontend`): language + theme selectors backed by the dashboard endpoint (reconciled with existing localStorage caching), and an MFA panel (enroll/QR/confirm/recovery-codes/disable) backed by the mfa module; plus the login-page change to handle the new MFA-challenge step.
5. **Mobile mirror** (`octbase-mobile`): same three settings, including first-time theme support there.

---

### 0. `internal/dashboard` — preferences module

- Migration `023_user_preferences`: a `user_preferences` table (`user_id` PK, FK to `users`, `language`, `theme`, `updated_at`), mirroring the `notification_preferences` shape rather than widening the core `users` migrations further.
- `GET`/`PATCH /api/v1/users/me/preferences`. `PATCH` validates `language` against `AVAILABLE_LOCALES` (`en`,`de`) and `theme` against the four existing frontend values (`system`,`light`,`dark`,`octopus`) so backend and frontend enums can never drift silently — reject with a stable error code (`INVALID_PREFERENCE_VALUE`) rather than silently clamping.
- Row auto-creates with defaults (`language='en'`, `theme='system'`) on first read if absent, so existing users without a row aren't a special case in the handler.
- Add the OpenAPI entry and an integration-style handler test (real chi + Postgres, per `internal/testutil` convention — no mocks).

### 1. `internal/security/mfa` — MFA module

- Migration `024_mfa`: `mfa_enabled boolean not null default false` on `users`, plus a `mfa_credentials` table (`user_id` PK, encrypted TOTP secret — encrypt at rest, do not store plaintext; document the encryption approach and where the key comes from, e.g. an env-configured key analogous to how other secrets are handled in this repo) and an `mfa_recovery_codes` table (`user_id`, `code_hash`, `used_at nullable`) for one-time recovery codes.
- Endpoints, all self-service under `users/me`:
  - `POST /api/v1/users/me/mfa/enroll` — generates a secret + `otpauth://` URI (for client-side QR rendering — no external QR service call, render the QR client-side from the URI), not yet active.
  - `POST /api/v1/users/me/mfa/confirm` — verifies a submitted TOTP code against the pending secret; on success flips `mfa_enabled=true` and returns a one-time batch of recovery codes (shown once, never retrievable again — only their hashes are stored).
  - `POST /api/v1/users/me/mfa/disable` — requires either the current password or a valid TOTP/recovery code in the request body (pick one as the primary path and document the choice; do not allow a bare toggle with only the existing session, since a stolen access token would otherwise be enough to strip MFA protection).
  - `POST /api/v1/users/me/mfa/recovery-codes/regenerate` — invalidates old codes, issues a fresh batch (same re-auth requirement as disable).
- Stable error codes: `MFA_REQUIRED`, `MFA_CODE_INVALID`, `MFA_ALREADY_ENABLED`, `MFA_NOT_ENABLED`, `MFA_RECOVERY_CODE_INVALID`.
- `activity.Write(...)` on enable/disable (state changes worth surfacing in the Activity view); preference changes from §0 are not activity-log-worthy (cosmetic).

### 2. Login-flow integration (the one touch point into `internal/auth`)

- In the existing `POST /api/v1/auth/login` handler: after password verification succeeds, check `mfa_enabled`. If true, **do not** issue the normal access/refresh pair — instead issue the short-lived signed `mfa_challenge` token described above (few minutes' TTL, a distinct claim so it cannot be used as a bearer token against any other route) and return it with a `MFA_REQUIRED`-shaped response the frontend can recognize.
- New `POST /api/v1/auth/mfa/verify` (public route, alongside `login`/`refresh` in the JWT-middleware exception list) accepts `{challenge_token, code}` (code = TOTP or recovery code), validates the challenge token's signature/claim/expiry, validates the code, and on success issues the real access/refresh pair exactly like a normal login — consuming a recovery code if that's what was used.
- No new server-side state anywhere in this flow — the challenge token *is* the state, self-contained and verifiable statelessly. Add a short note to `docs/architecture.md`'s auth section describing this addition.

### 3. Dev-environment default

- In `internal/seed/seed.go`, explicitly set the seeded superuser's `mfa_enabled=false` (matching the column default, but set it explicitly with a one-line comment explaining why: the demo/dev superuser is depended on for direct login by the frontend, mobile app, and most of the Playwright/pytest suite, and must never require a second factor locally).
- Do not add any global "require MFA for admins" policy in this pass — MFA stays fully opt-in per account.

### 4. Frontend settings page (`octbase-frontend`)

- New `views-settings.js` (dashboard/preferences: language + theme UI) — slots into the load order after `views-crud.js`, registered in `bootstrap.js`, new `/settings` branch in `router.js` alongside the `/admin` one, nav link added to `#sidebar-user` (`framework.js:178-183`).
- Keep the MFA UI in its own module, `views-mfa.js`, loaded alongside `views-settings.js` and rendered as a section within the same settings page — mirrors the backend's dashboard/mfa split so the two stay independently touchable (e.g., someone changing theme options shouldn't need to read TOTP code).
- Preferences: on login, fetch `GET users/me/preferences` and reconcile with localStorage — server value wins, then cache locally so `theme-init.js`'s pre-CSS synchronous read still works before any network round-trip resolves (server round-trip updates the cache for *next* load, it can't block first paint). Replace the topbar's cycle-only theme button with (or supplement it with) a proper `<select>` inside the settings page listing all four themes; reuse the existing language `<select>` component.
- MFA panel: enroll button → QR (rendered client-side from the `otpauth://` URI, no external service) + manual-entry secret fallback → code-confirmation input → one-time recovery-codes display with a clear "save these now" warning → enabled-state view with "disable" (re-auth modal) and "regenerate recovery codes" actions.
- Login page: handle the `MFA_REQUIRED` response by showing a second step (code input, referencing the challenge token) before falling through to the normal post-login redirect.

### 5. Mobile mirror (`octbase-mobile`)

- Extend the existing language bottom-sheet (`js/app.js:867`) into a proper settings page/section; add theme support for the first time (new capability there — reuse the same four-theme model and `data-theme` CSS approach conceptually, sharing constants via `octbase-shared` where it makes sense); add an MFA panel with the same enroll/confirm/disable flow; handle the login MFA-challenge step.

### 6. i18n

- New strings in both `en.json`/`de.json` for both frontends: settings page chrome, all four theme names, MFA panel copy (enroll, QR instructions, recovery codes warning, disable confirmation), and the login MFA-challenge step. Reuse the existing `settings.*` namespace already present in `octbase-frontend/locales/en.json:409-416` rather than inventing a parallel one.

### 7. OpenAPI, CHANGELOG, tests

- Add every new route (`users/me/preferences`, `users/me/mfa/*`, `auth/mfa/verify`) to `octbase-api/api/openapi.yaml`; confirm `TestEveryRouteIsDocumented` stays green.
- `CHANGELOG.md` under `## Unreleased`: preferences under Added, MFA under both Added and **Security**.
- Integration-style handler tests for both new packages against real Postgres (`internal/testutil`), no mocks; run the `coverage` skill check before pushing (current floor 73.0%, must not regress).
- Run the `go-security` skill review specifically on `internal/security/mfa` and the `internal/auth` login change before merging — this is a new crypto/auth surface.
- Playwright/pytest coverage: settings page renders and persists language/theme across reload; full MFA enroll→confirm→login-with-code→disable round trip; seeded superuser still logs in without any MFA step (regression guard for §3); locale parity check for new keys.

---

**Deliverables:** `internal/dashboard` (preferences migration, self-service endpoint, tests); `internal/security/mfa` (migration, enroll/confirm/disable/regenerate endpoints, stateless login-challenge integration into `internal/auth`, tests); the explicit dev-mode superuser MFA-disabled seed default; `views-settings.js` + `views-mfa.js` in `octbase-frontend` with the `/settings` route, nav entry, and login-page MFA step; the mobile mirror in `octbase-mobile` including net-new theme support there; `openapi.yaml` entries; `en`/`de` strings in both frontends; `CHANGELOG.md` entries; and the `docs/architecture.md` note on the stateless MFA challenge token. **Constraints:** dashboard and MFA are two separate backend packages (MFA nested under a new `internal/security`), never one combined package; the MFA login challenge introduces no server-side session state; MFA disable always requires re-verification, never a bare toggle; the seeded demo superuser never has MFA enabled; no forced-MFA policy in this pass.
