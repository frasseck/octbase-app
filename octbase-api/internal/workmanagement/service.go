package workmanagement

import (
	"database/sql"
	"fmt"

	"github.com/octbase/octbase-api/internal/shared"
)

// taskStore is the subset of TaskRepo methods required by Service.
type taskStore interface {
	FindByID(id string) (*Task, error)
	Create(t *Task) error
	CreateTx(tx *sql.Tx, t *Task) error
	CountOpenForRelease(releaseID string) (int, error)
}

// commentStore is the subset of TaskCommentRepo methods required by Service.
type commentStore interface {
	ListByTask(taskID string) ([]TaskComment, error)
	CreateTx(tx *sql.Tx, c *TaskComment) error
}

// linkStore is the subset of TaskLinkRepo methods required by Service.
type linkStore interface {
	ListByTask(taskID string) ([]TaskLink, error)
	CreateTx(tx *sql.Tx, l *TaskLink) error
}

// attachmentStore is the subset of TaskAttachmentRepo methods required by Service.
type attachmentStore interface {
	ListByTask(taskID string) ([]TaskAttachment, error)
	CreateTx(tx *sql.Tx, a *TaskAttachment) error
}

// relationStore is the subset of TaskRelationRepo methods required by Service.
type relationStore interface {
	CreateTx(tx *sql.Tx, rel *TaskRelation) error
	FindByID(id string) (*TaskRelation, error)
	ListByTask(taskID string) ([]TaskRelation, error)
	Exists(sourceID, targetID, relType string) (bool, error)
	HasCycle(sourceID, targetID string) (bool, error)
	DeleteInverseTx(tx *sql.Tx, sourceID, targetID, relType string) error
}

// releaseStore is the subset of ReleaseRepo methods required by Service.
type releaseStore interface {
	CloseGuarded(m *Release) error
}

// columnStore is the subset of BoardColumnRepo methods required by Service.
type columnStore interface {
	StatusExistsForBoard(boardID, status string) (bool, error)
	CountByBoard(boardID string) (int, error)
	Create(c *BoardColumn) error
	DeleteDetachingTasks(boardID, id, now string) (bool, []string, error)
	UpdateRetagTasks(c *BoardColumn, oldStatus, now string) ([]string, error)
	ListByBoard(boardID string) ([]BoardColumn, error)
	CreateTx(tx *sql.Tx, c *BoardColumn) error
}

// boardStore is the subset of BoardRepo methods required by Service.
type boardStore interface {
	FindByID(id string) (*Board, error)
	FindDefault(projectID string) (*Board, error)
	FindBySprint(sprintID string) (*Board, error)
	CreateTx(tx *sql.Tx, b *Board) error
	Delete(id, now string) ([]string, error)
	DeleteTx(tx *sql.Tx, id, now string) ([]string, error)
}

// sprintStore is the subset of SprintRepo methods required by Service.
type sprintStore interface {
	FindActive(projectID string) (*Sprint, error)
	Update(s *Sprint) error
	UpdateTx(tx *sql.Tx, s *Sprint) error
	ClearIncompleteTasksTx(tx *sql.Tx, sprintID string) error
	CountTasks(sprintID string) (total, done int, err error)
	SumEstimates(sprintID, unit string) (committed, completed float64, err error)
}

// templateStore is the subset of TaskTemplateRepo methods required by Service.
type templateStore interface {
	FindByID(id string) (*TaskTemplate, error)
}

// Service contains application-level business logic.
type Service struct {
	db          *sql.DB
	tasks       taskStore
	comments    commentStore
	links       linkStore
	attachments attachmentStore
	relations   relationStore
	releases    releaseStore
	boards      boardStore
	columns     columnStore
	sprints     sprintStore
	templates   templateStore
	// storage, when set, lets CopyTask duplicate uploaded attachment bytes so a
	// copied task's files have an independent lifecycle. nil disables file copy
	// (link-style attachments still copy).
	storage *AttachmentStorage
}

func NewService(
	db *sql.DB,
	tasks *TaskRepo,
	comments *TaskCommentRepo,
	links *TaskLinkRepo,
	attachments *TaskAttachmentRepo,
	relations *TaskRelationRepo,
	releases *ReleaseRepo,
	boards *BoardRepo,
	columns *BoardColumnRepo,
	sprints *SprintRepo,
	templates *TaskTemplateRepo,
) *Service {
	return &Service{
		db: db, tasks: tasks, comments: comments, links: links,
		attachments: attachments, relations: relations, releases: releases,
		boards: boards, columns: columns, sprints: sprints, templates: templates,
	}
}

