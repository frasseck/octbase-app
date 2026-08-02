# Feature Prompt — Octbase Task estimation: story points **or** hours, per project

> **Purpose of this document.** A single, self-contained build prompt for giving
> Octbase tasks an **effort estimate** — measured either in **story points** or
> in **hours**, chosen **per project** and **off by default**. Hand this to a
> build agent (or read it as the product brief). It is written to Octbase's own
> conventions (modular monolith, structs-as-contract, optimistic locking, stable
> error codes, RBAC, i18n, activity logging, migrations with up/down pairs,
> changelog + docs discipline).
>
> **This prompt is one half of a pair.** It adds the *setting* and the
> *estimate*. Making the sprint **burndown and velocity reports** measure that
> estimate is [`42b_octbase-effort-based-reports.md`](42b_octbase-effort-based-reports.md),
> which **depends on this one** and should be built after it. (Both were split
> out of the earlier single prompt `42_octbase-story-points-burndown.md`, which
> had no per-project activation and knew nothing about hours.)

---

## 1. One-line pitch

Let each project switch on **one** estimation unit — **story points** or
**hours** — and, once switched on, let every task in that project carry an
estimate in that unit. **By default a project has no estimation at all** and
nothing about estimates appears anywhere in its UI.

## 2. Who it's for & why now

Two different kinds of client want two different units, and today Octbase gives
them neither:

- A software team estimates in **story points** and wants the sprint reports to
  burn down effort, not ticket count. The OpenAPI says so outright today:
  *"Counts tasks, not story points — tasks carry no estimate field."*
- An agency or architecture practice estimates in **hours** — it is what they
  quote, plan, and eventually bill against.

Forcing one unit on everyone is wrong, and forcing *any* estimation on a team
that doesn't estimate is worse: an always-present empty "Story points" box is
clutter that teaches people to ignore the task form. So the capability is
**per-project and opt-in**, exactly like the optional `THEME`/`INITIATIVE`
hierarchy levels already are.

This is the foundation of the live **"Burndown-Charts + Story Points"** request
(OCT, seqNumber 92); the reports themselves are prompt 42b.

## 3. Scope — two capabilities

### 3.1 A per-project estimation setting

- New project setting **`estimationUnit`** (JSON tag `estimationUnit`) on the
  `Project` struct with exactly three values: **`NONE`** (default) | **`POINTS`**
  | **`HOURS`**. Add it to `GET /meta/enums` as `estimationUnits`.
- **Follow the existing precedent, don't invent a new one.** `ThemeEnabled` /
  `InitiativeEnabled` in `internal/workmanagement/domain.go` are per-project
  optional-capability settings already: column added in migration
  `029_project_task_settings`, set through the version-guarded
  `PATCH /projects/{id}`, gated with `shared.RequireOwner(role)` → 403
  `FORBIDDEN`, and refused when switching a setting off would strand data (422
  `TASK_TYPE_IN_USE`). Mirror that shape — read the settings block in
  `project_handler.go` before writing anything.
- **Only a project owner/admin may change it** (`shared.RequireOwner`), same as
  the hierarchy toggles. An invalid value is a loud 422
  **`ESTIMATION_UNIT_INVALID`**.
- **Switching the unit never destroys data.** Going `POINTS` → `HOURS` → `NONE`
  → `POINTS` keeps every stored value; estimates in the non-active unit are
  simply dormant — not shown, not summed — and reappear unchanged if the project
  switches back. This is *why* the two units get two columns (§4); do **not**
  collapse them into one polymorphic "estimate" column that would silently
  reinterpret 5 points as 5 hours.
- Log the change through **`activity.Write(...)`** on the project, mirroring how
  existing user-visible changes are logged (it is not a DB trigger).
- Ripple sites that already carry `themeEnabled` and must carry `estimationUnit`
  too — grep for it, all of these are real:
  `project_repo.go` (four explicit column lists: insert, list, get, update),
  `project_export.go` / `project_import.go` (an import from an older export
  without the field defaults to `NONE`), `api/openapi.yaml`,
  `octbase-shared/meta.js` + both SPA copies, and the en/de locale bundles.

### 3.2 The estimate on the task

- Task gains **two** nullable estimate fields, at most one of which is ever
  active for a given project: **`storyPoints`** (`*int`) and **`estimateHours`**
  (`*float64`). Structs are the contract — adding them to `Task` exposes them on
  every task response immediately, so be deliberate about the JSON tags.
- **`null` (unestimated) is a first-class state, distinct from `0`.** An
  unestimated task must never silently read as "zero effort" without the UI
  saying so; `0` is a legal, deliberate "no effort".
