package workmanagement

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		TaskType    string  `json:"taskType"`
		Priority    string  `json:"priority"`
		ParentID    *string `json:"parentId"`
		AssigneeID  *string `json:"assigneeId"`
		ReleaseID   *string `json:"releaseId"`
		SprintID    *string `json:"sprintId"`
		DueDate     *string `json:"dueDate"`
		// The effort estimate, in whichever unit the project switched on. Absent
		// and null both mean "unestimated" here — unlike the PATCH path there is
		// no prior value for null to clear, so the two cannot mean different
		// things at create.
		StoryPoints   *int     `json:"storyPoints"`
		EstimateHours *float64 `json:"estimateHours"`
	}
	// Reject a key this route does not model instead of dropping it and
	// answering 201. Structs are the contract here, so a client sees `pinned`,
	// `reviewerId`, `status` and `boardColumnId` on every task it reads and
	// naturally sends them back on create; each one was silently discarded while
	// the response said the task was created — with that field absent. UpdateTask
	// closed this for PATCH long ago; create kept the old behaviour, so the two
	// halves of one resource disagreed about whether an unknown key is an error.
	//
	// Fields with a dedicated route are named rather than lumped in with typos,
	// because "unsupported field: status" does not tell you that a task is always
	// created PLANNED and moved afterwards.
	creatableFields := map[string]bool{
		"title": true, "description": true, "taskType": true, "priority": true,
		"parentId": true, "assigneeId": true, "releaseId": true, "sprintId": true,
		"dueDate": true, "storyPoints": true, "estimateHours": true,
	}
	dedicatedEndpoint := map[string]string{
		"status":        "a task is always created PLANNED; move it with POST /api/v1/tasks/{taskId}/status",
		"reviewerId":    "reviewerId cannot be set here; use POST /api/v1/tasks/{taskId}/assign",
		"pinned":        "pinned cannot be set here; use POST /api/v1/tasks/{taskId}/pin",
		"boardColumnId": "boardColumnId cannot be set here; use POST /api/v1/boards/{boardId}/move-task",
		"boardRank":     "boardRank cannot be set here; use POST /api/v1/boards/{boardId}/move-task",
		"version":       "version is assigned by the server and cannot be set at create",
		"seqNumber":     "seqNumber is assigned by the server and cannot be set at create",
		"reporterId":    "reporterId is the authenticated caller and cannot be set at create",
	}
	if !shared.DecodePatch(w, r, creatableFields, dedicatedEndpoint, &req) {
		return
	}
	// Sanitize the description against the HTML allowlist before validating its
	// length and persisting. The server is the source of truth for stored markup.
	req.Description = CleanTaskDescription(req.Description)
	if err := ValidateTaskInput(req.Title, req.Description); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	if req.TaskType == "" {
		req.TaskType = TaskTypeTask
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	if !ValidTaskType(req.TaskType) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_TASK_TYPE", "unknown task type value")
		return
	}
	// The project row carries the task settings (optional hierarchy levels,
	// custom priorities) every remaining validation depends on.
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
	// Hierarchy: the parent (mandatory for SUBTASK, forbidden for EPIC) must be
	// a task of the type one level up in the same project. "" counts as unset so
	// clients can send it interchangeably with null.
	if req.ParentID != nil && *req.ParentID == "" {
		req.ParentID = nil
	}
	var parent *Task
	if req.ParentID != nil {
		var perr error
		parent, perr = h.tasks.FindByID(*req.ParentID)
		if perr != nil {
			shared.WriteServerError(w, r, perr)
			return
		}
	}
	if err := ValidateTaskParent(project, req.TaskType, "", parent, req.ParentID != nil); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	actorID := shared.GetUserID(r)
	now := shared.Now()

	// "" means unassigned, like null — store SQL NULL, never an empty string.
	req.AssigneeID = emptyToNil(req.AssigneeID)
	// Validate before taking a sequence number, so a rejected create does not
	// burn one and leave a gap in the project's task keys.
	if !h.requireAssignable(w, r, projectID, req.AssigneeID, "ASSIGNEE_INVALID", "assignee") {
		return
	}
	// An estimate supplied at create goes through exactly the same rules as one
	// supplied through PATCH — active unit, range, estimable type — so the two
	// entry points can never disagree about what a legal estimate is. Checked
	// here, before the sequence number is taken, for the reason above.
	if req.StoryPoints != nil || req.EstimateHours != nil {
		candidate := &Task{
			TaskType:    req.TaskType,
			StoryPoints: req.StoryPoints, EstimateHours: req.EstimateHours,
		}
		if verr := ValidateTaskEstimate(project, candidate, req.StoryPoints != nil, req.EstimateHours != nil); verr != nil {
			if !h.writeDomainError(w, verr) {
				shared.WriteServerError(w, r, verr)
			}
			return
		}
	}

	if !h.guardSprintAssignment(w, r, projectID, nil, req.SprintID) {
		return
	}
	if !h.guardReleaseAssignment(w, r, projectID, nil, req.ReleaseID) {
		return
	}

	seq, err := NextSeqNumber(h.db, projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	task := &Task{
		ID: shared.NewUUID(), ProjectID: projectID, Title: req.Title,
		Description: req.Description, TaskType: req.TaskType, Status: StatusPlanned,
		Priority: req.Priority, ParentID: req.ParentID, AssigneeID: req.AssigneeID,
		ReporterID: &actorID, ReleaseID: req.ReleaseID, SprintID: req.SprintID,
		DueDate: req.DueDate, SeqNumber: &seq,
		StoryPoints: req.StoryPoints, EstimateHours: req.EstimateHours,
		BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := h.tasks.Create(task); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	_ = h.writeActivity(projectID, task.ID, actorID, "TASK_CREATED", map[string]any{"title": task.Title})

	if req.AssigneeID != nil && h.notifier != nil {
		h.notifier.NotifyTaskAssigned(task.ID, task.Title, projectID, *req.AssigneeID, actorID)
	}

	shared.WriteJSON(w, http.StatusCreated, task)
}

// guardSprintAssignment validates an incoming sprint link on the task side and
// answers the request itself when the link is not allowed, reporting whether the
// caller may continue.
//
// The board side has always validated this (MoveTask looks the sprint up and
// refuses an ACTIVE one with SPRINT_SCOPE_LOCKED); the task side wrote sprint_id
// with no lookup at all. So the same commitment was accepted or refused
// depending on which route you took: creating a task straight into a running
// sprint worked, the equivalent board move was refused, and an unknown sprint id
// surfaced as a 500 from the foreign key rather than a stable 422.
//
// Rules, mirroring the board:
//   - clearing the link is always allowed (RemoveTaskFromBoard has no lock
//     either — a running sprint's scope is closed to additions, not a cage);
//   - re-sending the sprint a task is already in is a no-op, not an addition;
//   - the sprint must exist and belong to the task's project. A sprint in
//     another project reports SPRINT_NOT_FOUND rather than a distinct code, so
//     the response cannot be used to probe for sprints in other projects;
//   - joining an ACTIVE sprint is refused with SPRINT_SCOPE_LOCKED.
func (h *Handler) guardSprintAssignment(w http.ResponseWriter, r *http.Request, projectID string, oldSprintID, newSprintID *string) bool {
	if newSprintID == nil || *newSprintID == "" {
		return true
	}
	if oldSprintID != nil && *oldSprintID == *newSprintID {
		return true
	}
	sp, err := h.sprints.FindByID(*newSprintID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if sp == nil || sp.ProjectID != projectID {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_NOT_FOUND", "sprint not found")
		return false
	}
	if sp.Status == SprintStatusActive {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_SCOPE_LOCKED",
			"a running sprint's scope is locked; plan tasks before starting the sprint")
		return false
	}
	return true
}

// guardReleaseAssignment is guardSprintAssignment's twin for the release link.
// tasks.release_id is bare TEXT with no FK, so before this check any string —
// a typo'd UUID, another project's release — persisted silently and quietly
// mis-counted RELEASE_HAS_OPEN_TASKS and every release report (2026-08-02
// review; sprint got the guard first, release did not). nil/empty clears the
// link and passes; an unchanged value passes so an edit that doesn't touch the
// release never fails on it. Unknown and cross-project answer the same 422, so
// the response cannot be used to probe another project's release IDs.
func (h *Handler) guardReleaseAssignment(w http.ResponseWriter, r *http.Request, projectID string, oldReleaseID, newReleaseID *string) bool {
	if newReleaseID == nil || *newReleaseID == "" {
		return true
	}
	if oldReleaseID != nil && *oldReleaseID == *newReleaseID {
		return true
	}
	rel, err := h.releases.FindByID(*newReleaseID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if rel == nil || rel.ProjectID != projectID {
		shared.WriteError(w, http.StatusUnprocessableEntity, "RELEASE_NOT_FOUND", "release not found")
		return false
	}
	return true
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	h.sweepStaleDoneTasks(projectID)
	pg := shared.ParsePagination(r)

	// A status filter the project does not define matches nothing, and an empty
	// list is indistinguishable from "this project genuinely has no such tasks".
	// That silence turns a typo or an unsupported syntax (?status=DONE,ARCHIVED —
	// comma lists are not supported) into a confident wrong answer, so reject the
	// value instead of answering it. Custom lane statuses are legitimate filters,
	// which is why this asks statusAllowed rather than the built-in enum.
	if s := r.URL.Query().Get("status"); s != "" {
		allowed, err := h.statusAllowed(projectID, s)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if !allowed {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_STATUS",
				"unknown status value: "+s)
			return
		}
	}

	// taskType is spelled `taskType` in the OpenAPI spec but was only ever read
	// from `type`, so the documented spelling silently matched everything. Accept
	// both: `type` is what existing clients send, `taskType` is what the contract
	// promises.
	taskType := r.URL.Query().Get("type")
	if taskType == "" {
		taskType = r.URL.Query().Get("taskType")
	}
	filters := map[string]string{
		"status":     r.URL.Query().Get("status"),
		"priority":   r.URL.Query().Get("priority"),
		"assigneeId": r.URL.Query().Get("assigneeId"),
		"taskType":   taskType,
		"parentId":   r.URL.Query().Get("parentId"),
		"releaseId":  r.URL.Query().Get("releaseId"),
		"sprintId":   r.URL.Query().Get("sprintId"),
		"sortBy":     r.URL.Query().Get("sortBy"),
		"order":      r.URL.Query().Get("order"),
	}
	ts, err := h.tasks.List(projectID, filters, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// The body stays a bare array (structs are the contract; wrapping it would
	// break both SPAs), so the total rides in a header. Without it a client
	// receiving exactly `size` items cannot tell a full page from a truncated
	// one without a speculative extra request — and the default size of 20
	// meant an unpaginated caller silently saw a partial project.
	total, err := h.tasks.CountList(projectID, filters)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	shared.WriteJSON(w, http.StatusOK, ts)
}

// sweepThrottleInterval bounds how often the lazy DONE-task sweep runs per
// project. Retention is measured in days, so anything well under a day keeps
// the sweep effectively as fresh while taking the write query off almost every
// task-list read.
const sweepThrottleInterval = 10 * time.Minute

// sweepStaleDoneTasks auto-archives this project's DONE tasks that have been
// done longer than DoneTaskRetentionDays, hiding them from the board. It runs
// lazily whenever tasks are listed (the board, backlog and agile views all load
// the task list), so no background job is needed — but at most once per project
// per sweepThrottleInterval, so the hot list path isn't taxed with a write
// query on every request. Failures are non-fatal: hiding stale tasks is a
// convenience, not a correctness guarantee, so the caller still serves the
// listing even if the sweep errors.
func (h *Handler) sweepStaleDoneTasks(projectID string) {
	now := time.Now()
	h.sweepMu.Lock()
	if last, ok := h.lastSweep[projectID]; ok && now.Sub(last) < sweepThrottleInterval {
		h.sweepMu.Unlock()
		return
	}
	// Expired entries no longer throttle anything; drop them so the map tracks
	// only projects listed within the current interval, not every project ever
	// listed (including since-deleted ones).
	for id, last := range h.lastSweep {
		if now.Sub(last) >= sweepThrottleInterval {
			delete(h.lastSweep, id)
		}
	}
	h.lastSweep[projectID] = now
	h.sweepMu.Unlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -DoneTaskRetentionDays).Format(time.RFC3339)
	archived, err := h.tasks.ArchiveStaleDone(projectID, cutoff, shared.Now())
	if err != nil {
		return
	}
	// Each task is archived once (it leaves DONE), so this logs exactly once per
	// task. Actor is empty: this is a system action, not a user's.
	for i := range archived {
		// Status params make the transition replayable for sprint burndown
		// (the sweep only ever archives DONE tasks).
		_ = h.writeActivity(projectID, archived[i].ID, "", "TASK_AUTO_ARCHIVED",
			map[string]any{"status": StatusArchived, "from": StatusDone})
	}
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, _, ok := h.taskGuard(w, r, id)
	if !ok {
		return
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	// Decode into raw map to distinguish absent fields from explicit null.
	var rawReq map[string]json.RawMessage
	if err := shared.DecodeJSON(r, &rawReq); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	// Reject unknown/unsupported keys instead of silently accepting them. The
	// same principle the mistyped-field handling below states applies here: a 200
	// that dropped a field would tell the client its edit was saved when it was
	// not — the trap that let a batch of PATCH {"status":"DONE"} calls report
	// success while every task stayed PLANNED. Fields that have their own
	// transition endpoint (status, priority, assignee) are not editable through
	// this route and are called out by name so the caller knows where to go.
	updatableFields := map[string]bool{
		"title": true, "description": true, "taskType": true,
		"releaseId": true, "sprintId": true, "dueDate": true,
		"parentId": true, "version": true,
		"storyPoints": true, "estimateHours": true,
	}
	// placementFields is the subset of updatableFields that says where a task
	// sits rather than what it is; see the immutability carve-out below.
	placementFields := map[string]bool{
		"parentId": true, "sprintId": true, "releaseId": true,
	}
	dedicatedEndpoint := map[string]string{
		"status":     "POST /api/v1/tasks/{taskId}/status",
		"priority":   "POST /api/v1/tasks/{taskId}/priority",
		"assigneeId": "POST /api/v1/tasks/{taskId}/assign",
		"reviewerId": "POST /api/v1/tasks/{taskId}/assign",
	}
	for key := range rawReq {
		if updatableFields[key] {
			continue
		}
		if endpoint, ok := dedicatedEndpoint[key]; ok {
			shared.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_FIELD",
				key+" cannot be changed here; use "+endpoint)
			return
		}
		shared.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_FIELD", "unsupported field: "+key)
		return
	}

	// Immutability protects what a finished task *says* — its title, description,
	// type, dates and workflow outcome are the historical record and stay frozen.
	// It deliberately does not protect where the task *sits*: placing finished work
	// in the hierarchy, a sprint or a release is a statement about how the backlog
	// is organized today, not a rewrite of what happened. Without this carve-out a
	// project can never be reorganized retroactively, because completed work —
	// usually most of it — is permanently stranded: unparented, unsprinted and
	// unattributed. Re-parenting alone is not enough, since an epic/story structure
	// that no finished task can be scheduled into only solves half the problem.
	// ValidateTaskParent below still applies in full, so the cycle and type-nesting
	// rules hold for these edits, and status still moves only via its own endpoint.
	if IsImmutable(t.Status) {
		for key := range rawReq {
			if placementFields[key] || key == "version" {
				continue
			}
			shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_IMMUTABLE",
				"cannot modify a DONE or ARCHIVED task (except parentId, sprintId, releaseId)")
			return
		}
	}

	// applyStr updates a non-nullable string field when present in the request.
	// A present-but-mistyped value is a client error and must fail the request
	// (not be dropped): answering 200 while ignoring the field would tell the
	// client its edit was saved when it wasn't.
	badField := func(key string) {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", key+" must be a string")
	}
	applyStr := func(key string, target *string) bool {
		if raw, ok := rawReq[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				badField(key)
				return false
			}
			*target = s
		}
		return true
	}
	// applyNullStr updates a nullable string field: null (or "") clears it, a
	// string value sets it, anything else fails the request.
	applyNullStr := func(key string, target **string) bool {
		raw, ok := rawReq[key]
		if !ok {
			return true
		}
		if string(raw) == "null" {
			*target = nil
			return true
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			badField(key)
			return false
		}
		if s == "" {
			*target = nil
		} else {
			*target = &s
		}
		return true
	}
	// applyNullInt / applyNullFloat update a nullable numeric field. An
	// explicit null clears the value — "unestimated" is a real state, distinct
	// from an estimate of 0, so the two must not collapse into each other the
	// way applyNullStr folds "" into nil. A non-numeric value fails the request
	// rather than being dropped.
	applyNullInt := func(key string, target **int) bool {
		raw, ok := rawReq[key]
		if !ok {
			return true
		}
		if string(raw) == "null" {
			*target = nil
			return true
		}
		var n int
		if json.Unmarshal(raw, &n) != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", key+" must be an integer or null")
			return false
		}
		*target = &n
		return true
	}
	applyNullFloat := func(key string, target **float64) bool {
		raw, ok := rawReq[key]
		if !ok {
			return true
		}
		if string(raw) == "null" {
			*target = nil
			return true
		}
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", key+" must be a number or null")
			return false
		}
		*target = &f
		return true
	}

	// Snapshot the fields we report on before mutating, so we can summarize what
	// actually changed for the task-changed notification.
	old := *t

	if !applyStr("title", &t.Title) || !applyStr("description", &t.Description) || !applyStr("taskType", &t.TaskType) {
		return
	}

	// Sanitize the (possibly updated) description against the HTML allowlist
	// before validation and persistence, irrespective of client-side cleaning.
	t.Description = CleanTaskDescription(t.Description)
	if !applyNullStr("releaseId", &t.ReleaseID) || !applyNullStr("sprintId", &t.SprintID) || !applyNullStr("dueDate", &t.DueDate) || !applyNullStr("parentId", &t.ParentID) {
		return
	}
	if !applyNullInt("storyPoints", &t.StoryPoints) || !applyNullFloat("estimateHours", &t.EstimateHours) {
		return
	}
	if !h.guardSprintAssignment(w, r, t.ProjectID, old.SprintID, t.SprintID) {
		return
	}
	if !h.guardReleaseAssignment(w, r, t.ProjectID, old.ReleaseID, t.ReleaseID) {
		return
	}

	// An optional "version" makes the edit optimistic against the client's own
	// snapshot: the guarded UPDATE below only applies if the row still has this
	// version, so an edit based on a stale read gets 409 instead of silently
	// overwriting a concurrent editor's changes. Without it the guard still
	// covers the window between this handler's read and its write.
	if raw, ok := rawReq["version"]; ok {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "version must be an integer")
			return
		}
		t.Version = v
	}

	_, hasType := rawReq["taskType"]
	_, hasParent := rawReq["parentId"]
	_, hasPoints := rawReq["storyPoints"]
	_, hasHours := rawReq["estimateHours"]
	if hasType && !ValidTaskType(t.TaskType) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_TASK_TYPE", "unknown task type value")
		return
	}
	if err := ValidateTaskInput(t.Title, t.Description); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	// The project row carries the task settings (optional hierarchy levels,
	// estimation unit) the rules below depend on. Fetch it once for whichever
	// of them this edit triggers.
	var project *Project
	if hasType || hasParent || hasPoints || hasHours {
		var perr error
		project, perr = h.projects.FindByID(t.ProjectID)
		if perr != nil {
			shared.WriteServerError(w, r, perr)
			return
		}
		if project == nil {
			shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
			return
		}
	}
	// Hierarchy: re-validate the final (type, parent) pair whenever either side
	// of it was touched, so an edit can never leave the task one level away
	// from a wrong-typed parent.
	if hasType || hasParent {
		if hasType && !project.TaskTypeEnabled(t.TaskType) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_TYPE_DISABLED", "this task type is not enabled in the project settings")
			return
		}
		var parent *Task
		if t.ParentID != nil {
			parent, err = h.tasks.FindByID(*t.ParentID)
			if err != nil {
				shared.WriteServerError(w, r, err)
				return
			}
		}
		if verr := ValidateTaskParent(project, t.TaskType, t.ID, parent, t.ParentID != nil); verr != nil {
			if !h.writeDomainError(w, verr) {
				shared.WriteServerError(w, r, verr)
			}
			return
		}
		// A type change must also keep the existing children valid: each of
		// them has to still be allowed under the new type. That is the same
		// predicate the children themselves were validated with, so a retype
		// can never leave behind a pair the create path would have refused.
		if hasType && old.TaskType != t.TaskType {
			children, cerr := h.tasks.Children(t.ID)
			if cerr != nil {
				shared.WriteServerError(w, r, cerr)
				return
			}
			for _, c := range children {
				if !TaskParentTypeAllowed(project, c.TaskType, t.TaskType) {
					shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_HAS_CHILDREN", "cannot change type: existing child tasks would no longer fit the hierarchy")
					return
				}
			}
		}
	}
	// Estimation: re-check whenever an estimate or the type was touched, so
	// retyping an estimated task into a container is caught as surely as
	// writing an estimate onto one.
	if hasType || hasPoints || hasHours {
		if verr := ValidateTaskEstimate(project, t, hasPoints, hasHours); verr != nil {
			if !h.writeDomainError(w, verr) {
				shared.WriteServerError(w, r, verr)
			}
			return
		}
	}
	t.UpdatedAt = shared.Now()
	if err := h.tasks.Update(t); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeActivity(t.ProjectID, t.ID, actorID, "TASK_UPDATED", nil)
	if h.notifier != nil {
		h.notifier.NotifyTaskChanged(t.ID, t.Title, t.ProjectID, t.ReporterID, t.AssigneeID, actorID, taskEditChanges(&old, t))
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

// taskEditChanges returns brief, human-readable lines describing which of the
// generally-editable fields changed between two task snapshots.
func taskEditChanges(old, cur *Task) []string {
	var changes []string
	if old.Title != cur.Title {
		changes = append(changes, "Title")
	}
	if old.Description != cur.Description {
		changes = append(changes, "Description")
	}
	if old.TaskType != cur.TaskType {
		changes = append(changes, fmt.Sprintf("Type: %s → %s", old.TaskType, cur.TaskType))
	}
	if !ptrStrEq(old.DueDate, cur.DueDate) {
		changes = append(changes, "Due date: "+ptrStrOr(cur.DueDate, "cleared"))
	}
	if !ptrStrEq(old.ReleaseID, cur.ReleaseID) {
		changes = append(changes, "Release")
	}
	if !ptrStrEq(old.SprintID, cur.SprintID) {
		changes = append(changes, "Sprint")
	}
	if !ptrStrEq(old.ParentID, cur.ParentID) {
		changes = append(changes, "Parent task")
	}
	if !ptrIntEq(old.StoryPoints, cur.StoryPoints) {
		changes = append(changes, fmt.Sprintf("Story points: %s → %s",
			ptrIntOr(old.StoryPoints, "none"), ptrIntOr(cur.StoryPoints, "none")))
	}
	if !ptrFloatEq(old.EstimateHours, cur.EstimateHours) {
		changes = append(changes, fmt.Sprintf("Estimated hours: %s → %s",
			ptrFloatOr(old.EstimateHours, "none"), ptrFloatOr(cur.EstimateHours, "none")))
	}
	return changes
}

// ptrStrEq reports whether two *string values are equal by content.
func ptrStrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ptrStrOr dereferences p, or returns fallback when p is nil.
func ptrStrOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// ptrIntEq / ptrFloatEq compare two optional numbers, treating nil
// (unestimated) as a value in its own right — nil and 0 are not equal.
func ptrIntEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func ptrFloatEq(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ptrIntOr / ptrFloatOr render an optional number for an activity line, or
// fallback when it is unset. Hours print without trailing zeros ("2.5", "3"),
// since %g drops the noise a fixed precision would add.
func ptrIntOr(p *int, fallback string) string {
	if p == nil {
		return fallback
	}
	return strconv.Itoa(*p)
}

func ptrFloatOr(p *float64, fallback string) string {
	if p == nil {
		return fallback
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

// parseAssignTarget reads one assign field. It reports whether the key was
// present at all — the caller must leave an absent field untouched — alongside
// the person it names, with null and "" both resolving to nobody.
func parseAssignTarget(raw json.RawMessage) (present bool, target *string, err error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, nil, err
	}
	return true, emptyToNil(v), nil
}

// emptyToNil normalizes an assignment target: both JSON null and the empty
// string mean "nobody", and must be stored as SQL NULL rather than "" so the
// field reads back as null and the "is anyone assigned?" checks downstream stay
// honest.
func emptyToNil(v *string) *string {
	if v == nil || *v == "" {
		return nil
	}
	return v
}

// requireAssignable rejects an assignee or reviewer who is neither a member of
// the project nor a global admin — the same set GET /projects/{id}/assignable-users
// offers the picker. Until this check existed the API stored whatever id it was
// sent, so a typo'd UUID persisted silently and a notification row was written
// for a user that does not exist. A nil or empty value is the "unassign" case
// and passes. Import and CSV paths build tasks through the repo, not this
// handler, and are deliberately unaffected: they resolve people by email and
// carry their own reporting for addresses they cannot match.
func (h *Handler) requireAssignable(w http.ResponseWriter, r *http.Request, projectID string, userID *string, code, field string) bool {
	if userID == nil || *userID == "" {
		return true
	}
	assignable, err := shared.IsAssignableUser(h.db, projectID, *userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if !assignable {
		shared.WriteError(w, http.StatusUnprocessableEntity, code,
			field+" must be a member of this project or a global admin")
		return false
	}
	return true
}

func (h *Handler) AssignTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	// Raw messages, because the handler has to tell three cases apart and any
	// pointer depth collapses two of them: encoding/json sets a pointer field to
	// nil for JSON null, which is exactly what an absent field leaves behind. So
	// `{"assigneeId": null}` used to read as "not sent", and the handler answered
	// 200 while keeping the old assignee — and the UI's "Unassigned"/"None"
	// option sends precisely that, so clearing either field never took effect.
	// A raw message is empty only when the key is truly absent.
	var req struct {
		AssigneeID json.RawMessage `json:"assigneeId"`
		ReviewerID json.RawMessage `json:"reviewerId"`
		// Version, when sent, is the task version the client's change is based
		// on; the guarded update rejects the write with 409 if the task moved on.
		Version *int `json:"version"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.Version != nil {
		t.Version = *req.Version
	}
	setAssignee, newAssignee, err := parseAssignTarget(req.AssigneeID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "assigneeId must be a string or null")
		return
	}
	setReviewer, newReviewer, err := parseAssignTarget(req.ReviewerID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "reviewerId must be a string or null")
		return
	}
	if !h.requireAssignable(w, r, t.ProjectID, newAssignee, "ASSIGNEE_INVALID", "assignee") {
		return
	}
	if !h.requireAssignable(w, r, t.ProjectID, newReviewer, "REVIEWER_INVALID", "reviewer") {
		return
	}
	actorID := shared.GetUserID(r)
	oldAssignee, oldReviewer := t.AssigneeID, t.ReviewerID
	if setAssignee {
		t.AssigneeID = newAssignee
	}
	if setReviewer {
		t.ReviewerID = newReviewer
	}
	t.UpdatedAt = shared.Now()
	if err := h.tasks.Update(t); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	assignParams := map[string]any{}
	if setAssignee {
		assignParams["assigneeId"] = newAssignee
	}
	if setReviewer {
		assignParams["reviewerId"] = newReviewer
	}
	_ = h.writeActivity(t.ProjectID, t.ID, actorID, "TASK_ASSIGNED", assignParams)
	if h.notifier != nil {
		// Only an actual person gets notified — clearing the field notifies nobody.
		if newAssignee != nil {
			h.notifier.NotifyTaskAssigned(t.ID, t.Title, t.ProjectID, *newAssignee, actorID)
		}
		if newReviewer != nil {
			h.notifier.NotifyReviewerSet(t.ID, t.Title, t.ProjectID, *newReviewer, actorID)
		}
		// Email the reporter and assignee a summary of the assignment change. The
		// newly-assigned person already gets an in-app "assigned" notification; this
		// is the (email-only) "your task changed" channel and so does not duplicate it.
		var changes []string
		if setAssignee && !ptrStrEq(oldAssignee, t.AssigneeID) {
			changes = append(changes, "Assignee updated")
		}
		if setReviewer && !ptrStrEq(oldReviewer, t.ReviewerID) {
			changes = append(changes, "Reviewer updated")
		}
		h.notifier.NotifyTaskChanged(t.ID, t.Title, t.ProjectID, t.ReporterID, t.AssigneeID, actorID, changes)
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

// statusAllowed reports whether status may be assigned to a task in the given
// project. Built-in statuses are always allowed; a custom status is allowed only
// when a board lane in the project defines it. The status must also be
// well-shaped (non-empty, bounded length).
func (h *Handler) statusAllowed(projectID, status string) (bool, error) {
	if !ValidStatusShape(status) {
		return false, nil
	}
	if ValidStatus(status) {
		return true, nil
	}
	return h.columns.StatusExistsForProject(projectID, status)
}

// completionGuard refuses a DONE transition while an *open BLOCKER* task sits
// anywhere beneath the tasks in taskIDs. It is the one place the rule lives,
// because the rule is reachable from three endpoints — the status route, a drag
// into a Done lane, and bulk "set status" — which had drifted into three copies
// of the same check.
//
// What changed here is the reach, not the rule: it walks the whole subtree
// instead of the direct children. A BLOCKER child one level deeper used to slip
// straight past the guard built to catch it, so nesting was enough to defeat it.
//
// What deliberately did NOT change: an open child that is *not* a BLOCKER does
// not hold its parent open. BLOCKER priority is this product's mechanism for
// "finish me before closing the parent", and widening the guard to every open
// descendant would reverse that design — see
// TestChangeStatus_BlockedByBlockerChild, which asserts completion succeeds once
// the blocker is re-prioritized, and TestChangeStatus_DoneBlockerChildDoesNotBlock,
// which records the live lockout that narrowed this guard in the first place
// (beyags, 2026-07-18). A container closed over live non-blocker children is a
// UI affordance question (warn before closing), not an API refusal.
//
// Reports false when it has already written the response.
func (h *Handler) completionGuard(w http.ResponseWriter, r *http.Request, taskIDs []string) bool {
	blocked, err := h.tasks.AnyOpenDescendantPriorityExists(taskIDs, PriorityBlocker)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if blocked {
		shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_HAS_BLOCKER",
			"cannot mark done while a child task has BLOCKER priority")
		return false
	}
	return true
}

// alignBoardColumnToStatus moves a task's card into the lane matching t.Status,
// mutating t.BoardColumnID/BoardRank in place so the caller persists it in the
// same version-guarded Update. It is a no-op for a task whose current lane
// already matches the status, or whose board has no lane for the new status (a
// custom board that does not model this stage keeps the card where it is rather
// than dropping it off the board). The card is appended to the bottom of the
// target lane.
//
// A task on no board at all is placed rather than skipped — see
// placeUnboardedTaskForStatus for which statuses that covers and why.
func (h *Handler) alignBoardColumnToStatus(t *Task) error {
	if t.BoardColumnID == nil {
		return h.placeUnboardedTaskForStatus(t)
	}
	current, err := h.columns.FindByID(*t.BoardColumnID)
	if err != nil {
		return err
	}
	if current == nil || current.Status == t.Status {
		return nil
	}
	target, err := h.columns.FindByBoardAndStatus(current.BoardID, t.Status)
	if err != nil {
		return err
	}
	if target == nil || target.ID == current.ID {
		return nil
	}
	maxRank, err := h.columns.MaxBoardRankInColumn(target.ID)
	if err != nil {
		return err
	}
	t.BoardColumnID = &target.ID
	t.BoardRank = maxRank + DefaultBoardRank
	return nil
}

// placeUnboardedTaskForStatus puts a task that is on no board onto one, when its
// new status says the work is in flight (OCT-303).
//
// The backlog is not a field — TaskRepo.Backlog is defined as
// `status NOT IN ('DONE','ARCHIVED') AND board_column_id IS NULL`, so "not on a
// board" IS "in the backlog". Without this, moving a never-boarded task to
// IN_PROGRESS or IN_REVIEW through this endpoint left it in the backlog wearing
// an in-flight label: work visibly under way, filed under work not started, and
// invisible on the board that is supposed to be the single view of what moves.
//
// Two statuses are deliberately excluded:
//   - PLANNED is the backlog's own status. Placing it would empty the backlog of
//     exactly the tasks that belong there.
//   - DONE/ARCHIVED are already outside the backlog query, so there is no
//     contradiction to fix, and auto-filing every task completed straight from
//     the backlog would change what the Done lane means.
//
// If the board has no lane for the status, nothing is placed — the same
// conservative rule alignBoardColumnToStatus applies to a boarded card.
func (h *Handler) placeUnboardedTaskForStatus(t *Task) error {
	if t.Status == StatusPlanned || IsImmutable(t.Status) {
		return nil
	}
	b, err := h.boardForTask(t)
	if err != nil || b == nil {
		return err
	}
	target, err := h.columns.FindByBoardAndStatus(b.ID, t.Status)
	if err != nil || target == nil {
		return err
	}
	maxRank, err := h.columns.MaxBoardRankInColumn(target.ID)
	if err != nil {
		return err
	}
	t.BoardColumnID = &target.ID
	t.BoardRank = maxRank + DefaultBoardRank
	return nil
}

// boardForTask returns the board a task's card belongs on: its sprint's board
// while the task is committed to a sprint, otherwise the project's default
// board. The sprint board exists from the sprint's creation until its
// completion deletes it, so this covers PLANNED and ACTIVE sprints alike. A
// card lives in exactly one lane, so this is a choice, not a fan-out — a task
// committed to a sprint belongs on that sprint's board, which is the same
// board MoveTask would have put it on. Returns nil when the project has no
// default board, which is a project that has never been given one.
func (h *Handler) boardForTask(t *Task) (*Board, error) {
	if t.SprintID != nil && *t.SprintID != "" {
		b, err := h.boards.FindBySprint(*t.SprintID)
		if err != nil {
			return nil, err
		}
		if b != nil {
			return b, nil
		}
	}
	return h.boards.FindDefault(t.ProjectID)
}

func (h *Handler) ChangeStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	if IsImmutable(t.Status) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_IMMUTABLE", "cannot change status of a DONE or ARCHIVED task")
		return
	}
	var req struct {
		Status string `json:"status"`
		// Version, when sent, is the task version the client's change is based
		// on; the guarded update rejects the write with 409 if the task moved on.
		Version *int `json:"version"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.Version != nil {
		t.Version = *req.Version
	}
	req.Status = strings.TrimSpace(req.Status)
	allowed, err := h.statusAllowed(t.ProjectID, req.Status)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !allowed {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_STATUS", "unknown status value")
		return
	}
	// A task cannot be completed over unfinished work beneath it — see
	// completionGuard for which refusal fires and why closing is refused rather
	// than warned about.
	if req.Status == StatusDone && !h.completionGuard(w, r, []string{t.ID}) {
		return
	}
	oldStatus := t.Status
	t.Status = req.Status
	t.UpdatedAt = shared.Now()
	// Keep the board column aligned with the status. The board groups cards by
	// lane, so without this a status change (e.g. from the task panel) would
	// leave the card in its old lane. This is the status→board direction; the
	// board→status direction is handled server-side too (MoveTask adopts the
	// target lane's status), so the two stay coupled no matter which endpoint a
	// client uses. Only realign a card that is on a board, and only when the
	// card's board has a lane for the new status (OCT-90).
	if err := h.alignBoardColumnToStatus(t); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.tasks.Update(t); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	// "from" is recorded (forward-only since the sprint-reports release) so
	// burndown reconstruction can replay status transitions; "status" remains
	// the interpolation param the Activity view renders.
	_ = h.writeActivity(t.ProjectID, t.ID, actorID, "TASK_STATUS_CHANGED", map[string]any{"status": req.Status, "from": oldStatus})
	if h.notifier != nil && oldStatus != req.Status {
		if t.ReporterID != nil {
			h.notifier.NotifyStatusChanged(t.ID, t.Title, t.ProjectID, *t.ReporterID, actorID, req.Status, StatusLabel(req.Status))
		}
		h.notifier.NotifyTaskChanged(t.ID, t.Title, t.ProjectID, t.ReporterID, t.AssigneeID, actorID,
			[]string{fmt.Sprintf("Status: %s → %s", StatusLabel(oldStatus), StatusLabel(req.Status))})
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) ChangePriority(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Priority string `json:"priority"`
		// Version, when sent, is the task version the client's change is based
		// on; the guarded update rejects the write with 409 if the task moved on.
		Version *int `json:"version"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.Version != nil {
		t.Version = *req.Version
	}
	if allowed, perr := h.priorityAllowed(t.ProjectID, req.Priority); perr != nil {
		shared.WriteServerError(w, r, perr)
		return
	} else if !allowed {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PRIORITY", "unknown priority value")
		return
	}
	oldPriority := t.Priority
	t.Priority = req.Priority
	t.UpdatedAt = shared.Now()
	if err := h.tasks.Update(t); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeActivity(t.ProjectID, t.ID, actorID, "TASK_PRIORITY_CHANGED", map[string]any{"priority": req.Priority})
	if h.notifier != nil && oldPriority != req.Priority {
		h.notifier.NotifyTaskChanged(t.ID, t.Title, t.ProjectID, t.ReporterID, t.AssigneeID, actorID,
			[]string{fmt.Sprintf("Priority: %s → %s", oldPriority, req.Priority)})
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

// SetTaskPin pins or unpins a task so it sorts to the top of its board lane.
// Pinning is a board-organization action (like moving a card), so unlike
// UpdateTask it is allowed even on DONE/ARCHIVED tasks.
func (h *Handler) SetTaskPin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Pinned bool `json:"pinned"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	t.Pinned = req.Pinned
	t.UpdatedAt = shared.Now()
	if err := h.tasks.Update(t); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) CopyTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	src, _, ok := h.taskWriterGuard(w, r, id)
	if !ok {
		return
	}
	actorID := shared.GetUserID(r)
	cp, err := h.svc.CopyTask(id, actorID)
	if err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	_ = h.writeActivity(src.ProjectID, cp.ID, actorID, "TASK_COPIED", map[string]any{"sourceTitle": src.Title})
	shared.WriteJSON(w, http.StatusCreated, cp)
}

func (h *Handler) ArchiveTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, _, ok := h.taskWriterGuard(w, r, id)
	if !ok {
		return
	}
	oldStatus := t.Status
	t.Status = StatusArchived
	t.UpdatedAt = shared.Now()
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.tasks.UpdateTx(tx, t); err != nil {
			return err
		}
		return h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_ARCHIVED",
			map[string]any{"status": StatusArchived, "from": oldStatus})
	}); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.publishBoardEvent(t.ProjectID, t.ID, actorID, "TASK_ARCHIVED")
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) ReopenTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, _, ok := h.taskWriterGuard(w, r, id)
	if !ok {
		return
	}
	oldStatus := t.Status
	t.Status = StatusPlanned
	t.UpdatedAt = shared.Now()
	// Reopening is a status change like any other, so the card follows it. Without
	// this the task went back to PLANNED while its card stayed in the Done lane —
	// the status↔board divergence every other transition already closes
	// (ChangeStatus does this; MoveTask does the reverse), and the one the user
	// guide says cannot happen any more.
	if err := h.alignBoardColumnToStatus(t); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.tasks.UpdateTx(tx, t); err != nil {
			return err
		}
		return h.writeActivityTx(tx, t.ProjectID, t.ID, actorID, "TASK_REOPENED",
			map[string]any{"status": StatusPlanned, "from": oldStatus})
	}); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	h.publishBoardEvent(t.ProjectID, t.ID, actorID, "TASK_REOPENED")
	shared.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	if _, ok := h.requirePermission(w, r, t.ProjectID, rbac.PermTaskDelete); !ok {
		return
	}
	// A task that still has children cannot be deleted — that would orphan
	// them (and a subtask must never be parentless). Children must be deleted
	// or re-parented first.
	children, err := h.tasks.Children(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if len(children) > 0 {
		shared.WriteError(w, http.StatusUnprocessableEntity, "TASK_HAS_CHILDREN", "task still has child tasks; delete or detach them first")
		return
	}
	// Collect uploaded-file storage keys before the cascade deletes their rows,
	// so we can remove the underlying files afterward. If this lookup fails, we
	// cannot know what files need cleanup, so abort rather than delete the task
	// row and silently orphan its uploaded files.
	var fileKeys []string
	if h.storage != nil {
		fileKeys, err = h.attachments.StorageKeysForTask(id)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	if err := h.tasks.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for _, k := range fileKeys {
		_ = h.storage.Remove(k)
	}
	h.audit.Write(shared.GetUserID(r), auditlog.ActionTaskDeleted, "task", id,
		fmt.Sprintf(`{"projectId":%q,"title":%q}`, t.ProjectID, t.Title), "", "")
	// A deleted task's card vanishes from co-workers' boards; broadcast so they
	// refresh. Delete only audits (no activity row), so publish explicitly here.
	h.publishBoardEvent(t.ProjectID, id, shared.GetUserID(r), "TASK_DELETED")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Task sub-resource handlers ----

func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Text     string  `json:"text"`
		ParentID *string `json:"parentId"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	// Comments are constrained rich text: sanitize to the same allowlist as task
	// descriptions before validating and storing.
	req.Text = SanitizeDescriptionHTML(req.Text)
	if err := ValidateCommentInput(req.Text); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	// A reply must target an existing comment on the same task. Treat the empty
	// string as "no parent" so clients can send "" interchangeably with null.
	if req.ParentID != nil && *req.ParentID == "" {
		req.ParentID = nil
	}
	if req.ParentID != nil {
		parent, perr := h.comments.FindByIDInTask(*req.ParentID, taskID)
		if perr != nil {
			shared.WriteServerError(w, r, perr)
			return
		}
		if parent == nil {
			shared.WriteError(w, http.StatusBadRequest, "COMMENT_PARENT_INVALID", "parent comment not found on this task")
			return
		}
	}
	actorID := shared.GetUserID(r)
	now := shared.Now()
	c := &TaskComment{
		ID: shared.NewUUID(), TaskID: taskID, AuthorID: actorID, ParentID: req.ParentID,
		Text: req.Text, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.comments.Create(c); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Re-read so the response carries the resolved author display name (and any
	// DB-side defaults), matching the shape returned by ListComments.
	if full, ferr := h.comments.FindByID(c.ID); ferr == nil && full != nil {
		c = full
	}
	_ = h.writeActivity(t.ProjectID, taskID, actorID, "TASK_COMMENT_ADDED", nil)
	if h.notifier != nil {
		h.notifier.NotifyMentions(req.Text, t.ProjectID, taskID, actorID)
	}
	shared.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) ListComments(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskGuard(w, r, taskID); !ok {
		return
	}
	cs, err := h.comments.ListByTask(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, cs)
}

func (h *Handler) UpdateComment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	commentID := chi.URLParam(r, "commentId")
	c, err := h.comments.FindByIDInTask(commentID, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, c != nil, "COMMENT_NOT_FOUND", "comment not found") {
		return
	}
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the comment has moved on.
		Version *int `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"text": true, "version": true,
	}, nil, &req) {
		return
	}
	req.Text = SanitizeDescriptionHTML(req.Text)
	if err := ValidateCommentInput(req.Text); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	c.Text = req.Text
	c.UpdatedAt = shared.Now()
	if req.Version != nil {
		c.Version = *req.Version
	}
	if err := h.comments.Update(c); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeActivity(t.ProjectID, taskID, actorID, "TASK_COMMENT_UPDATED", nil)
	if h.notifier != nil {
		h.notifier.NotifyMentions(req.Text, t.ProjectID, taskID, actorID)
	}
	shared.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	commentID := chi.URLParam(r, "commentId")
	c, err := h.comments.FindByIDInTask(commentID, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, c != nil, "COMMENT_NOT_FOUND", "comment not found") {
		return
	}
	t, _, ok := h.taskWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	if err := h.comments.Delete(commentID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	_ = h.writeActivity(t.ProjectID, taskID, shared.GetUserID(r), "TASK_COMMENT_DELETED", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddLink(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if !shared.SafeHref(req.URL) {
		shared.WriteError(w, http.StatusBadRequest, "URL_UNSAFE", "url must be http(s), mailto, or relative")
		return
	}
	l := &TaskLink{
		ID: shared.NewUUID(), TaskID: taskID, URL: req.URL, Title: req.Title,
		CreatedAt: shared.Now(),
	}
	if err := h.links.Create(l); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskGuard(w, r, taskID); !ok {
		return
	}
	ls, err := h.links.ListByTask(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ls)
}

func (h *Handler) DeleteLink(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	_, _, ok := h.taskWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	id := chi.URLParam(r, "linkId")
	deleted, err := h.links.Delete(taskID, id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, deleted, "TASK_LINK_NOT_FOUND", "link not found") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		SizeBytes   int64  `json:"sizeBytes"`
		ExternalURL string `json:"externalUrl"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.ExternalURL != "" && !shared.SafeHref(req.ExternalURL) {
		shared.WriteError(w, http.StatusBadRequest, "URL_UNSAFE", "externalUrl must be http(s), mailto, or relative")
		return
	}
	a := &TaskAttachment{
		ID: shared.NewUUID(), TaskID: taskID, Filename: req.Filename,
		ContentType: req.ContentType, SizeBytes: req.SizeBytes,
		ExternalURL: req.ExternalURL, CreatedAt: shared.Now(),
	}
	if err := h.attachments.Create(a); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) ListAttachments(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskGuard(w, r, taskID); !ok {
		return
	}
	as, err := h.attachments.ListByTask(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, as)
}

func (h *Handler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	_, _, ok := h.taskWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	id := chi.URLParam(r, "attachmentId")
	// Look up the row first so we can remove the underlying file from disk (for
	// uploaded attachments) after the DB row is deleted.
	a, err := h.attachments.FindByIDInTask(id, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if a == nil {
		// Idempotent: nothing to delete.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.attachments.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if a.IsUpload() && h.storage != nil {
		// Tolerate an already-missing file.
		_ = h.storage.Remove(a.StorageKey)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AddRelation(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	var req struct {
		TargetTaskID string `json:"targetTaskId"`
		RelationType string `json:"relationType"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	// No default: openapi.yaml declares relationType required and enum-constrained,
	// and defaulting a missing/misspelled field to RELATES_TO turned a request the
	// server could not honour into a 201 with the wrong semantics. Service.AddRelation
	// rejects "" along with any other non-enum value.
	rel := &TaskRelation{
		ID: shared.NewUUID(), SourceTaskID: taskID, TargetTaskID: req.TargetTaskID,
		RelationType: req.RelationType, CreatedAt: shared.Now(),
	}
	if err := h.svc.AddRelation(t.ProjectID, rel); err != nil {
		if !h.writeDomainError(w, err) {
			shared.WriteServerError(w, r, err)
		}
		return
	}
	shared.WriteJSON(w, http.StatusCreated, rel)
}

func (h *Handler) ListRelations(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskGuard(w, r, taskID); !ok {
		return
	}
	rels, err := h.relations.ListByTask(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, rels)
}

// ListProjectRelations returns every task relation in the project in one
// response. The mindmap view uses it to build the epic → story → task
// hierarchy without issuing one relations request per task.
func (h *Handler) ListProjectRelations(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	rels, err := h.relations.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rels == nil {
		rels = []TaskRelation{}
	}
	shared.WriteJSON(w, http.StatusOK, rels)
}

func (h *Handler) DeleteRelation(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	_, _, ok := h.taskWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	id := chi.URLParam(r, "relationId")
	deleted, err := h.svc.DeleteRelation(taskID, id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, deleted, "TASK_RELATION_NOT_FOUND", "relation not found") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