// AddRelation validates and creates a task relation plus its symmetric inverse
// in a single transaction.
func (s *Service) AddRelation(rel *TaskRelation) error {
	if rel.SourceTaskID == rel.TargetTaskID {
		return &DomainError{Code: "TASK_SELF_RELATION", Message: "a task cannot relate to itself"}
	}
	if !IsValidRelationType(rel.RelationType) {
		return &DomainError{
			Code:    "TASK_RELATION_TYPE_INVALID",
			Message: "relation type must be one of RELATES_TO, BLOCKS, BLOCKED_BY, DUPLICATES",
			Field:   "relationType",
		}
	}
	exists, err := s.relations.Exists(rel.SourceTaskID, rel.TargetTaskID, rel.RelationType)
	if err != nil {
		return err
	}
	if exists {
		return &DomainError{Code: "TASK_RELATION_DUPLICATE", Message: "this relation already exists"}
	}
	// Cycle-check the effective BLOCKS edge, not the request direction. A
	// BLOCKED_BY request writes a BLOCKS row as its inverse, so checking only
	// RelationBlocks here let the same cycle in through the other end.
	if blocksSource, blocksTarget, ok := blocksEdge(rel); ok {
		cycle, err := s.relations.HasCycle(blocksSource, blocksTarget)
		if err != nil {
			return err
		}
		if cycle {
			return &DomainError{Code: "TASK_RELATION_CYCLE", Message: "adding this BLOCKS relation would create a cycle"}
		}
	}

	inverseType := inverseRelationType(rel.RelationType)
	inverse := &TaskRelation{
		ID:           shared.NewUUID(),
		SourceTaskID: rel.TargetTaskID,
		TargetTaskID: rel.SourceTaskID,
		RelationType: inverseType,
		CreatedAt:    rel.CreatedAt,
	}

	return shared.WithTx(s.db, func(tx *sql.Tx) error {
		if err := s.relations.CreateTx(tx, rel); err != nil {
			return err
		}
		return s.relations.CreateTx(tx, inverse)
	})
}

