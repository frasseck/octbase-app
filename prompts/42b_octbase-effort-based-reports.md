# Feature Prompt — Octbase Effort-based burndown & velocity

> **Purpose of this document.** A single, self-contained build prompt for
> teaching Octbase's existing **sprint burndown** and **project velocity**
> reports to measure **effort** — story points or hours — instead of only
> counting tickets. Hand this to a build agent (or read it as the product brief).
> It is written to Octbase's own conventions (modular monolith,
> structs-as-contract, stable error codes, migrations with up/down pairs, i18n,
> changelog + docs discipline).
>
> **This prompt is the second half of a pair and depends on the first.**
> [`42a_octbase-task-estimation.md`](42a_octbase-task-estimation.md) adds the
> per-project `estimationUnit` setting (`NONE` default | `POINTS` | `HOURS`) and
> the `storyPoints` / `estimateHours` fields on the task. **Build 42a first** —
> there is nothing here to sum without it. (Both were split out of the earlier
> single prompt `42_octbase-story-points-burndown.md`.)

---

## 1. One-line pitch

Let the **sprint burndown** and **project velocity** reports burn down and
measure the project's estimation unit — **points or hours** — instead of counting
tickets, so a team plans and tracks by effort.

## 2. Who it's for & why now

Octbase already ships a sprint **burndown** (`GET /sprints/{id}/burndown`) and a
project **velocity** report (`GET /projects/{id}/reports/velocity`) — but both
**count tasks**. For any team that estimates, a task-count burndown is
misleading: three 1-point chores and one 13-point epic read as "4 remaining"
whether the hard one is done or not. Same for an agency planning in hours.

The gap was never the chart — it was the missing estimate, which 42a supplies.
Here the two reports (which already reconstruct remaining-over-time from activity
transitions) learn to sum estimates instead of rows.

Together with 42a this closes the live **"Burndown-Charts + Story Points"**
request (OCT, seqNumber 92).

## 3. Scope — three capabilities

### 3.1 Effort-based burndown

- Extend `GET /sprints/{sprintId}/burndown` with a **`?unit=` query param —
  `tasks` (default) | `points` | `hours`** — so existing clients are
  byte-for-byte unaffected and effort is an explicit opt-in.
- **The requested unit must match the project's active `estimationUnit`.**
  Asking for `points` in an `HOURS` project, or either in a `NONE` project, is a
  422 **`ESTIMATION_UNIT_INACTIVE`** — the same code 42a defines for writes. Do
  not fall back silently to `tasks`; a chart that quietly changes what it measures
  is the bug this whole feature exists to fix.
- With an effort unit: `committed` = **sum of the committed tasks' estimates**;
  `remaining(day)` = sum of the estimates of committed tasks **not yet
  DONE/ARCHIVED** at end of that UTC day, reconstructed from the very same
  `activity_entries` status transitions (with the `tasks.done_at` fallback) the
  count path already uses. **Only the accumulator changes — `+1` becomes
  `+estimate`.** Do not fork the reconstruction; if it needs a seam, factor one
  and keep both paths on it.
- The `ideal` line runs from committed-effort → 0, exactly as it runs from
  committed-count → 0 today.
- **Unestimated committed tasks count as 0 *and* are reported.** Add an
  **`unestimated`** count to the response so the UI can say "3 tasks unestimated"
  rather than quietly under-reporting the commitment.
- **Echo the unit back** in the response body (`unit`) so a chart can never
  mislabel its own axis.
- `?unit=tasks` and the no-param call stay **byte-identical to today** — assert
  that in a test, don't assume it.
- The existing 422 `SPRINT_NOT_STARTED` path is unchanged.

### 3.2 Effort-based velocity

- Velocity is a pure projection of values **snapshotted at sprint completion**
  (today `committed_count` / `completed_count`, migration 015). A completed
  sprint's board is gone and its unfinished tasks are unlinked, so only a
  snapshot keeps the denominator honest — effort needs the **same** treatment.
- Snapshot **three** columns on `sprints`, filled at `POST /sprints/{id}/complete`
  next to the existing counts: `committed_estimate`, `completed_estimate`, and
  **`estimate_unit`**. The unit must be stored *per sprint*, not read live from
  the project: a project may switch `POINTS` → `HOURS` later, and a historical
  sprint's numbers must keep meaning what they meant when they were taken. A
  sprint completed while estimation was `NONE` snapshots `NULL` / `NULL` /
  `NULL`.
- Extend `GET /projects/{id}/reports/velocity` with `committedEstimate`,
  `completedEstimate` and `estimateUnit` per entry — **add fields, never replace**
  the existing counts (structs are contract; the count series stays valid and
  keeps working for `NONE` projects).
- A mixed-unit history (some sprints in points, later ones in hours) must render
  honestly rather than summing apples and oranges — per-entry `estimateUnit`
  makes that the client's decision, and the desktop chart should not draw a
  single continuous effort series across a unit change (§4).

### 3.3 Desktop report UI

- The burndown/velocity SVGs already exist in `octbase-frontend/js/views-agile.js`
  (`renderBurndownChart` / `renderVelocityChart`) reading `api.sprints.burndown` /
  `api.reports.velocity`.
