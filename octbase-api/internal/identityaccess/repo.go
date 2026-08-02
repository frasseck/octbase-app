package identityaccess

import (
	"database/sql"
	"fmt"

	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// UserRepo handles user persistence.
type UserRepo struct {
	db *sql.DB
}

func NewUserRepo(db *sql.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) FindByID(id string) (*User, error) {
	row := r.db.QueryRow(`SELECT id, email, display_name, created_at, updated_at, avatar_updated_at FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var avatarUpdatedAt sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &u.UpdatedAt, &avatarUpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	if avatarUpdatedAt.Valid {
		u.AvatarUpdatedAt = &avatarUpdatedAt.String
	}
	return &u, nil
}

// SetAvatar stores (or replaces) the user's profile picture and stamps
// avatar_updated_at, which the frontend uses both as a "has avatar" flag and a
// cache-busting token. Returns the new token.
func (r *UserRepo) SetAvatar(userID string, data []byte, contentType string) (string, error) {
	updatedAt := shared.Now()
	res, err := r.db.Exec(
		`UPDATE users SET avatar = $1, avatar_content_type = $2, avatar_updated_at = $3, updated_at = $3 WHERE id = $4`,
		data, contentType, updatedAt, userID)
	if err != nil {
		return "", fmt.Errorf("set avatar: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", sql.ErrNoRows
	}
	return updatedAt, nil
}

// GetAvatar returns the stored avatar bytes, its content type and cache token.
// found is false when the user exists but has no avatar (or does not exist).
func (r *UserRepo) GetAvatar(userID string) (data []byte, contentType, updatedAt string, found bool, err error) {
	var avatar []byte
	var ct, upd sql.NullString
	row := r.db.QueryRow(`SELECT avatar, avatar_content_type, avatar_updated_at FROM users WHERE id = $1`, userID)
	if err = row.Scan(&avatar, &ct, &upd); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", "", false, nil
		}
		return nil, "", "", false, fmt.Errorf("get avatar: %w", err)
	}
	if len(avatar) == 0 {
		return nil, "", "", false, nil
	}
	return avatar, ct.String, upd.String, true, nil
}

// ClearAvatar removes the user's profile picture. It is a no-op if none is set.
func (r *UserRepo) ClearAvatar(userID string) error {
	_, err := r.db.Exec(
		`UPDATE users SET avatar = NULL, avatar_content_type = NULL, avatar_updated_at = NULL, updated_at = $1 WHERE id = $2`,
		shared.Now(), userID)
	if err != nil {
		return fmt.Errorf("clear avatar: %w", err)
	}
	return nil
}

// MembershipRepo handles membership persistence.
type MembershipRepo struct {
	db *sql.DB
}

func NewMembershipRepo(db *sql.DB) *MembershipRepo { return &MembershipRepo{db: db} }

func (r *MembershipRepo) ListByProject(projectID string) ([]Membership, error) {
	rows, err := r.db.Query(
		`SELECT id, project_id, user_id, role, COALESCE(assigned_by_user_id,''), created_at, updated_at
		   FROM memberships WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ms []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.UserID, &m.Role, &m.AssignedByUserID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	if ms == nil {
		ms = []Membership{}
	}
	return ms, rows.Err()
}

func (r *MembershipRepo) Create(m *Membership) error {
	var assignedBy any
	if m.AssignedByUserID != "" {
		assignedBy = m.AssignedByUserID
	}
	_, err := r.db.Exec(
		`INSERT INTO memberships (id, project_id, user_id, role, assigned_by_user_id, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		m.ID, m.ProjectID, m.UserID, m.Role, assignedBy, m.CreatedAt, m.UpdatedAt)
	return err
}

func (r *MembershipRepo) UpdateRole(projectID, userID, role string, updatedAt string) error {
	_, err := r.db.Exec(`UPDATE memberships SET role=$1, updated_at=$2 WHERE project_id=$3 AND user_id=$4`,
		role, updatedAt, projectID, userID)
	return err
}

func (r *MembershipRepo) Delete(projectID, userID string) error {
	_, err := r.db.Exec(`DELETE FROM memberships WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	return err
}

// FindRole returns the role of userID in projectID, or "" if not a member.
func (r *MembershipRepo) FindRole(projectID, userID string) (string, error) {
	var role string
	err := r.db.QueryRow(`SELECT role FROM memberships WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return role, err
}

// CountByRole returns the number of members of projectID currently holding role.
func (r *MembershipRepo) CountByRole(projectID, role string) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM memberships WHERE project_id=$1 AND role=$2`, projectID, role).Scan(&n)
	return n, err
}

// ListAssignableUsers returns everyone who may be picked as a task's assignee or
// reviewer on projectID: the project's members, plus every active global
// ADMIN/SUPER_ADMIN account. Global admins reach every project without a
// membership row, so a picker fed from memberships alone silently excludes them.
// Members come first, in join order, then the extra admins by name; an admin who
// *is* a member appears once, as a member.
func (r *MembershipRepo) ListAssignableUsers(projectID string) ([]AssignableUser, error) {
	rows, err := r.db.Query(`
		SELECT u.id, u.display_name,
		       -- A member's email is already readable via GET /members. An admin
		       -- who is not a member is new surface, so only their display name
		       -- (all a picker shows) crosses the boundary — this endpoint must
		       -- not become an admin-address directory for every project member.
		       CASE WHEN m.id IS NOT NULL THEN u.email ELSE '' END,
		       COALESCE(m.role, ''), u.global_role,
		       (m.id IS NOT NULL) AS is_member,
		       u.avatar_updated_at
		FROM users u
		LEFT JOIN memberships m ON m.user_id = u.id AND m.project_id = $1
		WHERE m.id IS NOT NULL
		   OR (u.global_role IN ($2, $3) AND u.status = 'active')
		ORDER BY is_member DESC, m.created_at, u.display_name
	`, projectID, rbac.GlobalSuperAdmin, rbac.GlobalAdmin)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var us []AssignableUser
	for rows.Next() {
		var u AssignableUser
		var avatarUpdatedAt sql.NullString
		if err := rows.Scan(&u.UserID, &u.Name, &u.Email, &u.Role, &u.GlobalRole, &u.Member, &avatarUpdatedAt); err != nil {
			return nil, err
		}
		if avatarUpdatedAt.Valid {
			u.AvatarUpdatedAt = &avatarUpdatedAt.String
		}
		us = append(us, u)
	}
	if us == nil {
		us = []AssignableUser{}
	}
	return us, rows.Err()
}

// ListByProjectWithUsers returns memberships joined with user profiles.
func (r *MembershipRepo) ListByProjectWithUsers(projectID string) ([]MemberWithUser, error) {
	rows, err := r.db.Query(`
		SELECT m.id, m.project_id, m.user_id, u.display_name, u.email, m.role, m.created_at, u.avatar_updated_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.project_id = $1
		ORDER BY m.created_at
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ms []MemberWithUser
	for rows.Next() {
		var m MemberWithUser
		var avatarUpdatedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.UserID, &m.Name, &m.Email, &m.Role, &m.JoinedAt, &avatarUpdatedAt); err != nil {
			return nil, err
		}
		if avatarUpdatedAt.Valid {
			m.AvatarUpdatedAt = &avatarUpdatedAt.String
		}
		ms = append(ms, m)
	}
	if ms == nil {
		ms = []MemberWithUser{}
	}
	return ms, rows.Err()
}
