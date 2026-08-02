---
name: run-octbase
description: Build, run, and drive the whole Octbase stack (Go API + desktop web frontend + mobile SPA). Use when asked to start Octbase, bring up a stack, run the app, take a screenshot of the board/dashboard/mobile UI, smoke-test the API, or verify a change works end-to-end in the real running app (not just tests).
---

Octbase is three containers behind one Caddy front door: `octbase-api` (Go,
port 8000 internally), `octbase-frontend` (desktop SPA + reverse proxy to
`/api`, serves `octbase-mobile` under `/m/`), and `octbase-mobile` (phone SPA).
Drive it two ways: **`curl`** for the API, and **`.claude/skills/run-octbase/driver.py`**
(Playwright + system Chrome) for the desktop and mobile UIs. All paths below
are relative to the repo root.

Prefer reusing an already-running stack over starting a new one — see
`.claude/skills/dev-stack/SKILL.md` for what's normally already up.

## Prerequisites

Nothing to install if `octbase-frontend/tests/.venv` already exists (it ships
Playwright + `requests`) and `podman`/`podman-compose` are present. From a
clean machine:

```bash
sudo apt-get update && sudo apt-get install -y podman podman-compose
cd octbase-frontend/tests
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
```

System Google Chrome must be installed (`/usr/bin/google-chrome`) — Playwright's
bundled Chromium/Firefox do not install on this OS. `driver.py` launches Chrome
via `channel="chrome"`.

## Bring up the stack

```bash
cp .env.example .env   # first time only; edit ports if 8000/8080 collide
podman-compose up --build -d
```

Wait for it to actually serve, then confirm demo data is seeded:

```bash
timeout 60 bash -c 'until curl -sf http://localhost:8000/api/v1/health >/dev/null; do sleep 1; done'
curl -s -X POST http://localhost:8000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"demo@octbase.dev","password":"demopass1234"}'   # expect an accessToken
```

Substitute your stack's actual ports below (this session used the long-lived
`octbase_dev` stack: API on `8001`, frontend on `8081` — see `dev-stack` skill).
Tear down with `podman-compose down` (add `-v` to drop the DB volume too).

## Run (agent path)

### API — curl

```bash
API=http://127.0.0.1:8001   # adjust to your stack's API port

TOKEN=$(curl -s -X POST "$API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"demo@octbase.dev","password":"demopass1234"}' \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['accessToken'])")

curl -s "$API/api/v1/projects" -H "Authorization: Bearer $TOKEN"   # list

# write + delete round-trip
PID=$(curl -s -X POST "$API/api/v1/projects" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{"name":"smoke-test"}' \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "$API/api/v1/projects/$PID" \
  -H "Authorization: Bearer $TOKEN"   # expect 204
```

### Desktop + mobile UI — `driver.py`

```bash
octbase-frontend/tests/.venv/bin/python .claude/skills/run-octbase/driver.py desktop --ui-base http://127.0.0.1:8081
octbase-frontend/tests/.venv/bin/python .claude/skills/run-octbase/driver.py mobile  --ui-base http://127.0.0.1:8081
```

Each flow logs in as the demo user (`demo@octbase.dev` / `demopass1234`), lands on
the dashboard, drills into the Demo Project board, and screenshots both steps
to `/tmp/octbase-driver-shots/{mode}-dashboard.png` / `{mode}-board.png`
(override with `--out DIR`). It prints every browser console `error` and
exits non-zero if any were seen — treat that as "app broke," not "driver
broke," and check the actual message first (see Gotchas).

| driver.py mode | flow |
|---|---|
| `desktop` | login → dashboard → click "Demo Project" in the sidebar → board |
| `mobile`  | login (iPhone 13 emulation) → dashboard → tap Projects tab → tap Demo Project → board |

## Direct invocation (Go internals)

Most PRs touch `octbase-api/internal/...` directly — no browser needed:

```bash
cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
  go test ./internal/workmanagement -run TestCreateProject_OK -v
```

## Run (human path)

```bash
podman-compose up --build   # foreground; Ctrl-C to stop
# open http://localhost:8080/ (desktop) or emulate a phone UA for /m/
```

## Test

```bash
cd octbase-api && TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" go test ./...
cd octbase-frontend/tests && OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q
```

See the `testing` skill for details (single-test invocation, known flakes).

---

## Gotchas

- **`/m/` 302s away if the browser isn't phone-shaped.** Caddy device-routes
  by `User-Agent` (`octbase-frontend/caddy/Caddyfile` `@phoneEntry` /
  `@desktopOnMobile`): a non-phone UA hitting `/m/` gets redirected to `/`,
  and a phone UA hitting `/` gets redirected to `/m/`. `driver.py` handles
  this by launching the `mobile` mode in a `p.devices["iPhone 13"]` context —
  a plain `new_page()` will silently land on the desktop app instead.
- **Never pass `?apiBase=` to load the frontend cross-origin.** The app CSP is
  `connect-src 'self'` — a cross-origin `apiBase` gets every `fetch` blocked
  and the login form just spins. Load the frontend same-origin and let
  Caddy's `/api` reverse proxy do the routing (works for both `octbase_dev`
  on 8081 and `octbase` on 8080).
- **A JS file missing from the image serves as HTML, not as a 404.** The
  mobile Containerfile once listed `app.js core.js i18n.js meta.js` by hand
  and silently dropped `theme-init.js`/`qrcode.js` when they were added:
  requests for them 404'd, Caddy's SPA `try_files` fallback served
  `index.html` back with a `200`, and Chrome refused to execute HTML as a
  script (visible as two `console --errors` entries on every mobile page
  load). If `driver.py mobile` ever reports console errors again, check
  `curl -s -D- -o /dev/null http://127.0.0.1:8081/m/js/<file>.js` for a
  `text/html` content-type before assuming the driver is wrong. Since 37b
  stage 3 both images ship the **built `dist/`**, so the hand-maintained file
  list is gone — the bundler follows the entry's imports — but the symptom is
  worth remembering because it is what a broken build stage now looks like
  from the browser. Both images build from the **repository root** context
  (`podman build -f octbase-mobile/Containerfile .`).
- **Sidebar "Demo Project" text is ambiguous outside `#sidebar-nav`.** The
  dashboard's Recent Pages / My Projects panels also render the string "Demo
  Project" as a sublabel, so a bare `text=Demo Project` click can hit the
  wrong element or a strict-mode-violation. Scope the click to
  `#sidebar-nav .sidebar-item:has-text(...)`.
- **Mobile has no direct "board" nav tab.** The bottom nav is My Work /
  Projects / Search / Notifications — reach a board via Projects → tap the
  project row (`.row-card`, which routes straight to `/projects/{id}/board`).

## Troubleshooting

- **`playwright._impl._errors.TimeoutError` waiting for `#login-email`**: the
  stack isn't actually up yet, or you hit the wrong port — re-run the health
  curl from "Bring up the stack" first.
- **Login form spins forever, browser console shows a CSP `connect-src`
  violation**: you passed `?apiBase=` pointing at a different origin than the
  page was loaded from — drop it (see Gotchas).
- **`.venv/bin/pip: bad interpreter`**: stale venv shebang from a renamed
  checkout — `rm -rf octbase-frontend/tests/.venv` and recreate (see
  `frontend-testing` skill).
