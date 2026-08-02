# Feature Prompt — Octbase Vocabulary / terminology customization

> **Filename note:** the rates/budget/planner content this file's name promises
> was cut in `94255ba`; only the vocabulary feature remains. There is currently
> no rates/budget prompt.

> **Purpose of this document.** A single, self-contained build prompt for a new,
> optional Octbase capability that lets a stack **rename Octbase's core
> vocabulary** so the same product reads naturally for a different market (e.g. an
> architecture practice instead of a software team). Hand this to a build agent
> (or read it as the product brief). It is written to Octbase's own conventions
> (modular monolith, optional-module feature flags surfaced at `/config`, RBAC,
> i18n, activity logging, changelog discipline).

---

## 1. One-line pitch

Let a client **rename Octbase's core vocabulary** so the same product reads
naturally for their domain — an architecture practice sees "Phase / To-plan /
Deliverable / Planning board" where a software team sees "Sprint / Backlog /
Story / Board" — shipped as an **optional** capability a client can switch on or
off.

## 2. Who it's for & why now

Octbase is being taken to new verticals. Those firms don't run sprints and pull
requests — the UI speaks "Sprint / Backlog / Story / Board," but an architect
sees "Phase / To-plan / Deliverable / Planning board." Same mechanics, wrong
words.

This capability closes that gap without forcing the change on existing
software-team clients — it is **off by default** and enabled per stack.

## 3. Scope — Vocabulary / terminology customization

- Provide a **term-override map** so a stack (and ideally per-project) can rename
  core nouns: Project, Task, Board, Sprint, Backlog, Epic, Story, Release,
  Milestone, etc. → e.g. Phase, Deliverable, Planning board, …
- Implement as an **i18n overlay**, not a schema rename: the domain model keeps
  its names; only **labels** change. Ship a ready-made **"Architecture" preset**
  plus free-text overrides.
- Overrides load with the locale bundle (`octbase-shared/i18n.js`) so both the
  desktop and mobile SPAs pick them up byte-identically (respect the shared-JS
  drift guard).
- Editing terms is an **admin** settings screen; changes take effect without a
  rebuild.

## 4. Cross-cutting: it's optional

- Gated behind an admin toggle, documented in `.env.example` and `docs/`.
- Enabling/disabling is an **admin** action.

## 5. Architectural fit (hold the build against these)

- **i18n overlay, not a schema rename:** the domain model keeps its names; only
  labels change. Overrides load with the locale bundle so both SPAs pick them up
  byte-identically (respect the shared-JS drift guard).
- **Frontend stays plain DOM**; new strings go through i18n (English + German);
  respect the frontend guards (innerHTML/escaping, export-completeness, shared
  drift, asset hashes).
- **Changelog + docs:** entry under `## Unreleased` (Added / Changed); update the
  user guide and `.env.example`.

## 6. Definition of done (verifiable)

- Renaming "Sprint"→"Phase" (Architecture preset) changes every visible label
  in both SPAs with no code change and no shared-drift failure.
- Frontend guards, i18n, and changelog all green.

## 7. Open product decisions (assumptions taken — confirm)

1. **Terminology** overrides are **label-only** (i18n overlay), stack-scoped in
   MVP, per-project later.
