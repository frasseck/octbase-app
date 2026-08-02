# E2E known-failures baseline

Every stage of `prompts/37_octbase-frontend-execution-runbook.md` (and the work
it sequences in 37a / 37b) gates on *"the e2e suite is green with the same known
failures."* This file is what "the same" means. Without it that gate is
unfalsifiable.

**How to use it:** run the suite as described under *Reproducing* below, then
compare. A failure listed here is the baseline. **A failure that is not listed
is a stop** — fix it or report it. Do not add a new failure to this file to make
a gate pass; that defeats the artifact's purpose.

---

## Fixed since this baseline

### The reap fixture left parent tasks behind (2026-07-31)

`_reap_created_entities` deleted a test's new tasks by iterating a **set** of
ids. Deleting a task that still has children is refused with `422
TASK_HAS_CHILDREN` and `_delete_quietly` swallowed the refusal, so whenever the
set happened to yield a parent before its child the parent survived the run —
permanently, in the shared demo project. That is the source of the
"polluted-stack signature" below: the seeded demo task is the oldest row, so a
project creeping past the 200-row task window drops it out of the board's fetch
and every test clicking its card burns a 30s timeout.

The reap now deletes children before parents (`_reap_tasks`, driven by a
`{id: parentId}` snapshot) and retries what a pass could not remove.
`_delete_quietly` reports whether the entity is actually gone rather than
swallowing the answer. Measured against a freshly seeded stack: a three-level
hierarchy reaped in the worst-case order left **2 of 3 tasks behind** before the
change and **0** after.

`test_reap_order.py` pins the ordering rule without needing a running instance —
it is marked `no_stack`, a new marker that also makes the two autouse fixtures
resolve `api` lazily, so a helper-level test is no longer skipped along with the
session when no API is up.

---

## Captured at

| | |
|---|---|
| **Date** | 2026-07-16 |
| **Commit** | `8fe584c52a41cbf241c38bdfd1a883c3db216241` (`L11: run the API container as non-root, and give attachments a volume`) |
| **Stack** | Disposable podman-compose stack built from that commit — API `:8002`, frontend `:8084`, Postgres `:5434`, freshly seeded (2 tasks / 1 page in the demo project) |
| **Runner** | pytest 9.1.1, Google Chrome 150 (`OCTBASE_BROWSER=chrome`) |

## The baseline

Default invocation (UI from `file://`, no `OCTBASE_ACCESS_*` set):

```
302 passed, 22 skipped, 0 failed   (~215-233s)
```

**Zero failures.** Reproduced twice back-to-back at the commit above with an
identical pass/skip/fail split and an identical skip list.

The 22 skips are all deliberate env-gates or seed-state guards, not breakage:

| Count | Tests | Why it skips |
|---|---|---|
| 13 | `test_accessibility.py` (whole file) | `OCTBASE_ACCESS_API_BASE` / `OCTBASE_ACCESS_UI_URL` are unset, so they default to the deployed `dev.ocete.ch:8001/8081`, which is unreachable locally. They need a **served** UI — `file://` will not do. See below. |
| 8 | `test_rbac.py` super-admin cases | `OCTBASE_SUPERADMIN_EMAIL` / `_PASSWORD` unset (documented as optional in that file). |
| 1 | `test_board.py:333` drag case | Seeded Review column has no card to drag — `seed.go` places both demo tasks in Planned / In Progress. |

## Known failures

Nothing fails in the default invocation. Both entries below are conditional —
they need a non-default configuration to surface at all.

### 1. `test_accessibility.py::TestLiveRegions::test_bulk_bar_is_labelled_region` — real, deterministic

- **Surfaces only when** `OCTBASE_ACCESS_*` is configured (otherwise it skips).
- **Reproduced 4 / 4 runs** against a freshly seeded stack.
- **Cause — a test bug, not an app bug.** It navigates to Backlog and waits for
  `.task-checkbox` (`test_accessibility.py:184`), but **the seeded backlog is
  empty by design**: both demo tasks carry a `boardColumnId`, so they render on
  the board, never in the backlog. The selector can never match on clean seed
  data, and the test dies on an 8s `wait_for_selector` timeout.
- **It therefore only passes on a polluted stack** that happens to have a
  backlog task — the same "passes only on litter" class as the data-pollution
  trap the `testing` skill documents. **Fix** = create its own backlog task
  (cf. the `backlog_task` fixture in `test_tasks.py`); do not "fix" it by
  leaving data behind.

