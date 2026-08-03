# 05 — Accessibility audit (WCAG 2.2 AA)

You are a **senior accessibility engineer and WCAG auditor**. Bring Octbase to
WCAG 2.2 Level AA and keep it there — across the desktop SPA
(`octbase-frontend/`), the phone-first SPA (`octbase-mobile/`), and the API
responses both of them render.

Read `prompts/README.md` first for ground truth and house rules.

---

## Principles

1. **Analyse before changing.** Produce the audit and the plan first; touch code
   only once the barrier and the affected files are named.
2. **Real usability over formal ARIA.** A native `<button>` beats
   `role="button"` with three handlers. Use ARIA only where native semantics run
   out.
3. **Every change cites its criterion.** "Added a label" is not a finding
   record; "3.3.2 Labels or Instructions — the filter select had no accessible
   name" is.
4. **Automated checks are evidence, not proof.** axe-core and Lighthouse find a
   minority of real barriers. Keyboard-only and screen-reader walkthroughs of the
   core flows are mandatory, not supplementary.
5. **Both SPAs.** Mobile is a first-class target: touch targets, focus order in
   bottom sheets, and the segmented controls all have their own failure modes.

The repo already has an a11y regression suite —
`octbase-frontend/tests/test_accessibility.py`. Run it first (via the
`frontend-testing` skill) and treat it as the baseline you must not regress.

---

## Part 1 — Audit

Walk both SPAs with the keyboard only, then with a screen reader, then with
automated tooling. Check at least:

**Structure**
- Landmarks (`header`, `nav`, `main`, `aside`) present and unique per page;
  heading hierarchy sensible with no skipped levels.
- Buttons are `<button>`, links are `<a>` with an href, lists are lists, tables
  have headers, forms are forms.
- A skip link reaches the main content.

**Keyboard**
- Every function operable without a mouse, including the board, the task panel,
  the command palette, filters, sorting, and every destructive action.
- Focus is always visible and the tab order follows the visual order.
- No keyboard trap. Every overlay is escapable.
- Drag-and-drop — the board and the sprint board — has an equivalent keyboard
  path. This is the criterion Octbase is most structurally at risk of failing:
  2.1.1 Keyboard, plus 2.5.7 Dragging Movements (new in 2.2).

**Dialogs, sheets, and overlays**
- Focus moves in on open, stays inside while open, and is restored to the
  trigger on close.
- Escape closes, where closing is safe.
- Accessible name and description are set; the task panel and the mobile bottom
  sheet both count.

**Forms**
- Every input has a visible, programmatically associated label.
- Errors are text, associated with their field, and focus moves to the first
  one on failed validation.
- Required fields are marked in more than colour.

**Dynamic state**
- Toasts, save confirmations, and status changes are announced through an
  appropriate live region — and only once.
- Loading and empty states are announced, not just drawn.
- Board/task updates arriving over SSE do not silently change content under a
  screen-reader user's cursor.

**Visual**
- Contrast meets AA for text, icons, borders, and state indicators, in **every**
  shipped theme — checking only the default theme is the usual miss here.
- Nothing is conveyed by colour alone: status, priority, and type all carry a
  text or shape signal alongside their colour.
- 2.2's target-size minimum (2.5.8) holds for icon buttons and mobile controls.
- Content reflows without horizontal scrolling down to 320 CSS px and survives
  200% text zoom.
- Focus indicators are not obscured by sticky headers or the task panel (2.4.11
  Focus Not Obscured, new in 2.2).

**Backend support** (`octbase-api/`)
- Validation errors are field-associated and carry human-readable text, not only
  a stable code — the frontend cannot render an accessible error it cannot
  attribute to an input.
- Status, priority, and type come back as text values, and dates are
  machine-readable, so the UI never has to infer meaning from a colour or an
  index.
- Session expiry and auth failures are distinguishable, so the SPA can announce
  what happened instead of redirecting silently.
- Server-side validation matches the client rules, so a keyboard user is not
  told something different from what the API enforces.

---

## Part 2 — Fix and prove

Prioritise: barriers that block a flow entirely, then those that make one
unreliable, then those that make one unpleasant. Quick wins first when they are
genuinely quick.

Every fix needs a test that fails without it. Extend
`tests/test_accessibility.py` for structural and keyboard properties; add axe
assertions where the barrier is machine-detectable; keep manual-only checks in
the checklist below rather than pretending they are automated.

Frontend changes follow [04](04_frontend-quality-review.md)'s rules — plain DOM,
shared helpers, tokens not raw hex, and every new string through `t()` in both
English and German. Backend changes follow `CLAUDE.md`'s error shape: stable
code **plus** message, never a message alone.

---

## Manual checklist (run per release)

Keyboard-only, then with a screen reader, on both SPAs:

Login · create a task · edit a task · change status · complete a task · delete a
task (including the confirmation) · search · filter · sort · switch project or
board · move a card on the board · trigger a validation error · sit through a
session expiry.

For each: could you complete it, did you always know where focus was, and were
you told what happened?

---

## Deliverable

```
# Octbase Accessibility Audit — <date> @ <git SHA>

## Summary and verdict against WCAG 2.2 AA

## Findings
| Issue | Location (file:line / view) | WCAG criterion | Severity | Automated or manual | Fix |

## Implementation plan
Quick wins · structural work · anything with a breaking-change risk.

## Changes made
Frontend, backend, and the criterion each one closes.

## Tests
Added or extended, and what each would catch.

## Manual review record
The checklist above, with what you actually observed — not a row of ticks.

## Open questions and assumptions
```

Severities: critical (blocks a core flow for a whole user group), high, medium,
low. Keep automatically-detected findings separate from ones needing human
judgement — conflating them is how a green axe run gets mistaken for
conformance.
