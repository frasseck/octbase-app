Act as a senior full-stack engineer executing the frontend tooling work on Octbase.

## What this prompt is — and is not

**This is an orchestration runbook, not a scope document.** It defines the **order**, the **model per step**, the **per-stage working loop**, and the **stop conditions**. The actual work is defined in:

- `prompts/37a_octbase-no-build-value-capture.md` — the ungated work (unit tests without a build, vendored integrity, correcting the architecture record)
- `prompts/37b_octbase-frontend-build-step.md` **§ stage 1 only** — per-module action registration (the outstanding "step 2" of the SPA modularization roadmap; explicitly ungated in that prompt)

**Do not re-derive scope from this file.** Read the referenced stage in its source prompt and follow it. If this runbook and a source prompt disagree, the source prompt wins on *what* to build; this one wins on *order* and *process*.

**The rest of prompt 37b is gated and out of scope.** As of 2026-07-15 its §5.1 trigger conditions are not met on the merits — see that prompt's Decision gate table. Do not start the ESM/Vite migration from this runbook.

---

## Step 0 — capture the e2e baseline (do this first, before touching anything)

Every stage in both source prompts gates on *"the e2e suite is green with the same known-failures baseline."* **Nothing in the repo records what that baseline is**, which makes the gate unfalsifiable — the same defect the 37 precondition had. Fix it before the first line of code.

- Invoke the **`frontend-testing`** skill first (required before running any frontend test), then **`testing`**.
- **`OCTBASE_API_BASE` must be set.** `octbase-frontend/tests/conftest.py:19` defaults to `http://127.0.0.1:8000`, but the dev stack listens on **8001** — an unset var produces a baseline of connection errors, not real results. The MFA e2e tests also need `OCTBASE_MFA_ENC_KEY` on the API or they 500. Get a seeded stack up via the **`dev-stack`** skill first.
- Run the full suite and **record the result in `octbase-frontend/tests/KNOWN_FAILURES.md`** (new file): each failing test's node ID, the reason if known, and the date + commit SHA it was captured at. A flake needs several re-runs to classify.
- That file is the gate artifact for every later stage. Commit it on its own.

> **DONE 2026-07-16 at `8fe584c`** — `KNOWN_FAILURES.md` exists; read it, do not
> re-derive the baseline from this section. **The list guessed above did not
> survive measurement**, which is why the file, not this prompt, is the gate:
> the default suite is **302 passed / 22 skipped / 0 failed**, reproduced twice.
> The MFA tests pass (every stack sets `OCTBASE_MFA_ENC_KEY`); the rate-limit
> `429` was contention from a parallel session sharing the dev stack, not a code
> failure; and `test_accessibility.py` does not fail by default — all 13 of its
> tests **skip** unless `OCTBASE_ACCESS_*` names a served UI, which is a bigger
> problem than a failure, since a green suite never ran them. Pointed at a
> served stack, one fails deterministically (empty seeded backlog) and one is a
> 1-in-4 flake. **The baseline is only reproducible from a pinned worktree
> against an isolated stack** — the shared tree and `octbase_dev` both mutate
> mid-run; `KNOWN_FAILURES.md` has the recipe and the evidence.

> **Decide before writing it:** `KNOWN_FAILURES.md` is a new tracked artifact. It is justified — every stage's gate references a baseline nothing records — but if the maintainer would rather it live elsewhere (a `pytest` marker, the CI job), ask.

**Gate:** the suite runs to completion, the failure list is committed, and re-running it reproduces the same list.

---

## Order and model per step

Each row is **one session**. Do not batch.

| # | Work | Source | Model | Effort |
|---|---|---|---|---|
| 0 | Capture the e2e baseline | this file | Sonnet 5 | — |
| 1 | Wire `js/i18n.test.js` into CI | 37a §1, first bullet | Sonnet 5 | — |
| 2 | Plain-Node test harness + pure-function tests | 37a §1, rest | Sonnet 5 | — |
| 3 | Vendored-integrity manifest + checker + trivy | 37a §2 | Sonnet 5 | — |
| 4 | Per-module action registration | **37b §1** | **Opus 4.8** | `xhigh` |
| 5 | Correct §5.1's cost list; propose condition 5 | 37a §3 | Opus 4.8 | — |

> **Progress (2026-07-29): all five steps are DONE.** Step **4** landed as
> `28bc707` — per-module handler registration; the delegation registry is
> identical before/after (184 handlers) and the e2e suite is 372p/22s/0f. Step
> **5** landed with it as the following commit: §5.1 costs #3 and #4 now carry
> measured figures (**7,524 LOC** of bundler-obsoleted tooling; 10 test files /
> 112 tests), and prompt 37b's decision-gate table, cost ledger and "current
> state" figures were re-measured — **no condition fired on the merits, the gate
> stays closed**. The proposed **condition 5 was deliberately NOT merged** into
> `docs/architecture.md`; it is normative and awaits sign-off on the go/no-go
> task. Nothing below needs re-running.
>
> **Progress (2026-07-27):** steps **1–3 are DONE and landed** — `js/i18n.test.js`
> plus eight further `js/*.test.js` files run via `node --test` in CI
> (`.github/workflows/ci.yml`, Frontend checks job), and the vendored-integrity
> manifest (`scripts/vendor-manifest.txt` + `check-vendor-integrity.sh`) and
> Trivy image scans are in the CI security/build jobs. Do **not** redo them.
> ~~Step 4 (per-module action registration) is still open~~ — superseded by the
> 2026-07-29 note above. Step 5's *factual correction* of
> `docs/architecture.md` §5.1 costs 3–4 first landed 2026-07-27 (review-driven
> docs alignment) as prose without figures; step 5 proper replaced it with the
> post-step-4 measurement.