### 2. `test_accessibility.py::TestKeyboardNavigation::test_task_panel_opens_with_keyboard` — flake

- **Surfaces only when** `OCTBASE_ACCESS_*` is configured.
- **Failed 1 / 4 runs**, same commit, same stack, no change in between. Re-run
  before treating it as real. Corroborates the `testing` skill's "a couple of
  accessibility tests are flaky-ish".

With `OCTBASE_ACCESS_*` pointed at a served stack, `test_accessibility.py` is
therefore **12 passed / 1 failed** (or 11/2 when the flake fires).

## Explicitly NOT baseline failures

Claims that were folded into earlier prompts as expected failures and **did not
survive measurement** — do not treat these as known:

- **MFA e2e tests (`test_settings.py`).** All 13 pass. They 500 only if the
  target API lacks `OCTBASE_MFA_ENC_KEY`; both the dev stack and any stack built
  from the repo `.env` set it. An MFA failure means a missing key — check the
  API env before blaming a diff.
- **`test_rbac.py::TestBackendPermissions::test_admin_cannot_list_users`
  (`429` instead of `403`).** Reproduced on the **shared** dev stack (`:8001`)
  while a parallel session was driving it; did **not** reproduce on an isolated
  stack in two full suite runs, nor in `test_rbac.py` alone (9 passed / 8
  skipped). This is auth-rate-limit contention from sharing one stack, not a
  code failure. See *Contamination* below.

## Reproducing

The baseline is only meaningful against an **isolated** stack at a **pinned**
commit. Both matter — see *Contamination*.

```bash
# 1. Pin the tree (keeps a parallel session's edits out of the run)
git worktree add --detach /path/to/baseline <sha>
ln -s "$PWD/octbase-frontend/tests/.venv" /path/to/baseline/octbase-frontend/tests/.venv

# 2. Isolated stack on non-colliding ports, built from that commit
cd /path/to/baseline
cp /path/to/repo/.env .env
sed -i -e 's|^POSTGRES_PORT=.*|POSTGRES_PORT=5434|' \
       -e 's|^API_PORT=.*|API_PORT=8002|' \
       -e 's|^FRONTEND_PORT=.*|FRONTEND_PORT=8084|' \
       -e 's|^PGDATA_DIR=.*|PGDATA_DIR=./pgdata_test|' \
       -e 's|^OCTBASE_SITE_AUTH=.*|OCTBASE_SITE_AUTH=|' .env
podman-compose -p octbase_base up -d --build

# 3. The suite
cd octbase-frontend/tests
OCTBASE_BROWSER=chrome OCTBASE_API_BASE=http://127.0.0.1:8002 \
  .venv/bin/python -m pytest -q

# 4. The accessibility tests (need a SERVED UI; skipped otherwise)
OCTBASE_BROWSER=chrome OCTBASE_API_BASE=http://127.0.0.1:8002 \
OCTBASE_ACCESS_API_BASE=http://127.0.0.1:8002 \
OCTBASE_ACCESS_UI_URL=http://127.0.0.1:8084/index.html \
  .venv/bin/python -m pytest test_accessibility.py -q
```

`OCTBASE_SITE_AUTH` must be set **empty**, not `off`: the Caddyfile imports
`auth-{$OCTBASE_SITE_AUTH:}.caddy`, and there is no `auth-off.caddy`, so the
literal `off` crash-loops the frontend container.

### Against the built `dist/` (the 37b invocation)

Since 37b stage 2 the desktop SPA is bundled, so its gate runs the suite against
the **built** output rather than the source tree. Serve `dist/` with `/api`
proxied so the app is **same-origin with its API** — the session lives in an
HttpOnly refresh cookie, and a cross-origin preview never gets it back, so every
test that reloads would land on the login screen:

