package workmanagement

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// ProjectPriorityRepo handles the admin-defined additional priorities of a
// project (the built-in LOW/MEDIUM/HIGH/CRITICAL/BLOCKER set never hits this table).
type ProjectPriorityRepo struct{ db *sql.DB }

func NewProjectPriorityRepo(db *sql.DB) *ProjectPriorityRepo {
	return &ProjectPriorityRepo{db: db}
}

func (r *ProjectPriorityRepo) Create(p *ProjectPriority) error {
	_, err := r.db.Exec(`INSERT INTO project_priorities (id,project_id,name,rank,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		p.ID, p.ProjectID, p.Name, p.Rank, p.CreatedAt, p.UpdatedAt)
	return err
}

func (r *ProjectPriorityRepo) ListByProject(projectID string) ([]ProjectPriority, error) {
	rows, err := r.db.Query(`SELECT id,project_id,name,rank,created_at,updated_at FROM project_priorities WHERE project_id=$1 ORDER BY rank, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []ProjectPriority
	for rows.Next() {
		var p ProjectPriority
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Name, &p.Rank, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	if ps == nil {
		ps = []ProjectPriority{}
	}
	return ps, rows.Err()
}

func (r *ProjectPriorityRepo) FindByID(id string) (*ProjectPriority, error) {
	row := r.db.QueryRow(`SELECT id,project_id,name,rank,created_at,updated_at FROM project_priorities WHERE id=$1`, id)
	var p ProjectPriority
	err := row.Scan(&p.ID, &p.ProjectID, &p.Name, &p.Rank, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Exists reports whether the project already defines a custom priority with
// this name.
func (r *ProjectPriorityRepo) Exists(projectID, name string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_priorities WHERE project_id=$1 AND name=$2)`, projectID, name).Scan(&exists)
	return exists, err
}

func (r *ProjectPriorityRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM project_priorities WHERE id=$1`, id)
	return err
}

// priorityAllowed reports whether the priority value may be used in the
// project: the built-in set always, otherwise an existing custom project
// priority.
func (h *Handler) priorityAllowed(projectID, priority string) (bool, error) {
	if ValidPriority(priority) {
		return true, nil
	}
	return h.priorities.Exists(projectID, priority)
}

// ---- Project priority handlers ----

// ListPriorities returns the project's custom priorities (the built-in set is
// static and published via /meta/enums; clients concatenate).
func (h *Handler) ListPriorities(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ps, err := h.priorities.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ps)
}

// CreatePriority adds a custom priority to the project. Project admins only.
func (h *Handler) CreatePriority(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	var req struct {
		Name string `json:"name"`
		Rank int    `json:"rank"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	name := strings.ToUpper(strings.TrimSpace(req.Name))
	if !ValidPriorityName(name) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PRIORITY_NAME", "priority name must be 1-20 uppercase letters, digits or underscores, starting with a letter")
		return
	}
	if ValidPriority(name) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "PRIORITY_RESERVED", "priority name collides with a built-in priority")
		return
	}
	if exists, err := h.priorities.Exists(projectID, name); err != nil {
		shared.WriteServerError(w, r, err)
		return
	} else if exists {
		shared.WriteError(w, http.StatusConflict, "PRIORITY_EXISTS", "the project already defines this priority")
		return
	}
	now := shared.Now()
	p := &ProjectPriority{
		ID: shared.NewUUID(), ProjectID: projectID, Name: name, Rank: req.Rank,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.priorities.Create(p); err != nil {
		if shared.IsUniqueViolation(err) { // lost a create race after the Exists check
			shared.WriteError(w, http.StatusConflict, "PRIORITY_EXISTS", "the project already defines this priority")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, p)
}

// DeletePriority removes a custom priority. Project admins only; blocked with
// PRIORITY_IN_USE while any task or template of the project still carries it.
func (h *Handler) DeletePriority(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "priorityId")
	p, err := h.priorities.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if p == nil {
		shared.WriteError(w, http.StatusNotFound, "PRIORITY_NOT_FOUND", "priority not found")
		return
	}
	role, ok := h.memberGuard(w, r, p.ProjectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	var inUse bool
	err = h.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM tasks WHERE project_id=$1 AND priority=$2
		UNION ALL
		SELECT 1 FROM task_templates WHERE project_id=$1 AND priority=$2)`,
		p.ProjectID, p.Name).Scan(&inUse)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if inUse {
		shared.WriteError(w, http.StatusUnprocessableEntity, "PRIORITY_IN_USE", "tasks or templates still use this priority")
		return
	}
	if err := h.priorities.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
