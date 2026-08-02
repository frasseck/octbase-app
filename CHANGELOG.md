# Changelog

All notable changes to Octbase are documented here.

## Unreleased

### Changed

- **The compose stack forwards the environment variables the runbook says to
  set.** `podman-compose.yml`'s API `environment:` block is an allowlist, and
  a dozen documented tunables were missing from it — setting
  `OCTBASE_WEBHOOK_SECRET_GITHUB`/`_BITBUCKET`, the JWT TTLs,
  `OCTBASE_REQUIRE_MFA`, `OCTBASE_LOG_LEVEL` and friends in `.env` silently
  did nothing (the webhook secrets being the security-relevant case: the
  documented setup left the receivers disabled with no error). They are now
  forwarded with behavior-preserving defaults, including
  `OCTBASE_DEMO_MODE` (was hardcoded `"true"`; the default stays `true`, but
  a real deployment can finally turn it off in `.env`). `POSTGRES_SSLMODE`
  joined `.env.example`, which claims to list every supported variable.
- **The guard scripts are hardened against silent decay.** The pre-commit
  security sweep now fails loudly when a path it greps has been renamed away —
  previously a moved directory made every backend check vacuously pass. The
  metrics-not-proxied guard discovers all Caddyfiles and fails on *any*
  non-comment, non-negated mention of `/metrics`, closing the
  `path /metrics*`, `handle /metrics {` and `path_regexp` evasions the old
  token-bounded regex missed (the one sanctioned shape is a `not path`
  matcher, which cannot route what it refuses to match). The HTML-injection
  guard now recurses into subdirectories and scans `octbase-shared` — a sink
  there is a sink in both SPAs at once. `scripts/release.sh` no longer merges
  to main blind: it waits for the pushed branch's CI run via `gh` and aborts
  on failure (override with the new `--no-ci-gate`), and stages only tracked
  files instead of `git add -A`, which in a shared checkout could sweep a
  neighbour's scratch files into a release commit. A new
  `.github/dependabot.yml` (weekly npm/gomod/github-actions) covers the
  dev-tree advisory gap the runtime-only `npm audit` deliberately leaves open.
- **One icon-button class, for real.** The `.btn-icon` alias survived the
  styleguide's icon-button consolidation as a legacy escape hatch, and 27
  emitters across eight desktop modules were still using it. Every icon button
  now emits `.icon-btn` (the Playwright locator moved with them) and the alias
  selectors are gone from `app.css`. No visual change — the two names always
  shared one rule block.

### Fixed

- **The style guide's two newest sections render styled again, and its PDF is
  current.** The Progressive-disclosure and Transient-messages sections used a
  `dos` class the page never defines and two tables missing the page's `bp`
  table class, so their Do/Don't blocks and rule tables rendered as bare
  unpadded text; the error-snackbar demo also referenced the undefined
  `--md-on-error` and drew near-black text on the error red whose contrast it
  was demonstrating. All fixed; the guide is v1.8, the metrics page's
  `≤ 40rem` tweak is now documented as the system's one sub-1024px media
  query, and `docs/octbase-ui-styleguide.pdf` is regenerated after trailing
  the page at v1.6.

- **Bulk "Set status" can no longer revert finished work.** The bulk door was
  the one status door without the immutability rule: selecting `DONE` or
  `ARCHIVED` tasks and setting a status flipped them — clearing `done_at`, and
  with it velocity history and auto-archive eligibility — where the task panel
  answers `TASK_IMMUTABLE` and a board drag refuses the move. Finished tasks in
  a bulk selection are now skipped (the response's `updated` count tells the
  truth about how many changed), matching the bulk contract's existing
  silent-skip of unknown IDs; reopening stays a deliberate per-task act. Bulk
  archive likewise skips already-`ARCHIVED` tasks instead of re-stamping and
  re-logging them, while `DONE → ARCHIVED` still works — it is the auto-archive
  transition. The per-task activity trail is also honest now: entries are
  written only for tasks that actually changed, and only after the status write
  succeeded, and a failure while realigning cards no longer fails a request
  whose status change had already fully happened — the statuses are committed
  in one statement; the card pass is best-effort and self-heals on the next
  move.
