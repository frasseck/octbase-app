package workmanagement

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/octbase/octbase-api/internal/shared"
)

// Import of the manifest's planning sections — releases, sprints, boards with
// their columns, task categories and task templates (see project_export.go).
// Without them an imported project was a flat backlog: the tasks arrived but
// every release, sprint and board card they had been placed in was gone.
//
// The sections are planned before the tasks, because a task's releaseId,
// sprintId and boardColumnId are resolved through the ID maps built here. Every
// section is optional — an archive written before they existed imports exactly
// as it did before.
//
// A manifest is attacker-controllable input, so each section is bounded and
// each field validated the way its interactive endpoint validates it. Two
// project-level invariants are additionally protected against an archive that
// would break them in the *target* project:
//
//   - at most one default board — an imported default board is demoted when the
//     target project already has one;
//   - at most one ACTIVE sprint, and no two sprints with overlapping dates —
//     an imported ACTIVE sprint is demoted to PLANNED, and overlapping dates are
//     dropped, rather than failing the whole import.
const (
	maxImportReleases   = 1000
	maxImportSprints    = 1000
	maxImportBoards     = 100
	maxImportCategories = 1000
	maxImportTemplates  = 1000
	// maxImportName bounds a release/sprint/board/column/category/template name.
	// Same 255 characters ValidateTaskInput allows a task title.
	maxImportName = 255
	// maxImportText bounds the free-text fields (goals, descriptions, template
	// bodies), matching the task description limit.
	maxImportText = 50000
	// maxImportBoardRank keeps an imported rank inside the range the board
	// ranking arithmetic works in.
	maxImportBoardRank = 1 << 30
)

// plannedBoard pairs a board row with the columns to insert for it.
type plannedBoard struct {
	board   *Board
	columns []*BoardColumn
}

// planPlanning validates and remaps the five planning sections. It runs before
// planTask so the ID maps it fills are available when tasks are placed.
func (imp *projectImporter) planPlanning() error {
	if err := imp.planReleases(); err != nil {
		return err
	}
	if err := imp.planSprints(); err != nil {
		return err
	}
	if err := imp.planBoards(); err != nil {
		return err
	}
	imp.planCategories()
	imp.planTemplates()
	return nil
}

func (imp *projectImporter) planReleases() error {
	for i, er := range imp.manifest.Releases {
		if i >= maxImportReleases {
			imp.warnOnce("releases", fmt.Sprintf("only the first %d releases were imported", maxImportReleases))
			break
		}
		name := boundedName(er.Name)
		if name == "" {
			imp.warnOnce("releases", "release without a name skipped")
			continue
		}
		status := er.Status
		if status != StatusPlanned && status != StatusClosed {
			status = StatusPlanned
		}
		rel := &Release{
			ID:        shared.NewUUID(),
			ProjectID: imp.projectID,
			Name:      name,
			Goal:      boundedText(er.Goal),
			DueDate:   importedDate(er.DueDate),
			Status:    status,
			CreatedAt: normalizeTimestamp(er.CreatedAt, imp.now),
			UpdatedAt: normalizeTimestamp(er.UpdatedAt, imp.now),
			Version:   1,
		}
		if er.ID != "" {
			imp.releaseIDMap[er.ID] = rel.ID
		}
		imp.releases = append(imp.releases, rel)
	}
	return nil
}

