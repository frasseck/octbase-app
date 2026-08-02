---
name: frontend-testing
description: Playwright-based frontend testing for Octbase on this system — covers browser setup, venv, and app e2e tests (octbase-frontend/tests). Use whenever asked to run, screenshot, or visually verify the frontend.
---

# Frontend testing on this system

## Critical: browser situation (Ubuntu 26.04)

Playwright's **bundled browsers will not install** on this OS:

```
ERROR: Playwright does not support chromium on ubuntu26.04-x64
ERROR: Playwright does not support firefox on ubuntu26.04-x64
```

**Use system Chrome instead** via `channel='chrome'`:
- Binary: `/usr/bin/google-chrome` (v149)
- Works with Playwright's `chromium.launch(channel='chrome', ...)`

Never use `OCTBASE_BROWSER=chromium` or `OCTBASE_BROWSER=firefox` — both will fail.
**Always use `OCTBASE_BROWSER=chrome`.**

---

## App frontend tests (`octbase-frontend/tests/`)

### Venv setup

The venv at `octbase-frontend/tests/.venv` may have broken shebangs pointing to
the old `/home/claude/taskbase/` path (repo was renamed). If `pip` or `pytest`
fail, recreate it:

```bash
cd /home/claude/dev.ocete.ch/octbase-frontend/tests
rm -rf .venv
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
```

Do not use `source .venv/bin/activate` — it causes the shell cwd to reset.
Call binaries directly via `.venv/bin/python`, `.venv/bin/pytest`.

### Running tests

```bash
cd /home/claude/dev.ocete.ch/octbase-frontend/tests

# Whole suite
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q

# One file
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest test_board.py -x -q

# One test
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest test_board.py::TestBoard::test_x -v
```

Tests skip cleanly if the API at `http://127.0.0.1:8000` is unreachable.
They log in as `demo@octbase.dev` / `demopass1234` — the API must run with `OCTBASE_DEMO_MODE=true`.

To target a different API:
```bash
OCTBASE_BROWSER=chrome OCTBASE_API_BASE=http://127.0.0.1:8001 .venv/bin/python -m pytest -q
```

`conftest.py` already passes `--disable-web-security --allow-file-access-from-files`
for the `chrome` channel, so the default `file://` UI URL works.

---

## Common pitfalls

| Symptom | Fix |
|---|---|
| `Playwright does not support chromium/firefox on ubuntu26.04-x64` | Use `channel='chrome'` (system Chrome), not bundled browsers |
| `bad interpreter: No such file or directory` on `.venv/bin/pip` | Shebang points to old path — delete `.venv` and recreate |
| `ModuleNotFoundError: No module named 'playwright'` | Don't use `source .venv/bin/activate`; call `.venv/bin/python` directly |
| i18n keys show raw (e.g. `impressum.sections.info.heading`) | Page loaded from `file://` — serve via `python3 -m http.server` instead |
| Shell cwd resets mid-script | Use `&&` chains or absolute paths; avoid `cd` + separate commands in Bash tool |
| Login returns 401 for `demo@octbase.dev` | API not running with `OCTBASE_DEMO_MODE=true` |
| MFA tests (`test_settings.py`) get 500s from `/users/me/mfa/*` | Target API has no usable `OCTBASE_MFA_ENC_KEY`. The key must **decode** (base64 or hex) to 32 bytes — a 32-*character* ASCII string is not enough and fails with `must decode (base64 or hex) to 32 bytes`. Use 64 hex chars, e.g. `python3 -c 'print("ab"*32)'` |
| `app` fixture times out on `text=Demo Project` | Auth rate limit (120/min per IP on `/api/v1/auth/*`) hit — wait ~60s or run fewer tests at once |
