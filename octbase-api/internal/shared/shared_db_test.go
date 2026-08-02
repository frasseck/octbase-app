package shared_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

func TestOpenDB(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := shared.OpenDB(dsn)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping opened db: %v", err)
	}
}

func TestWithStatementTimeout(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	// A short statement_timeout must cancel a query that runs past it, so a
	// stuck query releases its pooled connection instead of pinning it forever.
	db, err := shared.OpenDB(dsn, shared.WithStatementTimeout(300*time.Millisecond))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.ExecContext(context.Background(), "SELECT pg_sleep(3)")
	if err == nil {
		t.Fatal("expected pg_sleep(3) to be cancelled by statement_timeout, got nil error")
	}
	// PostgreSQL raises SQLSTATE 57014 (query_canceled) for statement_timeout.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("expected statement_timeout cancellation (SQLSTATE 57014), got: %v", err)
	}

	// A fast query on the same pool still succeeds — the timeout bounds runaway
	// queries, not normal ones.
	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("fast query after timeout: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d", one)
	}
}

func TestWithStatementTimeout_ZeroDisables(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	// Zero duration must not set a timeout: a query longer than the old default
	// completes normally (the migration pool relies on this).
	db, err := shared.OpenDB(dsn, shared.WithStatementTimeout(0))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "SELECT pg_sleep(0.2)"); err != nil {
		t.Fatalf("pg_sleep(0.2) with no statement_timeout should succeed, got: %v", err)
	}
}

// The health check must never block on the pool: when connections are all
// checked out, its DB reads have to fail fast on the caller's deadline instead
// of hanging (the failure mode behind the ~20h API deadlock — an unbounded
// db.Ping() in /health hung too, hiding the outage).
func TestMigrationVersionContext_FailsFastUnderPoolExhaustion(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := shared.OpenDB(dsn)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// Occupy the pool's only connection so the next acquire must wait.
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("checkout sole conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := shared.MigrationVersionContext(ctx, db); err == nil {
		t.Fatal("expected MigrationVersionContext to fail while the pool is exhausted")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("MigrationVersionContext hung under exhaustion (%v); it must fail fast on the deadline", elapsed)
	}
}

func TestLatestMigrationVersion(t *testing.T) {
	// Synthetic dir: highest NNN_*.up.sql prefix wins; .down.sql and non-numeric
	// files are ignored.
	dir := t.TempDir()
	for _, name := range []string{
		"001_initial.up.sql", "001_initial.down.sql",
		"002_constraints.up.sql", "010_project_owner.up.sql",
		"README.md", "notes.txt",
	} {
		if err := os.WriteFile(dir+"/"+name, []byte("-- test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	v, err := shared.LatestMigrationVersion(dir)
	if err != nil {
		t.Fatalf("LatestMigrationVersion: %v", err)
	}
	if v != 10 {
		t.Errorf("version = %d, want 10 (highest .up.sql prefix)", v)
	}

	// Empty/absent dir is an error, not a silent zero.
	if _, err := shared.LatestMigrationVersion(t.TempDir()); err == nil {
		t.Error("expected error for a directory with no migrations")
	}
	if _, err := shared.LatestMigrationVersion(dir + "/does-not-exist"); err == nil {
		t.Error("expected error for a missing directory")
	}

	// Guards against the real migrations dir drifting from the health check:
	// the derived version must be positive.
	real, err := shared.LatestMigrationVersion("../../migrations")
	if err != nil {
		t.Fatalf("LatestMigrationVersion(real): %v", err)
	}
	if real == 0 {
		t.Error("real migrations dir reported version 0")
	}
}

// Every .up.sql must have a matching .down.sql (and vice versa), and the
// version numbers must be gapless from 001 to the derived head. golang-migrate
// only discovers a missing down file at rollback time — i.e. in production —
// and LatestMigrationVersion deliberately ignores down files, so nothing else
// checks this.
func TestMigrationsArePairedAndGapless(t *testing.T) {
	const dir = "../../migrations"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	ups := map[int]string{}
	downs := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		var m map[int]string
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			m = ups
		case strings.HasSuffix(name, ".down.sql"):
			m = downs
		default:
			t.Errorf("unexpected file in migrations dir: %s", name)
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Errorf("migration file without numeric prefix: %s", name)
			continue
		}
		n, err := strconv.Atoi(prefix)
		if err != nil {
			t.Errorf("migration file with non-numeric prefix: %s", name)
			continue
		}
		if prev, dup := m[n]; dup {
			t.Errorf("duplicate migration version %03d: %s and %s", n, prev, name)
		}
		m[n] = name
	}
	if len(ups) == 0 {
		t.Fatal("no migrations found")
	}
	max := 0
	for n := range ups {
		if n > max {
			max = n
		}
	}
	for n := 1; n <= max; n++ {
		if _, ok := ups[n]; !ok {
			t.Errorf("gap in migration versions: no %03d.up.sql", n)
		}
		if _, ok := downs[n]; !ok {
			t.Errorf("migration %03d (%s) has no matching .down.sql", n, ups[n])
		}
	}
	for n := range downs {
		if _, ok := ups[n]; !ok {
			t.Errorf("orphan down migration: %s has no matching .up.sql", downs[n])
		}
	}
}

func TestLoadUserGlobalRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	mw := shared.LoadUserGlobalRole(db)

	// Active Super Admin: role is loaded into context, request proceeds.
	t.Run("active user loads role", func(t *testing.T) {
		var gotRole string
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRole = shared.GetGlobalRole(r)
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, testutil.SuperAdminUserID))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if gotRole != "SUPER_ADMIN" {
			t.Errorf("loaded role = %q, want SUPER_ADMIN", gotRole)
		}
	})

	// Disabled account: 401 even with a valid user ID.
	t.Run("disabled user rejected", func(t *testing.T) {
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("handler should not run for a disabled account")
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, testutil.DisabledUserID))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	// Unknown user ID: lookup fails -> 401.
	t.Run("unknown user rejected", func(t *testing.T) {
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("handler should not run for an unknown user")
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, shared.NewUUID()))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	// No user ID in context: anonymous request passes through unchanged.
	t.Run("anonymous passes through", func(t *testing.T) {
		ran := false
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ran = true
			if role := shared.GetGlobalRole(r); role != "" {
				t.Errorf("anonymous request has role %q, want empty", role)
			}
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !ran || rec.Code != http.StatusOK {
			t.Errorf("anonymous request not passed through: ran=%v status=%d", ran, rec.Code)
		}
	})
}

func TestRequireProjectMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	now := shared.Now()
	projectID := shared.NewUUID()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, slug, visibility, created_by_user_id, created_at, updated_at) VALUES ($1,$2,$3,'PRIVATE',$4,$5,$6)`,
		projectID, "Member Project", "member-project", testutil.DemoUserID, now, now,
	); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO memberships (id, project_id, user_id, role, created_at, updated_at) VALUES ($1,$2,$3,'PROJECT_MEMBER',$4,$5)`,
		shared.NewUUID(), projectID, testutil.DemoUserID, now, now,
	); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	role, err := shared.RequireProjectMember(db, projectID, testutil.DemoUserID)
	if err != nil {
		t.Fatalf("RequireProjectMember(member) error: %v", err)
	}
	if role != "PROJECT_MEMBER" {
		t.Errorf("role = %q, want PROJECT_MEMBER", role)
	}

	_, err = shared.RequireProjectMember(db, projectID, testutil.SecondUserID)
	if !errors.Is(err, shared.ErrNotMember) {
		t.Errorf("non-member error = %v, want ErrNotMember", err)
	}
}

// TestRequireProjectMember_DBError exercises the wrapped-error branch (a query
// failure that is not sql.ErrNoRows) by using a closed connection.
func TestRequireProjectMember_DBError(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close() // force every subsequent query to error

	_, err = shared.RequireProjectMember(db, shared.NewUUID(), shared.NewUUID())
	if err == nil || errors.Is(err, shared.ErrNotMember) {
		t.Fatalf("err = %v, want a wrapped DB error (not nil, not ErrNotMember)", err)
	}
}

// TestWithTx_BeginError exercises the begin-failure branch using a closed pool.
func TestWithTx_BeginError(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close()

	called := false
	err = shared.WithTx(db, func(*sql.Tx) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("WithTx on a closed db should return a begin error")
	}
	if called {
		t.Error("fn must not run when the transaction cannot begin")
	}
}

func TestWithTx(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	if _, err := db.Exec(`CREATE TEMP TABLE withtx_demo (n int)`); err != nil {
		t.Fatalf("create temp table: %v", err)
	}

	// Commit path.
	if err := shared.WithTx(db, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO withtx_demo (n) VALUES (1)`)
		return e
	}); err != nil {
		t.Fatalf("WithTx commit: %v", err)
	}

	// Rollback path: fn returns an error, the insert must be undone.
	sentinel := errors.New("boom")
	if err := shared.WithTx(db, func(tx *sql.Tx) error {
		if _, e := tx.Exec(`INSERT INTO withtx_demo (n) VALUES (2)`); e != nil {
			return e
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("WithTx rollback err = %v, want sentinel", err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM withtx_demo`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1 (committed insert kept, rolled-back insert gone)", count)
	}
}

// NOTE: shared.RunMigrations and shared.MigrationVersion are intentionally not
// unit-tested here. They wrap golang-migrate, which holds a database-scoped
// advisory lock on one pooled connection for the duration of Up()/Version();
// driving that against the shared test database (with the schema-isolated
// search_path these tests rely on) deadlocks. Both functions are exercised on
// every application start (cmd/octbase-api) and in the deployed stack's
// /api/v1/health migration-version check, which is the meaningful integration
// path for this plumbing.
