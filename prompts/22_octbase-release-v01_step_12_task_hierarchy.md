You are a senior software architect and agile coach adding **task hierarchy (epics, stories/tasks/bugs, and subtasks)** to Octbase, so that work can be organized the way real agile teams do it. Read `prompts/_release-v01-audit.md` first. This is a schema-affecting change — coordinate with `step_02` (migration/idempotency standards) and `step_09`–`step_11` if they've landed (hierarchy should be visible in the task preview overlay and panel).

## Context

`internal/workmanagement/domain.go` already defines `task_type` values: `TASK`, `BUG`, `STORY`, `EPIC`, `CHORE` — but there is currently **no parent/child relationship** between tasks. Epics exist as a type but have no way to contain stories; there's no subtask concept at all.

## Phase 1 — Agile model (design, no code changes yet)

Define the hierarchy explicitly and document it in `prompts/_release-v01-audit.md` under "Task hierarchy model" before writing code:

1. **Levels** (standard agile structure):
   - **Epic** (`task_type = EPIC`): top-level container, no parent. Represents a large body of work, typically spans multiple sprints/releases.
   - **Story / Task / Bug / Chore** (`task_type` = `STORY`/`TASK`/`BUG`/`CHORE`): the unit of work that goes on the board and into sprints. May optionally belong to one Epic (`epic_id`).
   - **Subtask** (new `task_type = SUBTASK`): a checklist-like breakdown of a Story/Task/Bug, owned by exactly one parent via `parent_task_id`. Subtasks do **not** have their own subtasks (max depth = 1 below a Story/Task/Bug), and cannot belong to an Epic directly (`epic_id` must be null for subtasks — they inherit the epic from their parent for reporting/board purposes).
2. **Invariants** (enforce in the domain/service layer, not just docs):
   - `EPIC` tasks: `epic_id = NULL`, `parent_task_id = NULL`.
   - `SUBTASK` tasks: `parent_task_id` references a non-`SUBTASK`, non-`EPIC` task in the same project; `epic_id = NULL`.
   - `STORY`/`TASK`/`BUG`/`CHORE`: `parent_task_id = NULL`; `epic_id` optionally references an `EPIC` task in the same project.
   - No cross-project links (parent/epic must be in the same `project_id`).
   - Deleting an Epic does not delete its stories — it un-links them (`epic_id = NULL`) and the user is warned (cf. `step_05`'s "data loss prevention" for destructive actions). Deleting a Story/Task/Bug with subtasks: either cascade-delete subtasks or block deletion until subtasks are removed/reassigned — pick one, document it, and make the UI message match.
3. **Board/sprint semantics**: confirm with the existing sprint logic (`007_sprints` migration, sprint completion logic in `step_02`) how subtasks interact with sprints — recommended: subtasks are tracked for completion bookkeeping but do not independently occupy board columns separate from their parent (avoid clutter); epics are never sprint-scheduled themselves (only their stories are). Document the chosen behavior.

## Phase 2 — Backend implementation

1. **Migration** `011_task_hierarchy`: add `epic_id TEXT REFERENCES tasks(id)` and `parent_task_id TEXT REFERENCES tasks(id)` to `tasks`, both nullable, plus indexes (`idx_tasks_epic_id`, `idx_tasks_parent_task_id`) and a `down` migration. Add `SUBTASK` to wherever `task_type` is validated (`ValidTaskType` in `domain.go`).
2. **Validation**: implement the Phase 1 invariants in `internal/workmanagement/service.go` (`CreateTask`/`UpdateTask`) — reject violating combinations with clear, localized error messages (per `step_05`'s error-message standards). Add a same-project check for `epic_id`/`parent_task_id`.
3. **Cascade/un-link behavior**: implement the Epic-deletion un-link and Subtask-deletion/blocking behavior chosen in Phase 1, in the same transaction as the existing project/task deletion paths (`repo.go`).
4. **API surface**: extend the task read DTO (`domain.go` ~line 153 area) with `epicId`, `parentTaskId`, and (for Epics/Stories) a `subtaskCount`/`subtaskDoneCount` summary for progress display — computed efficiently (single aggregate query), not N+1.
5. **Sequence numbers**: confirm `TB-42`-style sequence numbering (flagged for concurrency in `step_02`) is unaffected — subtasks get their own sequence numbers like any other task.
6. **Tests**: cover each invariant (valid/invalid parent-epic combinations, cross-project rejection, cascade/un-link on delete, subtask progress aggregation) in `internal/workmanagement/*_test.go`.

## Phase 3 — Frontend implementation

1. **Backlog/board grouping**: add an "Group by Epic" view option in the backlog (per `step_06`'s navigation patterns — extend existing filter/view controls, don't add new chrome). Stories without an epic group under an "No epic" bucket.
2. **Task panel**:
   - For a Story/Task/Bug: show its Epic (if any) as a breadcrumb/chip with a link to filter the backlog by that epic, and a "Subtasks" checklist section — list existing subtasks with a status checkbox/quick-status control and progress (`done/total`), plus an inline "add subtask" affordance that creates a `SUBTASK` task pre-linked via `parent_task_id`.
   - For an Epic: show a "Stories in this epic" list with aggregate progress (e.g. "3/8 stories done").
   - For a Subtask: show its parent with a breadcrumb/link, no epic chip (inherits from parent — show the parent's epic transitively if useful).
3. **Task creation**: when creating a task from within a Story/Task/Bug's subtask section, default `task_type = SUBTASK` and pre-fill `parent_task_id`; when creating from an Epic's view, offer to pre-fill `epic_id`.
4. **Preview overlay (`step_11`)**: if landed, show the epic/parent breadcrumb and subtask progress in the preview too, consistent with the full panel.
5. **i18n & accessibility**: new labels (Epic, Subtask, "Group by epic", progress text) go through i18n; checklist/progress controls are keyboard-operable and announce state changes (`aria-live` for progress updates), per `step_05`'s WCAG baseline.

## Constraints

- Preserve existing task_type values and behavior for tasks with no parent/epic — this is additive, not a breaking change to existing data (existing rows get `epic_id = NULL, parent_task_id = NULL` by default).
- No change to sprint/release schema beyond what's needed to confirm subtask interaction in Phase 1.3.
- Keep the UI changes additive to existing backlog/board/panel layouts — don't restructure screens beyond what's needed (cf. `step_06`'s "fine-tuning, not a redesign" philosophy).

## Deliverable

Append to `prompts/_release-v01-audit.md` under "Task hierarchy": the agile model from Phase 1 (with a short rationale — agile sources/conventions referenced), migration summary, invariants enforced, cascade/un-link decision, and UI changes with before/after description.

## Verification

```bash
cd octbase-api && go vet ./... && go test -race ./internal/workmanagement/...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k "hierarchy or subtask or epic"
```
