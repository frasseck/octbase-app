package workmanagement

import (
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/docs"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler holds all workmanagement HTTP handlers.
type Handler struct {
	db          *sql.DB
	projects    *ProjectRepo
	tasks       *TaskRepo
	comments    *TaskCommentRepo
	links       *TaskLinkRepo
	attachments *TaskAttachmentRepo
	relations   *TaskRelationRepo
	boards      *BoardRepo
	columns     *BoardColumnRepo
	extColumns  *BoardExternalColumnRepo
	releases    *ReleaseRepo
	sprints     *SprintRepo
	categories  *TaskCategoryRepo
	templates   *TaskTemplateRepo
	priorities  *ProjectPriorityRepo
	svc         *Service
	activity    ActivityWriter
	notifier    Notifier
	events      BoardEventPublisher
	pages       PageSearcher
	pagePorter  PagePorter
	audit       *auditlog.Repo

	// storage holds uploaded attachment bytes. When nil, file upload/download is
	// disabled (only external-link attachments work). maxUploadBytes caps a
	// single upload.
	storage        *AttachmentStorage
	maxUploadBytes int64
	// maxUserStorageBytes caps the total stored size of all files a single
	// user has uploaded (OCTBASE_MAX_USER_STORAGE_MB); 0 or negative means
	// unlimited. See WithUserStorageQuota.
	maxUserStorageBytes int64

	// sweepMu/lastSweep throttle the lazy DONE-task archive sweep to at most
	// once per project per sweepThrottleInterval, so the hot task-list read
	// path isn't taxed with a write query on every request.
	sweepMu   sync.Mutex
	lastSweep map[string]time.Time
}

// WithAttachmentStorage enables file upload/download by attaching a storage
// backend and per-file size limit. Returns the handler for chaining.
func (h *Handler) WithAttachmentStorage(s *AttachmentStorage, maxUploadBytes int64) *Handler {
	h.storage = s
	h.maxUploadBytes = maxUploadBytes
	if h.svc != nil {
		h.svc.storage = s
	}
	return h
}

// WithUserStorageQuota sets the per-user total-storage cap for uploaded
// attachments. Values <= 0 disable the quota. A separate setter (not a
// WithAttachmentStorage parameter) so existing call sites and tests keep
// working unchanged.
func (h *Handler) WithUserStorageQuota(maxBytes int64) *Handler {
	h.maxUserStorageBytes = maxBytes
	return h
}

// ActivityWriter is a minimal interface for recording activity.
type ActivityWriter interface {
	Write(projectID, taskID, actorID, actType string, params map[string]any) error
}

type txActivityWriter interface {
	WriteTx(tx *sql.Tx, projectID, taskID, actorID, actType string, params map[string]any) error
}

// planningActivityWriter records an entry against a release or a sprint rather
// than a task. Declared here (like txActivityWriter) as an optional extension of
// ActivityWriter, so a writer that only knows Write still satisfies the handler
// — it just loses the reference, which costs the grey-out on delete, not the
// entry.
type planningActivityWriter interface {
	WriteRelease(projectID, releaseID, actorID, actType string, params map[string]any) error
	WriteSprint(projectID, sprintID, actorID, actType string, params map[string]any) error
}

// BoardEventPublisher pushes a project-scoped real-time event so that other
// users viewing the same project (e.g. a Kanban board) see a co-worker's change
// without a manual reload. It is satisfied structurally by *sse.Hub; declaring
// it here keeps workmanagement free of an sse import. When no publisher is
// wired (WithEventPublisher never called), publishing is a no-op.
type BoardEventPublisher interface {
	Publish(projectID string, payload map[string]any)
}

// WithEventPublisher wires the real-time publisher used to broadcast board
// changes. Optional (mirrors the WithAttachmentStorage setter pattern) so the
// many NewHandler call sites in tests keep working unchanged. Returns the
// handler for chaining.
func (h *Handler) WithEventPublisher(p BoardEventPublisher) *Handler {
	h.events = p
	return h
}

// Notifier is a minimal interface for delivering notifications.
type Notifier interface {
	NotifyTaskAssigned(taskID, taskTitle, projectID, assigneeID, actorID string)
	NotifyReviewerSet(taskID, taskTitle, projectID, reviewerID, actorID string)
	// newStatusLabel is a display label (StatusLabel), never the raw enum: the
	// message is stored as composed and rendered verbatim by the SPA.
	NotifyStatusChanged(taskID, taskTitle, projectID, reporterID, actorID, newStatusLabel string)
	NotifyTaskChanged(taskID, taskTitle, projectID string, reporterID, assigneeID *string, actorID string, changes []string)
	NotifyMentions(text, projectID, taskID, actorID string)
}

// PageSearcher abstracts page search queries for the unified search and dashboard.
type PageSearcher interface {
	SearchPages(userID, projectID, q string, limit int) ([]docs.PageSearchResult, error)
	GetRecentByAuthor(userID string, limit int) ([]docs.PageSearchResult, error)
}

// PagePorter abstracts the docs context for the whole-project export/import
// (implemented by docs.Porter). Like PageSearcher it is consumer-defined so
// workmanagement never reaches into docs tables. When nil, project export
// and import skip pages.
type PagePorter interface {
	ListByProject(projectID string) ([]docs.Page, error)
	SlugExists(projectID, slug string) (bool, error)
	CreateImportedPageTx(tx *sql.Tx, page *docs.Page, authorID string, taskIDs []string) error
}

// WithPagePorter wires the docs context into the project export/import.
// Returns the handler for chaining.
func (h *Handler) WithPagePorter(p PagePorter) *Handler {
	h.pagePorter = p
	return h
}

func NewHandler(
	db *sql.DB,
	projects *ProjectRepo,
	tasks *TaskRepo,
	comments *TaskCommentRepo,
	links *TaskLinkRepo,
	attachments *TaskAttachmentRepo,
	relations *TaskRelationRepo,
	boards *BoardRepo,
	columns *BoardColumnRepo,
	extColumns *BoardExternalColumnRepo,
	releases *ReleaseRepo,
	sprints *SprintRepo,
	categories *TaskCategoryRepo,
	templates *TaskTemplateRepo,
	svc *Service,
	activity ActivityWriter,
	notifier Notifier,
	pages PageSearcher,
	audit *auditlog.Repo,
) *Handler {
	return &Handler{
		db: db, projects: projects, tasks: tasks, comments: comments,
		links: links, attachments: attachments, relations: relations,
		boards: boards, columns: columns, extColumns: extColumns,
		releases: releases, sprints: sprints,
		categories: categories, templates: templates, svc: svc,
		priorities: NewProjectPriorityRepo(db),
		activity:   activity, notifier: notifier, pages: pages, audit: audit,
		lastSweep: make(map[string]time.Time),
	}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	// Projects
	r.Post("/api/v1/projects", h.CreateProject)
	r.Get("/api/v1/projects", h.ListProjects)
	r.Get("/api/v1/projects/{projectId}", h.GetProject)
	r.Patch("/api/v1/projects/{projectId}", h.UpdateProject)
	r.Post("/api/v1/projects/{projectId}/archive", h.ArchiveProject)
	r.Post("/api/v1/projects/{projectId}/unarchive", h.UnarchiveProject)
	r.Delete("/api/v1/projects/{projectId}", h.DeleteProject)

	// Task categories
	r.Post("/api/v1/projects/{projectId}/task-categories", h.CreateCategory)
	r.Get("/api/v1/projects/{projectId}/task-categories", h.ListCategories)
	r.Patch("/api/v1/task-categories/{categoryId}", h.UpdateCategory)
	r.Delete("/api/v1/task-categories/{categoryId}", h.DeleteCategory)

	// Custom project priorities (built-ins live in /meta/enums)
	r.Post("/api/v1/projects/{projectId}/task-priorities", h.CreatePriority)
	r.Get("/api/v1/projects/{projectId}/task-priorities", h.ListPriorities)
	r.Delete("/api/v1/task-priorities/{priorityId}", h.DeletePriority)

	// Task templates
	r.Post("/api/v1/projects/{projectId}/task-templates", h.CreateTemplate)
	r.Get("/api/v1/projects/{projectId}/task-templates", h.ListTemplates)
	r.Get("/api/v1/task-templates/{templateId}", h.GetTemplate)
	r.Patch("/api/v1/task-templates/{templateId}", h.UpdateTemplate)
	r.Delete("/api/v1/task-templates/{templateId}", h.DeleteTemplate)
	r.Post("/api/v1/task-templates/{templateId}/instantiate", h.InstantiateTemplate)

	// Tasks
	r.Post("/api/v1/projects/{projectId}/tasks", h.CreateTask)
	r.Get("/api/v1/projects/{projectId}/tasks", h.ListTasks)
	r.Get("/api/v1/tasks/{taskId}", h.GetTask)
	r.Patch("/api/v1/tasks/{taskId}", h.UpdateTask)
	r.Post("/api/v1/tasks/{taskId}/assign", h.AssignTask)
	r.Post("/api/v1/tasks/{taskId}/status", h.ChangeStatus)
	r.Post("/api/v1/tasks/{taskId}/priority", h.ChangePriority)
	r.Post("/api/v1/tasks/{taskId}/pin", h.SetTaskPin)
	r.Post("/api/v1/tasks/{taskId}/copy", h.CopyTask)
	r.Post("/api/v1/tasks/{taskId}/archive", h.ArchiveTask)
	r.Post("/api/v1/tasks/{taskId}/reopen", h.ReopenTask)
	r.Delete("/api/v1/tasks/{taskId}", h.DeleteTask)
	r.Post("/api/v1/projects/{projectId}/tasks/bulk", h.BulkUpdateTasks)

	// Task sub-resources
	r.Post("/api/v1/tasks/{taskId}/comments", h.AddComment)
	r.Get("/api/v1/tasks/{taskId}/comments", h.ListComments)
	r.Patch("/api/v1/tasks/{taskId}/comments/{commentId}", h.UpdateComment)
	r.Delete("/api/v1/tasks/{taskId}/comments/{commentId}", h.DeleteComment)
	r.Post("/api/v1/tasks/{taskId}/links", h.AddLink)
	r.Get("/api/v1/tasks/{taskId}/links", h.ListLinks)
	r.Delete("/api/v1/tasks/{taskId}/links/{linkId}", h.DeleteLink)
	r.Post("/api/v1/tasks/{taskId}/attachments", h.AddAttachment)
	r.Get("/api/v1/tasks/{taskId}/attachments", h.ListAttachments)
	r.Delete("/api/v1/tasks/{taskId}/attachments/{attachmentId}", h.DeleteAttachment)
	r.Post("/api/v1/tasks/{taskId}/relations", h.AddRelation)
	r.Get("/api/v1/tasks/{taskId}/relations", h.ListRelations)
	r.Delete("/api/v1/tasks/{taskId}/relations/{relationId}", h.DeleteRelation)
	r.Get("/api/v1/projects/{projectId}/relations", h.ListProjectRelations)

	// Boards
	r.Post("/api/v1/projects/{projectId}/boards", h.CreateBoard)
	r.Get("/api/v1/projects/{projectId}/boards", h.ListBoards)
	r.Get("/api/v1/projects/{projectId}/boards/default", h.GetDefaultBoard)
	r.Get("/api/v1/boards/{boardId}", h.GetBoard)
	r.Patch("/api/v1/boards/{boardId}", h.UpdateBoard)
	r.Delete("/api/v1/boards/{boardId}", h.DeleteBoard)
	r.Post("/api/v1/boards/{boardId}/columns", h.AddColumn)
	r.Patch("/api/v1/boards/{boardId}/columns/{columnId}", h.UpdateColumn)
	r.Delete("/api/v1/boards/{boardId}/columns/{columnId}", h.DeleteColumn)
	r.Post("/api/v1/boards/{boardId}/move-task", h.MoveTask)
	r.Post("/api/v1/boards/{boardId}/remove-task", h.RemoveTaskFromBoard)
	r.Get("/api/v1/projects/{projectId}/backlog", h.GetBacklog)

	// Cross-board read-only external columns
	r.Get("/api/v1/boards/{boardId}/external-columns", h.ListExternalColumns)
	r.Post("/api/v1/boards/{boardId}/external-columns", h.AddExternalColumn)
	r.Delete("/api/v1/boards/{boardId}/external-columns/{externalColumnId}", h.DeleteExternalColumn)

	// Releases (Releases)
	r.Post("/api/v1/projects/{projectId}/releases", h.CreateRelease)
	r.Get("/api/v1/projects/{projectId}/releases", h.ListReleases)
	r.Get("/api/v1/releases/{releaseId}", h.GetRelease)
	r.Patch("/api/v1/releases/{releaseId}", h.UpdateRelease)
	r.Post("/api/v1/releases/{releaseId}/close", h.CloseRelease)
	r.Post("/api/v1/releases/{releaseId}/reopen", h.ReopenRelease)
	r.Delete("/api/v1/releases/{releaseId}", h.DeleteRelease)

	// Sprints
	r.Post("/api/v1/projects/{projectId}/sprints", h.CreateSprint)
	r.Get("/api/v1/projects/{projectId}/sprints", h.ListSprints)
	r.Get("/api/v1/sprints/{sprintId}", h.GetSprint)
	r.Patch("/api/v1/sprints/{sprintId}", h.UpdateSprint)
	r.Post("/api/v1/sprints/{sprintId}/start", h.StartSprint)
	r.Post("/api/v1/sprints/{sprintId}/complete", h.CompleteSprint)
	r.Delete("/api/v1/sprints/{sprintId}", h.DeleteSprint)

	// Reports
	r.Get("/api/v1/sprints/{sprintId}/burndown", h.SprintBurndown)
	r.Get("/api/v1/projects/{projectId}/reports/velocity", h.ProjectVelocity)
	r.Get("/api/v1/projects/{projectId}/reports/statistics", h.ProjectStatistics)

	// Search & dashboard
	r.Get("/api/v1/projects/{projectId}/search/tasks", h.SearchTasks)
	r.Get("/api/v1/search", h.UnifiedSearch)
	r.Get("/api/v1/users/me/dashboard", h.GetDashboard)
}

// memberGuard checks that the acting user is a project member and returns
// their effective project role. Super Admin bypasses membership and is treated
// as PROJECT_ADMIN. Returns ("", false) on failure after writing the error.
func (h *Handler) memberGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	return shared.ProjectMemberGuard(h.db, w, r, projectID)
}

