Act as a senior full-stack engineer working on Octbase (Go/chi/PostgreSQL backend + two no-build plain-DOM SPAs). This prompt is a **quality-improvement pass**: cleanup, deduplication, and altitude fixes surfaced by a multi-angle code + security review, plus one genuine behavior decision. It is **not** a feature or a rewrite.

**Scope boundary — read first.** The *behavior* fixes from the same review already landed and are **out of scope** (do not redo them): the cross-project BOLA/IDOR guards, the `DeleteRelation`/`DeleteLink`/`DeleteExternalColumn` 404-on-missing semantics, the MFA enroll password re-auth (desktop + mobile), the `allowLegacyTarget` SUPER_ADMIN guard now failing closed, the login-timing equalisation, the CSV formula-injection sanitiser, and the SSRF error-oracle removal. The `octbase-frontend/js/` IIFE/export-block refactor also already landed. This prompt cleans up *how* several of those were implemented and closes follow-ups they left open — every item below must be **behavior-preserving** unless it is explicitly marked **[decision]**.

**Overriding constraints (all items):**
- Behavior-preserving unless marked **[decision]**; the existing test suites stay green (`go test ./...` against `TEST_DATABASE_URL`; the Playwright suite at its known-failures baseline) and the four "Frontend checks" guards stay green.
- No new framework, no build step (that is the separate `prompts/37b_octbase-frontend-build-step.md`); the SPAs stay plain-DOM, IIFE-scoped with explicit `window` exports.
- Each item is independently landable; prefer several small PRs over one large one. Each backend behavior change or new/renamed error code needs a `CHANGELOG.md` `## Unreleased` entry in the same commit (the review already flagged missing entries — do not repeat that mistake).
- Where an item removes duplication, the reference implementation must be the **single** authority afterwards — grep to confirm no stragglers remain.

Work the items roughly in the order given (highest leverage first). Each has a **verify** line; treat it as the acceptance test.

---

## A. Backend deduplication & altitude (behavior-preserving)

### A1. Collapse the `RequireWriter`-403 boilerplate into one helper
`if err := shared.RequireWriter(role); err != nil { shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error()); return }` is pasted **~58 times** across `internal/workmanagement/*_handler.go` and `internal/scmintegration/handler.go`. Add a single helper (mirroring the existing `memberGuard`/`requirePermission` style in `internal/workmanagement/handler.go`), e.g. `requireWriterOr403(w, r, role) bool`, and replace every call site.
- **verify:** `grep -rn 'RequireWriter(role); err != nil' internal | grep -v _test.go` returns nothing; `go test ./...` green; the 403 body/shape is byte-identical to before.

### A2. **[decision]** Resolve the archived-project inconsistency on writer mutations
The manual `RequireWriter` two-step skips the `PROJECT_ARCHIVED` 409 that `requirePermission` (`handler.go:269`) enforces. So today branch/PR creation (`scmintegration` CreateBranch/DeleteBranch/CreatePullRequest) and category/template updates+deletes **succeed on an archived project**, whereas `requirePermission`-gated writes return `409 PROJECT_ARCHIVED`. Decide the intended contract and make it consistent:
- Preferred: route these writer mutations through a writer-scoped variant of `requirePermission` so archived projects answer `409` uniformly (this composes with A1 — the helper can add the archived check).
- If archived projects are *intended* to still accept SCM/taxonomy writes, document that exception explicitly instead.
- Either way this is observable behavior → `CHANGELOG.md` entry + tests asserting the chosen status on an archived project.
- **verify:** a test creates a project, archives it, and asserts the agreed status (409 or success) for CreateBranch, CreatePullRequest, category update/delete, template delete; consistent with the sibling `requirePermission` paths.

