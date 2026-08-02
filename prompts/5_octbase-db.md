# Octbase DB — PostgreSQL Migration Prompt

**Purpose:** Migrate the Octbase API from SQLite (`modernc.org/sqlite`) to PostgreSQL (`lib/pq`), move the database into its own container, and update the full container and test infrastructure accordingly.

> **Historical reference:** This migration has already been applied. `1_octbase-api.md` already reflects the post-migration PostgreSQL state. This document is retained as a replay prompt showing the exact changes that were made. Do not apply it again on top of `1_octbase-api.md`. It also predates the frontend's later move from nginx to Caddy — every "nginx" reference below is historical; the frontend container is Caddy today.

Apply this prompt on top of the result of `1_octbase-api.md`.

---

## 1. What Changes and Why

The original prototype used SQLite for simplicity. PostgreSQL is the target database specified in the design document and is required for production-grade deployments. This migration:

- Replaces the SQLite driver with `lib/pq` (pure-Go PostgreSQL driver compatible with `database/sql`)
- Adds a dedicated postgres container to the compose stack
- Converts all SQL query placeholders from `?` (SQLite/MySQL style) to `$N` numbered params (PostgreSQL style)
- Replaces `INSERT OR IGNORE` with `INSERT ... ON CONFLICT DO NOTHING` or full upserts
- Fixes case-insensitive search: SQLite `LIKE` is case-insensitive by default; PostgreSQL `LIKE` is case-sensitive, so search queries must use `ILIKE`
- Adds a DB ping-retry loop at startup to handle container ordering (postgres takes a few seconds to become ready)
- Moves the frontend static files into the API image so both ports serve the app
- Updates the test infrastructure to use PostgreSQL with per-test schema isolation

---

## 2. Go Module

**`octbase-api/go.mod`** — replace `modernc.org/sqlite` with `lib/pq`:

```bash
cd octbase-api
go get github.com/lib/pq@latest
go mod tidy   # removes modernc.org/sqlite and all its transitive deps
```

The resulting `go.mod` requires only two explicit dependencies:
```
github.com/go-chi/chi/v5 v5.0.12
github.com/lib/pq v1.12.3
```

---

## 3. Shared DB Layer (`internal/shared/db.go`)

Replace the SQLite-specific `OpenDB` with a PostgreSQL version:

```go
import _ "github.com/lib/pq"

// OpenDB opens a PostgreSQL database using the given DSN.
func OpenDB(dsn string) (*sql.DB, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, fmt.Errorf("open db: %w", err)
    }
    return db, nil
}
```

`RunMigrations` and `ReadMigrationFile` are unchanged.

---

## 4. Environment Variable

Replace `OCTBASE_DB_PATH` with `OCTBASE_DATABASE_URL` everywhere (compose files, docs, tests).

Default value used when the variable is not set:
```
postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable
```

---

## 5. Startup Changes (`cmd/octbase-api/main.go`)

### 5a. Use OCTBASE_DATABASE_URL

```go
dsn := os.Getenv("OCTBASE_DATABASE_URL")
if dsn == "" {
    dsn = "postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable"
}
db, err := shared.OpenDB(dsn)
```

### 5b. DB ping-retry loop

`sql.Open` does not make a connection. Add a ping loop immediately after `OpenDB` so the API waits for PostgreSQL to become ready (important in containerised environments where `podman-compose` does not honour `condition: service_healthy`):

```go
for i := range 10 {
    if err = db.Ping(); err == nil {
        break
    }
    wait := time.Duration(i+1) * time.Second
    slog.Info("database not ready, retrying", "attempt", i+1, "wait", wait)
    time.Sleep(wait)
}
if err != nil {
    slog.Error("database unreachable after retries", "error", err)
    os.Exit(1)
}
```

### 5c. Static file serving

At this POC stage, the API serves only its own docs and OpenAPI spec — the SPA is served by a separate nginx container. Register only the system routes:

```go
r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "api/openapi.yaml")
})
r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "web/docs.html")
})
// No wildcard file server here — the SPA is served by nginx in the POC.
// The MVP (8_octbase-create-mvp.md) adds full SPA serving to the API image,
// eliminating the nginx container.
```

---

## 6. SQL Query Changes

### 6a. Numbered placeholders

Every `?` parameter placeholder must be replaced with `$1`, `$2`, … in all repo files:

- `internal/activity/repo.go`
- `internal/docs/repo.go`
- `internal/identityaccess/repo.go`
- `internal/scmintegration/repo.go`
- `internal/workmanagement/repo.go`
- `internal/seed/seed.go`
- `internal/testutil/testutil.go`
- `internal/workmanagement/service_test.go`

Example:
```go
// Before
db.QueryRow(`SELECT ... WHERE id=?`, id)

// After
db.QueryRow(`SELECT ... WHERE id=$1`, id)
```

For queries with dynamic filter conditions (e.g. `TaskRepo.List`), track the counter explicitly:

