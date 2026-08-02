// Package shared provides cross-cutting utilities: PostgreSQL helpers, HTTP
// response writers, JSON decoding, pagination, UUID generation, CORS headers,
// and the JWTMiddleware that validates Bearer tokens.
package shared

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// DBOption customises a pool opened by OpenDB.
type DBOption func(*dbOptions)

type dbOptions struct {
	statementTimeout time.Duration
}

// WithStatementTimeout applies a server-side statement_timeout to every
// connection in the pool, so a single query that blocks (on a lock, a runaway
// scan, an unavailable dependency) is cancelled by PostgreSQL instead of
// pinning its pooled connection indefinitely. Without this backstop a slow
// leak of stuck queries exhausts the pool over time and every subsequent
// request — including the health check — blocks forever waiting to acquire a
// connection (observed as a full API deadlock after ~20h uptime on a client
// stack). A zero or negative duration is a no-op; an operator-supplied
// statement_timeout already present in the DSN is preserved. Do NOT use this on
// the migration connection, whose DDL/backfills may legitimately run long.
func WithStatementTimeout(d time.Duration) DBOption {
	return func(o *dbOptions) { o.statementTimeout = d }
}

// OpenDB opens a PostgreSQL database using the given DSN. It uses the pgx
// driver via its database/sql-compatible stdlib adapter, so the DSN accepts
// the same libpq keyword and URL formats as before.
func OpenDB(dsn string, opts ...DBOption) (*sql.DB, error) {
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if o.statementTimeout > 0 {
		if connConfig.RuntimeParams == nil {
			connConfig.RuntimeParams = map[string]string{}
		}
		// Respect an explicit statement_timeout in the DSN; only supply the default.
		if _, ok := connConfig.RuntimeParams["statement_timeout"]; !ok {
			connConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(o.statementTimeout.Milliseconds(), 10)
		}
	}
	return stdlib.OpenDB(*connConfig), nil
}

// RunMigrations applies all pending migrations from the given directory path.
func RunMigrations(db *sql.DB, migrationsPath string) error {
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("create migrate driver: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+migrationsPath, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	version, dirty, _ := m.Version()
	slog.Info("migrations applied", "version", version, "dirty", dirty)
	return nil
}

// LatestMigrationVersion returns the highest migration version available on disk,
// derived from the numeric prefix of the `NNN_*.up.sql` files in migrationsPath.
// Callers use this as the expected/target version (e.g. the health check) so the
// value tracks the migration files automatically instead of a hand-bumped constant.
func LatestMigrationVersion(migrationsPath string) (uint, error) {
	entries, err := os.ReadDir(migrationsPath)
	if err != nil {
		return 0, fmt.Errorf("read migrations dir: %w", err)
	}
	var latest uint
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			continue
		}
		if uint(n) > latest {
			latest = uint(n)
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no migrations found in %s", migrationsPath)
	}
	return latest, nil
}

// MigrationVersion returns the current migration version from the database.
func MigrationVersion(db *sql.DB, migrationsPath string) (uint, bool, error) {
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return 0, false, fmt.Errorf("create migrate driver: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+migrationsPath, "pgx5", driver)
	if err != nil {
		return 0, false, fmt.Errorf("create migrate instance: %w", err)
	}
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

// MigrationVersionContext returns the current migration version directly from
// golang-migrate's schema_migrations table, honouring ctx's deadline. Unlike
// MigrationVersion it never blocks indefinitely: under pool exhaustion the
// QueryRowContext fails fast when ctx expires, so a caller such as the health
// check can report "degraded" instead of hanging. No rows (a fresh database
// before the first migration) reads as version 0, matching MigrationVersion.
func MigrationVersionContext(ctx context.Context, db *sql.DB) (uint, bool, error) {
	var version uint
	var dirty bool
	err := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return version, dirty, nil
}

// ErrVersionConflict is returned by version-guarded UPDATEs when the row's
// current version no longer matches the version the write was based on (a
// concurrent editor won, or the row was deleted). Handlers map it to
// 409 VERSION_CONFLICT so a stale write never silently overwrites a newer one.
var ErrVersionConflict = errors.New("version conflict: the row was changed or deleted concurrently")

// likeEscaper escapes the LIKE/ILIKE pattern metacharacters. Backslash must be
// replaced first conceptually, but strings.Replacer applies all pairs in one
// pass, so an already-escaped "\%" cannot be double-processed.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike escapes LIKE/ILIKE metacharacters (\, %, _) in user-supplied
// search text so the text matches literally inside a pattern. Without this, a
// trailing backslash makes PostgreSQL reject the pattern (a 500 for the
// caller), and % / _ act as wildcards the user never asked for.
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}

// VersionGuardedResult finishes a version-guarded UPDATE (WHERE id=... AND
// version=...): zero affected rows means the row's version moved on (or the
// row is gone) and yields ErrVersionConflict; on success the caller's
// in-memory version is bumped to match the row, so the struct written back to
// the client (and any follow-up write based on it) carries the new version.
func VersionGuardedResult(res sql.Result, err error, version *int) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrVersionConflict
	}
	*version++
	return nil
}

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), surfaced through the pgx stdlib driver. Prefer
// this over string-matching err.Error() for "unique"/"duplicate", which is
// fragile (locale/driver-dependent wording) and can misclassify unrelated
// errors.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// RowQueryer is the single-row query surface shared by *sql.DB and *sql.Tx,
// so helpers like CountUsers work both inside and outside a transaction.
type RowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// CountUsers returns how many user accounts currently occupy a seat: every
// row in users except GDPR-anonymized tombstones (status 'deleted'), i.e.
// active, disabled and invited accounts alike, including the admin. This is
// the number the installation-wide OCTBASE_MAX_USERS limit is enforced
// against.
func CountUsers(q RowQueryer) (int, error) {
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM users WHERE status <> 'deleted'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// WithTx runs fn inside a database transaction. It rolls back on error and
// commits on success.
func WithTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
