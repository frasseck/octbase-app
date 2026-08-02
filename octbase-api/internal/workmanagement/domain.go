// Package workmanagement is the core bounded context for projects, tasks,
// boards, releases, categories, and templates.  Business rules (immutable
// tasks, cycle detection in relations, release close guard) live in Service;
// HTTP handlers and PostgreSQL repositories are co-located in this package.
package workmanagement

import (
	"fmt"
	"math"
	"strings"
)

// Project domain object.
type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Abbreviation string `json:"abbreviation"`
	Description  string `json:"description"`
	Visibility   string `json:"visibility"`
	Status       string `json:"status"`
	// ThemeEnabled/InitiativeEnabled switch the optional top hierarchy levels
	// on for this project (THEME → INITIATIVE → EPIC → …); see TaskTypeChain.
	ThemeEnabled      bool `json:"themeEnabled"`
	InitiativeEnabled bool `json:"initiativeEnabled"`
	// EstimationUnit selects the project's effort-estimation unit: NONE (the
	// default — estimation is off and nothing about it appears in the UI),
	// POINTS or HOURS. Only a project owner/admin may change it, and switching
	// is non-destructive: a task's estimate in the unit that is no longer
	// active stays stored but dormant (see Task.StoryPoints/EstimateHours).
	EstimationUnit string `json:"estimationUnit"`
	// BoardLaneLimit caps how many cards a board lane draws at once; the rest
	// load as the reader scrolls. It is a display setting only — no task leaves
	// its lane and the lane's count badge still reports the full size.
	// DefaultBoardLaneLimit for a new project, 0 for "draw everything".
	BoardLaneLimit int    `json:"boardLaneLimit"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	Version        int    `json:"version"`
}

// Task domain object.
type Task struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	TaskType    string `json:"taskType"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	// ParentID links the task into the EPIC → STORY → TASK → SUBTASK
	// hierarchy: it must reference a task of the type exactly one level up in
	// the same project (see ValidateTaskParent). NULL for top-level tasks;
	// never NULL for SUBTASKs.
	ParentID      *string `json:"parentId"`
	AssigneeID    *string `json:"assigneeId"`
	ReporterID    *string `json:"reporterId"`
	ReviewerID    *string `json:"reviewerId"`
	ReleaseID     *string `json:"releaseId"`
	SprintID      *string `json:"sprintId"`
	DueDate       *string `json:"dueDate"`
	BoardColumnID *string `json:"boardColumnId"`
	BoardRank     int     `json:"boardRank"`
	Pinned        bool    `json:"pinned"`
	SeqNumber     *int    `json:"seqNumber"`
	ExternalRef   *string `json:"externalRef,omitempty"`
	// StoryPoints / EstimateHours hold the task's effort estimate. At most one
	// is writable at a time — the one matching the project's EstimationUnit —
	// but both are always serialized, so a value stored under a previously
	// active unit stays visible and survives a unit switch. nil means
	// *unestimated*, which is deliberately distinct from an estimate of 0.
	// Only the leaf types carry estimates (see EstimableTaskType).
	StoryPoints   *int     `json:"storyPoints"`
	EstimateHours *float64 `json:"estimateHours"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	DoneAt        *string  `json:"doneAt"`
	Version       int      `json:"version"`
}

// TaskComment domain object. ParentID, when set, threads the comment as a reply
// to another comment on the same task (NULL for top-level comments). AuthorName
// is the author's display name, resolved on read so clients can label a comment
// without separately resolving the author against the project member list.
type TaskComment struct {
	ID         string  `json:"id"`
	TaskID     string  `json:"taskId"`
	AuthorID   string  `json:"authorId"`
	AuthorName string  `json:"authorName"`
	ParentID   *string `json:"parentId"`
	Text       string  `json:"text"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
	Version    int     `json:"version"`
}

// TaskLink domain object.
type TaskLink struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
}

