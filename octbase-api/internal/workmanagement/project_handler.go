package workmanagement

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

type createProjectReq struct {
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation"`
	Description  string `json:"description"`
	Visibility   string `json:"visibility"`
}

// CreateProject creates a new project and automatically adds the acting user
// as PROJECT_ADMIN in a single transaction. Requires ADMIN or SUPER_ADMIN.
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if !rbac.CanCreateProject(shared.GetGlobalRole(r)) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only admins can create projects")
		return
	}
	var req createProjectReq
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "project name must not be blank", "name")
		return
	}
	if req.Visibility == "" {
		req.Visibility = VisibilityPrivate
	}
	if !ValidVisibility(req.Visibility) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_VISIBILITY", "visibility must be PUBLIC or PRIVATE")
		return
	}
	actorID := shared.GetUserID(r)
	now := shared.Now()
	abbr := strings.ToUpper(strings.TrimSpace(req.Abbreviation))
	if abbr == "" {
		abbr = AbbreviationFromName(req.Name)
	} else if !ValidAbbreviation(abbr) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_ABBREVIATION", "abbreviation must be 1-10 letters or digits")
		return
	}
	p := &Project{
		ID:           shared.NewUUID(),
		Name:         req.Name,
		Slug:         SlugFromName(req.Name),
		Abbreviation: abbr,
		Description:  req.Description,
		Visibility:   req.Visibility,
		Status:       StatusActive,
		// Estimation is opt-in: a new project estimates nothing until an owner
		// picks a unit, so no estimate field appears anywhere in its UI.
		EstimationUnit: EstimationUnitNone,
		// Lanes are capped from the start rather than after a project has grown
		// big enough to hurt: the cap is invisible on a small board, so there is
		// no state in which the default is the wrong one to have had.
		BoardLaneLimit: DefaultBoardLaneLimit,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.projects.CreateTx(tx, p); err != nil {
			return err
		}
		return insertOwnerMembershipTx(tx, p.ID, actorID, now)
	})
	if err != nil {
		// projects.slug is UNIQUE and derived from the name, so creating a
		// project whose name slugifies to an existing slug is a client-resolvable
		// conflict, not a server error.
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "a project with this name already exists")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, p)
}