// planSprints restores the sprints, including their velocity snapshot: a
// completed sprint whose counts were dropped would re-import as a sprint that
// apparently delivered nothing.
func (imp *projectImporter) planSprints() error {
	if len(imp.manifest.Sprints) == 0 {
		return nil
	}
	// The one-ACTIVE-sprint and no-overlap rules are project-wide, so the
	// destination's own sprints are part of the picture. Read once, before the
	// transaction — nothing inside persist() may touch the connection pool.
	existing, err := imp.h.sprints.ListByProject(imp.projectID)
	if err != nil {
		return err
	}
	activeTaken := false
	type span struct{ start, end string }
	var spans []span
	for _, s := range existing {
		if s.Status == SprintStatusActive {
			activeTaken = true
		}
		if s.StartDate != nil && s.EndDate != nil {
			spans = append(spans, span{*s.StartDate, *s.EndDate})
		}
	}

	for i, es := range imp.manifest.Sprints {
		if i >= maxImportSprints {
			imp.warnOnce("sprints", fmt.Sprintf("only the first %d sprints were imported", maxImportSprints))
			break
		}
		name := boundedName(es.Name)
		if name == "" {
			imp.warnOnce("sprints", "sprint without a name skipped")
			continue
		}
		status := es.Status
		switch status {
		case SprintStatusPlanned, SprintStatusActive, SprintStatusCompleted:
		default:
			status = SprintStatusPlanned
		}
		if status == SprintStatusActive && activeTaken {
			imp.warnOnce(name, "another sprint is already active in this project; imported as planned")
			status = SprintStatusPlanned
		}
		if status == SprintStatusActive {
			activeTaken = true
		}

		start, end := importedDate(es.StartDate), importedDate(es.EndDate)
		if start != nil && end != nil {
			switch {
			case *start > *end:
				imp.warnOnce(name, "sprint end date precedes its start date; imported without dates")
				start, end = nil, nil
			default:
				for _, sp := range spans {
					if *start <= sp.end && sp.start <= *end {
						imp.warnOnce(name, "sprint dates overlap an existing sprint; imported without dates")
						start, end = nil, nil
						break
					}
				}
			}
		}
		if start != nil && end != nil {
			spans = append(spans, span{*start, *end})
		}

		s := &Sprint{
			ID:                shared.NewUUID(),
			ProjectID:         imp.projectID,
			Name:              name,
			Goal:              boundedText(es.Goal),
			StartDate:         start,
			EndDate:           end,
			Status:            status,
			ReleaseID:         imp.mappedID(imp.releaseIDMap, es.ReleaseID),
			CommittedCount:    nonNegative(es.CommittedCount),
			CompletedCount:    nonNegative(es.CompletedCount),
			CommittedEstimate: es.CommittedEstimate,
			CompletedEstimate: es.CompletedEstimate,
			CreatedAt:         normalizeTimestamp(es.CreatedAt, imp.now),
			UpdatedAt:         normalizeTimestamp(es.UpdatedAt, imp.now),
			Version:           1,
		}
		if ValidEstimationUnit(es.EstimateUnit) && es.EstimateUnit != EstimationUnitNone {
			unit := es.EstimateUnit
			s.EstimateUnit = &unit
		}
		if s.EstimateUnit == nil {
			// A snapshot without its unit cannot be read as velocity, so the
			// two estimate halves go with it.
			s.CommittedEstimate, s.CompletedEstimate = nil, nil
		}
		if es.ID != "" {
			imp.sprintIDMap[es.ID] = s.ID
		}
		imp.sprints = append(imp.sprints, s)
	}
	return nil
}