// TaskAttachment domain object. An attachment is either an uploaded file
// (StorageKey set, ExternalURL empty) or an external link (ExternalURL set,
// StorageKey empty). StorageKey is an opaque, server-generated key into the
// attachments storage volume and is never serialized to clients.
type TaskAttachment struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	ExternalURL string `json:"externalUrl"`
	// StorageKey identifies the file on the attachments volume for uploaded
	// files. Empty for external links. Never exposed in JSON responses.
	StorageKey string `json:"-"`
	// UploadedBy is the ID of the user who stored the file, the basis for the
	// per-user storage quota (OCTBASE_MAX_USER_STORAGE_MB). Empty for external
	// links and for uploads that predate quota accounting. Not part of the
	// JSON contract.
	UploadedBy string `json:"-"`
	CreatedAt  string `json:"createdAt"`
}

// IsUpload reports whether the attachment is a server-stored uploaded file
// (as opposed to an external link).
func (a *TaskAttachment) IsUpload() bool { return a.StorageKey != "" }

// TaskRelation domain object.
type TaskRelation struct {
	ID           string `json:"id"`
	SourceTaskID string `json:"sourceTaskId"`
	TargetTaskID string `json:"targetTaskId"`
	RelationType string `json:"relationType"`
	CreatedAt    string `json:"createdAt"`
}

// Board domain object.
type Board struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	// MinColumns and MaxColumns bound how many lanes the board may have. Both
	// stay within the absolute range [BoardMinLanes, BoardMaxLanes].
	MinColumns int `json:"minColumns"`
	MaxColumns int `json:"maxColumns"`
	// IsSprintBoard marks the board as a Scrum sprint board; SprintID, when set,
	// links it to an existing timed sprint configuration in the same project.
	IsSprintBoard bool    `json:"isSprintBoard"`
	SprintID      *string `json:"sprintId"`
	// Sprint is populated on reads when SprintID is set so the frontend can show
	// the linked sprint's timing without an extra request.
	Sprint          *Sprint               `json:"sprint,omitempty"`
	Columns         []BoardColumn         `json:"columns,omitempty"`
	ExternalColumns []BoardExternalColumn `json:"externalColumns,omitempty"`
	CreatedAt       string                `json:"createdAt"`
	UpdatedAt       string                `json:"updatedAt"`
	Version         int                   `json:"version"`
}

// BoardExternalColumn is a read-only view of a column owned by another board.
// The source may live in a different project, as long as the viewer has read
// access to that project. SourceBoard/SourceColumn/SourceProject fields identify
// the origin so the consuming board can label the column and prevent edits to
// its tasks.
type BoardExternalColumn struct {
	ID                 string `json:"id"`
	BoardID            string `json:"boardId"`
	SourceColumnID     string `json:"sourceColumnId"`
	SourceBoardID      string `json:"sourceBoardId"`
	SourceBoardName    string `json:"sourceBoardName"`
	SourceProjectID    string `json:"sourceProjectId"`
	SourceProjectName  string `json:"sourceProjectName"`
	SourceColumnName   string `json:"sourceColumnName"`
	SourceColumnStatus string `json:"sourceColumnStatus"`
	Position           int    `json:"position"`
	// Accessible reports whether the current viewer may read the source project.
	// When false, Tasks is omitted so a board reader without access to the source
	// project never sees its task content. Populated on reads only.
	Accessible bool   `json:"accessible"`
	Tasks      []Task `json:"tasks,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// BoardColumn domain object.
type BoardColumn struct {
	ID        string `json:"id"`
	BoardID   string `json:"boardId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Version   int    `json:"version"`
}

// Sprint domain object.
type Sprint struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"projectId"`
	Name      string  `json:"name"`
	Goal      string  `json:"goal"`
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`
	Status    string  `json:"status"`
	ReleaseID *string `json:"releaseId"`
	// CommittedCount and CompletedCount are the board scope captured when the
	// sprint is completed: how many tasks were on the sprint board and how many
	// of those were DONE. They are 0 until the sprint is completed and let the
	// historical card show "done/committed" even after unfinished tasks have been
	// returned to the backlog.
	CommittedCount int `json:"committedCount"`
	CompletedCount int `json:"completedCount"`
	// CommittedEstimate/CompletedEstimate/EstimateUnit are the effort twin of
	// the two counts above, captured in the same completion transaction. The
	// unit is stored per sprint rather than read from the project on every
	// read: a project may switch POINTS → HOURS later, and a historical
	// sprint's velocity must keep meaning what it meant when it was taken. All
	// three are nil for a sprint completed while estimation was NONE, and for
	// any sprint that has not been completed yet.
	CommittedEstimate *float64 `json:"committedEstimate"`
	CompletedEstimate *float64 `json:"completedEstimate"`
	EstimateUnit      *string  `json:"estimateUnit"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt"`
	Version           int      `json:"version"`
}