```go
args := []interface{}{projectID}
n := 1
q := fmt.Sprintf(`SELECT ... FROM tasks WHERE project_id=$%d`, n)
if v, ok := filters["status"]; ok && v != "" {
    n++
    q += fmt.Sprintf(` AND status=$%d`, n)
    args = append(args, v)
}
// ...
n++
q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, n)
args = append(args, size)
n++
q += fmt.Sprintf(` OFFSET $%d`, n)
args = append(args, page*size)
```

### 6b. INSERT OR IGNORE → ON CONFLICT

Replace all SQLite-specific `INSERT OR IGNORE` with standard SQL:

```sql
-- Before
INSERT OR IGNORE INTO table (...) VALUES (...)

-- After (insert only, skip duplicates)
INSERT INTO table (...) VALUES (...) ON CONFLICT DO NOTHING

-- After (full upsert — reset mutable fields)
INSERT INTO table (...) VALUES (...) ON CONFLICT (id) DO UPDATE SET col=$N, ...
```

### 6c. Case-insensitive search → ILIKE

PostgreSQL `LIKE` is case-sensitive. Any search query must use `ILIKE`:

```sql
-- Before (SQLite — case-insensitive by default)
WHERE title LIKE $1 OR description LIKE $2

-- After (PostgreSQL)
WHERE title ILIKE $1 OR description ILIKE $2
```

Affected files: `internal/workmanagement/repo.go` (`SearchByTitle`), `internal/docs/repo.go` (`SearchByTitle`).

---

## 7. Seed Data — Full Upsert

The seed must reset the demo environment to a predictable state on every startup. Change the `Run` function to:

1. **Remove** the early-exit check (`SELECT COUNT(*) … if count > 0 return`).
2. **Change** all seed `INSERT ... ON CONFLICT DO NOTHING` statements to full upserts for mutable entities:

| Entity | Mutable fields to reset on conflict |
|---|---|
| `users` | `email`, `display_name` |
| `projects` | `name`, `status`, `visibility`, `version` |
| `memberships` | `role` |
| `releases` | `status`, `version` |
| `tasks` | `status`, `priority`, `assignee_id`, `reviewer_id`, `release_id`, `board_column_id`, `board_rank`, `version` |
| `pages` | `content`, `rendered_html`, `status`, `version` |

Structural entities that are never mutated by tests (`boards`, `board_columns`, `task_categories`, `task_templates`, `task_links`, `task_attachments`, `task_relations`, `page_revisions`, `repository_connections`, `branch_references`, `activity_entries`) keep `ON CONFLICT DO NOTHING`.

This ensures tests that move tasks, close releases, or edit pages always start from the documented seed state.

---

## 8. Container Infrastructure

### 8a. `octbase-api/.containerignore`

```
octbase-api
*.db
vendor/
```

### 8b. `octbase-api/Containerfile`

The builder image (`docker.io/library/golang:1.25-alpine`) runs as a non-root user (`nonroot`, uid 65532) — no root in `/etc/passwd`. Use `--chown=nonroot:nonroot` on COPY instructions and produce a `FROM scratch` final stage:

```dockerfile
FROM docker.io/library/golang:1.25-alpine AS builder
WORKDIR /app
COPY --chown=nonroot:nonroot go.mod go.sum ./
RUN go mod download
COPY --chown=nonroot:nonroot . .
RUN CGO_ENABLED=0 go build -o octbase-api ./cmd/octbase-api

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /app/octbase-api /app/octbase-api
COPY --from=builder /app/migrations /app/migrations
COPY --from=builder /app/api /app/api
COPY --from=builder /app/web/docs.html /app/web/docs.html
WORKDIR /app
EXPOSE 8000
USER nonroot
CMD ["/app/octbase-api"]
```

There is **no root-level `Containerfile`**. The frontend is a separate nginx container.

### 8c. Root `podman-compose.yml`

Three independent services. The API build context is `./octbase-api`. Frontend nginx listens on port 8080 (rootless Podman cannot bind port 80):

```yaml
services:
  postgres:
    image: docker.io/library/postgres:16
    environment:
      POSTGRES_DB: octbase
      POSTGRES_USER: octbase
      POSTGRES_PASSWORD: octbase
    volumes:
      - octbase_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U octbase"]
      interval: 5s
      timeout: 5s
      retries: 5

  octbase-api:
    image: localhost/octbase-api:latest
    build:
      context: ./octbase-api
      dockerfile: Containerfile
    ports:
      - "8000:8000"
    environment:
      OCTBASE_DATABASE_URL: "postgres://octbase:octbase@postgres:5432/octbase?sslmode=disable"
      OCTBASE_DEMO_MODE: "true"
    depends_on:
      postgres:
        condition: service_healthy

  octbase-frontend:
    image: localhost/octbase-frontend:latest
    build:
      context: ./octbase-frontend
      dockerfile: Containerfile
    ports:
      - "8080:8080"
    depends_on:
      - octbase-api

volumes:
  octbase_data:
```

