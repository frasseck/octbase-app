package workmanagement

import (
	"database/sql"
	"fmt"

	"github.com/octbase/octbase-api/internal/shared"
)

// ProjectRepo handles project persistence.
type ProjectRepo struct{ db *sql.DB }

func NewProjectRepo(db *sql.DB) *ProjectRepo { return &ProjectRepo{db: db} }

func (r *ProjectRepo) CreateTx(tx *sql.Tx, p *Project) error {
	return r.create(tx, p)
}

func (r *ProjectRepo) create(db execer, p *Project) error {
	_, err := db.Exec(`INSERT INTO projects (id,name,slug,abbreviation,description,visibility,status,theme_enabled,initiative_enabled,estimation_unit,board_lane_limit,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		p.ID, p.Name, p.Slug, p.Abbreviation, p.Description, p.Visibility, p.Status, p.ThemeEnabled, p.InitiativeEnabled, p.EstimationUnit, p.BoardLaneLimit, p.CreatedAt, p.UpdatedAt, p.Version)
	return err
}

// SlugExists reports whether any project already claims the slug.
func (r *ProjectRepo) SlugExists(slug string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE slug=$1)`, slug).Scan(&exists)
	return exists, err
}

func (r *ProjectRepo) FindByID(id string) (*Project, error) {
	row := r.db.QueryRow(`SELECT id,name,slug,abbreviation,description,visibility,status,theme_enabled,initiative_enabled,estimation_unit,board_lane_limit,created_at,updated_at,version FROM projects WHERE id=$1`, id)
	return scanProject(row)
}

// ListAll returns every project (for Super Admin), newest-first and paginated.
func (r *ProjectRepo) ListAll(page, size int) ([]Project, error) {
	rows, err := r.db.Query(`
		SELECT id,name,slug,abbreviation,description,visibility,status,theme_enabled,initiative_enabled,estimation_unit,board_lane_limit,created_at,updated_at,version
		  FROM projects
		 ORDER BY created_at DESC
		 LIMIT $1 OFFSET $2`, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Abbreviation, &p.Description, &p.Visibility, &p.Status, &p.ThemeEnabled, &p.InitiativeEnabled, &p.EstimationUnit, &p.BoardLaneLimit, &p.CreatedAt, &p.UpdatedAt, &p.Version); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	if ps == nil {
		ps = []Project{}
	}
	return ps, rows.Err()
}

// List returns projects the user has read access to: those the user is a member
// of (any role). Visibility no longer grants access on its own — a non-member
// must not see the project. Results are ordered newest-first and paginated.
func (r *ProjectRepo) List(userID string, page, size int) ([]Project, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT p.id,p.name,p.slug,p.abbreviation,p.description,p.visibility,p.status,p.theme_enabled,p.initiative_enabled,p.estimation_unit,p.board_lane_limit,p.created_at,p.updated_at,p.version
		  FROM projects p
		  JOIN memberships m ON m.project_id = p.id AND m.user_id = $1
		 ORDER BY p.created_at DESC
		 LIMIT $2 OFFSET $3`, userID, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Abbreviation, &p.Description, &p.Visibility, &p.Status, &p.ThemeEnabled, &p.InitiativeEnabled, &p.EstimationUnit, &p.BoardLaneLimit, &p.CreatedAt, &p.UpdatedAt, &p.Version); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	if ps == nil {
		ps = []Project{}
	}
	return ps, rows.Err()
}

func (r *ProjectRepo) Update(p *Project) error {
	res, err := r.db.Exec(`UPDATE projects SET name=$1,abbreviation=$2,description=$3,visibility=$4,status=$5,theme_enabled=$6,initiative_enabled=$7,estimation_unit=$8,board_lane_limit=$9,updated_at=$10,version=version+1 WHERE id=$11 AND version=$12`,
		p.Name, p.Abbreviation, p.Description, p.Visibility, p.Status, p.ThemeEnabled, p.InitiativeEnabled, p.EstimationUnit, p.BoardLaneLimit, p.UpdatedAt, p.ID, p.Version)
	return versionGuardedResult(res, err, &p.Version)
}

func (r *ProjectRepo) Delete(id string) error {
	stmts := []string{
		// Activity goes first, and unlike every other delete it is the whole
		// project's log rather than a per-task cascade: a deleted project has no
		// activity feed left to read its history in, so the entries go with it
		// (the one case where the log does not outlive its subject — elsewhere
		// deletion only unlinks; see migration 039). It must precede the tasks,
		// sprints and releases below because activity_entries now carries an FK
		// to each of them.
		`DELETE FROM activity_entries WHERE project_id=$1`,
		`DELETE FROM branch_references WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`,
		`DELETE FROM branch_references WHERE repository_id IN (SELECT id FROM repository_connections WHERE project_id=$1)`,
		`DELETE FROM page_task_references WHERE page_id IN (SELECT id FROM pages WHERE project_id=$1)`,
		`DELETE FROM task_relations WHERE source_task_id IN (SELECT id FROM tasks WHERE project_id=$1) OR target_task_id IN (SELECT id FROM tasks WHERE project_id=$1)`,
		`DELETE FROM task_attachments WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`,
		`DELETE FROM task_links WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`,
		`DELETE FROM task_comments WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`,
		`DELETE FROM tasks WHERE project_id=$1`,
		`DELETE FROM page_revisions WHERE page_id IN (SELECT id FROM pages WHERE project_id=$1)`,
		`DELETE FROM pages WHERE project_id=$1`,
		`DELETE FROM board_columns WHERE board_id IN (SELECT id FROM boards WHERE project_id=$1)`,
		`DELETE FROM boards WHERE project_id=$1`,
		`DELETE FROM sprints WHERE project_id=$1`,
		`DELETE FROM releases WHERE project_id=$1`,
		`DELETE FROM task_categories WHERE project_id=$1`,
		`DELETE FROM task_templates WHERE project_id=$1`,
		`DELETE FROM project_priorities WHERE project_id=$1`,
		`DELETE FROM repository_connections WHERE project_id=$1`,
		`DELETE FROM memberships WHERE project_id=$1`,
		`DELETE FROM projects WHERE id=$1`,
	}
	return shared.WithTx(r.db, func(tx *sql.Tx) error {
		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt, id); err != nil {
				return fmt.Errorf("project cascade delete: %w", err)
			}
		}
		return nil
	})
}

func scanProject(row *sql.Row) (*Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.Abbreviation, &p.Description, &p.Visibility, &p.Status, &p.ThemeEnabled, &p.InitiativeEnabled, &p.EstimationUnit, &p.BoardLaneLimit, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	return &p, nil
}

// taskColumns is the canonical SELECT list for task rows; always used with
// a "t" table alias (e.g. FROM tasks t).