```bash
npm run build                                 # both SPAs, all four targets

# Run the preview FROM octbase-frontend/ (or pass --config
# octbase-frontend/vite.config.js). There is no vite config at the repository
# root, so a preview started there silently uses Vite's defaults: no /api proxy
# and the wrong root. See "The preview lies in two ways" below.
cd octbase-frontend
OCTBASE_API_ORIGIN=http://127.0.0.1:8201 npx vite preview --port 4173 --strictPort &

# Confirm the server on 4173 is YOURS and proxies to the API you meant, before
# trusting anything it serves. A 200 here is not enough on its own — this
# environment runs concurrent sessions and a fixed port is not yours by default.
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST http://localhost:4173/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@octbase.dev","password":"demopass1234"}'   # expect 200

# Note the URL must carry a query string: test_taskview.py appends
# `&taskView=off`, so a bare `…/index.html` becomes `index.html&taskView=off`.
cd octbase-frontend/tests
OCTBASE_BROWSER=chrome OCTBASE_API_BASE=http://127.0.0.1:8201 \
OCTBASE_UI_URL='http://localhost:4173/index.html?e2e=1' \
  .venv/bin/python -m pytest -q
```

`vite preview` binds **`localhost` (IPv6 `::1`) only** — `http://127.0.0.1:4173`
is refused. Use `localhost`.

#### The preview lies in two ways, and both cost an hour on 2026-07-31

A session verified a change against **another session's stack** for the better
part of an hour, and changed a password on an instance it did not own, because
these two failures both present as success:

1. **Started from the repository root, the preview serves nothing useful.** The
   vite configs live in `octbase-frontend/` and `octbase-mobile/`; there is none
   at the root. A root-level `npx vite preview` therefore runs with default
   config — **`OCTBASE_API_ORIGIN` is read by nothing, so there is no `/api`
   proxy at all** — and still starts, still answers 200, still looks right.
2. **`--strictPort` reports the collision to the log, not to you.** When another
   process already holds 4173 the new server exits with `Error: Port 4173 is
   already in use`, which lands in whatever you redirected the preview's output
   to. A `curl` against `localhost:4173` then answers **200 from the other
   process's server**, which reads exactly like your server coming up. Every
   action after that lands on their app and their API — the login above
   succeeded with a password that only existed on the other session's stack.

So: `cd` into the SPA directory, and prove the proxy reaches your API before
believing the port is yours. Checking that the preview process is still alive
(`kill -0 "$pid"`) is what distinguishes "my server is up" from "somebody's
server is up" — the CI e2e job's readiness loop does this for the same reason.

**This is what CI runs since 37b stage 6** — the E2E job builds, serves and sets
`OCTBASE_UI_URL` exactly like the block above, so a local run of this recipe and
a CI run measure the same thing. It also means the desktop suite now runs with
the browser's **same-origin policy ON**: the `--disable-web-security` /
`--allow-file-access-from-files` flags exist only for a `null` origin, so
`conftest.py` passes them only when the UI URL is a `file://` one, and the mobile
tests that really do open the standalone bundle from disk take the relaxed
browser from the `file_browser` fixture. A CORS or CSP regression is now
something the suite can actually see.

**Both mobile artifacts are required since 37b stage 3.** `test_mobile.py` loads
`dist-standalone/` from `file://` for most of its cases and serves `dist/` over a
loopback HTTP origin for the three login tests — it used to serve the mobile
*source tree* there, which stopped working the moment the app imported
`@octbase/shared` (a bare specifier no browser resolves; the page just sits at
its spinner). A session-scoped fixture fails with that instruction rather than
letting 23 selector timeouts point nowhere.

**Result on 2026-07-31 (37b stage 3): `370 passed, 22 skipped, 2 failed`** — the
mobile vocabulary-label failure and
`test_task_panel.py::TestCopyAndArchive::test_immutable_done_task_hides_edit_controls`.
**Both are fixed; neither was ever an app bug.** The second one is worth reading
even though it is closed, because the shape recurs:

> It and `test_archive_and_reopen_task` drive the *same* seeded demo task
> (`…201`) through status changes, and each needs the opposite starting state —
> one waits for a button the panel offers only on an open task, the other for the
> button it offers only on a finished one. `conftest.py` had an autouse fixture
> putting the task back at its seed placement, but only **after** each test, and
> it deliberately swallows errors so bookkeeping can never fail a test. When that
> cleanup lost a version conflict, the cost was paid by the *next* test, which
> started from a state nobody had checked and failed on a missing button with no
> hint why — so the pair passed alone, failed in a suite, and swapped which one
> failed between runs. Fixed 2026-07-31 by restoring the placement on the way
> **in** as well as out: one GET in the common case, and no test inherits its
> predecessor's failed cleanup. **The general lesson: a cleanup-only fixture that
> swallows errors is not isolation — it is isolation that works right up until it
> doesn't, and then blames the wrong test.**

