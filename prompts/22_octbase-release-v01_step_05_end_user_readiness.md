You are a senior product manager and frontend engineer focused on the actual humans who will use Octbase every day. Read `prompts/_release-v01-audit.md` first, especially the "demo-mode / bootstrap" finding from `step_00` — it's likely the headline issue for this step.

Principle: a feature is not done when the API supports it. It's done when a non-technical user can discover it, use it correctly, recover from a mistake, and understand what happened when something fails.

## Practical steps

1. **First-run bootstrap (likely release blocker)**
   - With a fresh database and `OCTBASE_DEMO_MODE=false`, start the API and try to create the very first account. Trace the path: is there a `/signup` route? An invitation requires an existing ADMIN — but if no users exist, who sends the first invitation?
   - If there's no bootstrap path:
     - Add a startup-time bootstrap: if the `users` table is empty at startup AND `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` + `OCTBASE_BOOTSTRAP_ADMIN_PASSWORD` env vars are set, create one SUPER_ADMIN user with those credentials (hashed, same as normal signup). Log a single line (no password) confirming creation. Document both vars in `.env.example` and `README.md`.
     - Alternatively (if simpler given the existing invitation code), add a one-shot CLI subcommand: `octbase-api bootstrap-admin --email=... --password=...` that exits after creating the user — useful for ops to run once against prod.
   - Add a test covering: empty DB + bootstrap env vars set → SUPER_ADMIN exists after startup; empty DB + vars unset → no user created, no crash; non-empty DB + vars set → no duplicate/second admin created.

2. **Error message audit**
   - Grep the frontend for places that might leak raw backend errors:
     ```bash
     grep -rn 'catch\|\.message\|err\.' octbase-frontend/js/app.js | head -50
     ```
   - For each API error surfaced to the user, confirm:
     - It goes through the i18n layer (`octbase-frontend/js/i18n.js`) — no hardcoded English strings in error paths.
     - It's a human sentence ("Could not save task — please try again"), not `err.toString()` or a raw JSON error body.
   - On the backend, spot-check 3–4 handlers (auth, task create, project delete, webhook) and confirm error responses use a consistent envelope (`{"error": {"code": "...", "message": "..."}}` or whatever the existing convention is) and never include `err.Error()` from a DB driver directly in the response body.

3. **Empty / loading / offline states**
   - Manually walk these screens with an otherwise-empty project (no tasks, no pages, no releases) and with the API briefly stopped:
     - Dashboard ("My Work")
     - Board / Backlog / Task list
     - Pages
     - Notifications bell
     - SSE reconnect banner
   - For each, confirm there's a real empty-state message (with a next action, e.g. "No tasks yet — press N to create one") rather than a blank area, and that stopping the API produces a visible "connection lost, retrying..." indicator rather than a silent freeze.
   - Fix any screen that shows nothing or a raw error. Keep fixes scoped to adding/correcting empty/error-state markup and i18n strings — don't redesign layouts (that's `step_06`).

4. **Destructive action safety**
   - Confirm project deletion, user deletion, and bulk-archive all require an explicit confirmation step (modal/dialog), and that the project-deletion confirmation explicitly lists what will be deleted (tasks, boards, releases, pages, members, activity, SCM refs — per the README's cascade description).
   - If the confirmation text is generic ("Are you sure?") rather than specific about cascading impact, update the copy (with i18n) to be specific.

5. **Accessibility & i18n regression spot-check**
   - Re-test these flows against WCAG 2.2 AA basics (keyboard-only navigation, visible focus, screen-reader labels) and in at least two configured languages:
     - Login
     - Create task (inline `N` shortcut flow)
     - Board drag-and-drop / keyboard-based status change
     - Command palette (`Ctrl+K`)
   - These flows were targeted by `19_octbase-wcag.md` and `20_octbase-multi-lang.md`, and may have regressed from `21_octbase-design-tuning.md`'s spacing/layout changes. Fix regressions in place; don't redo the full WCAG/i18n passes.

6. **End-user documentation**
   - Check `octbase-frontend/user-guide.html` (the single canonical user guide) — is it current with the actual feature set (admin panel, sprints/releases, command palette, SCM integration, imports)?
   - Update stale sections. If the in-app `?` shortcut help (mentioned in the README) doesn't cover the full shortcut set, update it.
   - This is the non-technical-user-facing doc — keep language plain, no developer jargon ("API", "endpoint", "JWT").

## Deliverable

Append to `prompts/_release-v01-audit.md`:
- Bootstrap solution implemented (env vars or CLI subcommand) + test results.
- Error-message audit findings and fixes (before/after examples for 2–3 cases).
- Empty/loading/offline state fixes, screen by screen.
- Destructive-action confirmation copy changes.
- Accessibility/i18n regressions found and fixed.
- User-guide updates summary.

Verification:
```bash
cd octbase-api && go test ./...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k "not rbac"   # or full suite if SUPERADMIN env vars are set
```
