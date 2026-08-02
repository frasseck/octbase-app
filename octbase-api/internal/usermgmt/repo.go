package usermgmt

import (
	"database/sql"
	"fmt"
)

// Repo handles persistence for user management operations.
type Repo struct{ db *sql.DB }

// NewRepo creates a new Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// List returns all users ordered by creation date. Anonymized tombstones
// (status 'deleted') are excluded: they exist only to keep historical
// references (task authorship, audit trail) resolvable.
func (r *Repo) List() ([]ManagedUser, error) {
	rows, err := r.db.Query(
		`SELECT id, email, display_name, global_role, status, created_at, updated_at, last_login_at, avatar_updated_at
		   FROM users WHERE status <> 'deleted' ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []ManagedUser
	for rows.Next() {
		var u ManagedUser
		var avatarUpdatedAt sql.NullString
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.GlobalRole, &u.Status,
			&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &avatarUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if avatarUpdatedAt.Valid {
			u.AvatarUpdatedAt = &avatarUpdatedAt.String
		}
		users = append(users, u)
	}
	if users == nil {
		users = []ManagedUser{}
	}
	return users, rows.Err()
}

// FindByID returns a single user or nil. Anonymized tombstones are treated as
// not found, so deleted accounts keep answering 404 on every management route.
func (r *Repo) FindByID(id string) (*ManagedUser, error) {
	var u ManagedUser
	err := r.db.QueryRow(
		`SELECT id, email, display_name, global_role, status, created_at, updated_at, last_login_at
		   FROM users WHERE id = $1 AND status <> 'deleted'`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.GlobalRole, &u.Status,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	return &u, nil
}

// Create inserts a new user record.
func (r *Repo) Create(id, email, displayName, passwordHash, globalRole, status, now string) error {
	_, err := r.db.Exec(
		`INSERT INTO users (id, email, display_name, password_hash, global_role, status,
		                    is_active, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, email, displayName, passwordHash, globalRole, status,
		status == "active", now, now,
	)
	return err
}

// UpdateFields applies a partial update (only non-nil fields are changed).
func (r *Repo) UpdateFields(id, email, displayName, globalRole, status, now string) error {
	_, err := r.db.Exec(
		`UPDATE users SET email=$1, display_name=$2, global_role=$3, status=$4,
		                  is_active=(status='active'), updated_at=$5
		  WHERE id=$6`,
		email, displayName, globalRole, status, now, id,
	)
	return err
}

// SetStatus sets only the status (and is_active) column.
func (r *Repo) SetStatus(id, status, now string) error {
	_, err := r.db.Exec(
		`UPDATE users SET status=$1, is_active=(status='active'), updated_at=$2 WHERE id=$3`,
		status, now, id,
	)
	return err
}

// Anonymize erases a user for GDPR right-to-erasure. The row itself is kept as
// a tombstone (hard DELETE would violate the FKs from memberships, projects and
// invitations and would orphan authored content), but every piece of personal
// data on it is overwritten and all operational data owned by the user is
// dropped. Authored content (tasks, comments, pages) survives with the author
// ID pointing at the tombstone, whose display name renders as "Deleted user".
// Caller is responsible for checking that the target is not a Super Admin.
func (r *Repo) Anonymize(id, now string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("anonymize user: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Pending invitations addressed to the user's real email would keep that
	// email around, so drop them before the address is overwritten.
	if _, err := tx.Exec(
		`DELETE FROM invitations
		  WHERE accepted_at IS NULL AND email = (SELECT email FROM users WHERE id = $1)`, id,
	); err != nil {
		return fmt.Errorf("anonymize user: invitations: %w", err)
	}
	for _, q := range []string{
		`DELETE FROM refresh_tokens WHERE user_id = $1`,
		`DELETE FROM memberships WHERE user_id = $1`,
		`DELETE FROM notifications WHERE user_id = $1`,
		`DELETE FROM notification_preferences WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return fmt.Errorf("anonymize user: %w", err)
		}
	}
	// The placeholder email keeps the UNIQUE constraint happy and frees the
	// real address for re-registration; .invalid is a reserved TLD, so the
	// placeholder can never route mail.
	if _, err := tx.Exec(
		`UPDATE users SET email = 'deleted+' || id || '@anonymized.invalid',
		                  display_name = 'Deleted user',
		                  password_hash = NULL,
		                  global_role = 'USER',
		                  status = 'deleted',
		                  is_active = false,
		                  last_login_at = NULL,
		                  updated_at = $2
		  WHERE id = $1`, id, now,
	); err != nil {
		return fmt.Errorf("anonymize user: users: %w", err)
	}
	return tx.Commit()
}