**Result on 2026-07-31, after that fix: `372 passed, 22 skipped, 0 failed`** —
a clean run, and the first one recorded here. Treat that as the baseline: any
failure now needs an explanation, not a paragraph in this file.

**Re-captured for 1.1.2: `387 passed, 22 skipped, 0 failed`** (2026-07-31, an
isolated stack on a private port with a fresh seed, Chrome, `dist/` served by
`vite preview`). Same 22 skips. The count rose because the release added tests,
not because anything was relaxed.

That run is also the gate the **vite 5 → 8 upgrade** passed, and it is worth
knowing what the first attempt caught. Vite 8 bundles with rolldown and drops
esbuild entirely, so `esbuild: { keepNames: true }` silently stopped applying —
and event delegation keys its registry on `Function.prototype.name`. The build
was green, the desktop suite was green (it drives `dist/`, which had been fixed
first), and **eight `test_mobile.py` cases failed**, all of them interactions:
tapping a project, tapping a card, opening the status sheet, adding a comment.
That is the dead-delegation signature, and the mobile suite saw it because it
loads the standalone `file://` bundle, whose config still had the option in the
wrong place — a *second* `rollupOptions` key, which an object literal resolves
in favour of the later one. **Keep that pairing in mind: the desktop suite
cannot see a standalone-bundle regression, so a green desktop run is not
evidence about `dist-standalone/`.**

**`test_settings_offers_the_vocabulary_picker` — FIXED 2026-07-31, and it was
never an app bug.** It failed every run from 37b stage 2 onward because it
asserted the *desktop* wording ("Classic project management") for the mobile
vocabulary label, while `octbase-mobile/locales` deliberately ships the short
form ("Classic"). Several sessions, this one included, read the failure as a
parallel session's in-flight change waiting to be reconciled. It was not: the
shortening is deliberate and the style guide documents it as a rule — the mobile
companion may use a shorter label where the desktop wording does not fit a phone
cell, through its own key, provided the short form means the same thing — and
names this very setting as a shipped example. The test now asserts the mobile
wording, with that reasoning next to the assertion so it does not get "fixed"
back.

**The lesson worth keeping:** a failure that survives several runs and gets
attributed to someone else's unfinished work each time is a failure nobody owns.
Check the assertion against the documented intent before recording it as
somebody's in-flight change.

**Serving the app made one latent test bug matter**, and it is worth knowing
about because its symptom pointed nowhere near its cause: the fixtures decided
whether to sign in by sampling `#login-form` immediately after `page.goto()`,
which resolves on `load` — before the SPA has chosen between the login view and
the shell. Off `file://` that was effectively always won; over HTTP it lost about
1 load in 20 and produced **six to nine unrelated fixture timeouts scattered
through a full run**. Fixed by `sign_in_if_needed` in `conftest.py`. If a served
run shows a handful of moving `ERROR at setup` timeouts, suspect this class
before suspecting the app.

## Contamination — read before trusting a run

This checkout and the `octbase_dev` stack are **shared with parallel sessions**.
Two ways that silently corrupts a baseline, both observed while capturing this
one:

1. **The tree mutates mid-run.** The suite loads the UI straight from
   `octbase-frontend/` via `file://`. A parallel session rewriting
   `framework.js` / `views-*.js` / `index.html` during a run means the second
   half of the suite tests different code than the first, and the result maps to
   no commit at all. → run from a pinned worktree.
2. **The shared stack restarts or is driven concurrently.** A full-suite run
   against `:8001` returned `324 skipped in 0.5s` because the stack happened to
   be recreating; another produced the `429` above because a parallel session
   was spending the same auth rate-limit budget (rate limits are per stack, and
   `/api/v1/auth/*` allows 120/min). → run against your own stack.

A run whose numbers disagree with this file is more likely contaminated than a
real regression. Rule out both causes before recording a new failure.

## Maintaining this file

Re-capture when the suite legitimately changes (a test added, a real bug fixed).
Record, per entry: node ID, real-vs-flake with the run count behind that call,
the cause if known, and the date + commit SHA. A flake needs several runs to
classify — one red run is not evidence.
