package scmintegration

import (
	"database/sql"
	"fmt"

	"github.com/octbase/octbase-api/internal/shared"
)

// RepositoryConnectionRepo handles repository connection persistence.
type RepositoryConnectionRepo struct{ db *sql.DB }

func NewRepositoryConnectionRepo(db *sql.DB) *RepositoryConnectionRepo {
	return &RepositoryConnectionRepo{db: db}
}

const connectionColumns = `id,project_id,provider,display_name,repository_url,default_branch,api_base_url,auth_kind,access_token_enc,refresh_token_enc,token_expires_at,created_at,updated_at,version`

func (r *RepositoryConnectionRepo) Create(rc *RepositoryConnection) error {
	if rc.AuthKind == "" {
		rc.AuthKind = authKindPAT
	}
	_, err := r.db.Exec(`INSERT INTO repository_connections (`+connectionColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		rc.ID, rc.ProjectID, rc.Provider, rc.DisplayName, rc.RepositoryURL, rc.DefaultBranch,
		nullIfEmpty(rc.APIBaseURL), rc.AuthKind, nullIfEmpty(rc.AccessToken),
		nullIfEmpty(rc.RefreshToken), nullIfEmpty(rc.TokenExpiresAt), rc.CreatedAt, rc.UpdatedAt, rc.Version)
	return err
}

func (r *RepositoryConnectionRepo) FindByID(id string) (*RepositoryConnection, error) {
	row := r.db.QueryRow(`SELECT `+connectionColumns+` FROM repository_connections WHERE id=$1`, id)
	rc, err := scanConnection(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo connection: %w", err)
	}
	return rc, nil
}

// FindByIDInProject returns the connection only when it belongs to projectID,
// or nil — the parent-scoped ownership guard for body-supplied repository IDs.
func (r *RepositoryConnectionRepo) FindByIDInProject(id, projectID string) (*RepositoryConnection, error) {
	row := r.db.QueryRow(`SELECT `+connectionColumns+` FROM repository_connections WHERE id=$1 AND project_id=$2`, id, projectID)
	rc, err := scanConnection(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo connection: %w", err)
	}
	return rc, nil
}