- **Written through the existing version-guarded `PATCH /tasks/{id}`** — it
  already carries the optimistic-locking contract and returns 409
  `VERSION_CONFLICT` on a stale write. No new mutation route.
- **Clearing must actually clear.** Sending `"storyPoints": null` removes the
  estimate and the read-back proves it. Octbase has already shipped this exact
  bug once — clearing a task's assignee or reviewer returned `200` with the old
  value still in place (OCT `6728ea5f`) — so write the failing test first.
- **Writing an estimate the project hasn't switched on is rejected**, loudly and
  with one stable code: **`ESTIMATION_UNIT_INACTIVE`** (422), message naming the
  project's active unit (or that estimation is off). This covers both "project
  is `NONE`" and "project is `HOURS`, you sent `storyPoints`".
- **Validation** with stable codes:
  - `storyPoints`: integer, `0 ≤ n ≤ 100` → **`STORY_POINTS_INVALID`**.
  - `estimateHours`: decimal, `0 ≤ h ≤ 1000`, at most 2 decimal places →
    **`ESTIMATE_HOURS_INVALID`**.
  - The scale is **free numbers with a ceiling**, not a constrained set;
    Fibonacci (1/2/3/5/8/13/21) is UI sugar (preset chips), never a server-side
    constraint.
- **Estimable types only.** Estimates live on the leaf types `STORY`, `TASK`,
  `SUBTASK`. `EPIC`, `INITIATIVE`, `THEME` are containers: reject an estimate on
  them with **`ESTIMATION_NOT_ALLOWED_FOR_TYPE`** (422). *(The earlier prompt 42
  contradicted itself here — its §3.1 said "allow the raw field on any type", its
  §9.4 said "points live on leaf types". This settles it: reject; a read-only
  roll-up of descendants is §7 Later.)*
- **The type-change path must respect that too.** `PATCH /tasks/{id}` can change
  `taskType` (see the `hasType` branch in `task_handler.go`); retyping an
  estimated task into a container type is rejected with the same code — clear
  the estimate first.
- Log a value change through **`activity.Write(...)`**, following the existing
  change-string pattern (`Type: X → Y`) so it reads in the Activity view.
- **Copy Task must carry the estimate** (the copy path duplicates task fields);
  task **templates** do not gain an estimate default in MVP — say so in the docs
  rather than leaving it ambiguous.

## 4. Data & migrations

- **One migration, up/down pair:**
  - `ALTER TABLE projects ADD COLUMN estimation_unit TEXT NOT NULL DEFAULT 'NONE'`
    (mirrors `029_project_task_settings`).
  - `ALTER TABLE tasks ADD COLUMN story_points INT NULL`.
  - `ALTER TABLE tasks ADD COLUMN estimate_hours NUMERIC(7,2) NULL`.
  - The `.down` drops all three.
- The expected migration version is **auto-derived**
  (`shared.LatestMigrationVersion`) — there is no constant to bump, but the
  schema ripples into tests and seed.
- **Seed data is public surface.** Leave the seed project on `NONE` so the
  default path stays the tested path — *unless* prompt 42b needs a seeded,
  estimated sprint to demo a real burndown, in which case seed that deliberately
  and update every test that asserts seed shape.

## 5. Architectural fit (hold the build against these)

- **Bounded context:** everything here is `internal/workmanagement` (projects,
  tasks). No new context, no cross-context reach against the archtest direction
  rules.
- **Structs are the contract:** the new fields on `Project` and `Task` *are* the
  API change; tests assert exact shapes.
- **Optimistic locking:** reuse the existing version-guarded updates on both
  aggregates; 409 `VERSION_CONFLICT`. Nothing new to invent.
- **RBAC:** changing the project setting is owner/admin (`shared.RequireOwner`);
  setting an estimate on a task is an ordinary member write.
- **Stable error codes** asserted by tests: `ESTIMATION_UNIT_INVALID`,
  `ESTIMATION_UNIT_INACTIVE`, `ESTIMATION_NOT_ALLOWED_FOR_TYPE`,
  `STORY_POINTS_INVALID`, `ESTIMATE_HOURS_INVALID`, plus the reused
  `VERSION_CONFLICT` / `FORBIDDEN`.
- **Tests are integration-style:** real chi router, real migrations, Postgres via
  `internal/testutil`. Extend `project_settings_test.go` and the task-handler
  tests. Keep coverage above the CI floor (`coverage` skill).
- **Run the backend diff through `go-security` and the `go-backend-reviewer`
  agent** — new fields on two contract structs plus a new authorization gate.

## 6. Frontend (plain DOM, gated on existing patterns)

