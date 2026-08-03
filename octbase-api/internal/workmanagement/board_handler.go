package workmanagement

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

func (h *Handler) CreateBoard(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	// Creating a board (with a default column template) is owner/admin-only.
	if _, ok := h.requirePermission(w, r, projectID, rbac.PermBoardCreate); !ok {
		return
	}
	var req struct {
		Name          string  `json:"name"`
		IsDefault     bool    `json:"isDefault"`
		MinColumns    *int    `json:"minColumns"`
		MaxColumns    *int    `json:"maxColumns"`
		IsSprintBoard bool    `json:"isSprintBoard"`
		SprintID      *string `json:"sprintId"`
		// Template seeds default columns ("kanban", "scrum", "none"); Locale
		// chooses the language of the seeded column names.
		Template string `json:"template"`
		Locale   string `json:"locale"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if !IsValidBoardTemplate(req.Template) {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "unknown board template", "template")
		return
	}
	minCols := DefaultBoardMinColumns
	if req.MinColumns != nil {
		minCols = *req.MinColumns
	}
	maxCols := DefaultBoardMaxColumns
	if req.MaxColumns != nil {
		maxCols = *req.MaxColumns
	}
	if err := ValidateLaneLimits(minCols, maxCols); err != nil {
		h.writeDomainError(w, err)
		return
	}
	sprintID, err := h.resolveBoardSprint(w, r, projectID, req.IsSprintBoard, req.SprintID)
	if err != nil {
		return
	}
	now := shared.Now()
	boardID := shared.NewUUID()
	cols := templateColumnsFor(boardID, req.Template, req.Locale, now)
	if len(cols) > maxCols {
		shared.WriteError(w, http.StatusUnprocessableEntity, "BOARD_MAX_LANES", "template has more lanes than the board's maximum")
		return
	}
	b := &Board{
		ID: boardID, ProjectID: projectID, Name: req.Name,
		IsDefault: req.IsDefault, MinColumns: minCols, MaxColumns: maxCols,
		IsSprintBoard: req.IsSprintBoard, SprintID: sprintID,
		CreatedAt: now, UpdatedAt: now,
	}
	// Create the board and seed its template columns atomically, so a failure
	// mid-seed never leaves a board with a partial set of default columns.
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.boards.CreateTx(tx, b); err != nil {
			return err
		}
		for _, c := range cols {
			if err := h.columns.CreateTx(tx, c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// The board and its lanes are already committed; if the enriching re-read
	// fails, still answer 201 carrying the lanes created above rather than a
	// false failure that would invite a duplicate-creating retry.
	if err := h.enrichBoard(r, b); err != nil {
		b.Columns = b.Columns[:0]
		for _, c := range cols {
			b.Columns = append(b.Columns, *c)
		}
	}
	shared.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) ListBoards(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	bs, err := h.boards.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, bs)
}

func (h *Handler) GetDefaultBoard(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	b, err := h.boards.FindDefault(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "no default board found")
		return
	}
	if err := h.enrichBoard(r, b); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) GetBoard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	if _, ok := h.memberGuard(w, r, b.ProjectID); !ok {
		return
	}
	if err := h.enrichBoard(r, b); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, b)
}

// enrichBoard loads a board's own columns, its cross-board read-only external
// columns (with each source column's tasks, gated by the caller's read access
// to the source project), and (when linked) the associated sprint so reads
// carry everything the client needs to render the board. A load failure is
// returned rather than swallowed: a board rendered without its columns would
// look empty to the client, which is worse than an honest error.
func (h *Handler) enrichBoard(r *http.Request, b *Board) error {
	cols, err := h.columns.ListByBoard(b.ID)
	if err != nil {
		return err
	}
	b.Columns = cols
	ext, err := h.extColumns.ListByBoard(b.ID)
	if err != nil {
		return err
	}
	b.ExternalColumns, err = h.populateExternalColumnTasks(r, ext)
	if err != nil {
		return err
	}
	if b.SprintID != nil && *b.SprintID != "" {
		sp, err := h.sprints.FindByID(*b.SprintID)
		if err != nil {
			return err
		}
		b.Sprint = sp
	}
	return nil
}

// canReadProject reports whether the caller may read the given project (any
// membership role, or SUPER_ADMIN). Used to gate cross-project linked columns.
func (h *Handler) canReadProject(r *http.Request, projectID string) bool {
	if shared.GetGlobalRole(r) == rbac.GlobalSuperAdmin {
		return true
	}
	_, err := shared.RequireProjectMember(h.db, projectID, shared.GetUserID(r))
	return err == nil
}

// populateExternalColumnTasks fills each linked column's tasks from its source
// column, but only for source projects the caller can read. Columns whose source
// project is not readable are returned with Accessible=false and no tasks, so a
// board viewer never sees task content from a project they lack access to.
func (h *Handler) populateExternalColumnTasks(r *http.Request, cols []BoardExternalColumn) ([]BoardExternalColumn, error) {
	// First pass: mark which columns the viewer may read and collect their source
	// column ids, so all their tasks can be fetched in one query (avoids N+1).
	var ids []string
	for i := range cols {
		if h.canReadProject(r, cols[i].SourceProjectID) {
			cols[i].Accessible = true
			ids = append(ids, cols[i].SourceColumnID)
		}
	}
	byColumn, err := h.tasks.ListByColumns(ids)
	if err != nil {
		return nil, err
	}
	// Second pass: place each readable column's non-archived tasks.
	for i := range cols {
		if !cols[i].Accessible {
			continue
		}
		out := []Task{}
		for _, t := range byColumn[cols[i].SourceColumnID] {
			if t.Status != StatusArchived {
				out = append(out, t)
			}
		}
		cols[i].Tasks = out
	}
	return cols, nil
}

// resolveBoardSprint validates an optional sprint link for a board. A non-empty
// sprintID must reference an existing sprint in the same project. It returns the
// sprint id to persist (nil when unset). On validation failure it writes the
// error response and returns a non-nil error so the caller can stop.
func (h *Handler) resolveBoardSprint(w http.ResponseWriter, r *http.Request, projectID string, isSprintBoard bool, sprintID *string) (*string, error) {
	if sprintID == nil || *sprintID == "" {
		return nil, nil
	}
	sp, err := h.sprints.FindByID(*sprintID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return nil, err
	}
	if sp == nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_NOT_FOUND", "linked sprint not found")
		return nil, errBoardSprintInvalid
	}
	if sp.ProjectID != projectID {
		shared.WriteError(w, http.StatusUnprocessableEntity, "BOARD_SPRINT_PROJECT_MISMATCH", "sprint belongs to a different project")
		return nil, errBoardSprintInvalid
	}
	return sprintID, nil
}

var errBoardSprintInvalid = errors.New("board sprint link invalid")

func (h *Handler) UpdateBoard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		MinColumns    *int    `json:"minColumns"`
		MaxColumns    *int    `json:"maxColumns"`
		IsSprintBoard *bool   `json:"isSprintBoard"`
		SprintID      *string `json:"sprintId"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the board has moved on.
		Version *int `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"name": true, "minColumns": true, "maxColumns": true,
		"isSprintBoard": true, "sprintId": true, "version": true,
	}, nil, &req) {
		return
	}
	if req.Name != nil {
		b.Name = *req.Name
	}
	if req.MinColumns != nil {
		b.MinColumns = *req.MinColumns
	}
	if req.MaxColumns != nil {
		b.MaxColumns = *req.MaxColumns
	}
	if err := ValidateLaneLimits(b.MinColumns, b.MaxColumns); err != nil {
		h.writeDomainError(w, err)
		return
	}
	if req.IsSprintBoard != nil {
		b.IsSprintBoard = *req.IsSprintBoard
	}
	if req.SprintID != nil {
		sprintID, err := h.resolveBoardSprint(w, r, b.ProjectID, b.IsSprintBoard, req.SprintID)
		if err != nil {
			return
		}
		b.SprintID = sprintID
	}
	if req.Version != nil {
		b.Version = *req.Version
	}
	b.UpdatedAt = shared.Now()
	if err := h.boards.Update(b); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	if err := h.enrichBoard(r, b); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) DeleteBoard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	reset, err := h.boards.Delete(id, shared.Now())
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Deleting a board detaches its cards, and a detached task is reset to
	// PLANNED (OCT-304). One replayable entry per task, like a lane retag.
	h.writeBulkActivity(b.ProjectID, reset, shared.GetUserID(r), "TASK_STATUS_CHANGED", map[string]any{"status": StatusPlanned})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddColumn(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Position int    `json:"position"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Status = strings.TrimSpace(req.Status)
	if req.Status == "" {
		req.Status = StatusPlanned
	}
	now := shared.Now()
	c := &BoardColumn{
		ID: shared.NewUUID(), BoardID: boardID, Name: req.Name,
		Status: req.Status, Position: req.Position, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.svc.AddBoardColumn(c); err != nil {
		var de *DomainError
		if errors.As(err, &de) {
			shared.WriteError(w, http.StatusConflict, de.Code, de.Message)
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) UpdateColumn(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	colID := chi.URLParam(r, "columnId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Status   *string `json:"status"`
		Position *int    `json:"position"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the column has moved on.
		Version *int `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"name": true, "status": true, "position": true, "version": true,
	}, nil, &req) {
		return
	}
	c, err := h.columns.FindByIDInBoard(colID, boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, c != nil, "COLUMN_NOT_FOUND", "column not found") {
		return
	}
	oldStatus := c.Status
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Status != nil {
		c.Status = strings.TrimSpace(*req.Status)
	}
	if req.Position != nil {
		c.Position = *req.Position
	}
	if req.Version != nil {
		c.Version = *req.Version
	}
	c.UpdatedAt = shared.Now()
	retagged, err := h.svc.UpdateBoardColumn(c, oldStatus)
	if err != nil {
		var de *DomainError
		if errors.As(err, &de) {
			shared.WriteError(w, http.StatusConflict, de.Code, de.Message)
			return
		}
		h.writeUpdateError(w, r, err)
		return
	}
	// One replayable entry per retagged task, like a single status change, so
	// re-statusing a lane stays visible in the Activity view and the sprint
	// burndown. No "from": the retag never read each task's old status.
	h.writeBulkActivity(b.ProjectID, retagged, shared.GetUserID(r), "TASK_STATUS_CHANGED", map[string]any{"status": c.Status})
	shared.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) DeleteColumn(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	colID := chi.URLParam(r, "columnId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	reset, err := h.svc.RemoveBoardColumn(boardID, colID)
	if err != nil {
		var de *DomainError
		if errors.As(err, &de) {
			status := http.StatusConflict
			if de.Code == "COLUMN_NOT_FOUND" {
				status = http.StatusNotFound
			}
			shared.WriteError(w, status, de.Code, de.Message)
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	// The lane's cards were detached and reset to PLANNED (OCT-304). One
	// replayable entry per task, exactly as a lane retag logs.
	h.writeBulkActivity(b.ProjectID, reset, shared.GetUserID(r), "TASK_STATUS_CHANGED", map[string]any{"status": StatusPlanned})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) MoveTask(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		TaskID        string `json:"taskId"`
		BoardColumnID string `json:"boardColumnId"`
		BoardRank     int    `json:"boardRank"`
		// Version, when sent, is the task version the client's move is based on;
		// the guarded update rejects the write with 409 if the task has moved on.
		Version *int `json:"version"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	// The membership guard above covers the board's project; the task comes from
	// the request body, so the lookup is scoped to that same project or a writer
	// in one project could move (and, via the response, read) any task in the
	// installation.
	t, err := h.tasks.FindByIDInProject(req.TaskID, b.ProjectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, t != nil, "TASK_NOT_FOUND", "task not found") {
		return
	}
	// The destination column is also body-supplied: the lookup is scoped to this
	// board so a task can't be parked on a column of an unrelated board.
	col, err := h.columns.FindByIDInBoard(req.BoardColumnID, boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, col != nil, "BOARD_COLUMN_NOT_FOUND", "board column not found") {
		return
	}
	if req.Version != nil {
		t.Version = *req.Version
	}
	// The board is a status control: parking a card in a lane is the claim that
	// the task is at that lane's stage, so the move carries the status change
	// server-side — every client (web drag, mobile move sheet, raw API) gets the
	// same lane↔status coupling instead of re-implementing it best-effort. The
	// ChangeStatus rules apply unchanged: a DONE/ARCHIVED task keeps its status
	// (it must be reopened first — reordering within its lane stays allowed),
	// and a card cannot land in a DONE lane while a child still holds BLOCKER
	// priority.
	statusChanged := col.Status != t.Status
	oldStatus := t.Status
	if statusChanged {
		if IsImmutable(t.Status) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_IMMUTABLE", "a DONE or ARCHIVED task keeps its status; reopen it before moving it to another lane")
			return
		}
		// Dragging a card into the Done lane completes the task, so it answers to
		// the same completion rule as the status route (completionGuard).
		if col.Status == StatusDone && !h.completionGuard(w, r, []string{t.ID}) {
			return
		}
	}
	// Moving a task onto a sprint board enrolls it in that board's sprint, so the
	// sprint's task count reflects board membership (the board *is* the sprint's
	// scope). Moving onto a regular board leaves any sprint link untouched.
	if b.IsSprintBoard && b.SprintID != nil && *b.SprintID != "" {
		// A sprint's scope is set while it is PLANNED; once it is ACTIVE the
		// committed scope is locked, so a task that is not already part of the
		// sprint cannot be added. Tasks already in the sprint may still be moved
		// between lanes (To Do → In Progress → Done).
		alreadyInSprint := t.SprintID != nil && *t.SprintID == *b.SprintID
		if !alreadyInSprint {
			sp, err := h.sprints.FindByID(*b.SprintID)
			if err != nil {
				shared.WriteServerError(w, r, err)
				return
			}
			if sp != nil && sp.Status == SprintStatusActive {
				shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_SCOPE_LOCKED", "a running sprint's scope is locked; plan tasks before starting the sprint")
				return
			}
		}
		t.SprintID = b.SprintID
	}
	t.BoardColumnID = &req.BoardColumnID
	t.BoardRank = req.BoardRank
	if statusChanged {
		t.Status = col.Status
	}
	t.UpdatedAt = shared.Now()
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.tasks.UpdateTx(tx, t); err != nil {
			return err
		}
		if err := h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_MOVED", nil); err != nil {
			return err
		}
		if statusChanged {
			// "from" is recorded so burndown reconstruction can replay status
			// transitions, exactly like a ChangeStatus entry.
			return h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_STATUS_CHANGED", map[string]any{"status": t.Status, "from": oldStatus})
		}
		return nil
	}); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.publishBoardEvent(t.ProjectID, t.ID, actorID, "TASK_MOVED")
	if statusChanged && h.notifier != nil {
		if t.ReporterID != nil {
			h.notifier.NotifyStatusChanged(t.ID, t.Title, t.ProjectID, *t.ReporterID, actorID, t.Status, StatusLabel(t.Status))
		}
		h.notifier.NotifyTaskChanged(t.ID, t.Title, t.ProjectID, t.ReporterID, t.AssigneeID, actorID,
			[]string{fmt.Sprintf("Status: %s → %s", StatusLabel(oldStatus), StatusLabel(t.Status))})
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) RemoveTaskFromBoard(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		TaskID string `json:"taskId"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	// The task comes from the request body; the lookup is scoped to the guarded
	// board's project so a writer in one project cannot mutate another's tasks.
	t, err := h.tasks.FindByIDInProject(req.TaskID, b.ProjectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, t != nil, "TASK_NOT_FOUND", "task not found") {
		return
	}
	t.BoardColumnID = nil
	// Taking a task off the board resets its status (OCT-304). The backlog is
	// "has no card", so a task that kept IN_REVIEW here would be listed as work
	// not started while claiming to be under review — the same contradiction
	// OCT-303 closed from the other end. DONE and ARCHIVED keep their status:
	// they are immutable, they are outside the backlog query anyway, and
	// resetting them would un-complete finished work.
	oldStatus := t.Status
	if !IsImmutable(t.Status) {
		t.Status = StatusPlanned
	}
	// Removing a task from a sprint board also removes it from that sprint (the
	// inverse of MoveTask's enrollment), keeping the sprint count in sync.
	if b.IsSprintBoard && t.SprintID != nil && b.SprintID != nil && *t.SprintID == *b.SprintID {
		t.SprintID = nil
	}
	t.UpdatedAt = shared.Now()
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.tasks.UpdateTx(tx, t); err != nil {
			return err
		}
		if err := h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_REMOVED_FROM_BOARD", nil); err != nil {
			return err
		}
		if t.Status == oldStatus {
			return nil
		}
		// Recorded as its own entry, with "from", so the Activity view and the
		// burndown see the same status change every other door writes.
		return h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_STATUS_CHANGED",
			map[string]any{"status": t.Status, "from": oldStatus})
	}); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.publishBoardEvent(t.ProjectID, t.ID, actorID, "TASK_REMOVED_FROM_BOARD")
	shared.WriteJSON(w, http.StatusOK, t)
}

// ListExternalColumns returns the cross-board read-only columns referenced by a
// board, with the source board and column resolved for display.
func (h *Handler) ListExternalColumns(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	if _, ok := h.memberGuard(w, r, b.ProjectID); !ok {
		return
	}
	cols, err := h.extColumns.ListByBoard(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	cols, err = h.populateExternalColumnTasks(r, cols)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, cols)
}

// AddExternalColumn links a column from another board as a read-only column on
// this board. The source board may live in a different project, as long as the
// caller has read access to that project.
func (h *Handler) AddExternalColumn(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	var req struct {
		SourceColumnID string `json:"sourceColumnId"`
		Position       int    `json:"position"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.SourceColumnID == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "source column is required", "sourceColumnId")
		return
	}
	srcCol, err := h.columns.FindByID(req.SourceColumnID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if srcCol == nil {
		shared.WriteError(w, http.StatusNotFound, "COLUMN_NOT_FOUND", "source column not found")
		return
	}
	if srcCol.BoardID == boardID {
		shared.WriteError(w, http.StatusUnprocessableEntity, "EXTERNAL_COLUMN_SAME_BOARD", "cannot add a column from the same board")
		return
	}
	srcBoard, err := h.boards.FindByID(srcCol.BoardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if srcBoard == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "source board not found")
		return
	}
	// The source board may belong to another project, but the caller must have
	// read access to that project to mirror one of its columns.
	if srcBoard.ProjectID != b.ProjectID && !h.canReadProject(r, srcBoard.ProjectID) {
		shared.WriteError(w, http.StatusForbidden, "EXTERNAL_COLUMN_NO_ACCESS", "you do not have read access to the source project")
		return
	}
	exists, err := h.extColumns.ExistsForBoard(boardID, req.SourceColumnID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if exists {
		shared.WriteError(w, http.StatusConflict, "EXTERNAL_COLUMN_DUPLICATE", "this column is already shown on the board")
		return
	}
	now := shared.Now()
	ec := &BoardExternalColumn{
		ID: shared.NewUUID(), BoardID: boardID, SourceColumnID: req.SourceColumnID,
		Position: req.Position, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.extColumns.Create(ec); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Re-read so the response carries the resolved source labels and tasks. The
	// link itself is already created, so a failed re-read still answers 201 with
	// the bare link rather than a false failure.
	if cols, err := h.extColumns.ListByBoard(boardID); err == nil {
		if cols, err = h.populateExternalColumnTasks(r, cols); err == nil {
			for _, c := range cols {
				if c.ID == ec.ID {
					shared.WriteJSON(w, http.StatusCreated, c)
					return
				}
			}
		}
	}
	shared.WriteJSON(w, http.StatusCreated, ec)
}

// DeleteExternalColumn removes a cross-board read-only column reference.
func (h *Handler) DeleteExternalColumn(w http.ResponseWriter, r *http.Request) {
	boardID := chi.URLParam(r, "boardId")
	extID := chi.URLParam(r, "externalColumnId")
	b, err := h.boards.FindByID(boardID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if b == nil {
		shared.WriteError(w, http.StatusNotFound, "BOARD_NOT_FOUND", "board not found")
		return
	}
	_, ok := h.writerGuard(w, r, b.ProjectID)
	if !ok {
		return
	}
	deleted, err := h.extColumns.Delete(boardID, extID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, deleted, "EXTERNAL_COLUMN_NOT_FOUND", "external column not found") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetBacklog(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ts, err := h.tasks.Backlog(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ts)
}