- Add a **tasks ⇄ effort unit toggle** on the reports panel, shown **only when
  the project's `estimationUnit` is not `NONE`**, labelled with the actual unit
  ("Tasks / Story points" or "Tasks / Hours"). When effort is selected, request
  `?unit=points|hours` and relabel the axis from the echoed `unit`.
- Surface the **"N unestimated"** hint whenever the response reports any.
- On the velocity chart, mark (or break) a unit change rather than drawing one
  continuous line across it.
- Reuse existing helpers (`http`, `api`, `esc`, `toast`, modal helpers, global
  `S`); **no framework**. Respect the frontend guards.
- **i18n (English + German)** for every new label (`report.unit.*`,
  `report.unestimated`). German matters; this closes a German-filed request.
- **Mobile `octbase-mobile` is explicitly out of scope** — it has no reports view
  and gains none here.

## 4. Data & migrations

- **One migration, up/down pair:** add `committed_estimate NUMERIC NULL`,
  `completed_estimate NUMERIC NULL`, `estimate_unit TEXT NULL` to `sprints`
  (mirroring migration 015's count snapshot). Expected migration version is
  **auto-derived** — no constant to bump.
- `completeSprint` must fill all three in the same transaction it fills the
  counts.
- If demonstrating a real effort burndown needs a seeded, estimated sprint, seed
  it deliberately and update every test that asserts seed shape — **seed data is
  public surface**.

## 5. Architectural fit (hold the build against these)

- **Bounded context:** all of this is `internal/workmanagement` (sprints and
  `report_handler.go`). No new context; no cross-context reach against the
  archtest direction rules.
- **Structs are the contract:** the new burndown/velocity response fields *are*
  the API change; tests assert exact shapes.
- **Stable error codes:** reuse `ESTIMATION_UNIT_INACTIVE` from 42a (do not mint
  a second code for the same condition); `SPRINT_NOT_STARTED` unchanged.
- **Tests are integration-style:** extend `report_test.go` (real router, real
  migrations, Postgres via `internal/testutil`) with an effort-burndown case, an
  effort-velocity case, a back-compat `unit=tasks` case, an `unestimated` case,
  and a mixed-unit velocity history. Keep coverage above the CI floor.
- **Run the backend diff through `go-security` and the `go-backend-reviewer`
  agent** — a reused reconstruction path plus new query-param handling.

## 6. MVP vs later

**MVP:** `?unit=tasks|points|hours` on burndown with an `unestimated` signal and
the unit echoed back; effort snapshot on sprint completion (`committed_estimate`,
`completed_estimate`, `estimate_unit`); velocity extended with those fields;
desktop unit toggle + axis labelling + unestimated hint; en + de; migration
up/down; OpenAPI (**delete the "Counts tasks, not effort" caveat** on the
burndown/velocity descriptions — 42a already reworded the original note to
"Counts tasks, not effort: tasks can now carry a …"; once reports honor
estimates that caveat goes away entirely), user guide, CHANGELOG `## Unreleased`.

**Later:** making effort the default unit once adoption is proven, cumulative-flow
and scope-change lines, capacity planning against velocity, per-assignee effort,
CSV export of report data, and a mobile reports view.

## 7. Definition of done (verifiable)

- `GET /sprints/{id}/burndown?unit=points` in a `POINTS` project returns
  `committed` = Σ points of the committed tasks, and a `remaining` series that
  drops by a task's points on the day it goes DONE — proven against a seeded
  sprint, not asserted by inspection.
- The same call in an `HOURS` project works for `?unit=hours`, and the mismatched
  unit (and any effort unit in a `NONE` project) returns 422
  `ESTIMATION_UNIT_INACTIVE`.
- `?unit=tasks` and the no-param call are **unchanged from today** (regression
  test).
- A sprint with unestimated committed tasks reports a non-zero `unestimated`, and
  the response echoes its `unit`.
- Completing a sprint snapshots `committedEstimate` / `completedEstimate` /
  `estimateUnit`; velocity returns them alongside the untouched count fields; a
  sprint completed under `NONE` reports nulls and does not break the chart.
- Switching the project's unit after a sprint completed does **not** change that
  sprint's historical velocity numbers or their labelled unit.
- Desktop: the toggle is absent when estimation is off, switches tasks ⇄ effort
  with no code change, labels the axis from the echoed unit, flags unestimated
  tasks, and renders in en + de.
- OpenAPI updated (the "no estimate field" note is gone, `unit` and the new
  response fields documented); user guide, CHANGELOG, archtest, coverage floor,
  frontend guards and i18n all green.

## 8. Product decisions (confirmed by Lars, 2026-07-27, and inherited from 42a)

1. **A project has one estimation unit** (`NONE` | `POINTS` | `HOURS`), so a
   report has one effort unit — there is no points-*and*-hours chart.
2. **`unit=tasks` stays the default** on burndown; effort is an explicit opt-in,
   with no silent behaviour change for existing clients.
3. **Unestimated tasks count as 0 but are reported**, so the UI can warn instead
   of lying.
4. **Historical sprints keep the unit they were completed in** — the snapshot
   stores the unit, the report never re-reads it from the project.
5. **Hours are estimates, not logged time** — there is no actuals-vs-estimate
   report in this scope.