- **Project settings (`octbase-frontend/js/views-crud.js`)** — wherever
  `themeEnabled`/`initiativeEnabled` are edited today, add the estimation unit as
  a three-way choice (none / story points / hours). Owner-only, i18n'd, and it
  must round-trip through the same version-guarded project PATCH.
- **Task detail:** show an estimate input **only when the project's unit is not
  `NONE`** — labelled and suffixed per unit ("Story points" / "Hours"), with
  Fibonacci preset chips in the points case. No box at all when estimation is
  off; that invisibility is the feature.
- **Board / backlog card:** a small estimate badge, same condition.
- **`octbase-shared/meta.js`** is shared byte-identically by both SPAs — the
  project-settings shape lives there, so the change must be made in
  `octbase-shared/` and synced (`scripts/sync-shared.sh`); the drift guard fails
  CI otherwise. **No mobile view work is in scope** beyond that byte-identical
  sync.
- **i18n (English + German):** every new label goes through the locale bundles —
  `project.estimationUnit.*`, `task.storyPoints`, `task.estimateHours`. German
  matters; this closes a German-filed request.
- Respect the **frontend guards** (`frontend-guards` skill): innerHTML/escaping,
  export completeness, shared drift, asset cache-busting.

## 7. MVP vs later

**MVP:** `estimationUnit` project setting (default `NONE`, owner-gated,
non-destructive switching, activity-logged, export/import-carried); nullable
`storyPoints` + `estimateHours` on estimable task types with validation,
clearing, inactive-unit rejection and activity logging; project-settings UI;
conditional task-detail input + card badge; en + de; migration up/down; OpenAPI,
user guide, CHANGELOG `## Unreleased`.

**Later:** roll-up of descendant estimates onto epics/stories (read-only),
logged/actual hours and remaining time (this MVP is **estimate only** — no
worklogs, no timesheets), capacity and commitment planning, per-assignee totals,
estimates on task templates, a constrained Fibonacci scale, CSV export
of estimates, and rates/budget on top of hours (no prompt for that exists yet —
prompt 41 now contains only the vocabulary feature; its rates/budget content
was cut in `94255ba`). Note: the mobile estimate UI and estimate-on-create,
listed as out of scope here, **did ship** with this feature (`248c609`,
`7b1875d`).

## 8. Definition of done (verifiable)

- A fresh project reports `estimationUnit: "NONE"`, and **no** task in it accepts
  `storyPoints` or `estimateHours` (422 `ESTIMATION_UNIT_INACTIVE`).
- A project owner switches the project to `POINTS`; a non-owner member gets 403.
  An invalid value gets 422 `ESTIMATION_UNIT_INVALID`.
- With `POINTS` active: a task round-trips `storyPoints` through PATCH → GET;
  `null` clears it (proven by read-back); `-1` and `101` are rejected with
  `STORY_POINTS_INVALID`; sending `estimateHours` is rejected with
  `ESTIMATION_UNIT_INACTIVE`; a stale version returns 409 `VERSION_CONFLICT`.
- With `HOURS` active the mirror-image test passes for `estimateHours` /
  `ESTIMATE_HOURS_INVALID`.
- Setting an estimate on an `EPIC` is rejected with
  `ESTIMATION_NOT_ALLOWED_FOR_TYPE`, and so is retyping an estimated `TASK` into
  an `EPIC`.
- Switching `POINTS` → `HOURS` → `POINTS` leaves the original point values
  intact and readable (proven by read-back, not by inspection).
- The estimate change appears in the task's Activity; the setting change appears
  in the project's.
- Project export → import round-trips `estimationUnit` and both estimate fields;
  importing an export that predates the field yields `NONE`.
- Desktop: the estimate input is **absent** when the unit is `NONE` and present,
  correctly labelled, in en + de otherwise.
- OpenAPI, user guide, CHANGELOG, archtest, coverage floor, frontend guards,
  shared-drift and i18n all green.

## 9. Product decisions (confirmed by Lars, 2026-07-27)

1. **A project picks one unit, not both** — `estimationUnit` is `NONE` | `POINTS`
   | `HOURS`, not two independent booleans.
2. **Default is `NONE`** — neither unit is active until someone switches it on.
3. **Hours mean an *estimate*, not logged time.** No worklogs, no time tracking,
   no remaining time in this scope (§7 Later).
4. **Switching units is non-destructive** — the other unit's values are kept and
   dormant, which is why the schema has two columns rather than one.
5. **Estimates live on estimable leaf types**; container roll-up is Later.
6. Free numeric scale with a ceiling; Fibonacci chips are UI sugar.