- **A merged PR closes its task by the same rules as a person would.** The
  auto-close-on-merge webhook wrote `DONE` straight through the repository
  layer, bypassing every rule the interactive doors share: it completed tasks
  over an open **BLOCKER** descendant, left the card sitting in its old lane
  while the task read *Done*, wrote no Activity entry (so the sprint burndown
  replay never saw webhook completions), and could flip an `ARCHIVED` task back
  to `DONE`. It now goes through the same status door as the task panel: the
  blocker rule skips the close (the merge cannot answer a 422, so it is logged
  and left for a person), finished tasks are left alone, the card moves to the
  *Done* lane, `done_at` is stamped, and the Activity entry is written with the
  empty actor of a system action — the same convention as auto-archive.
- **The documentation tells the truth again where it had drifted behind the
  code.** The four passages still prescribing `esbuild: { keepNames: true }`
  (architecture, technical documentation, both frontend READMEs) now name the
  real setting — `rollupOptions.output: { keepNames: true }` — because under
  Vite 8's rolldown the esbuild form is a silent no-op, and following the old
  docs verbatim reproduces the exact green-build-dead-buttons failure the
  project already paid a debugging session for. The operations runbook was
  reconciled with the shipped stack: `OCTBASE_APP_URL` is marked required
  (the API refuses to start without it outside demo mode), the TLS steps say
  how `Caddyfile.tls` actually reaches the container (it is not in the
  image), and the Mailpit verification expects the 502 a healthy production
  stack really answers, not a 404. The user guide's bulk-actions section now
  describes the selection that exists (backlog and task list — board cards
  carry no checkboxes, there is no per-column bulk move and no Shift-range
  select), its status-change and sprint-scope passages match the OCT-303/304
  board rules in both directions, and `openapi.yaml`'s `deleteColumn` no
  longer asserts the opposite of the code (detached tasks reset to PLANNED);
  the move/remove-task 200s declare the Task body both SPAs rely on, and the
  409 `VERSION_CONFLICT` responses the prose promised are declared on the
  status/assign/priority/move operations.
- **A task relation's target must now exist — in the same project.** The
  create side of relations accepted any UUID: a nonexistent `targetTaskId`
  rode the foreign key into a raw `500 INTERNAL_ERROR` (a cross-project
  task-ID existence oracle, 500 vs 201), and a target in another project was
  accepted outright — the symmetric inverse row then surfaced the caller's
  task ID in the *other* project's relation list, in both directions, across
  a PRIVATE boundary. `AddRelation` now answers the same
  `422 TASK_NOT_FOUND` (field `targetTaskId`) for a missing and a
  cross-project target, so the response can no longer be used to probe — the
  same indistinguishability the delete side has had since its own hardening.
- **`releaseId` and the bulk assignee are validated like their single-task
  siblings.** `release_id` is bare TEXT with no foreign key, so create, PATCH
  and bulk `set_release` persisted any string silently — a typo'd or
  cross-project release ID quietly mis-counted `RELEASE_HAS_OPEN_TASKS` and
  every release report. All three doors now answer `422 RELEASE_NOT_FOUND`
  for a release that is not the project's own (unknown and cross-project
  indistinguishable; sprint assignment got this guard first, release now
  matches). Bulk `set_assignee` runs the same assignable-membership check as
  the task panel (`422 ASSIGNEE_INVALID`) — the exact check whose absence
  once let typo'd UUIDs persist — and an empty bulk value now stores as SQL
  `NULL` rather than `""`, so "is anyone assigned" reads stay honest.
- **The five SCM provider errors now speak the user's language — and the guard
  that swore they did has learned why it missed them.** `SCM_REPO_NOT_FOUND`,
  `SCM_AUTH_FAILED`, `SCM_BRANCH_EXISTS`, `SCM_PROVIDER_ERROR` and
  `SCM_NOT_CONFIGURED` had no `errors.*` translation in any locale file of
  either SPA, so a German user got the raw English provider message — while
  `check-error-translations.mjs` printed clean, because it only recognizes
  codes written as string literals at the `Write*Error` call site and these
  five travel through Go constants (`CodeRepoNotFound = "SCM_REPO_NOT_FOUND"`,
  …). The guard now also reads `Code*`-named constant declarations (109 → 114
  codes covered), and the five keys exist in English and German in both SPAs.
  The convention the guard encodes: name error-code constants `Code*` and it
  sees them.