// insertOwnerMembershipTx makes userID the PROJECT_OWNER of a freshly created
// project — shared by CreateProject and the project-import-as-new path.
func insertOwnerMembershipTx(tx *sql.Tx, projectID, userID, now string) error {
	_, err := tx.Exec(
		`INSERT INTO memberships (id,project_id,user_id,role,assigned_by_user_id,created_at,updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		shared.NewUUID(), projectID, userID, rbac.ProjectOwner, userID, now, now,
	)
	return err
}

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	pg := shared.ParsePagination(r)
	userID := shared.GetUserID(r)
	var (
		ps  []Project
		err error
	)
	// Super Admin sees every project regardless of membership or visibility.
	if shared.GetGlobalRole(r) == rbac.GlobalSuperAdmin {
		ps, err = h.projects.ListAll(pg.Page, pg.Size)
	} else {
		ps, err = h.projects.List(userID, pg.Page, pg.Size)
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ps)
}

func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "projectId")
	p, err := h.projects.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	// Read access requires membership (any role) or SUPER_ADMIN. Visibility no
	// longer grants access on its own — a non-member must not see the project.
	if shared.GetGlobalRole(r) != rbac.GlobalSuperAdmin {
		_, err := shared.RequireProjectMember(h.db, id, shared.GetUserID(r))
		if errors.Is(err, shared.ErrNotMember) {
			shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
			return
		}
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	p, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	// Snapshot the settings we log about before anything mutates p.
	oldEstimationUnit := p.EstimationUnit
	var req struct {
		Name         *string `json:"name"`
		Abbreviation *string `json:"abbreviation"`
		Description  *string `json:"description"`
		Visibility   *string `json:"visibility"`
		// The optional THEME/INITIATIVE hierarchy levels (project settings).
		// Admin-only; switching one off is blocked while tasks or templates of
		// that type still exist.
		ThemeEnabled      *bool `json:"themeEnabled"`
		InitiativeEnabled *bool `json:"initiativeEnabled"`
		// EstimationUnit switches effort estimation on for the project
		// (NONE/POINTS/HOURS). Admin-only like the hierarchy levels above, but
		// unlike them it is never blocked by existing data: switching units is
		// deliberately non-destructive, so estimates in the unit that is no
		// longer active simply go dormant instead of stranding the write.
		EstimationUnit *string `json:"estimationUnit"`
		// BoardLaneLimit caps the cards a board lane draws at once (0 =
		// unlimited). Admin-only like the settings above and, like the
		// estimation unit, never blocked by existing data: it changes how many
		// cards are drawn, never which tasks are on the board.
		BoardLaneLimit *int `json:"boardLaneLimit"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the project has moved on.
		Version *int `json:"version"`
	}
	if !shared.DecodePatch(w, r,
		map[string]bool{
			"name": true, "abbreviation": true, "description": true,
			"visibility": true, "themeEnabled": true, "initiativeEnabled": true,
			"estimationUnit": true, "boardLaneLimit": true, "version": true,
		},
		map[string]string{
			"status": "status cannot be changed here; use POST /api/v1/projects/{projectId}/archive",
		}, &req) {
		return
	}
	if req.ThemeEnabled != nil || req.InitiativeEnabled != nil || req.EstimationUnit != nil || req.BoardLaneLimit != nil {
		if err := shared.RequireOwner(role); err != nil {
			shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
		disabling := map[string]bool{}
		if req.ThemeEnabled != nil && p.ThemeEnabled && !*req.ThemeEnabled {
			disabling[TaskTypeTheme] = true
		}
		if req.InitiativeEnabled != nil && p.InitiativeEnabled && !*req.InitiativeEnabled {
			disabling[TaskTypeInitiative] = true
		}
		for tt := range disabling {
			var inUse bool
			err := h.db.QueryRow(`SELECT EXISTS(
				SELECT 1 FROM tasks WHERE project_id=$1 AND task_type=$2
				UNION ALL
				SELECT 1 FROM task_templates WHERE project_id=$1 AND task_type=$2)`,
				projectID, tt).Scan(&inUse)
			if err != nil {
				shared.WriteServerError(w, r, err)
				return
			}
			if inUse {
				shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_TYPE_IN_USE", "tasks or templates of this type still exist — convert or delete them first")
				return
			}
		}
		if req.ThemeEnabled != nil {
			p.ThemeEnabled = *req.ThemeEnabled
		}
		if req.InitiativeEnabled != nil {
			p.InitiativeEnabled = *req.InitiativeEnabled
		}
		if req.EstimationUnit != nil {
			if !ValidEstimationUnit(*req.EstimationUnit) {
				shared.WriteError(w, http.StatusUnprocessableEntity, "ESTIMATION_UNIT_INVALID",
					"estimationUnit must be one of NONE, POINTS, HOURS")
				return
			}
			p.EstimationUnit = *req.EstimationUnit
		}
		if req.BoardLaneLimit != nil {
			if !ValidBoardLaneLimit(*req.BoardLaneLimit) {
				shared.WriteError(w, http.StatusUnprocessableEntity, "BOARD_LANE_LIMIT_INVALID",
					fmt.Sprintf("boardLaneLimit must be between 0 (unlimited) and %d", MaxBoardLaneLimit))
				return
			}
			p.BoardLaneLimit = *req.BoardLaneLimit
		}
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			shared.WriteValidationError(w, "VALIDATION_ERROR", "project name must not be blank", "name")
			return
		}
		p.Name = *req.Name
	}
	if req.Abbreviation != nil {
		abbr := strings.ToUpper(strings.TrimSpace(*req.Abbreviation))
		if abbr != "" && !ValidAbbreviation(abbr) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_ABBREVIATION", "abbreviation must be 1-10 letters or digits")
			return
		}
		p.Abbreviation = abbr
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if req.Visibility != nil {
		p.Visibility = *req.Visibility
	}
	if req.Version != nil {
		p.Version = *req.Version
	}
	p.UpdatedAt = shared.Now()
	if err := h.projects.Update(p); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	// Turning estimation on or off changes what every task in the project is
	// expected to carry, so it belongs in the project's Activity view. Logged
	// after the guarded update so a 409 never records a change that lost.
	if p.EstimationUnit != oldEstimationUnit {
		_ = h.writeActivity(p.ID, "", shared.GetUserID(r), "PROJECT_ESTIMATION_UNIT_CHANGED",
			map[string]any{"from": oldEstimationUnit, "to": p.EstimationUnit})
	}
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) ArchiveProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	p, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	p.Status = StatusArchived
	p.UpdatedAt = shared.Now()
	if err := h.projects.Update(p); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.audit.Write(shared.GetUserID(r), auditlog.ActionProjectArchived, "project", projectID,
		fmt.Sprintf(`{"name":%q}`, p.Name), "", "")
	shared.WriteJSON(w, http.StatusOK, p)
}

