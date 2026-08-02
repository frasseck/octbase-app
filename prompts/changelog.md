# Changelog (historical — do not extend)

> **This file is a frozen 2026-06-19 snapshot** kept for history only. Half of
> it concerns the separate landing-site repo and a since-removed French locale.
> The living changelog is the repo-root `CHANGELOG.md`; add entries there.

## 2026-06-19 — Configurable project abbreviation for task numbers

### Backend (`octbase-api`)
- Added `Abbreviation` field to the `Project` domain struct (`domain.go`)
- Added `AbbreviationFromName()` helper that auto-generates a short uppercase prefix from the project name (first letter of each word for multi-word names, first 2 chars for single-word)
- Updated all project SQL queries (`INSERT`, `SELECT`, `UPDATE`) and scan functions in `repo.go` to include the `abbreviation` column
- `CreateProject` and `UpdateProject` handlers in `project_handler.go` now accept an optional `abbreviation` field; auto-generates from name if left empty

### Database migration (`migrations/011_project_abbreviation`)
- `up.sql`: Adds `abbreviation VARCHAR(10)` column to `projects` table; backfills existing rows with the first 2 characters of the project name
- `down.sql`: Drops the `abbreviation` column

### Frontend (`octbase-frontend`)
- **CSS** (`css/app.css`): Narrowed the task number (`#`) column from `7.5rem` to `5rem`; removed fixed width on the title column so it expands to fill available space
- **Task number display** (`js/app.js`): All 6 rendering locations (task list, backlog, board card, task panel, task label, branch suggestion) now use `project.abbreviation` with fallback to `slug.toUpperCase()`
- **Project forms** (`js/app.js`): Both create and edit project modals include an "Abbreviation" input field (max 4 chars, uppercase)

### Locales (`octbase-frontend/locales/en.json`, `de.json`, `fr.json`)
- Added `project.abbreviation` (EN: "Abbreviation", DE: "Kürzel", FR: "Abréviation")
- Added `project.abbreviationPlaceholder` (EN: "e.g. ED", DE: "z.B. ED", FR: "ex. ED")

## 2026-06-19 — Email validation with MX record check

### Backend (`octbase-web/mailer/main.go`)
- Added MX record lookup after syntactic email validation using `net.LookupMX()`
- Falls back to A/AAAA record lookup (`net.LookupHost()`) per RFC 5321 before rejecting
- Returns `invalid_email` for malformed addresses and `invalid_email_domain` for domains without mail capability

### Frontend (`octbase-web/js/app.js`)
- Added client-side email regex validation before sending the request
- Server error responses are now parsed to show specific localized error messages:
  - `invalid_email` -> `contact.form.errorInvalidEmail`
  - `invalid_email_domain` -> `contact.form.errorInvalidDomain`
  - Other errors -> generic `contact.form.error`

### Locales (`octbase-web/locales/en.json`, `de.json`, `fr.json`)
- Added `contact.form.errorInvalidEmail` in EN / DE / FR
- Added `contact.form.errorInvalidDomain` in EN / DE / FR
