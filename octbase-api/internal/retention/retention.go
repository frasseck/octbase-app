// Package retention enforces GDPR storage limitation (Art. 5(1)(e)): personal
// data in operational logs must not be kept longer than needed. It purges
//
//   - audit_logs older than the audit retention window (they carry IP address
//     and user agent),
//   - activity_entries older than the activity retention window,
//   - refresh_tokens past their expiry (the row itself only holds a hash, but
//     it maps a user to session times),
//   - password_reset_tokens past their expiry (hash-only, but each row maps a
//     user to a reset attempt; spent tokens expire within the hour too),
//   - unaccepted invitations long past their expiry (they hold the invitee's
//     email address),
//   - expired OAuth state records (they tie a user ID to an in-flight
//     authorization and are dead ten minutes after creation).
//
// Windows are deployment-configurable via OCTBASE_AUDIT_RETENTION_DAYS and
// OCTBASE_ACTIVITY_RETENTION_DAYS; a value of 0 or less disables that purge
// (some clients contractually require longer audit retention — that choice is
// the operator's, not the code's). Purge runs at API startup and then daily;
// there is deliberately no per-request lazy sweep because these tables are
// written far more often than they are read, and the daily cadence matches
// retention windows measured in days (cf. the DONE-task sweep, which is lazy
// because it is tied to a hot read path).
package retention

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Default retention windows. Audit and activity default to one year; expired
// unaccepted invitations are kept for a short grace period so an operator can
// still see that an invitation ran out before it disappears.
const (
	DefaultAuditRetentionDays    = 365
	DefaultActivityRetentionDays = 365
	invitationGraceDays          = 30
	purgeInterval                = 24 * time.Hour
)

// Config holds the effective retention windows in days; <= 0 disables a purge.
type Config struct {
	AuditDays    int
	ActivityDays int
}

// ConfigFromEnv reads the retention windows from the environment, falling back
// to the defaults on unset or unparsable values.
func ConfigFromEnv() Config {
	return Config{
		AuditDays:    envDays("OCTBASE_AUDIT_RETENTION_DAYS", DefaultAuditRetentionDays),
		ActivityDays: envDays("OCTBASE_ACTIVITY_RETENTION_DAYS", DefaultActivityRetentionDays),
	}
}

func envDays(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid retention value; using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}

// Purge runs one purge pass and returns the total number of rows removed.
// Failures on one table don't stop the others: each purge is independently
// useful, and the next pass retries anyway.
func Purge(db *sql.DB, cfg Config) int64 {
	now := time.Now().UTC()
	var total int64

	if cfg.AuditDays > 0 {
		// audit_logs.created_at is TIMESTAMPTZ.
		total += exec(db, "audit_logs",
			`DELETE FROM audit_logs WHERE created_at < $1`,
			now.AddDate(0, 0, -cfg.AuditDays))
	}
	if cfg.ActivityDays > 0 {
		// activity_entries.created_at is RFC3339 TEXT; lexicographic comparison
		// is chronological for a fixed-format UTC timestamp (same technique as
		// the DONE-task sweep).
		total += exec(db, "activity_entries",
			`DELETE FROM activity_entries WHERE created_at < $1`,
			now.AddDate(0, 0, -cfg.ActivityDays).Format(time.RFC3339))
	}
	total += exec(db, "refresh_tokens",
		`DELETE FROM refresh_tokens WHERE expires_at < $1`, now)
	total += exec(db, "password_reset_tokens",
		`DELETE FROM password_reset_tokens WHERE expires_at < $1`, now)
	total += exec(db, "invitations",
		`DELETE FROM invitations WHERE accepted_at IS NULL AND expires_at < $1`,
		now.AddDate(0, 0, -invitationGraceDays))
	// oauth_states.expires_at is RFC3339 TEXT (10-minute one-time CSRF records).
	total += exec(db, "oauth_states",
		`DELETE FROM oauth_states WHERE expires_at < $1`, now.Format(time.RFC3339))
	return total
}

func exec(db *sql.DB, table, query string, arg any) int64 {
	res, err := db.Exec(query, arg)
	if err != nil {
		slog.Error("retention purge failed", "table", table, "error", err)
		return 0
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		slog.Info("retention purge", "table", table, "rowsDeleted", n)
	}
	return n
}

// Start runs an immediate purge pass and then one per purgeInterval for the
// lifetime of the process. The single daily writer keeps this compatible with
// the one-container scaling stance (docs/architecture.md §4); with multiple
// replicas the DELETEs would simply be idempotent duplicates. The goroutine
// stops when ctx is cancelled, e.g. on graceful shutdown.
func Start(ctx context.Context, db *sql.DB, cfg Config) {
	Purge(db, cfg)
	go func() {
		ticker := time.NewTicker(purgeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				Purge(db, cfg)
			}
		}
	}()
}
