# Go Best Practices

A reference for writing clean, idiomatic Go in a domain-driven HTTP API. Applies to any Go service with PostgreSQL persistence and a chi router.

---

## Package Structure

Organise by domain, not by layer. Each bounded context is one package:

```
internal/
  workmanagement/     # domain + application service + repo + handler
    domain.go         # structs, constants, IsImmutable, etc.
    service.go        # application-level business logic, DomainError
    repo.go           # all SQL queries for this domain
    handler.go        # HTTP handlers + route registration
    domain_test.go
    service_test.go
    handler_test.go
  shared/             # cross-cutting: UUID, time, error helpers, middleware
  testutil/           # test helpers only (NewTestDB, MustCreate*, etc.)
```

Rules:
- Domain types (`domain.go`) have **no imports from http, sql, or any framework**.
- Repos depend on `database/sql` only; they do not call services.
- Handlers depend on repos and services; they never write SQL.
- `shared` contains things used by every package (`shared.WriteJSON`, `shared.NewUUID`, middleware). Keep it small.

---

## Error Handling

### Never expose internal errors to clients

```go
// Bad — leaks DB internals:
shared.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())

// Good — log internally, return opaque message:
shared.WriteServerError(w, r, err)
// WriteServerError logs with slog and writes {"code":"INTERNAL_ERROR","message":"An internal error occurred"}
```

### Map domain errors to 422

```go
if err := h.svc.AddRelation(rel); err != nil {
    var de *DomainError
    if errors.As(err, &de) {
        shared.WriteError(w, http.StatusUnprocessableEntity, de.Code, de.Message)
        return
    }
    shared.WriteServerError(w, r, err)
    return
}
```

### HTTP status codes

| Situation | Status | Code example |
|-----------|--------|--------------|
| Success (mutation) | 201 | — |
| Success (delete/action) | 204 | — |
| Invalid JSON body | 400 | `BAD_REQUEST` |
| Missing/invalid auth header | 401 | `UNAUTHORIZED` |
| Resource not found | 404 | `TASK_NOT_FOUND` |
| Business rule violation | 422 | `TASK_IMMUTABLE`, `RELEASE_HAS_OPEN_TASKS` |
| Unexpected internal error | 500 | `INTERNAL_ERROR` |

### DomainError

Put `DomainError` in the application service package, not in the domain:

```go
type DomainError struct {
    Code    string
    Message string
}
func (e *DomainError) Error() string { return e.Code + ": " + e.Message }
```

Repositories return plain `error`. Only services return `*DomainError`.

---

## Structured Logging

Use `log/slog` throughout. Set a JSON handler in `main`:

```go
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
```

Log at the boundary, not deep in the stack:

```go
// In a handler, not in the repo
slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
```

Always pass structured fields, never format into the message:

```go
// Bad:
slog.Info(fmt.Sprintf("opening database at %s", path))

// Good:
slog.Info("opening database", "path", path)
```

---

## HTTP Handler Pattern

```go
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
    // 1. Decode and validate input
    var req createTaskReq
    if err := shared.DecodeJSON(r, &req); err != nil {
        shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
        return
    }
    if strings.TrimSpace(req.Title) == "" {
        shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_TITLE_REQUIRED", "Task title must not be blank")
        return
    }

    // 2. Build domain object
    now := shared.Now()
    task := &Task{ID: shared.NewUUID(), ..., CreatedAt: now, UpdatedAt: now, Version: 1}

    // 3. Persist
    if err := h.tasks.Create(task); err != nil {
        shared.WriteServerError(w, r, err)
        return
    }

    // 4. Side effects (activity, notifications) — ignore errors from fire-and-forget
    _ = h.activity.Write(projectID, task.ID, actorID, "TASK_CREATED", "Task created")

    // 5. Respond
    shared.WriteJSON(w, http.StatusCreated, task)
}
```

Handler rules:
- One early-return path per validation failure; no nested if chains.
- Read-modify-write: always `FindByID` first, then mutate, then `Update`.
- Use typed request structs (`createTaskReq`) for multi-field inputs; inline structs are fine for 1–2 fields.
- Use `*string` request fields for optional PATCH fields so a missing field can be distinguished from an explicit null.

---

## Repository Pattern

```go
type TaskRepo struct{ db *sql.DB }

func NewTaskRepo(db *sql.DB) *TaskRepo { return &TaskRepo{db: db} }

func (r *TaskRepo) FindByID(id string) (*Task, error) {
    row := r.db.QueryRow(`SELECT ... FROM tasks WHERE id=?`, id)
    t, err := scanTask(row)
    return t, err  // nil, nil means "not found"
}
```

Conventions:
- Return `nil, nil` from `FindByID` when the row is not found; let the handler emit the 404.
- Always `defer rows.Close()` after `db.Query`.
- Initialise list results to `[]T{}` not `nil` so JSON encodes `[]` not `null`.
- Wrap scan errors: `fmt.Errorf("scan task: %w", err)`.
- Use `sql.NullString` or `*string` for nullable columns.