// planBoards restores the boards and their lanes. Board *external* columns are
// not part of the archive — they are read-only views of columns in other
// projects, which an import has nothing to point at.
func (imp *projectImporter) planBoards() error {
	if len(imp.manifest.Boards) == 0 {
		return nil
	}
	// Whether the target project already has a default board decides whether an
	// imported default board keeps that flag.
	def, err := imp.h.boards.FindDefault(imp.projectID)
	if err != nil {
		return err
	}
	defaultTaken := def != nil

	for i, eb := range imp.manifest.Boards {
		if i >= maxImportBoards {
			imp.warnOnce("boards", fmt.Sprintf("only the first %d boards were imported", maxImportBoards))
			break
		}
		name := boundedName(eb.Name)
		if name == "" {
			imp.warnOnce("boards", "board without a name skipped")
			continue
		}
		boardID := shared.NewUUID()
		var cols []*BoardColumn
		for _, ec := range eb.Columns {
			if len(cols) >= BoardMaxLanes {
				imp.warnOnce(name, fmt.Sprintf("board has more than %d lanes; the extra lanes were skipped", BoardMaxLanes))
				break
			}
			colName := boundedName(ec.Name)
			status := strings.TrimSpace(ec.Status)
			if colName == "" || !ValidStatusShape(status) {
				imp.warnOnce(name, "board lane without a name or status skipped")
				continue
			}
			cols = append(cols, &BoardColumn{
				ID:        shared.NewUUID(),
				BoardID:   boardID,
				Name:      colName,
				Status:    status,
				Position:  len(cols),
				CreatedAt: normalizeTimestamp(ec.CreatedAt, imp.now),
				UpdatedAt: normalizeTimestamp(ec.UpdatedAt, imp.now),
			})
			imp.laneStatuses[status] = true
			if ec.ID != "" {
				imp.columnIDMap[ec.ID] = cols[len(cols)-1].ID
			}
		}

		minCols, maxCols := eb.MinColumns, eb.MaxColumns
		if ValidateLaneLimits(minCols, maxCols) != nil {
			minCols, maxCols = DefaultBoardMinColumns, DefaultBoardMaxColumns
		}
		if maxCols < len(cols) {
			maxCols = len(cols)
		}
		b := &Board{
			ID:            boardID,
			ProjectID:     imp.projectID,
			Name:          name,
			IsDefault:     eb.IsDefault && !defaultTaken,
			MinColumns:    minCols,
			MaxColumns:    maxCols,
			IsSprintBoard: eb.IsSprintBoard,
			SprintID:      imp.mappedID(imp.sprintIDMap, eb.SprintID),
			CreatedAt:     normalizeTimestamp(eb.CreatedAt, imp.now),
			UpdatedAt:     normalizeTimestamp(eb.UpdatedAt, imp.now),
			Version:       1,
		}
		if eb.IsDefault && defaultTaken {
			imp.warnOnce(name, "this project already has a default board; imported as an additional board")
		}
		if b.IsDefault {
			defaultTaken = true
		}
		if b.IsSprintBoard && b.SprintID == nil {
			// A sprint board whose sprint did not travel is an ordinary board;
			// leaving the flag on would make the sprint view look for a sprint
			// that does not exist.
			imp.warnOnce(name, "sprint board without its sprint; imported as a normal board")
			b.IsSprintBoard = false
		}
		imp.boards = append(imp.boards, &plannedBoard{board: b, columns: cols})
	}
	return nil
}

func (imp *projectImporter) planCategories() {
	for i, ec := range imp.manifest.Categories {
		if i >= maxImportCategories {
			imp.warnOnce("categories", fmt.Sprintf("only the first %d categories were imported", maxImportCategories))
			break
		}
		name := boundedName(ec.Name)
		if name == "" {
			imp.warnOnce("categories", "category without a name skipped")
			continue
		}
		color := boundedName(ec.Color)
		if color == "" {
			color = "gray"
		}
		imp.categories = append(imp.categories, &TaskCategory{
			ID:          shared.NewUUID(),
			ProjectID:   imp.projectID,
			Name:        name,
			Description: boundedText(ec.Description),
			Color:       color,
			CreatedAt:   normalizeTimestamp(ec.CreatedAt, imp.now),
			UpdatedAt:   normalizeTimestamp(ec.UpdatedAt, imp.now),
			Version:     1,
		})
	}
}

func (imp *projectImporter) planTemplates() {
	for i, et := range imp.manifest.Templates {
		if i >= maxImportTemplates {
			imp.warnOnce("templates", fmt.Sprintf("only the first %d templates were imported", maxImportTemplates))
			break
		}
		name := boundedName(et.Name)
		if name == "" {
			imp.warnOnce("templates", "template without a name skipped")
			continue
		}
		// A template instantiates without a parent, so SUBTASK — which requires
		// one — is not a valid template type either, exactly as CreateTemplate
		// rules it out.
		taskType := et.TaskType
		if !validImportTaskType[taskType] || taskType == TaskTypeSubtask {
			taskType = TaskTypeTask
		}
		priority := et.Priority
		if !validImportPriority[priority] {
			priority = PriorityMedium
		}
		imp.templates = append(imp.templates, &TaskTemplate{
			ID:        shared.NewUUID(),
			ProjectID: imp.projectID,
			Name:      name,
			// The title is a template, not a task title, so it may legitimately
			// be blank; its body goes through the same HTML allowlist a task
			// description does.
			TitleTemplate:       boundedName(et.TitleTemplate),
			DescriptionTemplate: CleanTaskDescription(boundedText(et.DescriptionTemplate)),
			TaskType:            taskType,
			Priority:            priority,
			CreatedAt:           normalizeTimestamp(et.CreatedAt, imp.now),
			UpdatedAt:           normalizeTimestamp(et.UpdatedAt, imp.now),
			Version:             1,
		})
	}
}