### 8d. `octbase-api/podman-compose.yml` (standalone API + postgres, no frontend)

```yaml
services:
  postgres:
    image: docker.io/library/postgres:16
    environment:
      POSTGRES_DB: octbase
      POSTGRES_USER: octbase
      POSTGRES_PASSWORD: octbase
    volumes:
      - octbase_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U octbase"]
      interval: 5s
      timeout: 5s
      retries: 5

  octbase-api:
    image: localhost/octbase-api:latest
    build:
      context: .
      dockerfile: Containerfile
    ports:
      - "8000:8000"
    environment:
      OCTBASE_DATABASE_URL: "postgres://octbase:octbase@postgres:5432/octbase?sslmode=disable"
      OCTBASE_DEMO_MODE: "true"
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  octbase_data:
```

### 8e. `octbase-frontend/Containerfile`

Nginx on port 8080. Do not copy `docs.html` (the API serves its own docs page):

```dockerfile
FROM docker.io/library/nginx:1-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY app.js app.css index.html favicon.ico logo.png /usr/share/nginx/html/
EXPOSE 8080
```

`nginx.conf` must set `listen 8080;` (not 80).

### 8f. Image registry

Images are pulled from `docker.io/library` (Docker Hub official images). No registry login required.

---

## 9. Test Infrastructure

### 9a. `internal/testutil/testutil.go`

Replace the SQLite in-memory test DB with a PostgreSQL schema-based approach:

```go
import _ "github.com/lib/pq"

func NewTestDB(t *testing.T) *sql.DB {
    t.Helper()
    dsn := os.Getenv("TEST_DATABASE_URL")
    if dsn == "" {
        t.Skip("TEST_DATABASE_URL not set")
        return nil
    }
    db, err := sql.Open("postgres", dsn)
    // ...
    db.SetMaxOpenConns(1)

    // Unique schema per test — fully isolated, parallel-safe
    schema := "tbtest_" + strings.ReplaceAll(shared.NewUUID(), "-", "")
    db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
    db.Exec(fmt.Sprintf("SET search_path TO %s", schema))

    // Run migrations in this schema
    db.Exec(readMigration(t))

    // Seed demo user
    db.Exec(`INSERT INTO users (...) VALUES ($1,$2,$3,$4,$5)`, ...)

    t.Cleanup(func() {
        db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
        db.Close()
    })
    return db
}
```

`SetMaxOpenConns(1)` keeps the single connection's `SET search_path` in effect for the test lifetime.

### 9b. `internal/workmanagement/service_test.go`

Same pattern as `testutil.NewTestDB`: open PostgreSQL, create a unique schema, `SET search_path`, run migrations, clean up. Skip when `TEST_DATABASE_URL` is not set. Update the `insertProject` helper to use `$N` placeholders.

### 9c. Running tests

```bash
# Start a local postgres for tests (separate from the compose stack)
podman run -d --name octbase-test-pg \
  -e POSTGRES_DB=octbase -e POSTGRES_USER=octbase -e POSTGRES_PASSWORD=octbase \
  -p 5434:5432 docker.io/library/postgres:16

cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5434/octbase?sslmode=disable" \
go test ./...

# Clean up
podman stop octbase-test-pg && podman rm octbase-test-pg
```

---

## 10. Verification

After applying all changes:

```bash
# 1. Build check
cd octbase-api && go build ./... && go vet ./...

# 2. No remaining SQLite references
grep -rn "sqlite\|OCTBASE_DB_PATH\|INSERT OR IGNORE\|modernc" \
  --include="*.go" --include="*.yml" .

# 3. Go tests
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5434/octbase?sslmode=disable" \
go test ./... -count=1
# Expected: all packages ok, none failed

# 4. Stack startup (from repo root)
podman-compose up --build -d
curl http://localhost:8000/health
# Expected: {"database":"ok","status":"ok"}

curl -o /dev/null -w "%{http_code}" http://localhost:8080/
# Expected: 200 (SPA served by nginx)

# 5. Frontend tests (with stack running)
cd octbase-frontend/tests
OCTBASE_API_BASE=http://127.0.0.1:8000 pytest -v
```

---

## 11. Known Pitfalls

| Pitfall | Fix |
|---|---|
| `docker.io/library/golang:1.25-alpine` has no root user | Use `--chown=nonroot:nonroot` on COPY; use `FROM scratch` for final stage |
| `docker.io/library/nginx:1-alpine` cannot bind port 80 in rootless Podman | Set `listen 8080;` in nginx.conf and expose/map 8080 |
| Pre-built `octbase-api/octbase-api` binary conflicts with container build | `.containerignore` excludes it; delete it before building if it exists |
| `podman-compose` v1 ignores `condition: service_healthy` | The DB ping-retry loop in `main.go` compensates |
| Tests fail with "database not ready" after a fresh stack start | The seed upsert resets state; restart the stack to trigger it |
| Frontend tests fail on `test_task_in_correct_column` | Caused by stale task state from a previous test run; the upsert seed fixes this on the next API startup |
