You are a professional code reviewer — the kind a team would hire for a pre-launch external review. This step is a **review**, not a refactor: produce findings, and only fix issues that are small, safe, and high-confidence. Anything larger goes into the findings report for a human decision. Run this step *last*, after `step_01`–`step_07`, so it reviews the release-ready state, not the pre-release state. Read `prompts/_release-v01-audit.md` for full context on what's already been changed.

Scope: `octbase-api/` (Go backend) and `octbase-frontend/` (vanilla JS frontend). Treat this as if it were a paid external code review with a written report as the primary deliverable.

## Part A — Backend review (`octbase-api/`)

Review each package in `internal/` (`auth`, `identityaccess`, `rbac`, `usermgmt`, `auditlog`, `admin`, `workmanagement`, `docs`, `scmintegration`, `notifications`, `sse`, `webhooks`, `mailer`, `activity`, `shared`, `testutil`) for:

1. **Correctness**
   - Error handling: are errors checked, wrapped with context (`fmt.Errorf("...: %w", err)`), and not silently swallowed?
   - Resource cleanup: are `Close()`, `defer`, transactions (`Commit`/`Rollback`), and contexts handled correctly, especially in `shared` (DB) and `sse`?
   - Concurrency: any shared mutable state without a mutex/channel? (Cross-reference `step_02`'s race-condition findings — don't re-litigate those, just confirm they're resolved.)

2. **Architecture & boundaries**
   - Does `rbac` remain pure (no DB calls), as the README claims ("pure permission functions ... no DB")?
   - Do `internal/workmanagement`, `internal/docs`, etc. depend on `internal/shared` correctly without circular imports?
   ```bash
   cd octbase-api && go list -deps ./... | sort -u > /tmp/deps.txt
   ```
   Spot-check for any package importing something that creates a layering violation (e.g. `rbac` importing `shared/db`).

3. **Consistency**
   - Error response envelope consistent across handlers (cross-check with `step_05`'s error-message audit).
   - Naming consistency: handler functions, repository methods, DTO structs — same patterns across packages (e.g. `workmanagement` vs. `docs` vs. `scmintegration` should look like siblings, not different codebases).
   - Are there duplicated helper functions across packages that belong in `shared`?

4. **Dead code & unused dependencies**
   ```bash
   cd octbase-api && go vet ./... && golangci-lint run ./... --enable=unused,deadcode 2>/dev/null || golangci-lint run ./...
   go mod tidy -diff
   ```
   List anything `go mod tidy` would remove, and any exported-but-unused functions/types found by lint or manual grep.

5. **Test quality**
   - Are tests asserting behavior (status codes, response bodies, DB state) rather than just "no error returned"?
   - Any tests that are skipped, commented out, or marked with `t.Skip` without explanation?
   ```bash
   grep -rn 't.Skip\|TODO.*test\|// skip' octbase-api/internal
   ```

## Part B — Frontend review (`octbase-frontend/`)

1. **`js/app.js` structure**
   - Is the file organized into clear sections/modules (API client, routing, rendering, state)? If it's grown into one large undifferentiated file, recommend (don't necessarily execute, unless small) a split into modules with `<script type="module">` — note this is a larger change and should be a "Defer" recommendation unless trivially small.
   - Centralized API client: confirm all `fetch` calls go through one helper (auth headers, error handling, base URL) — flag any handler that calls `fetch` directly bypassing it.

2. **`js/i18n.js`**
   - Confirm `i18n.test.js` covers the public API (translation lookup, fallback language, missing-key behavior).
   ```bash
   node --test octbase-frontend/js/i18n.test.js 2>/dev/null || node octbase-frontend/js/i18n.test.js
   ```
   - Check for hardcoded strings outside the i18n system introduced by recent steps (`step_05`, `step_06`, `step_07`).

3. **CSS (`css/app.css`)**
   - Confirm adherence to the `step_21`/`step_06` design-token system — flag any new hardcoded px/color values introduced since.
   - Check for unused/duplicate selectors:
   ```bash
   grep -c '^\.' octbase-frontend/css/app.css
   ```
   (manual spot-check for obvious duplication is fine — don't need a full coverage tool).

4. **Security spot-check** (cross-reference, don't redo `step_01`)
   - Grep for `innerHTML`, `outerHTML`, `document.write`, `eval`:
   ```bash
   grep -rn 'innerHTML\|outerHTML\|document.write\|eval(' octbase-frontend/js octbase-frontend/landing 2>/dev/null
   ```
   - For each hit, confirm user-controlled content is escaped/sanitized before insertion. Flag any that aren't.

5. **Test suite health**
   ```bash
   cd octbase-frontend/tests && pytest --collect-only
   ```
   - Confirm test file naming and structure is consistent (`test_*.py`), and that no test silently no-ops (e.g. always passes due to a bad selector).

## Output format

Write the review as `prompts/_release-v01-code-review.md` (separate file, since this is a standalone deliverable a reviewer/client might read on its own):

1. **Executive summary** — overall code quality assessment, 3–5 sentences.
2. **Findings table**:
   | Severity (Critical/High/Medium/Low/Nit) | Area (Backend/Frontend) | File:Line | Issue | Recommendation | Fixed in this step? (Y/N) |
3. **Fixes applied** — list of small, safe fixes made directly, with file diffs summarized.
4. **Recommended follow-ups** — anything Medium+ that wasn't fixed, with enough detail for another engineer to pick up without re-discovering the issue.
5. **Positive notes** — call out things done well (a real review isn't only criticism, and it helps future-you know what patterns to keep).

## Constraints

- Do not fix anything rated Medium or above unless the fix is under ~10 lines and has a clear test.
- Do not introduce new architectural patterns (no new frameworks, no module bundler, no new test runner) as part of "fixes" — those go in "Recommended follow-ups".
- Run the full verification suite after any fixes:
```bash
cd octbase-api && go vet ./... && golangci-lint run ./... && go test -race ./...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest
```