// UnarchiveProject puts an archived project back into ACTIVE. It is the way out
// of the door ArchiveProject closes: every write route in the project runs
// through writerGuard/requirePermission, which answer 409 PROJECT_ARCHIVED once
// the project is archived, so without this route an archive could only be
// undone in the database. Guarded like ArchiveProject — memberGuard (not
// writerGuard, which is exactly what the archived state blocks) plus
// owner-only — and idempotent on a project that is already active.
func (h *Handler) UnarchiveProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	p, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	p.Status = StatusActive
	p.UpdatedAt = shared.Now()
	if err := h.projects.Update(p); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.audit.Write(shared.GetUserID(r), auditlog.ActionProjectUnarchived, "project", projectID,
		fmt.Sprintf(`{"name":%q}`, p.Name), "", "")
	shared.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	p, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	// Collect uploaded-file storage keys before the cascade removes their rows.
	var fileKeys []string
	if h.storage != nil {
		fileKeys, _ = h.attachments.StorageKeysForProject(projectID)
	}
	if err := h.projects.Delete(projectID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for _, k := range fileKeys {
		_ = h.storage.Remove(k)
	}
	h.audit.Write(shared.GetUserID(r), auditlog.ActionProjectDeleted, "project", projectID,
		fmt.Sprintf(`{"name":%q}`, p.Name), "", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Task category handlers ----

func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.Color == "" {
		req.Color = "gray"
	}
	now := shared.Now()
	c := &TaskCategory{
		ID: shared.NewUUID(), ProjectID: projectID, Name: req.Name,
		Description: req.Description, Color: req.Color, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := h.categories.Create(c); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	cs, err := h.categories.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, cs)
}

func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "categoryId")
	c, err := h.categories.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if c == nil {
		shared.WriteError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "category not found")
		return
	}
	_, ok := h.writerGuard(w, r, c.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
		Version     *int    `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"name": true, "description": true, "color": true, "version": true,
	}, nil, &req) {
		return
	}
	// Mutate only the provided fields to avoid overwriting existing data.
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Description != nil {
		c.Description = *req.Description
	}
	if req.Color != nil {
		c.Color = *req.Color
	}
	// An optional client version makes the edit optimistic against the
	// client's own snapshot (see UpdateTask for the same pattern): the
	// guarded UPDATE only applies if the row still has this version, so an
	// edit based on a stale read gets 409 instead of silently overwriting a
	// concurrent editor's changes.
	if req.Version != nil {
		c.Version = *req.Version
	}
	c.UpdatedAt = shared.Now()
	if err := h.categories.Update(c); err != nil {
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "categoryId")
	c, err := h.categories.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if c == nil {
		shared.WriteError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "category not found")
		return
	}
	_, ok := h.writerGuard(w, r, c.ProjectID)
	if !ok {
		return
	}
	if err := h.categories.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Task template handlers ----

func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Name                string `json:"name"`
		TitleTemplate       string `json:"titleTemplate"`
		DescriptionTemplate string `json:"descriptionTemplate"`
		TaskType            string `json:"taskType"`
		Priority            string `json:"priority"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.TaskType == "" {
		req.TaskType = TaskTypeTask
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	// Templates instantiate without a parent, so SUBTASK (which requires one)
	// is not a valid template type either. Optional hierarchy levels must be
	// switched on in the project settings, and custom priorities must exist.
	if !ValidTaskType(req.TaskType) || req.TaskType == TaskTypeSubtask {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_TASK_TYPE", "unknown or unsupported task type for a template")
		return
	}
	project, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if project == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}
	if !project.TaskTypeEnabled(req.TaskType) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_TYPE_DISABLED", "this task type is not enabled in the project settings")
		return
	}
	if allowed, perr := h.priorityAllowed(projectID, req.Priority); perr != nil {
		shared.WriteServerError(w, r, perr)
		return
	} else if !allowed {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PRIORITY", "unknown priority value")
		return
	}
	now := shared.Now()
	t := &TaskTemplate{
		ID: shared.NewUUID(), ProjectID: projectID, Name: req.Name,
		TitleTemplate: req.TitleTemplate, DescriptionTemplate: req.DescriptionTemplate,
		TaskType: req.TaskType, Priority: req.Priority, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := h.templates.Create(t); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ts, err := h.templates.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ts)
}

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateId")
	t, err := h.templates.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "template not found")
		return
	}
	if _, ok := h.memberGuard(w, r, t.ProjectID); !ok {
		return
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateId")
	t, err := h.templates.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "template not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name                *string `json:"name"`
		TitleTemplate       *string `json:"titleTemplate"`
		DescriptionTemplate *string `json:"descriptionTemplate"`
		TaskType            *string `json:"taskType"`
		Priority            *string `json:"priority"`
		Version             *int    `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"name": true, "titleTemplate": true, "descriptionTemplate": true,
		"taskType": true, "priority": true, "version": true,
	}, nil, &req) {
		return
	}
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.TitleTemplate != nil {
		t.TitleTemplate = *req.TitleTemplate
	}
	if req.DescriptionTemplate != nil {
		t.DescriptionTemplate = *req.DescriptionTemplate
	}
	if req.TaskType != nil {
		if !ValidTaskType(*req.TaskType) || *req.TaskType == TaskTypeSubtask {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_TASK_TYPE", "unknown or unsupported task type for a template")
			return
		}
		project, perr := h.projects.FindByID(t.ProjectID)
		if perr != nil {
			shared.WriteServerError(w, r, perr)
			return
		}
		if project == nil || !project.TaskTypeEnabled(*req.TaskType) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_TYPE_DISABLED", "this task type is not enabled in the project settings")
			return
		}
		t.TaskType = *req.TaskType
	}
	if req.Priority != nil {
		if allowed, perr := h.priorityAllowed(t.ProjectID, *req.Priority); perr != nil {
			shared.WriteServerError(w, r, perr)
			return
		} else if !allowed {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PRIORITY", "unknown priority value")
			return
		}
		t.Priority = *req.Priority
	}
	// An optional client version makes the edit optimistic against the
	// client's own snapshot (see UpdateTask for the same pattern).
	if req.Version != nil {
		t.Version = *req.Version
	}
	t.UpdatedAt = shared.Now()
	if err := h.templates.Update(t); err != nil {
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateId")
	tmpl, err := h.templates.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if tmpl == nil {
		shared.WriteError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "template not found")
		return
	}
	_, ok := h.writerGuard(w, r, tmpl.ProjectID)
	if !ok {
		return
	}
	if err := h.templates.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InstantiateTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := chi.URLParam(r, "templateId")
	tmpl, err := h.templates.FindByID(templateID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if tmpl == nil {
		shared.WriteError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "template not found")
		return
	}
	_, ok := h.writerGuard(w, r, tmpl.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := shared.DecodeJSON(r, &req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
			return
		}
	}
	actorID := shared.GetUserID(r)
	task, err := h.svc.InstantiateTemplate(templateID, actorID, req.Title)
	if err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	_ = h.writeActivity(tmpl.ProjectID, task.ID, actorID, "TASK_CREATED", map[string]any{"title": task.Title})
	shared.WriteJSON(w, http.StatusCreated, task)
}