### A3. One ownership-guard idiom for "child belongs to guarded parent"
The BOLA fixes express the same invariant three ways: an in-handler field compare (`board_handler.go:532,621` `t.ProjectID != b.ProjectID`; `:543` `col.BoardID != boardID`), a parent-scoped `DELETE … WHERE id=$1 AND parent=$2` returning a `deleted bool` (`BoardExternalColumnRepo.Delete`, `TaskLinkRepo.Delete`), and a service-layer sentinel (`Service.DeleteRelation` → `ErrRelationNotInTask`). Pick **one** convention (recommendation: parent-scoped repo methods returning a `found/deleted bool`, mapped to `404` by a small handler helper) and converge the read/mutate sub-resource paths onto it, so a reviewer auditing for IDOR sees one shape and a new sub-resource handler has one template to copy.
- **verify:** the cross-project + missing-child tests in `internal/workmanagement/security_fixes_test.go` still pass unchanged; the three shapes are reduced to one; no new endpoint loses its guard.

### A4. `allowLegacyTarget` delegates to the rbac authority
`internal/admin/handler.go`'s `allowLegacyTarget` re-encodes the "an ADMIN may not act on a SUPER_ADMIN" policy inline (its own comment cites `rbac.CanDisableUser`/`rbac.CanUpdateUserRole`). Have it delegate to those predicates so "who may act on whom" has a single authority and the legacy enable/disable + password-reset endpoints can't drift from the modern usermgmt path. Keep the fail-closed-on-DB-error behavior that already landed.
- **verify:** `TestLegacyAdmin_CannotActOnSuperAdmin` still passes; a follow-the-code check shows the SUPER_ADMIN string comparison lives only in `rbac`, not in `admin`.

### A5. Single timing-equalisation helper for the dummy-bcrypt compare
`internal/auth/email_provider.go` copies `_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))` at three branches (`:54,:65,:69`). Extract one helper (e.g. `equalizeLoginTiming(password string)`) so the anti-enumeration compare has a single home; a future change to the dummy hash, cost, or the decision to run it can't miss a branch and silently reopen the timing oracle.
- **verify:** all three sites call the helper; `TestLogin_*` timing/enumeration tests (add one if absent asserting disabled/deleted/unknown all take a bcrypt path) pass.

### A6. `allowLegacyTarget` single query (efficiency, minor)
`ResetPassword` runs `SELECT email … WHERE id=$1 AND status<>'deleted'` right after `allowLegacyTarget`'s own `SELECT global_role … WHERE id=$1` on the same PK row. Fold the role read into the handler's existing lookup (or have the guard return the role) so the common path is one round-trip, not two. Keep the 403-vs-404 distinction intact.
- **verify:** the reset-password admin tests pass; no second `SELECT … FROM users WHERE id` remains on that path.

## B. Frontend structural quality (behavior-preserving)

