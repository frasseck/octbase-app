package docs

import (
	"database/sql"

	"github.com/octbase/octbase-api/internal/shared"
)

// Porter bundles the page repos behind the narrow surface the whole-project
// export/import in workmanagement needs (its consumer-defined PagePorter
// interface). All docs-table SQL stays inside this context; workmanagement
// never touches page tables directly.
type Porter struct {
	pages     *PageRepo
	revisions *PageRevisionRepo
	refs      *PageReferenceRepo
}

// NewPorter returns a Porter over the given page repos.
func NewPorter(pages *PageRepo, revisions *PageRevisionRepo, refs *PageReferenceRepo) *Porter {
	return &Porter{pages: pages, revisions: revisions, refs: refs}
}

// ListByProject returns all pages of a project for export.
func (p *Porter) ListByProject(projectID string) ([]Page, error) {
	return p.pages.ListByProject(projectID)
}

// SlugExists reports whether a page slug is already taken in the project.
func (p *Porter) SlugExists(projectID, slug string) (bool, error) {
	return p.pages.SlugExistsForProject(projectID, slug, "")
}

// CreateImportedPageTx inserts an imported page together with its initial
// revision and its task references (already remapped and filtered to existing
// tasks by the caller) inside the import transaction. RenderedHTML is
// recomputed here so imported content passes through the same renderer and
// sanitizer as authored content.
func (p *Porter) CreateImportedPageTx(tx *sql.Tx, page *Page, authorID string, taskIDs []string) error {
	page.RenderedHTML = RenderAsciiDoc(page.Content)
	if err := p.pages.CreateTx(tx, page); err != nil {
		return err
	}
	rev := &PageRevision{
		ID:        shared.NewUUID(),
		PageID:    page.ID,
		Content:   page.Content,
		Message:   "Imported",
		AuthorID:  authorID,
		CreatedAt: page.CreatedAt,
	}
	if err := p.revisions.CreateTx(tx, rev); err != nil {
		return err
	}
	if len(taskIDs) > 0 {
		return p.refs.UpsertForPageTx(tx, page.ID, taskIDs, page.CreatedAt)
	}
	return nil
}

// Slugify converts a free-form title to a page slug (lowercase alphanumerics
// separated by single dashes). Exported for the project import, which must
// derive slugs the same way the page-create handler does.
func Slugify(s string) string { return slugify(s) }
