package docs

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// ActivityWriter is a minimal interface for recording activity.
type ActivityWriter interface {
	Write(projectID, taskID, actorID, actType string, params map[string]any) error
}

type txActivityWriter interface {
	WriteTx(tx *sql.Tx, projectID, taskID, actorID, actType string, params map[string]any) error
}

// Handler holds documentation HTTP handlers.
type Handler struct {
	db        *sql.DB
	pages     *PageRepo
	revisions *PageRevisionRepo
	refs      *PageReferenceRepo
	activity  ActivityWriter
}

func NewHandler(db *sql.DB, pages *PageRepo, revisions *PageRevisionRepo, refs *PageReferenceRepo, activity ActivityWriter) *Handler {
	return &Handler{db: db, pages: pages, revisions: revisions, refs: refs, activity: activity}
}

// memberGuard checks project membership and returns the role. Writes an error
// and returns ("", false) on failure. SUPER_ADMIN bypasses membership and is
// treated as PROJECT_ADMIN, consistent with the workmanagement package.
func (h *Handler) memberGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	return shared.ProjectMemberGuard(h.db, w, r, projectID)
}

// writerGuard is memberGuard plus the writer-role check and the 409
// PROJECT_ARCHIVED freeze on archived projects, for page mutations.
func (h *Handler) writerGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	return shared.ProjectWriterGuard(h.db, w, r, projectID)
}

// pageMemberGuard resolves a page to its owning project and enforces membership
// on that project, for page-scoped read routes whose URL carries only the page
// ID. Writes 404 if the page does not exist and the standard membership error
// (403) otherwise; returns false when the caller must stop.
//
// It looks up only the project ID (not the whole page) so a revisions/references
// read does not pull the page's content and rendered_html just to authorize.
//
// A non-member gets 403 (not 404), matching taskGuard and the other entity-read
// guards; GetProject is the lone existence-hiding outlier (404). See the
// package note in handler.go's read handlers before changing this — flipping
// only pages would fragment the API's not-authorized convention.
func (h *Handler) pageMemberGuard(w http.ResponseWriter, r *http.Request, pageID string) bool {
	projectID, err := h.pages.ProjectIDForPage(pageID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if projectID == "" {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "Page not found")
		return false
	}
	_, ok := h.memberGuard(w, r, projectID)
	return ok
}

func (h *Handler) writeActivityTx(tx *sql.Tx, projectID, taskID, actorID, actType string, params map[string]any) error {
	if aw, ok := h.activity.(txActivityWriter); ok {
		return aw.WriteTx(tx, projectID, taskID, actorID, actType, params)
	}
	return h.activity.Write(projectID, taskID, actorID, actType, params)
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/api/v1/projects/{projectId}/pages", h.CreatePage)
	r.Get("/api/v1/projects/{projectId}/pages", h.ListPages)
	r.Get("/api/v1/projects/{projectId}/search/pages", h.SearchPages)
	r.Get("/api/v1/pages/{pageId}", h.GetPage)
	r.Patch("/api/v1/pages/{pageId}", h.UpdatePage)
	r.Post("/api/v1/pages/{pageId}/render-preview", h.RenderPreview)
	r.Post("/api/v1/pages/{pageId}/publish", h.PublishPage)
	r.Post("/api/v1/pages/{pageId}/archive", h.ArchivePage)
	r.Get("/api/v1/pages/{pageId}/revisions", h.ListRevisions)
	r.Get("/api/v1/pages/{pageId}/references", h.ListReferences)
	r.Post("/api/v1/pages/{pageId}/references/rebuild", h.RebuildReferences)
	r.Delete("/api/v1/pages/{pageId}", h.DeletePage)
}

func (h *Handler) CreatePage(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Title        string  `json:"title"`
		Slug         string  `json:"slug"`
		Content      string  `json:"content"`
		ParentPageID *string `json:"parentPageId"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	slug := req.Slug
	if slug == "" {
		slug = slugify(req.Title)
	}
	// Check slug uniqueness
	exists, err := h.pages.SlugExistsForProject(projectID, slug, "")
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if exists {
		shared.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "A page with this slug already exists in the project")
		return
	}
	now := shared.Now()
	p := &Page{
		ID: shared.NewUUID(), ProjectID: projectID, ParentPageID: req.ParentPageID,
		Title: req.Title, Slug: slug, Content: req.Content,
		RenderedHTML: RenderAsciiDoc(req.Content), Status: StatusDraft,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.pages.CreateTx(tx, p); err != nil {
			return err
		}
		return h.writeActivityTx(tx, p.ProjectID, "", actorID, "PAGE_CREATED", map[string]any{"title": p.Title})
	}); err != nil {
		// The pre-check above is only advisory under concurrency; the unique
		// index idx_pages_project_slug enforces the rule, so a concurrent create
		// with the same slug surfaces here.
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "A page with this slug already exists in the project")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ps, err := h.pages.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ps)
}

func (h *Handler) GetPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "Page not found")
		return
	}
	if _, ok := h.memberGuard(w, r, p.ProjectID); !ok {
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "page not found")
		return
	}
	_, ok := h.writerGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	if p.Status == StatusArchived {
		shared.WriteError(w, http.StatusUnprocessableEntity, "PAGE_ARCHIVED", "cannot modify an archived page")
		return
	}
	var req struct {
		Title   *string `json:"title"`
		Content *string `json:"content"`
		Slug    *string `json:"slug"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the page has moved on.
		Version *int `json:"version"`
	}
	if !shared.DecodePatch(w, r,
		map[string]bool{"title": true, "content": true, "slug": true, "version": true},
		map[string]string{
			"status": "status cannot be changed here; use POST /api/v1/pages/{pageId}/publish or …/archive",
		}, &req) {
		return
	}
	if req.Title != nil {
		p.Title = *req.Title
	}
	if req.Content != nil {
		p.Content = *req.Content
		p.RenderedHTML = RenderAsciiDoc(p.Content)
	}
	if req.Slug != nil {
		exists, err := h.pages.SlugExistsForProject(p.ProjectID, *req.Slug, p.ID)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if exists {
			shared.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "Slug already in use")
			return
		}
		p.Slug = *req.Slug
	}
	if req.Version != nil {
		p.Version = *req.Version
	}
	p.UpdatedAt = shared.Now()
	if err := h.pages.Update(p); err != nil {
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "Slug already in use")
			return
		}
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) RenderPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	html := RenderAsciiDoc(req.Content)
	shared.WriteJSON(w, http.StatusOK, map[string]string{"html": html})
}