### B1. Commit an export-completeness CI guard (the missing 5th "Frontend checks" step)
The ~290-symbol per-file `Object.assign(window, { … })` export contract has **no committed check** — a missing/stale export is a runtime `ReferenceError` that `node --check` passes. Commit the acorn AST cross-reference analysis (the same one used to derive the export blocks) as a repo script and wire it into the "Frontend checks" CI job: assert (a) every cross-file-referenced bare identifier is exported by some earlier-loaded file, and (b) every exported name is consumed by another file, an HTML page, or the test suite (else it's dead surface or a whitelist entry). Run it over both `octbase-frontend/js/` and — once B-scope permits — `octbase-mobile/js/`.
- **verify:** deleting one name from an export block (or renaming a definition without updating a consumer) makes the new CI step fail locally; a clean tree passes; the step is in `.github/workflows/ci.yml`.

### B2. Standardise cross-file mutable state on one idiom
The mutable-binding refactor left **three** patterns for "read a file-private mutable from another file": accessor functions (`appVersion()`, `boardTasksCache()`), an `S` property (`S.bulkInFlight`), and mutated const objects (`FEATURES`/`LIMITS`) — and `js/README.md` blesses all three, with a silent-stale-value trap if someone exports a reassigned `let`. Pick one blessed idiom for genuinely cross-file mutable state (recommendation: a property on the exported `S` state object) and converge the accessor-only cases onto it (or document precisely when each is appropriate, if convergence hurts readability). Update the "File scope & exports" section accordingly.
- **verify:** no exported bare `let` is reassigned after load (the B1 guard can assert this); the app boots and the version tag / board cache / bulk guard behave as before; README documents one rule.

### B3. Fix the `showModal` `onSubmit` teardown contract
`doStartMfaEnrollment` (`views-settings.js`) ends with `setTimeout(refreshMfaSection, 0)` to dodge `showModal`'s hardcoded `await onSubmit(); hideModal();` teardown. Give `showModal` a real contract instead — e.g. an `onSubmit` return value or option that sequences the re-render after (or suppresses) `hideModal` — and remove the 0ms-timer bandaid. Any other caller needing to re-render after a modal submit should use the contract, not rediscover the timer.
- **verify:** the desktop MFA enroll e2e (`test_settings.py`) passes without the `setTimeout`; grep shows no `setTimeout(…, 0)` teardown workarounds around `showModal` callers.

### B4. Dedupe the MFA re-auth modal bodies
`enrollReauthModalBody()` and `reauthModalBody()` in `views-settings.js` render the same `settings.mfa.reauthDesc` + password field, differing only by input id and the omitted code field. Parameterise one function (e.g. `reauthModalBody({ withCode })`) so the enable / disable / regenerate modals can't drift. Do the same for the mobile counterparts (`reauthSheetBody` and the new enroll sheet in `app.js`) if it reads cleanly.
- **verify:** one modal-body function backs all three desktop MFA re-auth prompts; the enroll, disable, and regenerate e2e paths pass.

### B5. Tighten the innerHTML story (pre-existing follow-up)
Per `js/README.md` "Open follow-ups": either add an `innerHTML`-write helper and tighten `scripts/check-innerhtml.mjs` further, or migrate the remaining untagged `` innerHTML = `…` `` sites to the auto-escaping `` html`` `` tagged template so *all* interpolations are escaped by construction. Scope to a bounded set of files per PR; the guard must stay green throughout.
- **verify:** `node scripts/check-innerhtml.mjs` green; the migrated files use `` html`` `` (or the new helper) with no raw interpolation of user-content fields.

## C. Test coverage for the landed security batch

### C1. Unit-test the pure security helpers
Add table-driven unit tests (no DB needed) for `sanitizeCSVCell` (each dangerous leading char `= + - @ \t \r` gets quoted; benign cells untouched; empty string safe) and for the SCM egress classifier / `validRepoURL` (schemes and internal-IP hosts). These are exactly the "pure logic" the architecture doc reserves for unit tests.
- **verify:** `go test ./internal/workmanagement -run CSVCell` and the scmintegration URL tests pass; a deliberately-wrong expectation fails them.

### C2. Cover the reauth and error-shape paths
Add integration tests for the MFA enroll `REAUTH_REQUIRED` path (access token without password → 401; with correct password → 200; forced-enrollment token → exempt) and assert the SSRF dial-refusal error surfaced to the client contains no resolved IP or reason (the oracle-removal invariant). If the `allowLegacyTarget` fail-closed branch is not reachably testable without DB-error injection, document that in the test file rather than leaving it silently uncovered.
- **verify:** the new tests pass and would fail if the re-auth requirement or the error-text redaction regressed.

---

**Deliverables:** the `requireWriterOr403` helper + all call sites converted (A1); the archived-project contract made consistent and documented + tested (A2); one ownership-guard idiom across board/task sub-resources (A3); `allowLegacyTarget` delegating to `rbac` (A4); a single login-timing helper (A5); the reset-password single-query cleanup (A6); a committed export-completeness CI guard wired into "Frontend checks" (B1); one documented cross-file-mutable idiom (B2); a real `showModal` `onSubmit`/teardown contract with the `setTimeout` bandaid removed (B3); deduped MFA re-auth modal bodies desktop + mobile (B4); the innerHTML tightening (B5); unit tests for `sanitizeCSVCell` + SCM URL validation and integration tests for the reauth/error-shape paths (C1–C2); plus `CHANGELOG.md` entries for A2 and any other observable change. **Constraints:** every item behavior-preserving except A2 (marked **[decision]**); existing Go + Playwright suites stay green and the four Frontend guards stay green throughout; no framework, no build step; each item independently landable; duplication removals leave a single authority with no stragglers (grep-verified).
