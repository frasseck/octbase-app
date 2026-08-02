package workmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Sprint reports: burndown and velocity. Both are server-computed projections
// on read — no new tables, no snapshot jobs. A sprint is small (bounded by
// board scope), and the activity query is covered by the
// (project_id, created_at) and task_id indexes on activity_entries.

// sprintDateLayout is the date-only format sprints store (start/end dates).
const sprintDateLayout = "2006-01-02"

// velocityDefaultN / velocityMaxN bound how many completed sprints the
// velocity report returns.
const (
	velocityDefaultN = 6
	velocityMaxN     = 20
)

// burndownUnit* are the accepted ?unit= values. "tasks" is the default and
// keeps the report counting tickets; "points"/"hours" burn down effort and
// must match the project's active estimation unit.
const (
	burndownUnitTasks  = "tasks"
	burndownUnitPoints = "points"
	burndownUnitHours  = "hours"
)

type burndownPoint struct {
	Date string `json:"date"`
	// Remaining is null for days of an ACTIVE sprint that lie in the future.
	// It is a float because the same field carries effort (hours can be
	// fractional); whole values marshal without a decimal point, so the
	// task-counting series is byte-identical to what it always was.
	Remaining *float64 `json:"remaining"`
	Ideal     float64  `json:"ideal"`
}

