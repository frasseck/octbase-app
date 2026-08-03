# Octbase — quality-assurance prompts

This directory holds the QA prompt suite: the reusable, repeatable reviews that
answer *"is Octbase still correct, secure, tested, accessible, and internally
consistent?"*. Nothing here builds a feature. Feature and migration prompts used
to live here too; they were removed on 2026-08-02 because every one of them
described a past state of the product and had become a source of wrong facts.

## Ground truth — for every prompt in this directory

When two sources disagree, the earlier one wins:

1. The running code and its tests (`octbase-api/`, `octbase-frontend/`,
   `octbase-mobile/`, `octbase-shared/`).
2. `octbase-api/api/openapi.yaml` — the API contract. `internal/apicontract`
   asserts route↔spec parity, and `octbase-frontend/types/openapi.d.ts` is
   generated from it.
3. `docs/architecture.md` — **normative** for architecture questions.
4. `CLAUDE.md` — repo conventions (auth model, error shape, optimistic locking,
   defaults-as-contract, changelog discipline).
5. `README.md`, `CHANGELOG.md`, `docs/operations.md`,
   `docs/technical_documentation.md`, `octbase-frontend/user-guide.html`.

There is deliberately **no** consolidated "current state" document. One existed
(`99_octbase-current-state.md`) and drifted from the code, which is the failure
mode this directory now avoids: facts live where they are executed.

## Skills, not restatement

The repo's skills own the *how* (commands, environment gotchas, invariant
lists). These prompts own the *what* and the *judgement*. Invoke rather than
paraphrase: `dev-stack`, `testing`, `frontend-testing`, `coverage`,
`frontend-guards`, `go-security`, `js-security`, `go-best-practices`,
`db-migrations`, `i18n`, `stack-health`, `release`.

If a prompt and a skill disagree about a command, the skill wins — fix the
prompt.

## The suite, in execution order

| # | Prompt | Use it when |
|---|---|---|
| 01 | [Master quality audit](01_master-quality-audit.md) | Umbrella six-dimension audit with a single compliance verdict. Start here; it delegates depth to 02–06. |
| 02 | [Architecture & clean-code review](02_architecture-and-clean-code-review.md) | After structural backend work, or when the code feels like it is drifting from its own stated design. |
| 03 | [Test-coverage audit](03_test-coverage-audit.md) | Coverage near the CI floor, a new package or view with thin tests, or before trusting the suite as a release gate. |
| 04 | [Frontend quality review](04_frontend-quality-review.md) | Reviewing SPA code: module boundaries, render/escaping discipline, state, i18n, the guard set. |
| 05 | [Accessibility audit](05_accessibility-audit.md) | WCAG 2.2 AA conformance across both SPAs. |
| 06 | [Security assessment](06_security-assessment.md) | The periodic pentest-grade assessment that stands in for a paid third-party scan. |
| 07 | [Release consistency & functional review](07_release-consistency-and-functional-review.md) | The release gate: docs truthfulness, style-guide conformance, and *every function provably works*. |

01 and 07 are whole-product reviews and are the two worth running on a schedule.
02–06 are deep dives you run when 01 flags their dimension, when the area
changes, or on request.

## House rules (all prompts)

- **Evidence over opinion.** Every finding carries `file:line` or a command with
  its output, the source of truth it contradicts, the concrete fix, and how to
  prove the fix.
- **Review means report.** These are review prompts. Apply fixes only for
  unambiguous mechanical defects, each as a small verified patch. Anything
  needing product judgement is reported, not silently changed.
- **Grade against the decision record, not generic best practice.** Octbase is
  deliberately not hexagonal, deliberately plain-DOM, and deliberately
  desktop-only for some features. `docs/architecture.md` outranks any textbook.
- **Never lower a gate to make a check pass** — not the coverage floor, not a CI
  guard, not the known-failures baseline.
- **Never log or paste secrets**, tokens, cookies, `Authorization` headers, or
  PII into a report or a scratch file. Scratch work goes in the scratchpad
  directory, never in the repo tree.
- Any behaviour change that lands as a result of a review needs its
  `CHANGELOG.md` `## Unreleased` entry in the same commit.
