package docs

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/octbase/octbase-api/internal/shared"
)

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// PageRepo handles page persistence.
type PageRepo struct{ db *sql.DB }

func NewPageRepo(db *sql.DB) *PageRepo { return &PageRepo{db: db} }

func (r *PageRepo) Create(p *Page) error {
	return r.create(r.db, p)
}

// CreateTx inserts a page inside an existing transaction.
func (r *PageRepo) CreateTx(tx *sql.Tx, p *Page) error {
	return r.create(tx, p)
}

func (r *PageRepo) create(db execer, p *Page) error {
	_, err := db.Exec(`INSERT INTO pages (id,project_id,parent_page_id,title,slug,content,rendered_html,status,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.ID, p.ProjectID, p.ParentPageID, p.Title, p.Slug, p.Content, p.RenderedHTML, p.Status, p.CreatedAt, p.UpdatedAt, p.Version)
	return err
}

func (r *PageRepo) FindByID(id string) (*Page, error) {
	row := r.db.QueryRow(`SELECT id,project_id,parent_page_id,title,slug,content,rendered_html,status,created_at,updated_at,version FROM pages WHERE id=$1`, id)
	return scanPage(row)
}

// ProjectIDForPage returns the owning project's ID for a page, or "" if the
// page does not exist. Used by page-scoped authorization checks that need only
// the project to run ProjectMemberGuard — avoids loading the page's content and
// rendered_html (large TEXT columns) just to read a foreign key.
func (r *PageRepo) ProjectIDForPage(pageID string) (string, error) {
	var projectID string
	err := r.db.QueryRow(`SELECT project_id FROM pages WHERE id=$1`, pageID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return projectID, err
}

func (r *PageRepo) ListByProject(projectID string) ([]Page, error) {
	rows, err := r.db.Query(`SELECT id,project_id,parent_page_id,title,slug,content,rendered_html,status,created_at,updated_at,version FROM pages WHERE project_id=$1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []Page
	for rows.Next() {
		p, err := scanPageRow(rows)
		if err != nil {
			return nil, err
		}
		ps = append(ps, *p)
	}
	if ps == nil {
		ps = []Page{}
	}
	return ps, rows.Err()
}

func (r *PageRepo) Delete(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	stmts := []string{
		`DELETE FROM page_task_references WHERE page_id=$1`,
		`DELETE FROM page_revisions WHERE page_id=$1`,
		`DELETE FROM pages WHERE id=$1`,
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PageRepo) Update(p *Page) error {
	return r.update(r.db, p)
}

// UpdateTx updates a page inside an existing transaction.
func (r *PageRepo) UpdateTx(tx *sql.Tx, p *Page) error {
	return r.update(tx, p)
}

func (r *PageRepo) update(db execer, p *Page) error {
	// The version guard makes the read-modify-write optimistic: the UPDATE only
	// applies if the row still has the version the caller's snapshot was based
	// on, so a concurrent editor's write is never silently overwritten.
	res, err := db.Exec(`UPDATE pages SET title=$1,slug=$2,content=$3,rendered_html=$4,status=$5,updated_at=$6,version=version+1 WHERE id=$7 AND version=$8`,
		p.Title, p.Slug, p.Content, p.RenderedHTML, p.Status, p.UpdatedAt, p.ID, p.Version)
	return shared.VersionGuardedResult(res, err, &p.Version)
}

func (r *PageRepo) SlugExistsForProject(projectID, slug, excludeID string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM pages WHERE project_id=$1 AND slug=$2 AND id!=$3`, projectID, slug, excludeID).Scan(&count)
	return count > 0, err
}

func (r *PageRepo) SearchByTitle(projectID, q string, page, size int) ([]Page, error) {
	likeQ := "%" + shared.EscapeLike(q) + "%"
	rows, err := r.db.Query(`SELECT id,project_id,parent_page_id,title,slug,content,rendered_html,status,created_at,updated_at,version FROM pages WHERE project_id=$1 AND (title ILIKE $2 OR content ILIKE $3) ORDER BY created_at DESC LIMIT $4 OFFSET $5`,
		projectID, likeQ, likeQ, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []Page
	for rows.Next() {
		p, err := scanPageRow(rows)
		if err != nil {
			return nil, err
		}
		ps = append(ps, *p)
	}
	if ps == nil {
		ps = []Page{}
	}
	return ps, rows.Err()
}

func scanPage(row *sql.Row) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.ProjectID, &p.ParentPageID, &p.Title, &p.Slug, &p.Content, &p.RenderedHTML, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan page: %w", err)
	}
	// Render-on-read: always recompute RenderedHTML from the authored Content
	// under the current renderer. This makes existing pages (whose stored
	// rendered_html was produced by the old, partly-incorrect renderer) display
	// correctly without a data migration, and is the single sanitization source
	// of truth. The stored rendered_html column is kept in sync on write but is
	// not trusted on read.
	p.RenderedHTML = RenderAsciiDoc(p.Content)
	return &p, nil
}

func scanPageRow(rows *sql.Rows) (*Page, error) {
	var p Page
	err := rows.Scan(&p.ID, &p.ProjectID, &p.ParentPageID, &p.Title, &p.Slug, &p.Content, &p.RenderedHTML, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if err != nil {
		return nil, err
	}
	// Render-on-read (see scanPage).
	p.RenderedHTML = RenderAsciiDoc(p.Content)
	return &p, nil
}

// PageRevisionRepo handles page revision persistence.
type PageRevisionRepo struct{ db *sql.DB }

func NewPageRevisionRepo(db *sql.DB) *PageRevisionRepo { return &PageRevisionRepo{db: db} }

// CreateTx inserts a page revision inside an existing transaction.
func (r *PageRevisionRepo) CreateTx(tx *sql.Tx, rev *PageRevision) error {
	return r.create(tx, rev)
}

func (r *PageRevisionRepo) create(db execer, rev *PageRevision) error {
	_, err := db.Exec(`INSERT INTO page_revisions (id,page_id,content,message,author_id,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		rev.ID, rev.PageID, rev.Content, rev.Message, rev.AuthorID, rev.CreatedAt)
	return err
}

func (r *PageRevisionRepo) ListByPage(pageID string) ([]PageRevision, error) {
	rows, err := r.db.Query(`SELECT id,page_id,content,message,author_id,created_at FROM page_revisions WHERE page_id=$1 ORDER BY created_at DESC`, pageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var revs []PageRevision
	for rows.Next() {
		var rev PageRevision
		if err := rows.Scan(&rev.ID, &rev.PageID, &rev.Content, &rev.Message, &rev.AuthorID, &rev.CreatedAt); err != nil {
			return nil, err
		}
		revs = append(revs, rev)
	}
	if revs == nil {
		revs = []PageRevision{}
	}
	return revs, rows.Err()
}

// PageReferenceRepo handles page task reference persistence.
type PageReferenceRepo struct{ db *sql.DB }

func NewPageReferenceRepo(db *sql.DB) *PageReferenceRepo { return &PageReferenceRepo{db: db} }

// UpsertForPageTx replaces page references inside an existing transaction.
func (r *PageReferenceRepo) UpsertForPageTx(tx *sql.Tx, pageID string, taskIDs []string, now string) error {
	return r.upsertForPage(tx, pageID, taskIDs, now)
}

func (r *PageReferenceRepo) upsertForPage(db execer, pageID string, taskIDs []string, now string) error {
	if _, err := db.Exec(`DELETE FROM page_task_references WHERE page_id=$1`, pageID); err != nil {
		return err
	}
	for _, taskID := range taskIDs {
		id := shared.NewUUID()
		if _, err := db.Exec(`INSERT INTO page_task_references (id,page_id,task_id,created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			id, pageID, taskID, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *PageReferenceRepo) ListByPage(pageID string) ([]PageReference, error) {
	rows, err := r.db.Query(`SELECT id,page_id,task_id,created_at FROM page_task_references WHERE page_id=$1 ORDER BY created_at`, pageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var refs []PageReference
	for rows.Next() {
		var ref PageReference
		if err := rows.Scan(&ref.ID, &ref.PageID, &ref.TaskID, &ref.CreatedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if refs == nil {
		refs = []PageReference{}
	}
	return refs, rows.Err()
}

// SearchPages finds published pages matching q, visible to userID.
// If projectID is non-empty, results are scoped to that project.
func (r *PageRepo) SearchPages(userID, projectID, q string, limit int) ([]PageSearchResult, error) {
	like := "%" + shared.EscapeLike(q) + "%"
	var (
		rows *sql.Rows
		err  error
	)
	if projectID != "" {
		rows, err = r.db.Query(`
			SELECT p.id, p.project_id, p.title, p.slug, pr.name
			  FROM pages p
			  JOIN projects pr ON pr.id = p.project_id
			  JOIN memberships m ON m.project_id = p.project_id AND m.user_id = $1
			 WHERE p.project_id = $2 AND p.title ILIKE $3 AND p.status != 'ARCHIVED'
			 LIMIT $4`, userID, projectID, like, limit)
	} else {
		rows, err = r.db.Query(`
			SELECT p.id, p.project_id, p.title, p.slug, pr.name
			  FROM pages p
			  JOIN projects pr ON pr.id = p.project_id
			  JOIN memberships m ON m.project_id = p.project_id AND m.user_id = $1
			 WHERE p.title ILIKE $2 AND p.status != 'ARCHIVED'
			 LIMIT $3`, userID, like, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPageSearchResults(rows)
}

// GetRecentByAuthor returns the most recently revised pages authored by userID,
// deduplicated by page so multiple revisions on the same page count as one result.
func (r *PageRepo) GetRecentByAuthor(userID string, limit int) ([]PageSearchResult, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT ON (pr.id)
		       pr.id, pr.project_id, pr.title, pr.slug, p.name
		  FROM pages pr
		  JOIN projects p ON p.id = pr.project_id
		  JOIN page_revisions rev ON rev.page_id = pr.id AND rev.author_id = $1
		  JOIN memberships m ON m.project_id = pr.project_id AND m.user_id = $1
		 WHERE pr.status = 'PUBLISHED'
		 ORDER BY pr.id, rev.created_at DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPageSearchResults(rows)
}

func scanPageSearchResults(rows *sql.Rows) ([]PageSearchResult, error) {
	var results []PageSearchResult
	for rows.Next() {
		var r PageSearchResult
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Title, &r.Slug, &r.ProjectName); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if results == nil {
		results = []PageSearchResult{}
	}
	return results, rows.Err()
}