// requirePermission combines memberGuard with a rbac.HasPermission check for
// the given permission key. Non-view permissions are additionally rejected
// with 409 PROJECT_ARCHIVED when the project has been archived. Returns
// ("", false) on failure after writing the error response.
func (h *Handler) requirePermission(w http.ResponseWriter, r *http.Request, projectID, permission string) (string, bool) {
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return "", false
	}
	if !rbac.HasPermission(shared.GetGlobalRole(r), role, permission) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "missing permission: "+permission)
		return "", false
	}
	if permission != rbac.PermProjectView && permission != rbac.PermTaskView {
		if !shared.RequireProjectWritable(h.db, w, r, projectID) {
			return "", false
		}
	}
	return role, true
}

// writerGuard is memberGuard plus the writer-role check and the 409
// PROJECT_ARCHIVED freeze — the guard for every RequireWriter-level mutation,
// mirroring requirePermission's archived rule. Returns ("", false) on failure
// after writing the error response.
func (h *Handler) writerGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	return shared.ProjectWriterGuard(h.db, w, r, projectID)
}

// taskWriterGuard is taskGuard plus the writer checks of writerGuard, for
// mutations addressed by task ID.
func (h *Handler) taskWriterGuard(w http.ResponseWriter, r *http.Request, taskID string) (*Task, string, bool) {
	t, role, ok := h.taskGuard(w, r, taskID)
	if !ok {
		return nil, "", false
	}
	if !shared.RequireWriterOr403(w, role) {
		return nil, "", false
	}
	if !shared.RequireProjectWritable(h.db, w, r, t.ProjectID) {
		return nil, "", false
	}
	return t, role, true
}