// Release domain object.
type Release struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"projectId"`
	Name      string  `json:"name"`
	Goal      string  `json:"goal"`
	DueDate   *string `json:"dueDate"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Version   int     `json:"version"`
}

// TaskCategory domain object.
type TaskCategory struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Version     int    `json:"version"`
}

// TaskTemplate domain object.
type TaskTemplate struct {
	ID                  string `json:"id"`
	ProjectID           string `json:"projectId"`
	Name                string `json:"name"`
	TitleTemplate       string `json:"titleTemplate"`
	DescriptionTemplate string `json:"descriptionTemplate"`
	TaskType            string `json:"taskType"`
	Priority            string `json:"priority"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
	Version             int    `json:"version"`
}

// DoneTaskRetentionDays is how long a task stays visible on the board after it
// is marked DONE before the lazy sweep auto-archives it (hiding it from the
// board while keeping it accessible via the Archive view and reopenable). It is
// a single named constant so it can later become a per-project setting.
const DoneTaskRetentionDays = 30

// Status constants.
const (
	StatusPlanned    = "PLANNED"
	StatusInProgress = "IN_PROGRESS"
	StatusInReview   = "IN_REVIEW"
	StatusDone       = "DONE"
	StatusArchived   = "ARCHIVED"
	StatusActive     = "ACTIVE"
	StatusClosed     = "CLOSED"
)

// statusLabels maps built-in status enum values to their human-readable English
// labels, mirroring the frontend task.status translations. Used for outbound
// email where the raw enum (e.g. "IN_PROGRESS") would otherwise leak to users.
var statusLabels = map[string]string{
	StatusPlanned:    "Planned",
	StatusInProgress: "In Progress",
	StatusInReview:   "In Review",
	StatusDone:       "Done",
	StatusArchived:   "Archived",
}

// StatusLabel returns the human-readable label for a status. Built-in statuses
// are translated; custom statuses (board lane names) are already human-readable
// and returned as-is.
func StatusLabel(status string) string {
	if label, ok := statusLabels[status]; ok {
		return label
	}
	return status
}

// Sprint status constants.
const (
	SprintStatusPlanned   = "PLANNED"
	SprintStatusActive    = "ACTIVE"
	SprintStatusCompleted = "COMPLETED"
)

// Priority constants.
const (
	PriorityLow      = "LOW"
	PriorityMedium   = "MEDIUM"
	PriorityHigh     = "HIGH"
	PriorityCritical = "CRITICAL"
	PriorityBlocker  = "BLOCKER"
)

// Task type constants. Types form a strict hierarchy
// (THEME → INITIATIVE → EPIC → STORY → TASK → SUBTASK); see TaskTypeChain and
// ParentTaskTypeFor. THEME and INITIATIVE are opt-in per project
// (Project.ThemeEnabled / Project.InitiativeEnabled).
const (
	TaskTypeTask       = "TASK"
	TaskTypeStory      = "STORY"
	TaskTypeEpic       = "EPIC"
	TaskTypeSubtask    = "SUBTASK"
	TaskTypeInitiative = "INITIATIVE"
	TaskTypeTheme      = "THEME"
)

// Relation type constants.
const (
	RelationRelatesTo  = "RELATES_TO"
	RelationBlocks     = "BLOCKS"
	RelationBlockedBy  = "BLOCKED_BY"
	RelationDuplicates = "DUPLICATES"
)

// IsValidRelationType reports whether rt is one of the four relation types.
// The column is plain TEXT with no CHECK constraint, so this is the only thing
// standing between a request body and an arbitrary string in the relation
// graph.
func IsValidRelationType(rt string) bool {
	switch rt {
	case RelationRelatesTo, RelationBlocks, RelationBlockedBy, RelationDuplicates:
		return true
	}
	return false
}

// Visibility constants.
const (
	VisibilityPublic  = "PUBLIC"
	VisibilityPrivate = "PRIVATE"
)

// Estimation unit constants — the per-project effort-estimation setting.
// NONE is the default: a project estimates nothing until someone switches a
// unit on.
const (
	EstimationUnitNone   = "NONE"
	EstimationUnitPoints = "POINTS"
	EstimationUnitHours  = "HOURS"
)

// Bounds for the two estimate scales. The scale is deliberately *free numbers
// with a ceiling* rather than a constrained set: a Fibonacci sequence is a UI
// convention (preset chips), never a server-side constraint, because teams
// legitimately use t-shirt-derived, linear or doubling scales too. The
// ceilings only exist to catch a fat-fingered order of magnitude.
// EstimateHoursDecimals matches the NUMERIC(7,2) column, so a value the server
// accepts is a value the database stores without rounding.
const (
	MinStoryPoints        = 0
	MaxStoryPoints        = 100
	MinEstimateHours      = 0.0
	MaxEstimateHours      = 1000.0
	EstimateHoursDecimals = 2
)

// DefaultBoardRank is the board rank assigned to new tasks.
const DefaultBoardRank = 1000

// Absolute bounds for the number of lanes (columns) a board may have. Per-board
// min/max limits are configurable but must stay within this range.
const (
	BoardMinLanes = 1
	BoardMaxLanes = 10
)

// DefaultBoardMinColumns and DefaultBoardMaxColumns are applied to boards that
// do not specify their own lane limits.
const (
	DefaultBoardMinColumns = 1
	DefaultBoardMaxColumns = 10
)

// ValidateLaneLimits checks that a board's configured min/max lane limits are
// within the absolute allowed range and mutually consistent.
func ValidateLaneLimits(min, max int) error {
	if min < BoardMinLanes || min > BoardMaxLanes {
		return &DomainError{Code: "BOARD_LIMITS_INVALID", Message: "minimum lanes must be between 1 and 10", Field: "minColumns"}
	}
	if max < BoardMinLanes || max > BoardMaxLanes {
		return &DomainError{Code: "BOARD_LIMITS_INVALID", Message: "maximum lanes must be between 1 and 10", Field: "maxColumns"}
	}
	if min > max {
		return &DomainError{Code: "BOARD_LIMITS_INVALID", Message: "minimum lanes must not exceed maximum lanes", Field: "minColumns"}
	}
	return nil
}

// DomainError represents a business rule violation. Code is a machine-readable
// constant; Message is human-readable. Both service rules and input validation
// use this type so callers can handle them uniformly.
type DomainError struct {
	Code    string
	Message string
	// Field is the request field this error applies to, if any. It is
	// surfaced to clients via ErrorResponse.Details so the frontend can
	// associate the message with the corresponding form field
	// (WCAG 3.3.1 Error Identification).
	Field string
}

func (e *DomainError) Error() string { return e.Code + ": " + e.Message }

// IsImmutable returns true if the task cannot be modified.
func IsImmutable(status string) bool {
	return status == StatusDone || status == StatusArchived
}

// ValidStatus reports whether s is a known built-in task status value.
func ValidStatus(s string) bool {
	switch s {
	case StatusPlanned, StatusInProgress, StatusInReview, StatusDone, StatusArchived:
		return true
	}
	return false
}

// MaxStatusLength bounds a custom status string. Custom statuses are derived
// from board lane names, so this matches the practical upper bound on a lane
// name and keeps stray garbage out of the status column.
const MaxStatusLength = 40

// ValidStatusShape reports whether s is acceptable as a status string by shape
// alone: non-empty (after trimming) and within MaxStatusLength. Whether a custom
// value is actually permitted is decided by the caller, which additionally
// checks that a board lane defines it (see Handler.statusAllowed). Built-in
// statuses always satisfy this.
func ValidStatusShape(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && len(s) <= MaxStatusLength
}

// ValidVisibility reports whether s is a known project visibility value.
func ValidVisibility(s string) bool {
	return s == VisibilityPublic || s == VisibilityPrivate
}

// ValidTaskType reports whether s is a known task type value. THEME and
// INITIATIVE are valid values even where a project has not enabled them —
// use Project.TaskTypeEnabled for the per-project check.
func ValidTaskType(s string) bool {
	switch s {
	case TaskTypeTask, TaskTypeStory, TaskTypeEpic, TaskTypeSubtask,
		TaskTypeInitiative, TaskTypeTheme:
		return true
	}
	return false
}

// ValidStatuses / ValidTaskTypes / ValidPriorities are the ordered slice forms
// of the ValidStatus / ValidTaskType / ValidPriority predicates: same members,
// in the order a report or a chart should present them. The statistics report
// buckets by exactly these, so its axes cannot drift away from what the
// validators accept. They are deliberately *not* wired into GET /meta/enums:
// that endpoint publishes its own long-standing order, which clients build
// dropdowns from, and reordering it would be a silent API change.
// Project-specific extras — custom priorities, board-lane statuses — are
// additions on top of these, not replacements (the report appends them).
//
// ValidStatuses is in workflow order.
func ValidStatuses() []string {
	return []string{StatusPlanned, StatusInProgress, StatusInReview, StatusDone, StatusArchived}
}

// ValidTaskTypes returns the task types from the largest container down to the
// smallest leaf (the TaskTypeChain order). THEME and INITIATIVE appear even
// where a project has not enabled them — use Project.TaskTypeEnabled for the
// per-project check.
func ValidTaskTypes() []string {
	return []string{TaskTypeTheme, TaskTypeInitiative, TaskTypeEpic, TaskTypeStory, TaskTypeTask, TaskTypeSubtask}
}

// ValidPriorities returns the built-in priorities from lowest to highest.
func ValidPriorities() []string {
	return []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical, PriorityBlocker}
}

// EstimableTaskTypes returns the task types that may carry an effort estimate,
// as the ordered slice form of EstimableTaskType (which stays the predicate).
func EstimableTaskTypes() []string {
	return []string{TaskTypeStory, TaskTypeTask, TaskTypeSubtask}
}

// ValidEstimationUnits returns the estimation units in presentation order.
// GET /meta/enums serves exactly this slice, so the list a client is offered
// and the set ValidEstimationUnit accepts cannot drift apart.
func ValidEstimationUnits() []string {
	return []string{EstimationUnitNone, EstimationUnitPoints, EstimationUnitHours}
}

// ValidEstimationUnit reports whether s is a known estimation unit value.
func ValidEstimationUnit(s string) bool {
	switch s {
	case EstimationUnitNone, EstimationUnitPoints, EstimationUnitHours:
		return true
	}
	return false
}

// DefaultBoardLaneLimit is how many cards a lane draws before the rest load on
// scroll. MaxBoardLaneLimit bounds what a project may ask for: past it the cap
// stops being a cap, and the point of the setting is to keep a lane's DOM
// bounded. BoardLaneLimitUnlimited (0) opts out entirely and draws every card.
const (
	DefaultBoardLaneLimit   = 20
	MaxBoardLaneLimit       = 500
	BoardLaneLimitUnlimited = 0
)

// ValidBoardLaneLimit reports whether n is an acceptable board lane limit:
// 0 (unlimited) or 1..MaxBoardLaneLimit. Negative values are rejected rather
// than clamped — a client sending -1 has a bug, and silently storing 0 for it
// would turn that bug into "the setting does nothing", which is the hardest
// kind to notice.
func ValidBoardLaneLimit(n int) bool {
	return n >= 0 && n <= MaxBoardLaneLimit
}

// EstimableTaskType reports whether tasks of this type may carry an effort
// estimate. Estimates live on the leaf types that represent actual work;
// EPIC, INITIATIVE and THEME are containers, and their effort is the sum of
// what they contain, not a number somebody types. (A read-only roll-up of
// descendants onto containers is deliberately out of scope for now.)
func EstimableTaskType(s string) bool {
	switch s {
	case TaskTypeStory, TaskTypeTask, TaskTypeSubtask:
		return true
	}
	return false
}

// ValidateStoryPoints checks a story-point estimate against the free-scale
// bounds. nil (unestimated) is always valid.
func ValidateStoryPoints(p *int) error {
	if p == nil {
		return nil
	}
	if *p < MinStoryPoints || *p > MaxStoryPoints {
		return &DomainError{Code: "STORY_POINTS_INVALID", Message: fmt.Sprintf(
			"storyPoints must be an integer between %d and %d", MinStoryPoints, MaxStoryPoints)}
	}
	return nil
}

// ValidateEstimateHours checks an hours estimate against the free-scale bounds
// and the two-decimal precision the column stores. nil (unestimated) is always
// valid. NaN and ±Inf fail the range check, so they can never reach the DB.
func ValidateEstimateHours(h *float64) error {
	if h == nil {
		return nil
	}
	invalid := &DomainError{Code: "ESTIMATE_HOURS_INVALID", Message: fmt.Sprintf(
		"estimateHours must be a number between %g and %g with at most %d decimal places",
		MinEstimateHours, MaxEstimateHours, EstimateHoursDecimals)}
	if math.IsNaN(*h) || math.IsInf(*h, 0) || *h < MinEstimateHours || *h > MaxEstimateHours {
		return invalid
	}
	// Reject more precision than the column keeps, rather than silently
	// rounding it away and reading back a number the client never sent.
	scaled := *h * 100
	if math.Abs(scaled-math.Round(scaled)) > 1e-6 {
		return invalid
	}
	return nil
}

// ValidateTaskEstimate enforces the estimation rules on a task about to be
// written. wrotePoints/wroteHours say which estimate fields the request
// actually carried, which is what separates "set this value" from "left it
// alone": a dormant estimate stored under a unit the project no longer uses
// must not block an unrelated edit.
func ValidateTaskEstimate(project *Project, t *Task, wrotePoints, wroteHours bool) error {
	// A value may only be written in the unit the project switched on.
	// Clearing (null) stays allowed whatever the unit — otherwise switching a
	// project away from a unit would permanently freeze the estimates stored
	// under it, with no way to correct a wrong number afterwards.
	if wrotePoints && t.StoryPoints != nil && project.EstimationUnit != EstimationUnitPoints {
		return estimationUnitInactive(project, "storyPoints")
	}
	if wroteHours && t.EstimateHours != nil && project.EstimationUnit != EstimationUnitHours {
		return estimationUnitInactive(project, "estimateHours")
	}
	if wrotePoints {
		if err := ValidateStoryPoints(t.StoryPoints); err != nil {
			return err
		}
	}
	if wroteHours {
		if err := ValidateEstimateHours(t.EstimateHours); err != nil {
			return err
		}
	}
	// Containers are not estimated. Checking the *resulting* task rather than
	// just the incoming fields also catches the other direction: retyping an
	// already-estimated TASK into an EPIC, which would otherwise smuggle an
	// estimate onto a container without ever naming one in the request.
	if !EstimableTaskType(t.TaskType) && (t.StoryPoints != nil || t.EstimateHours != nil) {
		return &DomainError{Code: "ESTIMATION_NOT_ALLOWED_FOR_TYPE", Message: fmt.Sprintf(
			"%s is a container type and cannot carry an estimate — clear the estimate first",
			t.TaskType)}
	}
	return nil
}

// estimationUnitInactive builds the one stable error for "you sent an estimate
// this project does not currently keep", naming the active unit so the client
// can say something useful instead of guessing.
func estimationUnitInactive(project *Project, field string) error {
	reason := "estimation is switched off for this project"
	if project.EstimationUnit != EstimationUnitNone {
		reason = "this project estimates in " + project.EstimationUnit
	}
	return &DomainError{Code: "ESTIMATION_UNIT_INACTIVE", Message: field + " cannot be set: " + reason}
}

// TaskTypeEnabled reports whether the task type may be used in this project:
// the core EPIC → STORY → TASK → SUBTASK types always, THEME and INITIATIVE
// only when the project has switched them on in its settings.
func (p *Project) TaskTypeEnabled(s string) bool {
	switch s {
	case TaskTypeTheme:
		return p.ThemeEnabled
	case TaskTypeInitiative:
		return p.InitiativeEnabled
	}
	return ValidTaskType(s)
}

// TaskTypeChain returns the project's active hierarchy from the top down.
// THEME and INITIATIVE are opt-in levels above the always-on core chain; a
// type's parent is the type directly before it in this slice.
func (p *Project) TaskTypeChain() []string {
	chain := make([]string, 0, 6)
	if p.ThemeEnabled {
		chain = append(chain, TaskTypeTheme)
	}
	if p.InitiativeEnabled {
		chain = append(chain, TaskTypeInitiative)
	}
	return append(chain, TaskTypeEpic, TaskTypeStory, TaskTypeTask, TaskTypeSubtask)
}

// ParentTaskTypeFor returns the task type a parent of the given child type
// must have in the project's active chain, and whether a parent is mandatory.
// Parents are always exactly one hierarchy level up: only SUBTASK→TASK is
// mandatory, every other link is optional. The chain's top type — and a type
// the project has not enabled — returns ("", false): no parent allowed.
func ParentTaskTypeFor(p *Project, childType string) (parentType string, required bool) {
	chain := p.TaskTypeChain()
	for i, tt := range chain {
		if tt == childType {
			if i == 0 {
				return "", false
			}
			return chain[i-1], childType == TaskTypeSubtask
		}
	}
	return "", false
}

// ChildTaskTypeFor returns the only task type the children of a task of the
// given type may have in the project's active chain ("" when the type cannot
// have children, i.e. SUBTASK or a type the project has not enabled).
func ChildTaskTypeFor(p *Project, parentType string) string {
	chain := p.TaskTypeChain()
	for i, tt := range chain {
		if tt == parentType && i+1 < len(chain) {
			return chain[i+1]
		}
	}
	return ""
}

// ValidateTaskParent checks the hierarchy rules for a task of childType in
// project p against its prospective parent. parent is the resolved parent task
// (nil when no parent is set, or when the referenced ID was not found — pass
// parentSet=true in the latter case). Returns a *DomainError with a stable
// code on violation, nil when the combination is allowed.
func ValidateTaskParent(p *Project, childType, childID string, parent *Task, parentSet bool) error {
	parentType, required := ParentTaskTypeFor(p, childType)
	if parentType == "" && parentSet {
		return &DomainError{Code: "TASK_PARENT_NOT_ALLOWED", Message: "a " + strings.ToLower(childType) + " cannot have a parent task", Field: "parentId"}
	}
	if required && !parentSet {
		return &DomainError{Code: "TASK_PARENT_REQUIRED", Message: "a subtask requires a parent task", Field: "parentId"}
	}
	if !parentSet {
		return nil
	}
	if parent == nil || parent.ProjectID != p.ID || parent.ID == childID {
		return &DomainError{Code: "TASK_PARENT_INVALID", Message: "parent task not found in this project", Field: "parentId"}
	}
	if parent.TaskType != parentType {
		return &DomainError{Code: "TASK_PARENT_TYPE_INVALID", Message: "parent of a " + strings.ToLower(childType) + " must be a " + strings.ToLower(parentType), Field: "parentId"}
	}
	return nil
}

// ProjectPriority is an admin-defined additional task priority for one
// project, extending the built-in LOW/MEDIUM/HIGH/CRITICAL/BLOCKER set. Tasks and
// templates store the name in their priority column.
type ProjectPriority struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Rank      int    `json:"rank"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ValidPriorityName reports whether s is acceptable as a custom priority
// name: 1–20 chars, uppercase letters/digits/underscore, starting with a
// letter. Handlers normalize (trim + upper) before calling.
func ValidPriorityName(s string) bool {
	if len(s) < 1 || len(s) > 20 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case i > 0 && (r == '_' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}

// ValidPriority reports whether s is a built-in task priority value.
// Projects can define additional priorities (project_priorities); use
// Handler.priorityAllowed for the per-project check.
func ValidPriority(s string) bool {
	switch s {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical, PriorityBlocker:
		return true
	}
	return false
}

// ValidateTaskInput validates task title and description, returning a *DomainError
// when either value violates a domain constraint. The description length is
// checked against the constrained-HTML upper bound; callers must additionally
// run the description through SanitizeDescriptionHTML before persisting (see
// CleanTaskDescription) since the server is the source of truth for stored
// markup.
func ValidateTaskInput(title, description string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return &DomainError{Code: "TASK_TITLE_REQUIRED", Message: "task title must not be blank", Field: "title"}
	}
	if len(trimmed) > 255 {
		return &DomainError{Code: "TASK_TITLE_TOO_LONG", Message: "task title must not exceed 255 characters", Field: "title"}
	}
	if len(description) > 50000 {
		return &DomainError{Code: "DESCRIPTION_TOO_LONG", Message: "task description must not exceed 50 000 characters", Field: "description"}
	}
	return nil
}

// CleanTaskDescription sanitizes a task description against the HTML allowlist.
// It is the single chokepoint applied on every write path (create, update, copy,
// template instantiation, CSV import) so untrusted markup can never be stored.
func CleanTaskDescription(description string) string {
	return SanitizeDescriptionHTML(description)
}

// ValidateCommentInput validates comment text, returning a *DomainError when
// the text violates a domain constraint.
func ValidateCommentInput(text string) error {
	if len(text) == 0 {
		return &DomainError{Code: "COMMENT_INVALID", Message: "comment text must not be empty", Field: "text"}
	}
	if len(text) > 10000 {
		return &DomainError{Code: "COMMENT_INVALID", Message: "comment text must not exceed 10 000 characters", Field: "text"}
	}
	return nil
}

// MaxAbbreviationLen bounds a project abbreviation. Abbreviations are short,
// uppercase, alphanumeric task-key prefixes (e.g. "OCTB") rendered into task
// keys throughout the UI; the bound and charset are a security control as much
// as a formatting one (see ValidAbbreviation).
const MaxAbbreviationLen = 10

// isAbbrevChar reports whether c is an allowed abbreviation character: an ASCII
// uppercase letter or digit.
func isAbbrevChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ValidAbbreviation reports whether s is an acceptable project abbreviation:
// 1..MaxAbbreviationLen characters, each an ASCII uppercase letter or digit.
// Callers uppercase user input before validating.
func ValidAbbreviation(s string) bool {
	if len(s) == 0 || len(s) > MaxAbbreviationLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAbbrevChar(s[i]) {
			return false
		}
	}
	return true
}