func (r *RepositoryConnectionRepo) ListByProject(projectID string) ([]RepositoryConnection, error) {
	rows, err := r.db.Query(`SELECT `+connectionColumns+` FROM repository_connections WHERE project_id=$1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rcs []RepositoryConnection
	for rows.Next() {
		rc, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		rcs = append(rcs, *rc)
	}
	if rcs == nil {
		rcs = []RepositoryConnection{}
	}
	return rcs, rows.Err()
}

// Update persists changes to a repository connection, guarded by the
// optimistic-locking convention (docs/architecture.md §3): zero rows
// affected means a concurrent edit won the race (e.g. an admin's PATCH
// racing background OAuth token rotation), surfaced as
// shared.ErrVersionConflict rather than silently dropping one writer's change.
func (r *RepositoryConnectionRepo) Update(rc *RepositoryConnection) error {
	if rc.AuthKind == "" {
		rc.AuthKind = authKindPAT
	}
	res, err := r.db.Exec(`UPDATE repository_connections SET provider=$1,display_name=$2,repository_url=$3,default_branch=$4,api_base_url=$5,auth_kind=$6,access_token_enc=$7,refresh_token_enc=$8,token_expires_at=$9,updated_at=$10,version=version+1 WHERE id=$11 AND version=$12`,
		rc.Provider, rc.DisplayName, rc.RepositoryURL, rc.DefaultBranch,
		nullIfEmpty(rc.APIBaseURL), rc.AuthKind, nullIfEmpty(rc.AccessToken),
		nullIfEmpty(rc.RefreshToken), nullIfEmpty(rc.TokenExpiresAt), rc.UpdatedAt, rc.ID, rc.Version)
	return shared.VersionGuardedResult(res, err, &rc.Version)
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanConnection(s rowScanner) (*RepositoryConnection, error) {
	var rc RepositoryConnection
	var apiBase, tokenEnc, refreshEnc, expiresAt sql.NullString
	if err := s.Scan(&rc.ID, &rc.ProjectID, &rc.Provider, &rc.DisplayName, &rc.RepositoryURL,
		&rc.DefaultBranch, &apiBase, &rc.AuthKind, &tokenEnc, &refreshEnc, &expiresAt,
		&rc.CreatedAt, &rc.UpdatedAt, &rc.Version); err != nil {
		return nil, err
	}
	rc.APIBaseURL = apiBase.String
	rc.AccessToken = tokenEnc.String
	rc.RefreshToken = refreshEnc.String
	rc.TokenExpiresAt = expiresAt.String
	return &rc, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *RepositoryConnectionRepo) Delete(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM branch_references WHERE repository_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM repository_connections WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// BranchReferenceRepo handles branch reference persistence.
type BranchReferenceRepo struct{ db *sql.DB }

func NewBranchReferenceRepo(db *sql.DB) *BranchReferenceRepo { return &BranchReferenceRepo{db: db} }

func (r *BranchReferenceRepo) Create(br *BranchReference) error {
	_, err := r.db.Exec(
		`INSERT INTO branch_references (id,task_id,repository_id,branch_name,branch_type,pr_status,pr_url,pr_number,created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		br.ID, br.TaskID, br.RepositoryID, br.BranchName, br.BranchType,
		br.PRStatus, br.PRUrl, br.PRNumber, br.CreatedAt)
	return err
}

// FindTaskProjectID returns the project_id for the given task ID, or "" if not found.
func (r *BranchReferenceRepo) FindTaskProjectID(taskID string) (string, error) {
	var projectID string
	err := r.db.QueryRow(`SELECT project_id FROM tasks WHERE id=$1`, taskID).Scan(&projectID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return projectID, err
}

func (r *BranchReferenceRepo) FindByID(id string) (*BranchReference, error) {
	var br BranchReference
	err := r.db.QueryRow(`SELECT id,task_id,repository_id,branch_name,branch_type,created_at FROM branch_references WHERE id=$1`, id).
		Scan(&br.ID, &br.TaskID, &br.RepositoryID, &br.BranchName, &br.BranchType, &br.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan branch reference: %w", err)
	}
	return &br, nil
}

// FindByIDInTask returns the branch reference only when it belongs to taskID,
// or nil — the parent-scoped ownership guard for branch sub-resource routes.
func (r *BranchReferenceRepo) FindByIDInTask(id, taskID string) (*BranchReference, error) {
	var br BranchReference
	err := r.db.QueryRow(`SELECT id,task_id,repository_id,branch_name,branch_type,created_at FROM branch_references WHERE id=$1 AND task_id=$2`, id, taskID).
		Scan(&br.ID, &br.TaskID, &br.RepositoryID, &br.BranchName, &br.BranchType, &br.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan branch reference: %w", err)
	}
	return &br, nil
}

func (r *BranchReferenceRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM branch_references WHERE id=$1`, id)
	return err
}

func (r *BranchReferenceRepo) ListByTask(taskID string) ([]BranchReference, error) {
	rows, err := r.db.Query(`SELECT id,task_id,repository_id,branch_name,branch_type,pr_status,pr_url,pr_number,created_at FROM branch_references WHERE task_id=$1 ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var brs []BranchReference
	for rows.Next() {
		var br BranchReference
		if err := rows.Scan(&br.ID, &br.TaskID, &br.RepositoryID, &br.BranchName, &br.BranchType, &br.PRStatus, &br.PRUrl, &br.PRNumber, &br.CreatedAt); err != nil {
			return nil, err
		}
		brs = append(brs, br)
	}
	if brs == nil {
		brs = []BranchReference{}
	}
	return brs, rows.Err()
}

// UpdatePRStatus updates the PR status, URL, and number for branches matching the branch name.
func (r *BranchReferenceRepo) UpdatePRStatus(branchName, prStatus, prURL string, prNumber int) error {
	_, err := r.db.Exec(
		`UPDATE branch_references SET pr_status=$1, pr_url=$2, pr_number=$3 WHERE branch_name=$4`,
		prStatus, prURL, prNumber, branchName,
	)
	return err
}

// UpdatePRByID sets the PR status, URL, and number for a single branch reference.
func (r *BranchReferenceRepo) UpdatePRByID(id, prStatus, prURL string, prNumber int) error {
	_, err := r.db.Exec(
		`UPDATE branch_references SET pr_status=$1, pr_url=$2, pr_number=$3 WHERE id=$4`,
		prStatus, prURL, prNumber, id,
	)
	return err
}

// OAuthStateRepo persists one-time OAuth CSRF state records.
type OAuthStateRepo struct{ db *sql.DB }

// NewOAuthStateRepo constructs an OAuthStateRepo.
func NewOAuthStateRepo(db *sql.DB) *OAuthStateRepo { return &OAuthStateRepo{db: db} }

// OAuthState ties an in-flight authorization to its connection and user.
type OAuthState struct {
	State        string
	Provider     string
	RepositoryID string
	UserID       string
	ExpiresAt    string
}

// Create stores a new state record.
func (r *OAuthStateRepo) Create(s *OAuthState, createdAt string) error {
	_, err := r.db.Exec(
		`INSERT INTO oauth_states (state,provider,repository_id,user_id,created_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		s.State, s.Provider, s.RepositoryID, s.UserID, createdAt, s.ExpiresAt)
	return err
}

// Consume atomically fetches and deletes a state record, returning nil if it
// does not exist.
func (r *OAuthStateRepo) Consume(state string) (*OAuthState, error) {
	var s OAuthState
	err := r.db.QueryRow(
		`DELETE FROM oauth_states WHERE state=$1 RETURNING state,provider,repository_id,user_id,expires_at`, state).
		Scan(&s.State, &s.Provider, &s.RepositoryID, &s.UserID, &s.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("consume oauth state: %w", err)
	}
	return &s, nil
}