func (h *Handler) taskGuard(w http.ResponseWriter, r *http.Request, taskID string) (*Task, string, bool) {
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return nil, "", false
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return nil, "", false
	}
	role, ok := h.memberGuard(w, r, t.ProjectID)
	if !ok {
		return nil, "", false
	}
	return t, role, true
}

// writeActivity records an activity entry and then broadcasts a project-scoped
// real-time event so other viewers of the project (Kanban board, task panel)
// refresh automatically. All non-transactional mutation call sites funnel
// through here rather than calling h.activity.Write directly, so the broadcast
// is guaranteed to accompany every logged change. The publish is best-effort
// and non-blocking (the hub drops frames for slow clients); a write error is
// still returned to the caller unchanged.
func (h *Handler) writeActivity(projectID, taskID, actorID, actType string, params map[string]any) error {
	err := h.activity.Write(projectID, taskID, actorID, actType, params)
	if err == nil {
		h.publishBoardEvent(projectID, taskID, actorID, actType)
	}
	return err
}

// writeReleaseActivity records a release-scoped entry, carrying the release id
// so DeleteRelease can unlink it later. The board event is published with an
// empty task id, exactly as the plain writeActivity call these replaced did.
func (h *Handler) writeReleaseActivity(projectID, releaseID, actorID, actType string, params map[string]any) error {
	var err error
	if aw, ok := h.activity.(planningActivityWriter); ok {
		err = aw.WriteRelease(projectID, releaseID, actorID, actType, params)
	} else {
		err = h.activity.Write(projectID, "", actorID, actType, params)
	}
	if err == nil {
		h.publishBoardEvent(projectID, "", actorID, actType)
	}
	return err
}