type burndownResponse struct {
	SprintID  string  `json:"sprintId"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	Committed float64 `json:"committed"`
	// Unit and Unestimated are present only for an effort burndown, so a
	// ?unit=tasks (or no-param) response stays byte-identical to the one
	// clients have consumed since the report shipped. For an effort report the
	// unit is echoed so a chart can never mislabel its axis, and Unestimated
	// says how many committed tasks carry no estimate and therefore weigh
	// nothing — the difference between "nothing left to do" and "nobody
	// estimated it".
	Unit        string          `json:"unit,omitempty"`
	Unestimated *int            `json:"unestimated,omitempty"`
	Points      []burndownPoint `json:"points"`
}

// statusTransition is one observed change of a task's done-ness over time.
type statusTransition struct {
	at   time.Time
	done bool
}

// SprintBurndown handles GET /api/v1/sprints/{sprintId}/burndown.
//
// remaining(day) = committed tasks not yet DONE/ARCHIVED at the end of that
// calendar day (UTC), reconstructed from activity_entries status transitions
// (TASK_STATUS_CHANGED / TASK_ARCHIVED / TASK_REOPENED / TASK_AUTO_ARCHIVED
// payloads carry the new status), with tasks.done_at as a fallback signal for
// transitions that never produced a status-bearing activity entry (bulk and
// webhook closes recorded before this release, or history predating it) — so
// pre-release history renders approximately. The reconstruction also depends
// on activity retention (OCTBASE_ACTIVITY_RETENTION_DAYS, default 365)
// covering the sprint window, which it comfortably does for any sane sprint
// length.
func (h *Handler) SprintBurndown(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	if _, ok := h.memberGuard(w, r, s.ProjectID); !ok {
		return
	}
	if s.Status == SprintStatusPlanned || s.StartDate == nil || s.EndDate == nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_NOT_STARTED", "burndown is available once the sprint has started")
		return
	}
	start, err1 := time.ParseInLocation(sprintDateLayout, *s.StartDate, time.UTC)
	end, err2 := time.ParseInLocation(sprintDateLayout, *s.EndDate, time.UTC)
	if err1 != nil || err2 != nil || end.Before(start) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_NOT_STARTED", "sprint has no usable start/end dates")
		return
	}

	unit, ok := h.burndownUnit(w, r, s.ProjectID)
	if !ok {
		return
	}

	// Committed scope. ACTIVE: the live board membership (same definition as
	// the sprint card count). COMPLETED: the count snapshotted at completion —
	// the board is gone and unfinished tasks were unlinked, so only the
	// finished tasks (still carrying sprint_id) can be replayed; the snapshot
	// keeps the denominator honest.
	var taskIDs []string
	committed := 0.0
	switch s.Status {
	case SprintStatusActive:
		taskIDs, err = h.sprints.ScopeTaskIDs(s.ID)
		committed = float64(len(taskIDs))
	default: // COMPLETED
		taskIDs, err = h.sprints.LinkedTaskIDs(s.ID)
		committed = float64(s.CommittedCount)
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// The only thing an effort burndown changes is what a task weighs: 1 for a
	// task count, its estimate for effort (0 when unestimated). The
	// reconstruction below is shared verbatim by both.
	weights, unestimated, err := h.burndownWeights(taskIDs, unit)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if unit != burndownUnitTasks {
		committed = 0
		for _, id := range taskIDs {
			committed += weights[id]
		}
		// A sprint completed before the effort snapshot existed (or under a
		// different unit) has no usable snapshot; its live sum over the
		// still-linked finished tasks is the best available and is what the
		// count path already falls back to in spirit.
		if s.Status != SprintStatusActive && s.EstimateUnit != nil && *s.EstimateUnit == estimationUnitFor(unit) && s.CommittedEstimate != nil {
			committed = *s.CommittedEstimate
		}
	}

	transitions, err := h.loadStatusTransitions(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// One point per calendar day, evaluated at the end of that day (UTC).
	days := int(end.Sub(start).Hours()/24) + 1
	now := time.Now().UTC()
	points := make([]burndownPoint, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		endOfDay := day.AddDate(0, 0, 1)
		ideal := committed * (1 - float64(i+1)/float64(days))
		p := burndownPoint{
			Date:  day.Format(sprintDateLayout),
			Ideal: math.Round(ideal*100) / 100,
		}
		// For a running sprint, days that haven't begun have no actual value;
		// the current (partial) day is evaluated live at "now". A completed
		// sprint's state is frozen, so every day is computed at its end.
		evalAt := endOfDay
		if s.Status == SprintStatusActive && endOfDay.After(now) {
			evalAt = now
		}
		if s.Status != SprintStatusActive || !day.After(now) {
			done := 0.0
			for _, id := range taskIDs {
				if doneAt(transitions[id], evalAt) {
					done += weights[id]
				}
			}
			remaining := committed - done
			if remaining < 0 {
				remaining = 0
			}
			remaining = math.Round(remaining*100) / 100
			p.Remaining = &remaining
		}
		points = append(points, p)
	}

	resp := burndownResponse{
		SprintID:  s.ID,
		Name:      s.Name,
		Status:    s.Status,
		StartDate: *s.StartDate,
		EndDate:   *s.EndDate,
		Committed: math.Round(committed*100) / 100,
		Points:    points,
	}
	if unit != burndownUnitTasks {
		resp.Unit = unit
		resp.Unestimated = &unestimated
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

// burndownUnit reads and validates the ?unit= query param against the
// project's active estimation unit. Asking for an effort unit the project does
// not estimate in is a 422 ESTIMATION_UNIT_INACTIVE — the same code the write
// path uses — rather than a silent fall back to counting tasks: a chart that
// quietly changes what it measures is exactly the bug effort burndown exists
// to fix. It writes the error response and returns ("", false) on failure.
func (h *Handler) burndownUnit(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	unit := r.URL.Query().Get("unit")
	if unit == "" {
		unit = burndownUnitTasks
	}
	switch unit {
	case burndownUnitTasks:
		return unit, true
	case burndownUnitPoints, burndownUnitHours:
	default:
		shared.WriteValidationError(w, "VALIDATION_ERROR", "unit must be one of tasks, points, hours", "unit")
		return "", false
	}
	project, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return "", false
	}
	active := EstimationUnitNone
	if project != nil {
		active = project.EstimationUnit
	}
	if active != estimationUnitFor(unit) {
		reason := "this project does not estimate effort"
		if active != EstimationUnitNone {
			reason = "this project estimates in " + active
		}
		shared.WriteError(w, http.StatusUnprocessableEntity, "ESTIMATION_UNIT_INACTIVE",
			"unit cannot be used: "+reason)
		return "", false
	}
	return unit, true
}

// taskEstimateQueries reads each task's estimate in one unit. Whitelisted
// complete literals rather than one query with the column spliced in — see
// sumEstimatesQueries (planning_repo.go) for why.
var taskEstimateQueries = map[string]string{
	EstimationUnitPoints: `SELECT id, story_points FROM tasks WHERE id = ANY($1)`,
	EstimationUnitHours:  `SELECT id, estimate_hours FROM tasks WHERE id = ANY($1)`,
}

// estimationUnitFor maps a ?unit= value onto the project-level estimation unit
// it corresponds to (NONE for "tasks", which estimates nothing).
func estimationUnitFor(burndownUnit string) string {
	switch burndownUnit {
	case burndownUnitPoints:
		return EstimationUnitPoints
	case burndownUnitHours:
		return EstimationUnitHours
	default:
		return EstimationUnitNone
	}
}

// burndownWeights returns what each committed task contributes to the burndown
// — 1 for a task count, its estimate for an effort unit — plus how many of
// them carry no estimate at all. An unestimated task weighs 0 *and* is
// reported, so the UI can warn instead of quietly under-reporting the
// commitment.
func (h *Handler) burndownWeights(taskIDs []string, unit string) (map[string]float64, int, error) {
	weights := make(map[string]float64, len(taskIDs))
	if unit == burndownUnitTasks {
		for _, id := range taskIDs {
			weights[id] = 1
		}
		return weights, 0, nil
	}
	if len(taskIDs) == 0 {
		return weights, 0, nil
	}
	query, ok := taskEstimateQueries[estimationUnitFor(unit)]
	if !ok {
		return weights, 0, nil
	}
	rows, err := h.db.Query(query, taskIDs)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	estimated := 0
	for rows.Next() {
		var id string
		var est *float64
		if err := rows.Scan(&id, &est); err != nil {
			return nil, 0, err
		}
		if est != nil {
			weights[id] = *est
			estimated++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return weights, len(taskIDs) - estimated, nil
}

// loadStatusTransitions builds each task's chronological done/not-done
// transition list from status-bearing activity entries plus a synthetic DONE
// transition at tasks.done_at (done_at is stamped on entering DONE and
// cleared on leaving it, so when set it marks the start of the task's current
// DONE period — this covers paths that never wrote a status-bearing entry).
func (h *Handler) loadStatusTransitions(taskIDs []string) (map[string][]statusTransition, error) {
	out := make(map[string][]statusTransition, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}

	rows, err := h.db.Query(
		`SELECT task_id, payload_json, created_at
		   FROM activity_entries
		  WHERE task_id = ANY($1)
		    AND type IN ('TASK_STATUS_CHANGED','TASK_ARCHIVED','TASK_REOPENED','TASK_AUTO_ARCHIVED')
		  ORDER BY created_at ASC`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var taskID, payload, createdAt string
		if err := rows.Scan(&taskID, &payload, &createdAt); err != nil {
			return nil, err
		}
		var params struct {
			Status string `json:"status"`
		}
		// Entries from before this release carry no status — skip them; the
		// done_at fallback below still anchors the task's final DONE period.
		if json.Unmarshal([]byte(payload), &params) != nil || params.Status == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			continue
		}
		out[taskID] = append(out[taskID], statusTransition{
			at:   at,
			done: params.Status == StatusDone || params.Status == StatusArchived,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	doneRows, err := h.db.Query(
		`SELECT id, done_at FROM tasks WHERE id = ANY($1) AND done_at IS NOT NULL`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = doneRows.Close() }()
	for doneRows.Next() {
		var taskID, doneAtStr string
		if err := doneRows.Scan(&taskID, &doneAtStr); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339, doneAtStr)
		if err != nil {
			continue
		}
		out[taskID] = append(out[taskID], statusTransition{at: at, done: true})
		sort.Slice(out[taskID], func(i, j int) bool { return out[taskID][i].at.Before(out[taskID][j].at) })
	}
	return out, doneRows.Err()
}

// doneAt reports whether a task with the given transition history counts as
// done at instant t: the last transition at or before t wins; a task with no
// prior transition is not done (tasks start in a non-terminal status).
func doneAt(transitions []statusTransition, t time.Time) bool {
	done := false
	for _, tr := range transitions {
		if tr.at.After(t) {
			break
		}
		done = tr.done
	}
	return done
}

type velocityEntry struct {
	SprintID  string  `json:"sprintId"`
	Name      string  `json:"name"`
	EndDate   *string `json:"endDate"`
	Committed int     `json:"committed"`
	Completed int     `json:"completed"`
	// The effort series sits *beside* the counts rather than replacing them:
	// the count series stays valid (and is all a non-estimating project has).
	// EstimateUnit is per entry, not per report, because a project may switch
	// POINTS → HOURS and each sprint keeps the unit it was completed in — so a
	// mixed-unit history renders honestly instead of summing apples and
	// oranges. All three are null for a sprint completed while estimation was
	// off, and for every sprint completed before the snapshot existed.
	CommittedEstimate *float64 `json:"committedEstimate"`
	CompletedEstimate *float64 `json:"completedEstimate"`
	EstimateUnit      *string  `json:"estimateUnit"`
}

// ProjectVelocity handles GET /api/v1/projects/{projectId}/reports/velocity:
// the last N (default 6, cap 20) COMPLETED sprints as {committed, completed}
// pairs, oldest first — a pure projection of the counts snapshotted at sprint
// completion (migration 015), no computation to invent.
func (h *Handler) ProjectVelocity(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}

	n := velocityDefaultN
	if raw := r.URL.Query().Get("n"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			n = v
		}
	}
	if n > velocityMaxN {
		n = velocityMaxN
	}

	sprints, err := h.sprints.CompletedByProject(projectID, n)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Repo returns newest-first; charts read oldest → newest.
	entries := make([]velocityEntry, 0, len(sprints))
	for i := len(sprints) - 1; i >= 0; i-- {
		s := sprints[i]
		entries = append(entries, velocityEntry{
			SprintID:          s.ID,
			Name:              s.Name,
			EndDate:           s.EndDate,
			Committed:         s.CommittedCount,
			Completed:         s.CompletedCount,
			CommittedEstimate: s.CommittedEstimate,
			CompletedEstimate: s.CompletedEstimate,
			EstimateUnit:      s.EstimateUnit,
		})
	}
	shared.WriteJSON(w, http.StatusOK, entries)
}