func (h *Handler) PublishPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "page not found")
		return
	}
	_, ok := h.writerGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	_ = shared.DecodeJSON(r, &req)

	p.Status = StatusPublished
	p.RenderedHTML = RenderAsciiDoc(p.Content)
	p.UpdatedAt = shared.Now()
	actorID := shared.GetUserID(r)
	taskIDs := ExtractTaskReferences(p.Content)
	rev := &PageRevision{
		ID: shared.NewUUID(), PageID: p.ID, Content: p.Content,
		Message: req.Message, AuthorID: actorID, CreatedAt: p.UpdatedAt,
	}
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.pages.UpdateTx(tx, p); err != nil {
			return err
		}
		if err := h.revisions.CreateTx(tx, rev); err != nil {
			return err
		}
		if err := h.refs.UpsertForPageTx(tx, p.ID, taskIDs, p.UpdatedAt); err != nil {
			return err
		}
		return h.writeActivityTx(tx, p.ProjectID, "", actorID, "PAGE_PUBLISHED", map[string]any{"title": p.Title})
	}); err != nil {
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) ArchivePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "page not found")
		return
	}
	_, ok := h.writerGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	p.Status = StatusArchived
	p.UpdatedAt = shared.Now()
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.pages.UpdateTx(tx, p); err != nil {
			return err
		}
		return h.writeActivityTx(tx, p.ProjectID, "", actorID, "PAGE_ARCHIVED", map[string]any{"title": p.Title})
	}); err != nil {
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	pageID := chi.URLParam(r, "pageId")
	if !h.pageMemberGuard(w, r, pageID) {
		return
	}
	revs, err := h.revisions.ListByPage(pageID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, revs)
}

func (h *Handler) ListReferences(w http.ResponseWriter, r *http.Request) {
	pageID := chi.URLParam(r, "pageId")
	if !h.pageMemberGuard(w, r, pageID) {
		return
	}
	refs, err := h.refs.ListByPage(pageID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, refs)
}

func (h *Handler) RebuildReferences(w http.ResponseWriter, r *http.Request) {
	pageID := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(pageID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "Page not found")
		return
	}
	_, ok := h.writerGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	taskIDs := ExtractTaskReferences(p.Content)
	now := shared.Now()
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		return h.refs.UpsertForPageTx(tx, pageID, taskIDs, now)
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	refs, _ := h.refs.ListByPage(pageID)
	shared.WriteJSON(w, http.StatusOK, refs)
}

// minSearchQueryLen is the shortest query the trigram-indexed page search
// accepts. It mirrors the unified search's threshold in workmanagement; the
// constant is duplicated rather than shared because the two bounded contexts do
// not import each other.
const minSearchQueryLen = 3

func (h *Handler) SearchPages(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 500 {
		shared.WriteError(w, http.StatusBadRequest, "QUERY_TOO_LONG", "search query must not exceed 500 characters")
		return
	}
	if len(q) < minSearchQueryLen {
		// Same threshold and same no-error shape as the unified search: pg_trgm
		// extracts no trigram from a pattern shorter than three characters, so a
		// 1- or 2-character `%q%` cannot use the GIN indexes from migration 022 and
		// would sequentially scan every page. Answer an empty result set instead.
		shared.WriteJSON(w, http.StatusOK, []Page{})
		return
	}
	pg := shared.ParsePagination(r)
	ps, err := h.pages.SearchByTitle(projectID, q, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ps)
}

func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "pageId")
	p, err := h.pages.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PAGE_NOT_FOUND", "page not found")
		return
	}
	_, ok := h.writerGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	if err := h.pages.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.activity.Write(p.ProjectID, "", actorID, "PAGE_DELETED", map[string]any{"title": p.Title})
	w.WriteHeader(http.StatusNoContent)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var out []rune
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	result := strings.Trim(string(out), "-")
	// Collapse multiple dashes
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return result
}