---

## Domain Objects

```go
type Task struct {
    ID          string  `json:"id"`
    ProjectID   string  `json:"projectId"`
    Status      string  `json:"status"`
    AssigneeID  *string `json:"assigneeId"`   // nullable
    ReleaseID *string `json:"releaseId"`  // nullable
    BoardRank   int     `json:"boardRank"`
    CreatedAt   string  `json:"createdAt"`
    UpdatedAt   string  `json:"updatedAt"`
    Version     int     `json:"version"`
}
```

- Use `string` for all IDs and timestamps (UUID v4 strings, RFC3339).
- Use `*string` for nullable foreign keys so JSON encodes `null` correctly.
- Keep `version int` on aggregate roots for optimistic locking awareness.
- Constants belong in `domain.go`, never inline magic strings in handlers or repos.

---

## Database Conventions

- Foreign keys are enforced by default; declare them explicitly in migrations.
- Set `db.SetMaxOpenConns(1)` in tests for deterministic ordering.
- Board rank: use gaps of 1000 (1000, 2000, 3000) to allow reordering without renumbering.
- Timestamps: store as `TEXT` in RFC3339 UTC format.
- Primary keys: `id TEXT PRIMARY KEY`.
- All tables get `created_at TEXT NOT NULL` and `updated_at TEXT NOT NULL`.
- Aggregate roots also get `version INTEGER NOT NULL DEFAULT 1`.

---

## Application Service

```go
type Service struct {
    db        *sql.DB
    tasks     *TaskRepo
    relations *TaskRelationRepo
    releases *ReleaseRepo
}

// Methods enforce cross-entity business rules only.
// Single-entity validation belongs in the handler.
func (s *Service) AddRelation(rel *TaskRelation) error {
    // self-reference, duplicate, cycle — returns *DomainError
}
```

- Services contain logic that spans multiple repositories or requires a transaction.
- Single-entity validation (blank title, immutable status) stays in the handler for simplicity.
- Return `*DomainError` for business rule violations; plain `error` for infra failures.

---

## Testing

### Unit test domain logic

```go
func TestIsImmutable(t *testing.T) {
    if !IsImmutable("DONE") { t.Error("DONE should be immutable") }
    if IsImmutable("PLANNED") { t.Error("PLANNED should not be immutable") }
}
```

### Integration test handlers with real PostgreSQL

```go
func TestCreateTask_OK(t *testing.T) {
    srv := testutil.NewTestServer(t)
    project := testutil.MustCreateProject(t, srv)

    body := `{"title":"Test task"}`
    res := testutil.Do(t, srv, "POST", "/api/projects/"+project.ID+"/tasks", body)
    testutil.AssertStatus(t, res, http.StatusCreated)

    var task workmanagement.Task
    testutil.DecodeJSON(t, res, &task)
    if task.Title != "Test task" {
        t.Errorf("title = %q", task.Title)
    }
}
```

- Use a real PostgreSQL test database for all tests; no mocking of persistence.
- Share setup helpers in `testutil` (`NewTestDB`, `NewTestServer`, `MustCreate*`).
- Test the HTTP boundary, not internal methods.
- Clean up state by using table-scoped test IDs or running each test against a fresh DB.
- Test error paths: not-found, validation failure, domain rule violation.

---

## Pagination

```go
type PaginationParams struct {
    Page int
    Size int
}

func ParsePagination(r *http.Request) PaginationParams {
    page, _ := strconv.Atoi(r.URL.Query().Get("page"))
    size, _ := strconv.Atoi(r.URL.Query().Get("size"))
    if size <= 0 { size = 20 }
    if page < 0  { page = 0  }
    return PaginationParams{Page: page, Size: size}
}
```

Standard query params: `page`, `size`, `sort`, `q` (full-text), `status`, `priority`, `assigneeId`.

---

## Interfaces

Define interfaces at the **consumer** side, not the producer side:

```go
// In workmanagement/handler.go — consumer defines what it needs:
type ActivityWriter interface {
    Write(projectID, taskID, actorID, actType, message string) error
}
```

Keep interfaces small (1–3 methods). Avoid large interfaces that couple packages.

---

## Common Pitfalls

| Pitfall | Fix |
|---------|-----|
| Exposing `err.Error()` in 500 responses | Use `WriteServerError(w, r, err)` |
| `nil` slice returned as JSON `null` | Initialise with `[]T{}` |
| Forgetting `defer rows.Close()` | Always pair with `db.Query` |
| Magic status/type strings in handlers | Define constants in `domain.go` |
| Domain logic in repositories | Keep repos dumb; logic in services or handlers |
| Returning the same error at every layer | Wrap once at the repo boundary; handle once at the handler |
| Ignoring the error from `json.Encoder.Encode` | Acceptable after `w.WriteHeader` has been called |