// DeleteRelation removes a relation and its symmetric inverse in a transaction,
// reporting whether a relation was deleted. taskID is the (already
// membership-guarded) task the request was scoped to; the relation must have
// that task as one of its endpoints, otherwise a writer on one task could
// delete relations connecting two other projects' tasks. A missing relation
// and one that belongs to other tasks both report false — the handler maps
// that to 404 via shared.RequireFound, so a caller cannot tell a never-existed
// id from one it simply may not touch (the same parent-scoped deleted-bool
// contract as the sibling DeleteLink / DeleteExternalColumn paths).
func (s *Service) DeleteRelation(taskID, id string) (bool, error) {
	rel, err := s.relations.FindByID(id)
	if err != nil {
		return false, err
	}
	if rel == nil || (rel.SourceTaskID != taskID && rel.TargetTaskID != taskID) {
		return false, nil
	}
	inverseType := inverseRelationType(rel.RelationType)
	err = shared.WithTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM task_relations WHERE id = $1`, id); err != nil {
			return fmt.Errorf("delete relation: %w", err)
		}
		return s.relations.DeleteInverseTx(tx, rel.TargetTaskID, rel.SourceTaskID, inverseType)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// blocksEdge returns the source and target of the BLOCKS edge that adding rel
// would create, and whether it creates one at all. A BLOCKS relation is that
// edge directly; a BLOCKED_BY relation creates it as its inverse, pointing the
// other way. RELATES_TO and DUPLICATES create no BLOCKS edge.
func blocksEdge(rel *TaskRelation) (source, target string, ok bool) {
	switch rel.RelationType {
	case RelationBlocks:
		return rel.SourceTaskID, rel.TargetTaskID, true
	case RelationBlockedBy:
		return rel.TargetTaskID, rel.SourceTaskID, true
	}
	return "", "", false
}

// inverseRelationType returns the symmetric relation type for a given type.
func inverseRelationType(rt string) string {
	switch rt {
	case RelationBlocks:
		return RelationBlockedBy
	case RelationBlockedBy:
		return RelationBlocks
	default:
		// RELATES_TO and DUPLICATES are self-inverse.
		return rt
	}
}

// AddBoardColumn validates column status uniqueness and the board's maximum lane
// limit, then creates the column.
func (s *Service) AddBoardColumn(c *BoardColumn) error {
	b, err := s.boards.FindByID(c.BoardID)
	if err != nil {
		return err
	}
	if b == nil {
		return &DomainError{Code: "BOARD_NOT_FOUND", Message: "board not found"}
	}
	// A lane's status doubles as a task status, so keep it well-shaped: a
	// custom lane name longer than MaxStatusLength would produce an unusable
	// status string.
	if !ValidStatusShape(c.Status) {
		return &DomainError{Code: "COLUMN_STATUS_INVALID", Message: fmt.Sprintf("lane status must be 1 to %d characters", MaxStatusLength), Field: "status"}
	}
	count, err := s.columns.CountByBoard(c.BoardID)
	if err != nil {
		return err
	}
	if count >= b.MaxColumns {
		return &DomainError{Code: "BOARD_MAX_LANES", Message: fmt.Sprintf("board cannot have more than %d lanes", b.MaxColumns)}
	}
	exists, err := s.columns.StatusExistsForBoard(c.BoardID, c.Status)
	if err != nil {
		return err
	}
	if exists {
		return &DomainError{Code: "COLUMN_STATUS_DUPLICATE", Message: "a column with this status already exists on this board"}
	}
	return s.columns.Create(c)
}

// RemoveBoardColumn deletes a column while enforcing the board's minimum lane
// limit, so a board never drops below its configured floor. The lane's tasks
// are detached back to the backlog in the same transaction, and the delete is
// scoped to the board so the caller's writer guard on this board cannot be
// leveraged against another board's lanes.
//
// The detached tasks are reset to PLANNED (OCT-304) — they keep their status
// only if it is DONE or ARCHIVED. Returns the ids that were reset so the caller
// can log per-task activity, like UpdateBoardColumn does for a retag.
func (s *Service) RemoveBoardColumn(boardID, columnID string) ([]string, error) {
	b, err := s.boards.FindByID(boardID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, &DomainError{Code: "BOARD_NOT_FOUND", Message: "board not found"}
	}
	count, err := s.columns.CountByBoard(boardID)
	if err != nil {
		return nil, err
	}
	if count <= b.MinColumns {
		return nil, &DomainError{Code: "BOARD_MIN_LANES", Message: fmt.Sprintf("board must have at least %d lane(s)", b.MinColumns)}
	}
	deleted, reset, err := s.columns.DeleteDetachingTasks(boardID, columnID, shared.Now())
	if err != nil {
		return nil, err
	}
	if !deleted {
		return nil, &DomainError{Code: "COLUMN_NOT_FOUND", Message: "column not found"}
	}
	return reset, nil
}

// UpdateBoardColumn validates a column edit the same way AddBoardColumn
// validates a create — status shape and per-board status uniqueness — then
// persists it, retagging the lane's tasks when the status changed. Returns the
// ids of the retagged tasks so the handler can log per-task activity.
func (s *Service) UpdateBoardColumn(c *BoardColumn, oldStatus string) ([]string, error) {
	if !ValidStatusShape(c.Status) {
		return nil, &DomainError{Code: "COLUMN_STATUS_INVALID", Message: fmt.Sprintf("lane status must be 1 to %d characters", MaxStatusLength), Field: "status"}
	}
	if c.Status != oldStatus {
		exists, err := s.columns.StatusExistsForBoard(c.BoardID, c.Status)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, &DomainError{Code: "COLUMN_STATUS_DUPLICATE", Message: "a column with this status already exists on this board"}
		}
	}
	return s.columns.UpdateRetagTasks(c, oldStatus, shared.Now())
}

// StartSprint activates a sprint and provisions its sprint board. The
// one-active-sprint-per-project rule (a sprint becomes "running" only when
// ACTIVE) is what keeps at most one sprint board live at a time.
func (s *Service) StartSprint(sp *Sprint) error {
	active, err := s.sprints.FindActive(sp.ProjectID)
	if err != nil {
		return err
	}
	if active != nil && active.ID != sp.ID {
		return &DomainError{Code: "SPRINT_ALREADY_ACTIVE", Message: "another sprint is already active for this project"}
	}
	sp.Status = SprintStatusActive
	sp.UpdatedAt = shared.Now()
	if err := s.sprints.Update(sp); err != nil {
		// The check above is only advisory under concurrency; the partial unique
		// index idx_sprints_one_active is what actually enforces the rule, so a
		// concurrent start surfaces here as a unique violation.
		if shared.IsUniqueViolation(err) {
			return &DomainError{Code: "SPRINT_ALREADY_ACTIVE", Message: "another sprint is already active for this project"}
		}
		return err
	}
	return s.provisionSprintBoard(sp)
}

// CompleteSprint closes a sprint, moves unfinished tasks back to the backlog,
// and tears down the sprint board (which only lives while the sprint runs).
// The board scope (total tasks and how many were DONE) is snapshotted onto the
// sprint *before* the unfinished tasks are unlinked, so the completed sprint can
// still report e.g. "2/5" rather than collapsing to "2/2". When the project
// estimates, the same scope is snapshotted in effort — together with the unit
// it was measured in, so a later project-level unit switch cannot retroactively
// relabel this sprint's velocity.
// estimationUnit is the project's unit at completion time, passed in by the
// caller (which has already loaded the project) rather than re-read here.
// It returns the ids of the tasks whose status the board teardown reset, so the
// caller can log per-task activity. That is the carried-over work: tearing the
// sprint board down detaches its cards, and a detached task is reset to PLANNED
// (OCT-304), so unfinished work returns to the backlog as work not started
// rather than as work that claims to be under way with nowhere to be seen. The
// velocity snapshot above is taken before the teardown, so this cannot change a
// completed sprint's committed/completed numbers.
func (s *Service) CompleteSprint(sp *Sprint, estimationUnit string) ([]string, error) {
	total, done, err := s.sprints.CountTasks(sp.ID)
	if err != nil {
		return nil, err
	}
	if estimationUnit != EstimationUnitNone && estimationUnit != "" {
		unit := estimationUnit
		committedEffort, completedEffort, err := s.sprints.SumEstimates(sp.ID, unit)
		if err != nil {
			return nil, err
		}
		sp.CommittedEstimate = &committedEffort
		sp.CompletedEstimate = &completedEffort
		sp.EstimateUnit = &unit
	}
	board, err := s.boards.FindBySprint(sp.ID)
	if err != nil {
		return nil, err
	}
	sp.CommittedCount = total
	sp.CompletedCount = done
	sp.Status = SprintStatusCompleted
	sp.UpdatedAt = shared.Now()
	// One transaction so completion is all-or-nothing: the version-guarded
	// sprint update makes a concurrent double-complete (or complete racing any
	// sprint edit) roll the task unlinking and board teardown back with a 409
	// instead of leaving them applied under a sprint that is still ACTIVE.
	var reset []string
	err = shared.WithTx(s.db, func(tx *sql.Tx) error {
		if err := s.sprints.ClearIncompleteTasksTx(tx, sp.ID); err != nil {
			return err
		}
		if board != nil {
			r, err := s.boards.DeleteTx(tx, board.ID, sp.UpdatedAt)
			if err != nil {
				return err
			}
			reset = r
		}
		return s.sprints.UpdateTx(tx, sp)
	})
	if err != nil {
		// The transaction rolled back, so nothing was reset after all.
		return nil, err
	}
	return reset, nil
}

// provisionSprintBoard creates the board that belongs to a running sprint,
// seeded from the project's main (default) board's own lanes. Linked/external
// columns are deliberately excluded — only the board's own columns are copied,
// as a point-in-time snapshot. It is idempotent: if the sprint already owns a
// board, it does nothing. When the project has no default board (or it has no
// lanes), it falls back to a Scrum template so the sprint board is never empty.
func (s *Service) provisionSprintBoard(sp *Sprint) error {
	existing, err := s.boards.FindBySprint(sp.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	now := shared.Now()
	boardID := shared.NewUUID()
	minCols, maxCols := DefaultBoardMinColumns, DefaultBoardMaxColumns
	var cols []*BoardColumn
	def, err := s.boards.FindDefault(sp.ProjectID)
	if err != nil {
		return err
	}
	if def != nil {
		minCols, maxCols = def.MinColumns, def.MaxColumns
		srcCols, err := s.columns.ListByBoard(def.ID)
		if err != nil {
			return err
		}
		for i, c := range srcCols {
			cols = append(cols, &BoardColumn{
				ID: shared.NewUUID(), BoardID: boardID, Name: c.Name,
				Status: c.Status, Position: i, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	if len(cols) == 0 {
		cols = templateColumnsFor(boardID, BoardTemplateScrum, "en", now)
		if maxCols < len(cols) {
			maxCols = len(cols)
		}
	}
	sprintID := sp.ID
	b := &Board{
		ID: boardID, ProjectID: sp.ProjectID, Name: sp.Name,
		IsDefault: false, MinColumns: minCols, MaxColumns: maxCols,
		IsSprintBoard: true, SprintID: &sprintID,
		CreatedAt: now, UpdatedAt: now,
	}
	// Seed the board and its lanes atomically so a mid-seed failure never leaves
	// a sprint board with a partial column set.
	return shared.WithTx(s.db, func(tx *sql.Tx) error {
		if err := s.boards.CreateTx(tx, b); err != nil {
			return err
		}
		for _, c := range cols {
			if err := s.columns.CreateTx(tx, c); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveSprintBoard deletes the board owned by the given sprint, if any. It is
// idempotent (a no-op when the sprint owns no board) so completing or deleting
// a sprint more than once is safe. Returns the ids of the tasks whose status the
// detach reset, for the caller to log.
func (s *Service) RemoveSprintBoard(sprintID string) ([]string, error) {
	b, err := s.boards.FindBySprint(sprintID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return s.boards.Delete(b.ID, shared.Now())
}

// CloseRelease closes a release, rejecting if any non-terminal task exists.
// The close itself is atomic (CloseGuarded re-checks for open tasks in the
// UPDATE), so a task added to the release between the count and the write
// cannot be closed over; the count exists to tell the two zero-row causes —
// open tasks vs. a stale version — apart and report the right error.
func (s *Service) CloseRelease(m *Release) error {
	openTasksErr := func(count int) error {
		return &DomainError{Code: "RELEASE_HAS_OPEN_TASKS", Message: fmt.Sprintf("release has %d tasks that are not DONE or ARCHIVED", count)}
	}
	count, err := s.tasks.CountOpenForRelease(m.ID)
	if err != nil {
		return err
	}
	if count > 0 {
		return openTasksErr(count)
	}
	m.Status = StatusClosed
	m.UpdatedAt = shared.Now()
	if err := s.releases.CloseGuarded(m); err != nil {
		if err == shared.ErrVersionConflict {
			if count, cerr := s.tasks.CountOpenForRelease(m.ID); cerr == nil && count > 0 {
				return openTasksErr(count)
			}
		}
		return err
	}
	return nil
}

// CopyTask deep-copies a task with its comments, links, attachments, and
// relations in a single transaction. The copy starts in PLANNED status with no
// assignee. Activity logging is left to the caller.
func (s *Service) CopyTask(srcID, actorID string) (*Task, error) {
	src, err := s.tasks.FindByID(srcID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, &DomainError{Code: "TASK_NOT_FOUND", Message: "source task not found"}
	}

	seq, err := NextSeqNumber(s.db, src.ProjectID)
	if err != nil {
		return nil, err
	}
	now := shared.Now()
	cp := &Task{
		ID: shared.NewUUID(), ProjectID: src.ProjectID,
		Title: "Copy of " + src.Title, Description: src.Description,
		// The copy keeps the source's place in the hierarchy: same type, same
		// parent (a copied SUBTASK must never end up parentless).
		TaskType: src.TaskType, Status: StatusPlanned, Priority: src.Priority,
		ParentID: src.ParentID,
		// The estimate is part of what the task *is*, so a copy carries it —
		// both units, since the dormant one survives a unit switch on the
		// original and must survive being copied too.
		StoryPoints: src.StoryPoints, EstimateHours: src.EstimateHours,
		ReporterID: &actorID, SeqNumber: &seq, BoardRank: DefaultBoardRank,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}

	comments, err := s.comments.ListByTask(srcID)
	if err != nil {
		return nil, err
	}
	links, err := s.links.ListByTask(srcID)
	if err != nil {
		return nil, err
	}
	attachments, err := s.attachments.ListByTask(srcID)
	if err != nil {
		return nil, err
	}
	relations, err := s.relations.ListByTask(srcID)
	if err != nil {
		return nil, err
	}

	// Duplicate uploaded-file bytes so the copy owns independent storage keys.
	// Done before the DB transaction; on failure nothing is persisted. Keys
	// written here that end up unused (e.g. a later tx failure) are harmless
	// orphans tolerated by the storage layer.
	copiedKeys := make([]string, len(attachments))
	if s.storage != nil {
		for i, a := range attachments {
			if !a.IsUpload() {
				continue
			}
			newKey, copyErr := s.storage.Copy(a.StorageKey)
			if copyErr != nil {
				return nil, fmt.Errorf("copy attachment file: %w", copyErr)
			}
			copiedKeys[i] = newKey
		}
	}

	if err := shared.WithTx(s.db, func(tx *sql.Tx) error {
		if err := s.tasks.CreateTx(tx, cp); err != nil {
			return err
		}
		// Preserve the reply tree in the copy by remapping each parent reference
		// to the freshly-generated ID. ListByTask orders by created_at, so a
		// parent is always created before its replies and is in the map by then.
		commentIDMap := make(map[string]string, len(comments))
		for _, c := range comments {
			cc := c
			oldID := c.ID
			cc.ID = shared.NewUUID()
			cc.TaskID = cp.ID
			if cc.ParentID != nil {
				if mapped, ok := commentIDMap[*cc.ParentID]; ok {
					cc.ParentID = &mapped
				} else {
					cc.ParentID = nil
				}
			}
			commentIDMap[oldID] = cc.ID
			if err := s.comments.CreateTx(tx, &cc); err != nil {
				return err
			}
		}
		for _, l := range links {
			ll := l
			ll.ID = shared.NewUUID()
			ll.TaskID = cp.ID
			if err := s.links.CreateTx(tx, &ll); err != nil {
				return err
			}
		}
		for i, a := range attachments {
			aa := a
			aa.ID = shared.NewUUID()
			aa.TaskID = cp.ID
			if copiedKeys[i] != "" {
				aa.StorageKey = copiedKeys[i]
			}
			if err := s.attachments.CreateTx(tx, &aa); err != nil {
				return err
			}
		}
		for _, rel := range relations {
			rr := rel
			rr.ID = shared.NewUUID()
			if rr.SourceTaskID == srcID {
				rr.SourceTaskID = cp.ID
			}
			if rr.TargetTaskID == srcID {
				rr.TargetTaskID = cp.ID
			}
			if err := s.relations.CreateTx(tx, &rr); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return cp, nil
}

// InstantiateTemplate creates a new task from a template in a single
// transaction. Activity logging is left to the caller. If titleOverride is
// non-empty it is used as the task title, taking precedence over the
// template's titleTemplate (which itself falls back to the template's name).
func (s *Service) InstantiateTemplate(templateID, actorID, titleOverride string) (*Task, error) {
	tmpl, err := s.templates.FindByID(templateID)
	if err != nil {
		return nil, err
	}
	if tmpl == nil {
		return nil, &DomainError{Code: "TEMPLATE_NOT_FOUND", Message: "template not found"}
	}
	// A SUBTASK needs a parent at creation time and instantiation has none to
	// offer. Template writes reject the type, but rows predating that rule must
	// still fail cleanly here.
	if tmpl.TaskType == TaskTypeSubtask {
		return nil, &DomainError{Code: "TASK_PARENT_REQUIRED", Message: "a subtask requires a parent task; templates cannot create subtasks", Field: "taskType"}
	}

	seq, err := NextSeqNumber(s.db, tmpl.ProjectID)
	if err != nil {
		return nil, err
	}
	title := titleOverride
	if title == "" {
		title = tmpl.TitleTemplate
	}
	if title == "" {
		title = tmpl.Name
	}
	now := shared.Now()
	task := &Task{
		ID: shared.NewUUID(), ProjectID: tmpl.ProjectID, Title: title,
		Description: CleanTaskDescription(tmpl.DescriptionTemplate), TaskType: tmpl.TaskType,
		Status: StatusPlanned, Priority: tmpl.Priority, ReporterID: &actorID,
		SeqNumber: &seq, BoardRank: DefaultBoardRank,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	return task, s.tasks.Create(task)
}