**Why this order.** It is a cost and risk ramp, **not** a hard dependency chain — be honest about that rather than inventing a rationale. Step 1 is a few lines in `.github/workflows/ci.yml` for a test that already exists and passes. Steps 1–2 settle the factual claim (§5.1 cost #3, "no unit-testable modules") that the whole prompt-37b decision leans on, so they buy disproportionate value for their size. Step 3 is independent of 1–2 and may run in parallel if you prefer. Step 4 is the largest and most delicate piece, so it goes after the cheap work is banked and CI is green.

**One real ordering constraint:** step 5 measures export counts and bespoke-tooling LOC. Step 4 changes the export counts. Step 5 must therefore run **last**, or it records numbers that are already stale.

**Why these models.** Steps 0–3 are mechanical and well-specified; Sonnet 5 is near-Opus on coding work at $3/$15 per MTok (introductory $2/$10 through 2026-08-31). Step 4 touches 21 files and ~290 exports and must leave the delegation registry byte-identical — that is where a subtle cross-file mistake costs more than the model does, and `xhigh` is the recommended effort for coding and agentic work on that tier. Step 5 is a judgement call about a normative document. **Do not use Claude Fable 5 for any of this:** it is double Opus 4.8's price for work that is not at the frontier of difficulty, its cybersecurity classifiers can refuse benign security tooling (step 3 is exactly that, and this repo is full of security-review scripts and a pentest report), and it requires 30-day data retention. Staying on Opus 4.8 throughout is a defensible simplification if switching models per session is more friction than it's worth.

---

## The per-stage loop

For every step, in order — this is `CLAUDE.md`'s goal-driven execution applied to this work:

1. **Read the stage in its source prompt.** Not the summary in this file.
2. **State the success criteria before writing code**, as the source prompt's gate. If you cannot state a criterion you could fail, stop and ask.
3. **Implement.**
4. **Verify against that gate** — not against a gut feel, and not by re-reading your own diff. For anything touching the running frontend, invoke **`frontend-testing`** before running or screenshotting. Compare the e2e result against `KNOWN_FAILURES.md`; a new failure is a stop, not a note.
5. **Land docs in the same commit as the change** — never as a cleanup pass. Per the repo's standing rule, every change verifies: `CLAUDE.md`, `docs/technical_documentation.md`, the relevant `README`s, the user guide and styleguide if user-visible, and architecture compliance. A `CHANGELOG.md` entry under `## Unreleased` is required for any `octbase-api/` or `octbase-frontend/` behavior change; step 1's CI wiring is tooling-only and does not need one, steps 2–4 do.
6. **Before pushing any frontend change, invoke `frontend-guards`** — the six CI checks. Expect step 4 to make `check-exports.mjs` rule 4 (dead public surface) start firing as export blocks shrink: that is the guard working, not a regression.
7. **Commit, then stop.** One stage, one commit, one session.

---

## Stop conditions

Stop and report rather than working around, if:

- **A new e2e failure appears** that is not in `KNOWN_FAILURES.md`. Do not add it to the baseline to make the gate pass — that defeats the artifact's purpose. Fix it or report it.
- **A security guard would have to be weakened** to make a stage pass (CSP, the escaping producers, the DOMPurify policy, the innerHTML guard). This is an abort, not a trade-off.
- **Step 4's delegation registry differs before/after.** The gate is exact equality of `Object.keys(ACTIONS|CHANGES|…).sort()`. A diff means a handler was dropped or renamed — find it, don't rationalize it.
- **`git stash` appears to succeed but the working tree is unchanged.** Known quirk in this checkout (`pgdata_dev` permission denied). Do not build on top of a stash you did not verify.
- **A source prompt's claim contradicts the repo.** Both 37 and 37a were audited on 2026-07-15, but they will drift. Report the drift and fix the prompt in the same session; do not silently code against a stale spec.

---

**Deliverables:** `octbase-frontend/tests/KNOWN_FAILURES.md` with a reproducible baseline; then steps 1–5 landed as five separate commits, each green on its own gate, each with its docs and changelog entry in the same commit.

**Constraints:** the work is defined by 37a and 37b §1 — this file only sequences it; **no `package.json`, no `node_modules`, no npm dependency graph, no bundler** (37a's premise, and 37b's gate is closed) — note this is *not* "no build step": the two `Containerfile`s already run an esbuild minify stage and it stays (see the correction note atop 37a); one stage per commit and per session; the e2e suite is the regression gate at every step and `KNOWN_FAILURES.md` is what "same baseline" means; no weakening of any security guard; do not start the gated part of prompt 37b from this runbook.