// persistPlanningTx inserts the planning rows. Order matters: sprints reference
// releases, boards reference sprints, and the tasks inserted afterwards
// reference all three.
func (imp *projectImporter) persistPlanningTx(tx *sql.Tx) error {
	for _, rel := range imp.releases {
		if err := imp.h.releases.CreateTx(tx, rel); err != nil {
			return fmt.Errorf("create release %q: %w", rel.Name, err)
		}
	}
	for _, s := range imp.sprints {
		if err := imp.h.sprints.CreateTx(tx, s); err != nil {
			return fmt.Errorf("create sprint %q: %w", s.Name, err)
		}
	}
	for _, pb := range imp.boards {
		if err := imp.h.boards.CreateTx(tx, pb.board); err != nil {
			return fmt.Errorf("create board %q: %w", pb.board.Name, err)
		}
		for _, c := range pb.columns {
			if err := imp.h.columns.CreateTx(tx, c); err != nil {
				return fmt.Errorf("create board lane %q: %w", c.Name, err)
			}
		}
	}
	for _, c := range imp.categories {
		if err := imp.h.categories.CreateTx(tx, c); err != nil {
			return fmt.Errorf("create category %q: %w", c.Name, err)
		}
	}
	for _, t := range imp.templates {
		if err := imp.h.templates.CreateTx(tx, t); err != nil {
			return fmt.Errorf("create template %q: %w", t.Name, err)
		}
	}
	return nil
}

// mappedID resolves an exported ID through one of the import's ID maps,
// returning nil when the archive references something it does not carry.
func (imp *projectImporter) mappedID(m map[string]string, exportedID string) *string {
	if exportedID == "" {
		return nil
	}
	if newID, ok := m[exportedID]; ok {
		return &newID
	}
	return nil
}

// importedTaskStatus keeps a task's exported status when this build knows it,
// or when one of the imported board lanes defines it as a custom status. A task
// that lands in a lane whose status it no longer matches would otherwise show
// up in one place on the board and read as something else in the task list, so
// the mapped lane's own status is the next-best answer.
func (imp *projectImporter) importedTaskStatus(status, boardColumnID string) string {
	if validImportStatus[status] || (imp.laneStatuses[status] && ValidStatusShape(status)) {
		return status
	}
	if boardColumnID != "" {
		for _, pb := range imp.boards {
			for _, c := range pb.columns {
				if c.ID == boardColumnID {
					return c.Status
				}
			}
		}
	}
	return StatusPlanned
}

// importedBoardRank keeps an exported rank when it is inside the range the
// board arithmetic works in; anything else takes the default rank.
func importedBoardRank(rank int) int {
	if rank <= 0 || rank > maxImportBoardRank {
		return DefaultBoardRank
	}
	return rank
}

// importedDate keeps a YYYY-MM-DD date, dropping anything else.
func importedDate(d string) *string {
	if d == "" {
		return nil
	}
	if _, err := time.Parse(internalDueDateLayout, d); err != nil {
		return nil
	}
	return &d
}

func boundedName(s string) string {
	return truncateBytes(strings.TrimSpace(s), maxImportName)
}

func boundedText(s string) string {
	return truncateBytes(s, maxImportText)
}

// truncateBytes cuts s to at most n bytes without splitting a rune. Cutting
// mid-rune would store an invalid UTF-8 sequence, which PostgreSQL rejects —
// one over-long name would fail the whole import transaction.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
