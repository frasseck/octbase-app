# Jira-Compatible CSV Export & Import

## Overview

Octbase supports exporting and importing tasks (including comments) in a format compatible with the Jira Cloud CSV Importer. The feature is accessible per project via the **⚙ settings menu** in the project topbar.

---

## API Endpoints

### Export
```
GET /api/v1/projects/{projectId}/export/jira-csv
```
Query parameters:
- `projectKey` — override the auto-derived Jira project key (default: derived from project slug, max 10 chars, uppercased)
- `userMapping` — comma-separated `email:jiraAccountId` pairs for user substitution, e.g. `alice@example.com:557057:abc-123`

Response: `text/csv; charset=utf-8` with `Content-Disposition: attachment; filename="<slug>-jira-export.csv"`

### Import
```
POST /api/v1/projects/{projectId}/import/jira-csv
```
Accepts:
- `multipart/form-data` with a `file` field (CSV file upload)
- `text/csv` body (direct CSV content)

Query parameters:
- `dryRun=true` — validate and report without persisting anything
- `userMapping` — comma-separated `jiraAccountId:email` pairs for reverse user lookup

Response: JSON `ImportResult`

---

## CSV Column Structure

| CSV Header    | Internal field       | Notes                                      |
|---------------|----------------------|--------------------------------------------|
| `Issue Key`   | `task.ExternalRef`   | Optional; preserved as external reference  |
| `Project Key` | derived from slug    | Auto-derived or overridden via query param |
| `Summary`     | `task.Title`         | Required; max 255 chars                    |
| `Issue Type`  | `task.TaskType`      | `Task`, `Bug`, `Story`, `Epic`, `Chore`    |
| `Status`      | `task.Status`        | See status mapping below                   |
| `Priority`    | `task.Priority`      | `Low`, `Medium`, `High`, `Critical`        |
| `Assignee`    | `task.AssigneeID`    | Resolved to/from email via user lookup     |
| `Reporter`    | `task.ReporterID`    | Resolved to/from email via user lookup     |
| `Description` | `task.Description`   |                                            |
| `Created`     | `task.CreatedAt`     | Format: `DD/Mon/YY h:mm AM/PM`             |
| `Updated`     | `task.UpdatedAt`     | Format: `DD/Mon/YY h:mm AM/PM`             |
| `Due date`    | `task.DueDate`       | Format: `DD/Mon/YY` (stored as YYYY-MM-DD) |
| `Labels`      | —                    | Exported as empty; imported as warning     |
| `Comment`     | `task.Comments[]`    | Repeated column; one per comment           |

### Status Mapping

| Jira value           | Internal value |
|----------------------|----------------|
| `To Do`              | `PLANNED`      |
| `In Progress`        | `IN_PROGRESS`  |
| `In Review`          | `IN_REVIEW`    |
| `Done`               | `DONE`         |
| `Archived`           | `ARCHIVED`     |

Unknown status values default to `PLANNED` on import.

---

## Comment Format

Each comment is exported as a separate `Comment` column in the same row:
```
Comment,Comment,Comment
```

When author and timestamp are available, the value uses the Jira-compatible format:
```
DD/Mon/YY h:mm AM/PM;<authorIdentifier>;<comment text>
```
Example:
```
"31/Mar/24 6:49 AM;557057:abc-123;Fixed the bug"
```

If the timestamp cannot be parsed, the fallback format is:
```
<authorIdentifier>;<comment text>
```

On import, the parser splits on `;` with `SplitN(..., 3)` to preserve semicolons inside the comment body.

---

## User Mapping

**Export:** User IDs are resolved to emails via DB lookup. If a `userMapping` query param provides `email:jiraAccountId` pairs, the Jira account ID is substituted.

**Import:** The `userMapping` query param provides `jiraAccountId:email` pairs. Resolution order:
1. Check `userMapping` for the raw identifier → look up email in DB
2. If identifier contains `@` → direct email lookup in DB
3. Otherwise → emit a warning; field is left empty

---

## Import Behaviour

- **Validation** runs before any writes. Rows with a blank or too-long `Summary` are skipped and reported in `errors[]`.
- **Unknown columns** are not silently dropped — they generate a `warning` entry with the column name. They are not stored (no `external_fields` persistence).
- **Dry-run mode** (`?dryRun=true`) returns the full `ImportResult` without writing to the database.
- **Transaction model**: all valid rows are written in a single transaction. A DB failure rolls back everything.
- **Duplicate detection** is not enforced. Re-importing creates duplicate tasks. The original `Issue Key` is stored in `task.ExternalRef`.

### ImportResult shape
```json
{
  "imported": 2,
  "skipped": 1,
  "dryRun": false,
  "errors": [
    { "row": 3, "message": "Summary is required" }
  ],
  "warnings": [
    { "row": 1, "message": "unknown column: CustomField1" }
  ]
}
```

---

## Frontend

The export is accessible via the **⚙ gear icon** in the project topbar (visible on all views, desktop and mobile). Clicking it opens a dropdown with:

- **Export as Jira CSV** — fetches the CSV with the current JWT and triggers a browser download
- **Edit project** / **Delete project** — project management actions

The download uses `fetch()` with the `Authorization: Bearer <token>` header and triggers a file save via a temporary object URL.

---

## CSV Rules

- Separator: comma
- Encoding: UTF-8
- All fields are quoted correctly per RFC 4180 (`encoding/csv` in Go)
- Double quotes inside values are escaped as `""`
- Import uses `LazyQuotes: true` to tolerate common quoting quirks from third-party exporters

---

## Jira-Specific Notes

- Jira's CSV Importer requires a `Summary` column (mapped from `Summary`).
- `Issue Key` is optional; Jira will assign its own keys on import.
- Jira does not accept lowercase project keys — `toJiraProjectKey()` uppercases and strips hyphens.
- Comment timestamps use `DD/Mon/YY h:mm AM/PM` (Go format: `"02/Jan/06 3:04 PM"`).
- The Jira CSV Importer maps repeated `Comment` columns to individual comments in order.
- User matching in Jira requires Atlassian Account IDs; emails are used as a fallback for internal round-trips.
