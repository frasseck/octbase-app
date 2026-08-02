You are a senior product designer and frontend engineer doing the final UI and feature fine-tuning pass for Octbase v0.1. This step has two parts: **analyze first, then tune**. Do not skip the analysis — fine-tuning without it tends to "fix" things that aren't broken and miss things that are.

This step comes after `step_05` (end-user readiness, empty/error states) and assumes `21_octbase-design-tuning.md`'s spacing/design-token system is already in place. Read `prompts/_release-v01-audit.md` first.

## Part A — Analysis (no code changes)

1. **Feature inventory vs. usage reality**
   - From the README's feature list, build a table of every user-facing feature (board, backlog, releases, sprints, docs/pages, SCM integration, imports, notifications, command palette, admin panel, etc.).
   - For each, note: is it reachable from the UI without reading docs? Is there an entry point from the main navigation, the dashboard, or the command palette? Anything that's API-only (built but not wired into the UI) is a gap — list it.

2. **Screen-by-screen walkthrough**
   - Open each major screen (Dashboard, Board, Backlog, Task list, Task panel, Releases, Sprints, Pages, Admin panel, Audit log, Notifications) and record, per screen:
     - Visual issues: inconsistent spacing/alignment that survived `step_21` (design tuning), inconsistent button styles, truncation/overflow issues with long task titles or names.
     - Interaction issues: anything that requires more clicks than it should for a common action; any action with no visible feedback (no toast/spinner) after clicking.
     - Density: is information density appropriate (not too sparse on a 1080p screen, not cramped on a laptop)?
   - Use actual data, not just the demo seed — create a project with ~30 tasks, several long titles, several users, and a couple of pages with deep headings, so layout issues under realistic load are visible.

3. **Feature completeness vs. core workflow**
   - Walk the core loop: create project → create sprint → add tasks to backlog → move to sprint → work tasks on board → complete sprint → ship release. At each step, note anything that feels incomplete, confusing, or requires a workaround.
   - Classify every issue found as: **Fix now** (small, high-impact, in scope for v0.1), **Defer** (real but not blocking, becomes a backlog item), or **Out of scope** (speculative feature, not requested).

Produce an analysis report appended to `prompts/_release-v01-audit.md` under "UI & Feature Analysis" before doing any Part B work. Get the "Fix now" list to a manageable size (aim for the highest-impact ~10–15 items) — this is fine-tuning, not a redesign.

## Part B — Fine-tuning (implementation)

For each "Fix now" item from Part A:

1. **Visual/spacing fixes**: apply using the existing design tokens from `21_octbase-design-tuning.md` (`--space-*` variables in `octbase-frontend/css/app.css`). Do not introduce new ad-hoc values.

2. **Missing feedback states**: for any action without visible feedback, add a toast/inline confirmation using the existing notification/toast pattern already present in `app.js` (search for existing toast helper before adding a new one).

3. **Truncation/overflow**: for long task titles, project names, or user names — apply `text-overflow: ellipsis` with a `title` attribute (tooltip) for the full value, or wrap, depending on context. Verify against the realistic dataset from Part A.

4. **Navigation gaps**: for any feature found reachable only via direct URL/API in Part A step 1, add a discoverable entry point — nav link, dashboard card, or command-palette entry (`Ctrl+K`) — using existing patterns. Do not build new navigation chrome from scratch.

5. **Core-loop friction**: for friction points in the create→sprint→board→complete→release loop, prefer copy/labeling fixes and small UI affordances (e.g., a button to "Start Sprint" directly from the backlog view if currently only reachable from project settings) over new screens.

6. **Responsive check**: re-verify the top 5 fixed items at three widths — desktop (≥1280px), tablet (~768px), mobile (~390px) — since spacing/layout fixes can shift breakpoints.

## Constraints

- No new dependencies, no framework introduction (vanilla JS/CSS stays vanilla).
- No new database tables/migrations — if a "Fix now" item needs a schema change, move it to "Defer" and flag it for a future prompt.
- Preserve all keyboard shortcuts and bookmarkable URL behavior (per README's "Navigation & UX" section) — test that filter/hash state still round-trips after layout changes.
- Re-run the WCAG/i18n spot-checks from `step_05` on any screen you touch.

## Deliverable

Append to `prompts/_release-v01-audit.md`:
- Part A: full analysis report (feature inventory table, screen-by-screen findings, core-loop friction list, with Fix now/Defer/Out-of-scope classification).
- Part B: for each "Fix now" item, before/after description and files changed.
- Screenshots are not required, but describe visual changes precisely enough that a reviewer can verify by following the same walkthrough.

Verification:
```bash
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest
```
