package auditlog

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/octbase/octbase-api/internal/shared"
)

// Repo handles audit log persistence.
type Repo struct{ db *sql.DB }

// NewRepo creates a new Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// Write appends a new audit record. Errors are logged but never returned so
// an audit failure never blocks the primary operation.
func (r *Repo) Write(actorID, action, targetType, targetID, meta, ip, ua string) {
	_, err := r.db.Exec(
		`INSERT INTO audit_logs
		   (id, actor_user_id, action, target_type, target_id, metadata_json, ip_address, user_agent, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		shared.NewUUID(), actorID, action, targetType, targetID, meta, ip, ua, time.Now(),
	)
	if err != nil {
		slog.Error("audit log write failed", "action", action, "error", err)
	}
}

// ListResult is the paginated response from List.
type ListResult struct {
	Logs  []AuditLog `json:"logs"`
	Total int64      `json:"total"`
}

// List returns audit logs newest-first with offset pagination and an optional
// action filter. Pass action="" to return all actions.
func (r *Repo) List(page, size int, action string) (ListResult, error) {
	var (
		total int64
		rows  *sql.Rows
		err   error
	)

	if action != "" {
		err = r.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = $1`, action).Scan(&total)
	} else {
		err = r.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&total)
	}
	if err != nil {
		return ListResult{}, fmt.Errorf("count audit_logs: %w", err)
	}

	if action != "" {
		rows, err = r.db.Query(
			`SELECT id, actor_user_id, action, target_type, target_id,
			        metadata_json, ip_address, user_agent, created_at
			   FROM audit_logs
			  WHERE action = $1
			  ORDER BY created_at DESC
			  LIMIT $2 OFFSET $3`,
			action, size, page*size,
		)
	} else {
		rows, err = r.db.Query(
			`SELECT id, actor_user_id, action, target_type, target_id,
			        metadata_json, ip_address, user_agent, created_at
			   FROM audit_logs
			  ORDER BY created_at DESC
			  LIMIT $1 OFFSET $2`,
			size, page*size,
		)
	}
	if err != nil {
		return ListResult{}, fmt.Errorf("query audit_logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(
			&l.ID, &l.ActorUserID, &l.Action, &l.TargetType, &l.TargetID,
			&l.MetadataJSON, &l.IPAddress, &l.UserAgent, &l.CreatedAt,
		); err != nil {
			return ListResult{}, fmt.Errorf("scan audit_log: %w", err)
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []AuditLog{}
	}
	return ListResult{Logs: logs, Total: total}, rows.Err()
}
