# Changelog

All notable changes to Octbase are documented here.

## Unreleased

### Changed

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