// sanitizeAbbreviation uppercases s and keeps only ASCII letters/digits,
// truncated to MaxAbbreviationLen. Returns "PRJ" when nothing usable remains so
// a derived abbreviation is always a valid task-key prefix.
func sanitizeAbbreviation(s string) string {
	up := strings.ToUpper(s)
	var b []byte
	for i := 0; i < len(up) && len(b) < MaxAbbreviationLen; i++ {
		if isAbbrevChar(up[i]) {
			b = append(b, up[i])
		}
	}
	if len(b) == 0 {
		return "PRJ"
	}
	return string(b)
}

// AbbreviationFromName generates a short uppercase prefix from a project name.
// Multi-word names use the first letter of each word (up to 4); single-word
// names use the first two letters. The result is sanitized to be a valid
// abbreviation (uppercase alphanumeric, bounded) regardless of the input.
func AbbreviationFromName(name string) string {
	words := strings.Fields(name)
	var raw string
	switch {
	case len(words) >= 2:
		var abbr []byte
		for _, w := range words {
			if len(abbr) >= 4 {
				break
			}
			if len(w) > 0 {
				abbr = append(abbr, w[0])
			}
		}
		raw = string(abbr)
	case len(name) >= 2:
		raw = name[:2]
	default:
		raw = name
	}
	return sanitizeAbbreviation(raw)
}

// SlugFromName generates a URL-safe slug from a name by lowercasing,
// replacing non-alphanumeric characters with hyphens, and collapsing
// consecutive hyphens.
func SlugFromName(name string) string {
	s := strings.ToLower(name)
	var out []rune
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	result := strings.Trim(string(out), "-")
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return result
}
