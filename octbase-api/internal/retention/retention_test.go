package retention_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/retention"
	"github.com/octbase/octbase-api/internal/testutil"
)

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func seedRetentionRows(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -400)

	for _, ins := range []struct {
		what string
		q    string
		args []interface{}
	}{
		{"old audit row", `INSERT INTO audit_logs (id, action, created_at) VALUES ('a-old','LOGIN_SUCCESS',$1)`, []interface{}{old}},
		{"fresh audit row", `INSERT INTO audit_logs (id, action, created_at) VALUES ('a-new','LOGIN_SUCCESS',$1)`, []interface{}{now}},
		{"old activity row", `INSERT INTO activity_entries (id, project_id, actor_user_id, type, message, created_at)
			VALUES ('act-old','p1','u1','TASK_CREATED','m',$1)`, []interface{}{old.Format(time.RFC3339)}},
		{"fresh activity row", `INSERT INTO activity_entries (id, project_id, actor_user_id, type, message, created_at)
			VALUES ('act-new','p1','u1','TASK_CREATED','m',$1)`, []interface{}{now.Format(time.RFC3339)}},
		{"expired refresh token", `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
			VALUES ('rt-old',$1,'h-old',$2)`, []interface{}{testutil.SecondUserID, now.AddDate(0, 0, -1)}},
		{"valid refresh token", `INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
			VALUES ('rt-new',$1,'h-new',$2)`, []interface{}{testutil.SecondUserID, now.AddDate(0, 0, 7)}},
		{"expired password reset token", `INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
			VALUES ('prt-old',$1,$2)`, []interface{}{testutil.SecondUserID, now.Add(-time.Hour)}},
		{"live password reset token", `INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
			VALUES ('prt-new',$1,$2)`, []interface{}{testutil.SecondUserID, now.Add(time.Hour)}},
		{"long-expired unaccepted invitation", `INSERT INTO invitations (id, email, role, token_hash, invited_by, expires_at)
			VALUES ('inv-old','stale@test.dev','PROJECT_MEMBER','t1',$1,$2)`, []interface{}{testutil.DemoUserID, now.AddDate(0, 0, -40)}},
		{"recently expired unaccepted invitation", `INSERT INTO invitations (id, email, role, token_hash, invited_by, expires_at)
			VALUES ('inv-grace','grace@test.dev','PROJECT_MEMBER','t2',$1,$2)`, []interface{}{testutil.DemoUserID, now.AddDate(0, 0, -1)}},
		{"accepted old invitation", `INSERT INTO invitations (id, email, role, token_hash, invited_by, expires_at, accepted_at)
			VALUES ('inv-done','done@test.dev','PROJECT_MEMBER','t3',$1,$2,$2)`, []interface{}{testutil.DemoUserID, now.AddDate(0, 0, -40)}},
		{"project for repo connection", `INSERT INTO projects (id, name, slug, created_at, updated_at)
			VALUES ('p1','Retention P','retention-p',$1,$1)`, []interface{}{now.Format(time.RFC3339)}},
		{"repo connection", `INSERT INTO repository_connections (id, project_id, display_name, repository_url, created_at, updated_at)
			VALUES ('rc1','p1','Repo','https://example.test/repo.git',$1,$1)`, []interface{}{now.Format(time.RFC3339)}},
		{"expired oauth state", `INSERT INTO oauth_states (state, provider, repository_id, user_id, created_at, expires_at)
			VALUES ('st-old','GITHUB','rc1','u1',$1,$1)`, []interface{}{now.Add(-time.Hour).Format(time.RFC3339)}},
		{"live oauth state", `INSERT INTO oauth_states (state, provider, repository_id, user_id, created_at, expires_at)
			VALUES ('st-new','GITHUB','rc1','u1',$1,$1)`, []interface{}{now.Add(time.Hour).Format(time.RFC3339)}},
	} {
		if _, err := db.Exec(ins.q, ins.args...); err != nil {
			t.Fatalf("insert %s: %v", ins.what, err)
		}
	}
}

func TestPurge_RemovesOnlyAgedRows(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedRetentionRows(t, db)

	deleted := retention.Purge(db, retention.Config{AuditDays: 365, ActivityDays: 365})
	if deleted != 6 {
		t.Errorf("expected 6 purged rows, got %d", deleted)
	}

	if n := count(t, db, `SELECT COUNT(*) FROM audit_logs`); n != 1 {
		t.Errorf("audit_logs: want 1 surviving row, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM activity_entries`); n != 1 {
		t.Errorf("activity_entries: want 1 surviving row, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM refresh_tokens WHERE id LIKE 'rt-%'`); n != 1 {
		t.Errorf("refresh_tokens: want 1 surviving row, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM password_reset_tokens WHERE token_hash LIKE 'prt-%'`); n != 1 {
		t.Errorf("password_reset_tokens: want 1 surviving row, got %d", n)
	}
	// The stale unaccepted invitation is gone; the one inside the grace window
	// and the accepted one survive.
	if n := count(t, db, `SELECT COUNT(*) FROM invitations`); n != 2 {
		t.Errorf("invitations: want 2 surviving rows, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM invitations WHERE id='inv-old'`); n != 0 {
		t.Error("long-expired unaccepted invitation should be purged")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM oauth_states`); n != 1 {
		t.Errorf("oauth_states: want only the live state to survive, got %d rows", n)
	}
}

func TestPurge_ZeroDaysDisablesLogPurges(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedRetentionRows(t, db)

	retention.Purge(db, retention.Config{AuditDays: 0, ActivityDays: 0})

	if n := count(t, db, `SELECT COUNT(*) FROM audit_logs`); n != 2 {
		t.Errorf("audit purge should be disabled, got %d rows left", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM activity_entries`); n != 2 {
		t.Errorf("activity purge should be disabled, got %d rows left", n)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("OCTBASE_AUDIT_RETENTION_DAYS", "90")
	t.Setenv("OCTBASE_ACTIVITY_RETENTION_DAYS", "not-a-number")
	cfg := retention.ConfigFromEnv()
	if cfg.AuditDays != 90 {
		t.Errorf("AuditDays = %d, want 90", cfg.AuditDays)
	}
	if cfg.ActivityDays != retention.DefaultActivityRetentionDays {
		t.Errorf("ActivityDays = %d, want default %d", cfg.ActivityDays, retention.DefaultActivityRetentionDays)
	}
}