// writeSprintActivity is writeReleaseActivity's sprint counterpart.
func (h *Handler) writeSprintActivity(projectID, sprintID, actorID, actType string, params map[string]any) error {
	var err error
	if aw, ok := h.activity.(planningActivityWriter); ok {
		err = aw.WriteSprint(projectID, sprintID, actorID, actType, params)
	} else {
		err = h.activity.Write(projectID, "", actorID, actType, params)
	}
	if err == nil {
		h.publishBoardEvent(projectID, "", actorID, actType)
	}
	return err
}

// writeActivityTx records an activity entry inside an existing transaction. It
// deliberately does NOT publish a real-time event: the tx may still roll back
// after this returns, which would broadcast a change that never happened.
// Transactional callers publish via publishBoardEvent after a successful commit.
func (h *Handler) writeActivityTx(tx *sql.Tx, projectID, taskID, actorID, actType string, params map[string]any) error {
	if aw, ok := h.activity.(txActivityWriter); ok {
		return aw.WriteTx(tx, projectID, taskID, actorID, actType, params)
	}
	return h.activity.Write(projectID, taskID, actorID, actType, params)
}

// publishBoardEvent broadcasts a lightweight project-scoped change event. The
// frontend re-renders the open board (and the task panel when taskId matches),
// skipping the actor's own events. No-op when no publisher is wired.
func (h *Handler) publishBoardEvent(projectID, taskID, actorID, actType string) {
	if h.events == nil {
		return
	}
	payload := map[string]any{"type": "board.changed", "activityType": actType, "actorId": actorID}
	if taskID != "" {
		payload["taskId"] = taskID
	}
	h.events.Publish(projectID, payload)
}

// writeUpdateError writes the response for a failed version-guarded update;
// see shared.WriteUpdateError (409 VERSION_CONFLICT on conflict, 500 otherwise).
func (h *Handler) writeUpdateError(w http.ResponseWriter, r *http.Request, err error) {
	shared.WriteUpdateError(w, r, err)
}

// writeDomainError writes a 422 response when err is a *DomainError and returns
// true. Returns false when err is not a domain error so the caller can fall
// through to a 500 response.
func (h *Handler) writeDomainError(w http.ResponseWriter, err error) bool {
	var de *DomainError
	if errors.As(err, &de) {
		if de.Field != "" {
			shared.WriteValidationError(w, de.Code, de.Message, de.Field)
		} else {
			shared.WriteError(w, http.StatusUnprocessableEntity, de.Code, de.Message)
		}
		return true
	}
	return false
}
