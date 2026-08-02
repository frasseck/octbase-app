# Octbase — Advanced Task Search

**Role:** Act as a Senior Full-Stack Engineer (Go + vanilla-JS frontend).

**Purpose:** Build a **full advanced task search** — a dedicated search experience for
finding tasks by free-text **and** structured filters, with relevance-ranked results and a
usable results UI. Octbase already ships a *basic* search; this work **extends it**, it does
not replace the parts that work.

**What exists today (build on it, don't duplicate):**
- `GET /api/v1/projects/{projectId}/search/tasks?q=` → `Handler.SearchTasks` →
  `TaskRepo.SearchByTitle` — a plain `ILIKE '%q%'` on `title`/`description`, paginated,
  ordered by `created_at DESC`. No filters, no ranking.
- `GET /api/v1/search?q=&projectId=` → `Handler.UnifiedSearch` — global command-palette
  search returning ≤ 5 tasks/pages/projects (title-only `ILIKE`). **Keep as-is**; the
  command palette stays a quick-jump, not the advanced view.
- Frontend: `renderSearch` + `.search-page` (a thin search page) and the command palette
  overlay (`#palette-overlay`).
- `Handler.ListTasks` already filters tasks by `status`, `priority`, `assigneeId`,
  `type`, with `sortBy`/`order` + pagination — **reuse this filter vocabulary**.

---

## 1. Scope

- **Backend:** `octbase-api/internal/workmanagement/` — `search_handler.go`, `repo.go`,
  route registration in `handler.go`, and tests (`search_handler_test.go` /
  `handler_test.go` / `search_dashboard_test.go`). Reuse the existing helpers:
  `memberGuard`, `shared.ParsePagination`, `ValidStatus` / `ValidPriority` and the
  `TaskType*` constants in `domain.go`.
- **Frontend:** `octbase-frontend/` — `js/app.js` (the `renderSearch` view + filter
  controls), `css/app.css` (`.search-*`), `locales/en|de|fr.json`.
- **Out of scope:** the command palette / `UnifiedSearch` behavior, and any new DB concept
  not already modeled.

> ⚠️ `js/app.js` has non-ASCII bytes in comment banners — search it with
> `LC_ALL=C grep -a …` or grep treats it as binary.

> ⚠️ **There is no label/tag concept on tasks** (`domain.go` `Task` has no labels). Do
> **not** invent one. The filterable dimensions are exactly the columns that exist.

---

## 2. Backend — search endpoint

Extend the per-project task search into a real query endpoint. Prefer extending
`GET /api/v1/projects/{projectId}/search/tasks` (keep the path) to accept the full filter
set below; optionally add a global `GET /api/v1/search/tasks` (no `projectId`) that searches
every project the user can see, for cross-project search.

### 2.1 Query parameters (all optional, combinable, AND-ed together)
- `q` — free text, ≤ 500 chars (keep the existing length guard + `QUERY_TOO_LONG`).
- `status` — one or repeated; validate against project statuses (`ValidStatus` shape).
- `priority` — `LOW|MEDIUM|HIGH|CRITICAL` (`ValidPriority`).
- `type` — `TASK|BUG|STORY|EPIC|CHORE` (`TaskType*`).
- `assigneeId`, `reporterId`, `reviewerId` — user id, or the literal `none` for unassigned.
- `sprintId`, `releaseId` — id, or `none`.
- `dueFrom` / `dueTo`, `createdFrom` / `createdTo`, `updatedFrom` / `updatedTo` — ISO dates.
- `includeArchived` — default `false` (exclude `ARCHIVED` like `UnifiedSearchTasks` does).
- `sortBy` (`relevance|created|updated|due|priority`) + `order` (`asc|desc`); default
  `relevance` when `q` is present, else `updated desc`.
- `page` / `size` via `shared.ParsePagination`.

Reject unknown enum values with a `422 VALIDATION_ERROR` carrying the offending `Field`
(match the existing `DomainError`/error-response convention so the frontend can mark the
field). Empty `q` with filters present is valid (filter-only search).

### 2.2 Relevance ranking (replace blind ILIKE for the `q` part)
Move text matching from substring `ILIKE` to **Postgres full-text search**:
`to_tsvector('simple', title || ' ' || coalesce(description,'')) @@
websearch_to_tsquery('simple', $q)`, ordered by `ts_rank`. Keep a trailing `ILIKE`
(or `pg_trgm`) fallback so short/partial tokens (e.g. `aut`) still match — full-text alone
won't match prefixes. Title matches should outrank description-only matches. Add a GIN index
on the tsvector (or an expression index) via a migration if migrations live in the repo;
otherwise document the index in the prompt's verification notes.

### 2.3 Response
Return a **paginated envelope** (reuse the project's existing list/pagination response
shape — don't invent a new one): the result rows plus total count and page info. Each row
carries what the results UI renders without an extra fetch: `id, seqNumber, title, taskType,
status, priority, assigneeId, sprintId, releaseId, dueDate, projectId, updatedAt`, and (when
`q` is set) a short highlighted snippet (`ts_headline`) — sanitize/escape it; never return
raw HTML the frontend injects unescaped.

### 2.4 Authorization
Per-project search keeps `memberGuard`. The global variant must only return tasks from
projects the user can see (mirror `UnifiedSearchTasks` / `SearchVisible` visibility) — never
leak titles across permission boundaries.

---

## 3. Frontend — advanced search view

Grow `renderSearch` (`.search-page`) into the advanced experience; keep the command palette
separate.

- **Search bar:** text input (debounced ~250 ms, min 2 chars to fire `q`, but filters can
  search alone), submit on Enter, a clear button, and a result count.
- **Filter controls:** reuse the existing task-list filter widgets / option lists so the
  vocabulary matches the board and backlog — Status, Priority, Type, Assignee, Reviewer,
  Sprint, Release, and date-range pickers (due / created / updated). Active filters show as
  removable chips; an "Clear all" resets them. Reflect the current query + filters in the
  URL (query string) so a search is shareable/back-button-safe.
- **Results list:** one row per task — type icon, `#seqNumber`, title with the highlighted
  snippet, status chip, priority dot, assignee avatar, due date. Clicking a row opens the
  task detail panel (reuse the existing open-task path), not a new page. Sort control bound
  to `sortBy`/`order`. Keyboard nav (↑/↓ + Enter) over results.
- **States:** loading skeleton, empty ("no tasks match" + a hint to relax filters), and a
  zero-query idle state (e.g. recent / assigned-to-me). Pagination or infinite scroll using
  the envelope's page info.
- **Responsive:** filters collapse into a toggle on Compact widths; results stay a single
  scrollable column. (Coordinate with the responsive system in
  `28_octbase-full-responsive-design.md` — no new breakpoints, reuse the tokens, no new
  hardcoded hex/spacing/radius literals.)
- **i18n:** every label, placeholder, filter name, and empty/error string goes through
  `en/de/fr.json` (keep all three in sync) — no hardcoded English in markup.

---

## 4. Acceptance criteria

- [ ] `GET /api/v1/projects/{projectId}/search/tasks` accepts `q` **and** the §2.1 filters,
      combinable; filter-only (empty `q`) search works.
- [ ] Text search is relevance-ranked (full-text + `ts_rank`) with a partial-match fallback;
      title matches outrank description-only matches.
- [ ] Unknown enum/date values return `422` with the offending field; `q` > 500 chars still
      returns `QUERY_TOO_LONG`.
- [ ] Archived tasks are excluded unless `includeArchived=true`.
- [ ] Response is the project's standard paginated envelope with total count; rows include
      the fields the UI needs + an escaped highlight snippet.
- [ ] Global/cross-project search (if added) only returns tasks the user may see.
- [ ] The command palette / `UnifiedSearch` is unchanged.
- [ ] The advanced search view supports text + all filters, removable filter chips, URL
      state, keyboard nav, and opens results in the task panel.
- [ ] Loading / empty / idle states exist; layout works at 360 / 768 / 1440 px with no
      unintended horizontal scroll.
- [ ] All labels are i18n (en/de/fr in sync); no hardcoded literals.
- [ ] New Go tests cover: each filter, combined filters, ranking order, archived exclusion,
      validation errors, and cross-project visibility. All `octbase-api` tests pass.
- [ ] `octbase-frontend/tests` Playwright tests pass; a test covers searching + filtering +
      opening a result.

---

## 5. Build / verify

```bash
# Backend
cd octbase-api && go test ./internal/workmanagement/... && go build ./...

# Frontend (static bundle served by Caddy/nginx — rebuild the container after edits)
podman-compose build octbase-frontend
podman stop octbase_octbase-frontend_1 && podman rm octbase_octbase-frontend_1
podman-compose up -d octbase-frontend
```

> Before running, screenshotting, or visually verifying the frontend, invoke the
> **`frontend-testing`** skill first (per `CLAUDE.md`).

Run the `octbase-frontend/tests` Playwright suite, then verify by hand: search a term, apply
and remove filters, change sort, page through results, and open a result into the task panel
— at mobile and desktop widths.
