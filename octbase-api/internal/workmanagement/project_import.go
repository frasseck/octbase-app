package workmanagement

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/docs"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// Limits for the project ZIP import. Archives are attacker-controllable, so
// every dimension is bounded: total upload size, number of zip entries, the
// manifest size, and the entity counts (tasks share the CSV import's
// maxImportRows cap).
const (
	maxImportZipBytes      = 1 << 30   // 1 GiB whole archive
	maxImportManifestBytes = 128 << 20 // project.json alone
	maxImportZipEntries    = 20000
	maxImportPages         = 5000
	maxImportPageContent   = 500000 // characters of AsciiDoc source per page
)

// ProjectImportResult summarises a project ZIP import (or dry run).
type ProjectImportResult struct {
	DryRun      bool `json:"dryRun"`
	Tasks       int  `json:"tasks"`
	Comments    int  `json:"comments"`
	Links       int  `json:"links"`
	Attachments int  `json:"attachments"`
	Files       int  `json:"files"`
	Relations   int  `json:"relations"`
	Pages       int  `json:"pages"`
	// The planning sections restored alongside the tasks. Always present, 0
	// for an archive that predates them.
	Releases   int               `json:"releases"`
	Sprints    int               `json:"sprints"`
	Boards     int               `json:"boards"`
	Categories int               `json:"categories"`
	Templates  int               `json:"templates"`
	Skipped    int               `json:"skipped"`
	Errors     []ImportItemIssue `json:"errors,omitempty"`
	Warnings   []ImportItemIssue `json:"warnings,omitempty"`
}

// ImportItemIssue describes a per-item problem during a project import.
// Item names the affected entity (e.g. a task title or page slug).
type ImportItemIssue struct {
	Item    string `json:"item"`
	Message string `json:"message"`
}

// validImportStatus / validImportPriority / validImportTaskType are the enum
// allowlists for imported tasks; unknown values fall back to the defaults.
var validImportStatus = map[string]bool{
	StatusPlanned: true, StatusInProgress: true, StatusInReview: true,
	StatusDone: true, StatusArchived: true,
}

var validImportPriority = map[string]bool{
	PriorityLow: true, PriorityMedium: true, PriorityHigh: true, PriorityCritical: true, PriorityBlocker: true,
}

// Legacy BUG/CHORE values (pre-hierarchy exports) fall through the
// !valid check below and become plain TASKs.
var validImportTaskType = map[string]bool{
	TaskTypeTask: true, TaskTypeStory: true,
	TaskTypeEpic: true, TaskTypeSubtask: true,
}

var validImportPageStatus = map[string]bool{
	docs.StatusDraft: true, docs.StatusPublished: true, docs.StatusArchived: true,
}

// ImportProject imports a project export archive (see project_export.go) into
// an existing project. Everything is created with fresh IDs; comment threads,
// the page hierarchy, task relations and TASK-<uuid> references inside page
// content are remapped onto the new IDs. Supports ?dryRun=true to validate
// without persisting. Requires a writer role on the target project.
func (h *Handler) ImportProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
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

	dryRun := r.URL.Query().Get("dryRun") == "true"

	zr, manifest, done, ok := readImportArchive(w, r)
	if !ok {
		return
	}
	defer done()

	imp := &projectImporter{
		h:           h,
		zip:         zr,
		manifest:    manifest,
		projectID:   projectID,
		destProject: project,
		actorID:     shared.GetUserID(r),
		now:         shared.Now(),
		result:      &ProjectImportResult{DryRun: dryRun},
		userCache:   make(map[string]string),
		warned:      make(map[string]bool),
		taskIDMap:   make(map[string]string),

		releaseIDMap: make(map[string]string),
		sprintIDMap:  make(map[string]string),
		columnIDMap:  make(map[string]string),
		laneStatuses: make(map[string]bool),
	}
	if err := imp.plan(); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !dryRun {
		if err := imp.persist(); err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	imp.finalizeCounts()
	shared.WriteJSON(w, http.StatusOK, imp.result)
}

// ProjectImportCreateResult is the response of ImportProjectAsNew: the newly
// created project plus the content import summary. On a dry run the project
// carries the metadata that would be used but is not persisted.
type ProjectImportCreateResult struct {
	Project *Project             `json:"project"`
	Import  *ProjectImportResult `json:"import"`
}

// ImportProjectAsNew creates a brand-new project from a project export
// archive: project metadata (name, slug, abbreviation, description,
// visibility) comes from the manifest, the acting user becomes PROJECT_OWNER,
// and the archive's content is imported into the new project — project,
// membership and content share one transaction. The slug is de-conflicted
// against existing projects. Requires ADMIN or SUPER_ADMIN, the same rule as
// CreateProject. Supports ?dryRun=true to validate without persisting.
func (h *Handler) ImportProjectAsNew(w http.ResponseWriter, r *http.Request) {
	if !rbac.CanCreateProject(shared.GetGlobalRole(r)) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only admins can create projects")
		return
	}
	dryRun := r.URL.Query().Get("dryRun") == "true"

	zr, manifest, done, ok := readImportArchive(w, r)
	if !ok {
		return
	}
	defer done()

	meta := manifest.Project
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		shared.WriteError(w, http.StatusBadRequest, "IMPORT_ARCHIVE_INVALID", "archive manifest has no project name")
		return
	}
	visibility := meta.Visibility
	if !ValidVisibility(visibility) {
		visibility = VisibilityPrivate
	}
	abbr := strings.ToUpper(strings.TrimSpace(meta.Abbreviation))
	if abbr == "" || !ValidAbbreviation(abbr) {
		abbr = AbbreviationFromName(name)
	}
	slugBase := SlugFromName(meta.Slug)
	if slugBase == "" {
		slugBase = SlugFromName(name)
	}
	slug, err := h.uniqueProjectSlug(slugBase)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	now := shared.Now()
	project := &Project{
		ID:           shared.NewUUID(),
		Name:         name,
		Slug:         slug,
		Abbreviation: abbr,
		Description:  meta.Description,
		Visibility:   visibility,
		Status:       StatusActive,
		// Task settings travel with the export so a re-imported project keeps
		// its hierarchy levels (and its tasks of those types stay valid).
		ThemeEnabled:      meta.ThemeEnabled,
		InitiativeEnabled: meta.InitiativeEnabled,
		// An export that predates the estimation setting (or one of a project
		// that does not estimate) carries no unit — import it as NONE rather
		// than as the empty string the manifest literally holds.
		EstimationUnit: importedEstimationUnit(meta.EstimationUnit),
		BoardLaneLimit: importedBoardLaneLimit(meta.BoardLaneLimit),
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	imp := &projectImporter{
		h:           h,
		zip:         zr,
		manifest:    manifest,
		projectID:   project.ID,
		newProject:  project,
		destProject: project,
		actorID:     shared.GetUserID(r),
		now:         now,
		result:      &ProjectImportResult{DryRun: dryRun},
		userCache:   make(map[string]string),
		warned:      make(map[string]bool),
		taskIDMap:   make(map[string]string),

		releaseIDMap: make(map[string]string),
		sprintIDMap:  make(map[string]string),
		columnIDMap:  make(map[string]string),
		laneStatuses: make(map[string]bool),
	}
	if err := imp.plan(); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	status := http.StatusOK
	if !dryRun {
		if err := imp.persist(); err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		status = http.StatusCreated
	}
	imp.finalizeCounts()
	shared.WriteJSON(w, status, ProjectImportCreateResult{Project: project, Import: imp.result})
}

// uniqueProjectSlug de-conflicts base against the projects.slug unique index
// by appending -2, -3, … — an import must not fail just because the source
// project (or a previous import of it) already claims the slug.
func (h *Handler) uniqueProjectSlug(base string) (string, error) {
	if base == "" {
		base = "project"
	}
	candidate := base
	for n := 2; ; n++ {
		exists, err := h.projects.SlugExists(candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}

// readImportArchive bounds, spools and opens the uploaded export archive and
// decodes its manifest, enforcing every size cap. On failure it writes the
// error response and returns ok=false. The returned done func releases the
// temp file backing the zip.Reader and must be deferred by the caller.
func readImportArchive(w http.ResponseWriter, r *http.Request) (zr *zip.Reader, manifest *projectExportManifest, done func(), ok bool) {
	// Bound the whole request body, then spool it to a temp file: ZIP needs
	// random access, and archives can exceed what we want in memory.
	src, cleanup, err := extractZipReader(w, r)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "import archive exceeds the maximum allowed size")
			return nil, nil, nil, false
		}
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return nil, nil, nil, false
	}
	if cleanup != nil {
		defer cleanup()
	}

	tmp, err := os.CreateTemp("", "octbase-import-*.zip")
	if err != nil {
		shared.WriteServerError(w, r, err)
		return nil, nil, nil, false
	}
	done = func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	size, err := io.Copy(tmp, src)
	if err != nil {
		done()
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "import archive exceeds the maximum allowed size")
			return nil, nil, nil, false
		}
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "failed to read upload")
		return nil, nil, nil, false
	}

	zr, err = zip.NewReader(tmp, size)
	if err != nil {
		done()
		shared.WriteError(w, http.StatusBadRequest, "IMPORT_ARCHIVE_INVALID", "not a valid ZIP archive")
		return nil, nil, nil, false
	}
	if len(zr.File) > maxImportZipEntries {
		done()
		shared.WriteError(w, http.StatusBadRequest, "IMPORT_TOO_LARGE", fmt.Sprintf("archive exceeds %d entries", maxImportZipEntries))
		return nil, nil, nil, false
	}

	manifest, errCode, errMsg := readImportManifest(zr)
	if errCode != "" {
		done()
		shared.WriteError(w, http.StatusBadRequest, errCode, errMsg)
		return nil, nil, nil, false
	}
	if len(manifest.Tasks) > maxImportRows {
		done()
		shared.WriteError(w, http.StatusBadRequest, "IMPORT_TOO_LARGE", fmt.Sprintf("import exceeds maximum of %d tasks", maxImportRows))
		return nil, nil, nil, false
	}
	if len(manifest.Pages) > maxImportPages {
		done()
		shared.WriteError(w, http.StatusBadRequest, "IMPORT_TOO_LARGE", fmt.Sprintf("import exceeds maximum of %d pages", maxImportPages))
		return nil, nil, nil, false
	}
	return zr, manifest, done, true
}

// extractZipReader returns an io.Reader for the archive body and an optional
// cleanup func, accepting multipart/form-data ("file" field) or a raw body.
// The body is bounded to maxImportZipBytes before any read is attempted.
func extractZipReader(w http.ResponseWriter, r *http.Request) (io.Reader, func(), error) {
	return extractUploadReader(w, r, "file", maxImportZipBytes)
}

// readImportManifest locates and decodes project.json, checking the format
// marker. Returns a stable error code + message instead of an error so the
// handler can map straight to WriteError.
func readImportManifest(zr *zip.Reader) (*projectExportManifest, string, string) {
	f, err := zr.Open(exportManifestName)
	if err != nil {
		return nil, "IMPORT_ARCHIVE_INVALID", "archive is missing " + exportManifestName
	}
	defer func() { _ = f.Close() }()

	var manifest projectExportManifest
	dec := json.NewDecoder(io.LimitReader(f, maxImportManifestBytes))
	if err := dec.Decode(&manifest); err != nil {
		return nil, "IMPORT_ARCHIVE_INVALID", "invalid " + exportManifestName + ": " + err.Error()
	}
	if manifest.Format != projectExportFormat {
		return nil, "IMPORT_FORMAT_UNSUPPORTED", "archive is not an Octbase project export"
	}
	if manifest.FormatVersion != projectExportVersion {
		return nil, "IMPORT_FORMAT_UNSUPPORTED", fmt.Sprintf("unsupported export format version %d", manifest.FormatVersion)
	}
	return &manifest, "", ""
}

// projectImporter carries the state of one import through its two phases:
// plan (validate everything, resolve users, remap IDs — no writes) and
// persist (storage writes, then one DB transaction).
type projectImporter struct {
	h         *Handler
	zip       *zip.Reader
	manifest  *projectExportManifest
	projectID string
	actorID   string
	now       string
	result    *ProjectImportResult

	// newProject, when set, is created (with the actor as PROJECT_OWNER) in
	// the same transaction as the imported content; projectID is its ID.
	newProject *Project
	// destProject is the import target (== newProject for import-as-new).
	// Resolved before persist() so nothing inside the transaction has to
	// touch the connection pool (which deadlocks on a single-connection pool).
	destProject *Project

	userCache map[string]string // email → user ID ("" = not found)
	warned    map[string]bool
	taskIDMap map[string]string // exported task ID → new task ID
	// The planning sections are remapped the same way tasks are; a task's
	// releaseId/sprintId/boardColumnId is resolved through these maps.
	releaseIDMap map[string]string
	sprintIDMap  map[string]string
	columnIDMap  map[string]string
	// laneStatuses holds the custom statuses the imported board lanes define,
	// so a task sitting in such a lane keeps its status instead of falling back
	// to PLANNED.
	laneStatuses map[string]bool

	tasks      []*plannedTask
	relations  []*TaskRelation
	pages      []*plannedPage
	releases   []*Release
	sprints    []*Sprint
	boards     []*plannedBoard
	categories []*TaskCategory
	templates  []*TaskTemplate
}

type plannedTask struct {
	task        *Task
	comments    []*TaskComment
	links       []*TaskLink
	attachments []*plannedAttachment
	// parentRef is the exported ID of the task's hierarchy parent, resolved
	// against taskIDMap in a second pass after all task rows exist (the export
	// order does not guarantee parents before children).
	parentRef string
}

// plannedAttachment pairs the row to insert with the zip entry holding its
// bytes (nil for external links / rows without a file).
type plannedAttachment struct {
	att     *TaskAttachment
	zipFile *zip.File
}

type plannedPage struct {
	page    *docs.Page
	taskIDs []string
}

// linkTaskParentsTx applies the exported parent references in a second pass,
// after every task row exists (export order does not guarantee parents before
// children). Links that cannot be honoured — parent missing from the export or
// of the wrong hierarchy type — are skipped with a warning, and a SUBTASK left
// parentless that way is downgraded to TASK so the subtask-requires-parent
// invariant holds.
func (imp *projectImporter) linkTaskParentsTx(tx *sql.Tx) error {
	byNewID := make(map[string]*plannedTask, len(imp.tasks))
	for _, pt := range imp.tasks {
		byNewID[pt.task.ID] = pt
	}
	byExportedID := make(map[string]*plannedTask, len(imp.taskIDMap))
	for exported, newID := range imp.taskIDMap {
		if pt, ok := byNewID[newID]; ok {
			byExportedID[exported] = pt
		}
	}
	// The hierarchy rules depend on the destination project's task settings
	// (optional THEME/INITIATIVE levels), resolved before the transaction.
	project := imp.destProject
	if project == nil {
		project = imp.newProject
	}
	if project == nil {
		return fmt.Errorf("import target project %s not resolved", imp.projectID)
	}
	for _, pt := range imp.tasks {
		wantType, _ := ParentTaskTypeFor(project, pt.task.TaskType)
		var parent *plannedTask
		if pt.parentRef != "" {
			parent = byExportedID[pt.parentRef]
		}
		switch {
		case parent != nil && wantType != "" && parent.task.TaskType == wantType:
			if _, err := tx.Exec(`UPDATE tasks SET parent_id=$1 WHERE id=$2`, parent.task.ID, pt.task.ID); err != nil {
				return fmt.Errorf("link parent for task %q: %w", pt.task.Title, err)
			}
		case pt.parentRef != "":
			imp.warnOnce(pt.task.Title, "parent task reference could not be restored; imported without parent")
			fallthrough
		default:
			if pt.task.TaskType == TaskTypeSubtask {
				imp.warnOnce(pt.task.Title, "subtask without restorable parent imported as TASK")
				if _, err := tx.Exec(`UPDATE tasks SET task_type=$1 WHERE id=$2`, TaskTypeTask, pt.task.ID); err != nil {
					return fmt.Errorf("downgrade orphaned subtask %q: %w", pt.task.Title, err)
				}
			}
		}
	}
	return nil
}

func (imp *projectImporter) warnOnce(item, msg string) {
	key := item + "|" + msg
	if imp.warned[key] {
		return
	}
	imp.warned[key] = true
	imp.result.Warnings = append(imp.result.Warnings, ImportItemIssue{Item: item, Message: msg})
}

func (imp *projectImporter) errorItem(item, msg string) {
	imp.result.Errors = append(imp.result.Errors, ImportItemIssue{Item: item, Message: msg})
	imp.result.Skipped++
}

// resolveEmail maps an exported email to a local user ID, caching lookups and
// warning once per unknown address. Returns nil when unresolvable.
func (imp *projectImporter) resolveEmail(email, context string) (*string, error) {
	if email == "" {
		return nil, nil
	}
	id, hit := imp.userCache[email]
	if !hit {
		var err error
		id, err = findUserIDByEmail(imp.h.db, email)
		if err != nil {
			return nil, err
		}
		imp.userCache[email] = id
	}
	if id == "" {
		imp.warnOnce(context, fmt.Sprintf("user %q not found; field left empty", email))
		return nil, nil
	}
	return &id, nil
}

// normalizeTimestamp returns ts when it is valid RFC3339, otherwise fallback.
func normalizeTimestamp(ts, fallback string) string {
	if ts == "" {
		return fallback
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		return fallback
	}
	return ts
}

func isHTTPURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// plan validates the whole manifest and builds the insert plan without
// touching storage or the database (except read-only user lookups).
func (imp *projectImporter) plan() error {
	zipEntries := make(map[string]*zip.File, len(imp.zip.File))
	for _, f := range imp.zip.File {
		zipEntries[f.Name] = f
	}

	// Planning first: a task's release, sprint and board lane are resolved
	// through the ID maps this fills.
	if err := imp.planPlanning(); err != nil {
		return err
	}
	for _, et := range imp.manifest.Tasks {
		if err := imp.planTask(et, zipEntries); err != nil {
			return err
		}
	}
	imp.planRelations()
	if err := imp.planPages(); err != nil {
		return err
	}
	return nil
}

func (imp *projectImporter) planTask(et exportTask, zipEntries map[string]*zip.File) error {
	label := et.Title
	if label == "" {
		label = "task " + et.ID
	}
	title := strings.TrimSpace(et.Title)
	if title == "" {
		imp.errorItem(label, "task title is required")
		return nil
	}
	description := CleanTaskDescription(et.Description)
	if err := ValidateTaskInput(title, description); err != nil {
		imp.errorItem(label, err.Error())
		return nil
	}

	boardColumnID := imp.mappedID(imp.columnIDMap, et.BoardColumnID)
	columnID := ""
	if boardColumnID != nil {
		columnID = *boardColumnID
	}
	status := imp.importedTaskStatus(et.Status, columnID)
	priority := et.Priority
	if !validImportPriority[priority] {
		priority = PriorityMedium
	}
	taskType := et.TaskType
	if !validImportTaskType[taskType] {
		taskType = TaskTypeTask
	}

	assigneeID, err := imp.resolveEmail(et.AssigneeEmail, label)
	if err != nil {
		return err
	}
	reporterID, err := imp.resolveEmail(et.ReporterEmail, label)
	if err != nil {
		return err
	}
	reviewerID, err := imp.resolveEmail(et.ReviewerEmail, label)
	if err != nil {
		return err
	}

	var dueDate, externalRef, doneAt *string
	if et.DueDate != "" {
		if _, perr := time.Parse(internalDueDateLayout, et.DueDate); perr == nil {
			d := et.DueDate
			dueDate = &d
		}
	}
	if et.ExternalRef != "" && len(et.ExternalRef) <= 255 {
		e := et.ExternalRef
		externalRef = &e
	}
	if et.DoneAt != "" {
		if norm := normalizeTimestamp(et.DoneAt, ""); norm != "" {
			doneAt = &norm
		}
	}

	newID := shared.NewUUID()
	if et.ID != "" {
		imp.taskIDMap[et.ID] = newID
	}
	pt := &plannedTask{
		parentRef: et.ParentID,
		task: &Task{
			ID:          newID,
			ProjectID:   imp.projectID,
			Title:       title,
			Description: description,
			TaskType:    taskType,
			Status:      status,
			Priority:    priority,
			AssigneeID:  assigneeID,
			ReporterID:  reporterID,
			ReviewerID:  reviewerID,
			DueDate:     dueDate,
			ExternalRef: externalRef,
			Pinned:      et.Pinned,
			// A manifest is untrusted input, so estimates are filtered rather
			// than trusted: an out-of-range or wrong-typed estimate is dropped
			// instead of failing the whole import, which would strand an
			// otherwise-valid project on one bad number.
			StoryPoints:   importedStoryPoints(taskType, et.StoryPoints),
			EstimateHours: importedEstimateHours(taskType, et.EstimateHours),
			// Placement: a reference the archive does not carry resolves to nil,
			// so the task lands unplaced rather than pointing at a stranger's
			// release or another project's lane.
			ReleaseID:     imp.mappedID(imp.releaseIDMap, et.ReleaseID),
			SprintID:      imp.mappedID(imp.sprintIDMap, et.SprintID),
			BoardColumnID: boardColumnID,
			BoardRank:     importedBoardRank(et.BoardRank),
			CreatedAt:     normalizeTimestamp(et.CreatedAt, imp.now),
			UpdatedAt:     normalizeTimestamp(et.UpdatedAt, imp.now),
			DoneAt:        doneAt,
			Version:       1,
		},
	}

	if err := imp.planComments(et, pt, label); err != nil {
		return err
	}
	imp.planLinks(et, pt, label)
	imp.planAttachments(et, pt, label, zipEntries)
	imp.tasks = append(imp.tasks, pt)
	return nil
}

func (imp *projectImporter) planComments(et exportTask, pt *plannedTask, label string) error {
	commentIDMap := make(map[string]string)
	type pending struct {
		ec exportComment
		c  *TaskComment
	}
	var remaining []pending
	for _, ec := range et.Comments {
		text := SanitizeDescriptionHTML(ec.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if err := ValidateCommentInput(text); err != nil {
			imp.warnOnce(label, "comment skipped: "+err.Error())
			continue
		}
		authorID := imp.actorID
		if aid, err := imp.resolveEmail(ec.AuthorEmail, label); err != nil {
			return err
		} else if aid != nil {
			authorID = *aid
		}
		createdAt := normalizeTimestamp(ec.CreatedAt, imp.now)
		c := &TaskComment{
			ID:        shared.NewUUID(),
			TaskID:    pt.task.ID,
			AuthorID:  authorID,
			Text:      text,
			CreatedAt: createdAt,
			UpdatedAt: normalizeTimestamp(ec.UpdatedAt, createdAt),
		}
		if ec.ID != "" {
			commentIDMap[ec.ID] = c.ID
		}
		remaining = append(remaining, pending{ec: ec, c: c})
	}
	// Order replies after their parents; a parent that was skipped or is
	// unknown (or cyclic) degrades the reply to a top-level comment.
	placed := make(map[string]bool)
	for len(remaining) > 0 {
		progressed := false
		var next []pending
		for _, p := range remaining {
			if p.ec.ParentID == "" {
				pt.comments = append(pt.comments, p.c)
				placed[p.c.ID] = true
				progressed = true
				continue
			}
			parentNewID, known := commentIDMap[p.ec.ParentID]
			if !known {
				pt.comments = append(pt.comments, p.c) // parent skipped/foreign → top-level
				placed[p.c.ID] = true
				progressed = true
				continue
			}
			if placed[parentNewID] {
				pid := parentNewID
				p.c.ParentID = &pid
				pt.comments = append(pt.comments, p.c)
				placed[p.c.ID] = true
				progressed = true
				continue
			}
			next = append(next, p)
		}
		remaining = next
		if !progressed {
			// Cycle: flatten the rest to top-level.
			for _, p := range remaining {
				pt.comments = append(pt.comments, p.c)
			}
			break
		}
	}
	return nil
}

func (imp *projectImporter) planLinks(et exportTask, pt *plannedTask, label string) {
	for _, el := range et.Links {
		if !isHTTPURL(el.URL) {
			imp.warnOnce(label, "link skipped: URL must be http(s)")
			continue
		}
		pt.links = append(pt.links, &TaskLink{
			ID:        shared.NewUUID(),
			TaskID:    pt.task.ID,
			URL:       el.URL,
			Title:     el.Title,
			CreatedAt: normalizeTimestamp(el.CreatedAt, imp.now),
		})
	}
}

func (imp *projectImporter) planAttachments(et exportTask, pt *plannedTask, label string, zipEntries map[string]*zip.File) {
	for _, ea := range et.Attachments {
		att := &TaskAttachment{
			ID:          shared.NewUUID(),
			TaskID:      pt.task.ID,
			Filename:    sanitizeFilename(ea.Filename),
			ContentType: normalizeContentType(ea.ContentType),
			SizeBytes:   ea.SizeBytes,
			CreatedAt:   normalizeTimestamp(ea.CreatedAt, imp.now),
		}
		switch {
		case ea.File != "":
			if imp.h.storage == nil {
				imp.warnOnce(label, "file uploads are not configured; attached file skipped")
				continue
			}
			zf, ok := zipEntries[ea.File]
			if !ok || !strings.HasPrefix(ea.File, exportFilesPrefix) {
				imp.warnOnce(label, fmt.Sprintf("attachment %q: file missing from archive", ea.Filename))
				continue
			}
			// Compare in uint64: a crafted size above MaxInt64 would wrap
			// negative through int64() and slip past the cap.
			if zf.UncompressedSize64 > uint64(imp.maxUpload()) { // #nosec G115 -- maxUpload() is a positive config value well below MaxInt64
				imp.warnOnce(label, fmt.Sprintf("attachment %q exceeds the maximum file size", ea.Filename))
				continue
			}
			contentType, ok := imp.sniffZipEntry(zf, att.ContentType, att.Filename)
			if !ok {
				imp.warnOnce(label, fmt.Sprintf("attachment %q: file type not allowed", ea.Filename))
				continue
			}
			att.ContentType = contentType
			pt.attachments = append(pt.attachments, &plannedAttachment{att: att, zipFile: zf})
		case ea.ExternalURL != "":
			if !isHTTPURL(ea.ExternalURL) {
				imp.warnOnce(label, "external attachment skipped: URL must be http(s)")
				continue
			}
			att.ExternalURL = ea.ExternalURL
			pt.attachments = append(pt.attachments, &plannedAttachment{att: att})
		default:
			// Upload whose bytes were not exported (e.g. file was already
			// missing at export time) — nothing usable to restore.
			imp.warnOnce(label, fmt.Sprintf("attachment %q has no file in the archive; skipped", ea.Filename))
		}
	}
}

func (imp *projectImporter) maxUpload() int64 {
	if imp.h.maxUploadBytes > 0 {
		return imp.h.maxUploadBytes
	}
	return defaultMaxUploadBytes
}

// sniffZipEntry applies the same declared-vs-sniffed content-type policy as
// direct uploads to a file inside the archive.
func (imp *projectImporter) sniffZipEntry(zf *zip.File, declared, filename string) (string, bool) {
	rc, err := zf.Open()
	if err != nil {
		return "", false
	}
	defer func() { _ = rc.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(rc, head)
	sniffed := normalizeContentType(http.DetectContentType(head[:n]))
	if declared == "" {
		declared = sniffed
	}
	return resolveUploadType(declared, sniffed, filename)
}

func (imp *projectImporter) planRelations() {
	for _, er := range imp.manifest.Relations {
		srcID, okS := imp.taskIDMap[er.SourceTaskID]
		dstID, okT := imp.taskIDMap[er.TargetTaskID]
		if !okS || !okT || er.RelationType == "" {
			imp.warnOnce("relations", "relation skipped: task not part of the import")
			continue
		}
		imp.relations = append(imp.relations, &TaskRelation{
			ID:           shared.NewUUID(),
			SourceTaskID: srcID,
			TargetTaskID: dstID,
			RelationType: er.RelationType,
			CreatedAt:    normalizeTimestamp(er.CreatedAt, imp.now),
		})
	}
}

func (imp *projectImporter) planPages() error {
	if len(imp.manifest.Pages) == 0 {
		return nil
	}
	if imp.h.pagePorter == nil {
		imp.warnOnce("pages", "page import is not available; pages skipped")
		return nil
	}

	newTaskIDs := make(map[string]bool, len(imp.taskIDMap))
	for _, id := range imp.taskIDMap {
		newTaskIDs[id] = true
	}
	usedSlugs := make(map[string]bool)
	pageIDMap := make(map[string]string)

	type pending struct {
		ep exportPage
		p  *plannedPage
	}
	var valid []pending
	for _, ep := range imp.manifest.Pages {
		label := ep.Title
		if label == "" {
			label = "page " + ep.ID
		}
		title := strings.TrimSpace(ep.Title)
		if title == "" {
			imp.errorItem(label, "page title is required")
			continue
		}
		if len(title) > 255 {
			imp.errorItem(label, "page title exceeds 255 characters")
			continue
		}
		if len(ep.Content) > maxImportPageContent {
			imp.errorItem(label, "page content exceeds the maximum length")
			continue
		}

		// Remap TASK-<uuid> references in the AsciiDoc source onto the newly
		// created task IDs so cross-links keep working after the import.
		content := ep.Content
		for _, ref := range docs.ExtractTaskReferences(content) {
			if newID, ok := imp.taskIDMap[strings.ToLower(ref)]; ok {
				content = strings.ReplaceAll(content, "TASK-"+ref, "TASK-"+newID)
			}
		}
		var refs []string
		for _, ref := range docs.ExtractTaskReferences(content) {
			if newTaskIDs[strings.ToLower(ref)] {
				refs = append(refs, strings.ToLower(ref))
			}
		}

		slug, err := imp.uniquePageSlug(ep.Slug, title, usedSlugs)
		if err != nil {
			return err
		}

		status := ep.Status
		if !validImportPageStatus[status] {
			status = docs.StatusDraft
		}
		createdAt := normalizeTimestamp(ep.CreatedAt, imp.now)
		page := &docs.Page{
			ID:        shared.NewUUID(),
			ProjectID: imp.projectID,
			Title:     title,
			Slug:      slug,
			Content:   content,
			Status:    status,
			CreatedAt: createdAt,
			UpdatedAt: normalizeTimestamp(ep.UpdatedAt, createdAt),
			Version:   1,
		}
		if ep.ID != "" {
			pageIDMap[ep.ID] = page.ID
		}
		valid = append(valid, pending{ep: ep, p: &plannedPage{page: page, taskIDs: refs}})
	}

	// Order parents before children so the parent_page_id foreign key is
	// always satisfiable; unknown or cyclic parents become root pages.
	placed := make(map[string]bool)
	remaining := valid
	for len(remaining) > 0 {
		progressed := false
		var next []pending
		for _, p := range remaining {
			parentOld := p.ep.ParentPageID
			if parentOld == "" {
				imp.pages = append(imp.pages, p.p)
				placed[p.p.page.ID] = true
				progressed = true
				continue
			}
			parentNew, known := pageIDMap[parentOld]
			if !known {
				imp.pages = append(imp.pages, p.p) // parent skipped/foreign → root
				placed[p.p.page.ID] = true
				progressed = true
				continue
			}
			if placed[parentNew] {
				pid := parentNew
				p.p.page.ParentPageID = &pid
				imp.pages = append(imp.pages, p.p)
				placed[p.p.page.ID] = true
				progressed = true
				continue
			}
			next = append(next, p)
		}
		remaining = next
		if !progressed {
			for _, p := range remaining {
				imp.pages = append(imp.pages, p.p) // cycle → root
			}
			break
		}
	}
	return nil
}

// uniquePageSlug derives a slug from the exported slug (or the title when the
// slug is unusable) and de-conflicts it against both the target project and
// the slugs already claimed by this import.
func (imp *projectImporter) uniquePageSlug(exportedSlug, title string, used map[string]bool) (string, error) {
	base := docs.Slugify(exportedSlug)
	if base == "" {
		base = docs.Slugify(title)
	}
	if base == "" {
		base = "page"
	}
	candidate := base
	for n := 2; ; n++ {
		if !used[candidate] {
			exists, err := imp.h.pagePorter.SlugExists(imp.projectID, candidate)
			if err != nil {
				return "", err
			}
			if !exists {
				used[candidate] = true
				return candidate, nil
			}
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}

// persist writes the attachment bytes to storage, then inserts every planned
// row in one transaction. Storage writes that turn out oversized (a lying zip
// header) demote their attachment to a warning; on a failed transaction all
// freshly written files are removed again.
func (imp *projectImporter) persist() error {
	// Imported files are attributed to the importing user, so they must fit
	// into the actor's remaining per-user storage quota. Like oversized
	// entries, files that don't fit demote to a warning rather than failing
	// the whole import. quotaRemaining < 0 means no quota is configured.
	quotaRemaining := int64(-1)
	if imp.h.maxUserStorageBytes > 0 {
		used, err := imp.h.attachments.UploadedBytesByUser(imp.actorID)
		if err != nil {
			return err
		}
		quotaRemaining = imp.h.maxUserStorageBytes - used
	}

	var writtenKeys []string
	for _, pt := range imp.tasks {
		kept := pt.attachments[:0]
		for _, pa := range pt.attachments {
			if pa.zipFile == nil {
				kept = append(kept, pa)
				continue
			}
			key, written, err := imp.copyZipEntryToStorage(pa.zipFile)
			if err != nil {
				imp.removeKeys(writtenKeys)
				return err
			}
			if key == "" { // oversized despite the header check
				imp.warnOnce(pt.task.Title, fmt.Sprintf("attachment %q exceeds the maximum file size", pa.att.Filename))
				continue
			}
			if quotaRemaining >= 0 && written > quotaRemaining {
				_ = imp.h.storage.Remove(key)
				imp.warnOnce(pt.task.Title, fmt.Sprintf("attachment %q does not fit into your storage quota; skipped", pa.att.Filename))
				continue
			}
			if quotaRemaining >= 0 {
				quotaRemaining -= written
			}
			writtenKeys = append(writtenKeys, key)
			pa.att.StorageKey = key
			pa.att.SizeBytes = written
			pa.att.UploadedBy = imp.actorID
			kept = append(kept, pa)
		}
		pt.attachments = kept
	}

	err := shared.WithTx(imp.h.db, func(tx *sql.Tx) error {
		if imp.newProject != nil {
			if err := imp.h.projects.CreateTx(tx, imp.newProject); err != nil {
				return fmt.Errorf("create project %q: %w", imp.newProject.Name, err)
			}
			if err := insertOwnerMembershipTx(tx, imp.newProject.ID, imp.actorID, imp.now); err != nil {
				return fmt.Errorf("add project owner: %w", err)
			}
			// Recreate the exported custom priorities so imported tasks that
			// carry them stay editable. Invalid or built-in names are skipped —
			// the manifest is external input.
			for i, name := range imp.manifest.Project.CustomPriorities {
				name = strings.ToUpper(strings.TrimSpace(name))
				if !ValidPriorityName(name) || ValidPriority(name) {
					imp.warnOnce(name, "custom priority from the archive was skipped (invalid or built-in name)")
					continue
				}
				_, err := tx.Exec(`INSERT INTO project_priorities (id,project_id,name,rank,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (project_id,name) DO NOTHING`,
					shared.NewUUID(), imp.newProject.ID, name, i, imp.now, imp.now)
				if err != nil {
					return fmt.Errorf("create custom priority %q: %w", name, err)
				}
			}
		}
		// Releases, sprints and boards before the tasks that reference them —
		// sprint_id is a foreign key, and a task placed on a lane that does not
		// exist yet would land unplaced.
		if err := imp.persistPlanningTx(tx); err != nil {
			return err
		}
		for _, pt := range imp.tasks {
			seq, err := NextSeqNumber(tx, imp.projectID)
			if err != nil {
				return fmt.Errorf("seq number for task %q: %w", pt.task.Title, err)
			}
			pt.task.SeqNumber = &seq
			if err := imp.h.tasks.CreateTx(tx, pt.task); err != nil {
				return fmt.Errorf("create task %q: %w", pt.task.Title, err)
			}
			for _, c := range pt.comments {
				if err := imp.h.comments.CreateTx(tx, c); err != nil {
					return fmt.Errorf("create comment for task %q: %w", pt.task.Title, err)
				}
			}
			for _, l := range pt.links {
				if err := imp.h.links.CreateTx(tx, l); err != nil {
					return fmt.Errorf("create link for task %q: %w", pt.task.Title, err)
				}
			}
			for _, pa := range pt.attachments {
				if err := imp.h.attachments.CreateTx(tx, pa.att); err != nil {
					return fmt.Errorf("create attachment for task %q: %w", pt.task.Title, err)
				}
			}
		}
		if err := imp.linkTaskParentsTx(tx); err != nil {
			return err
		}
		for _, rel := range imp.relations {
			if err := imp.h.relations.CreateTx(tx, rel); err != nil {
				return fmt.Errorf("create relation: %w", err)
			}
		}
		for _, pp := range imp.pages {
			if err := imp.h.pagePorter.CreateImportedPageTx(tx, pp.page, imp.actorID, pp.taskIDs); err != nil {
				return fmt.Errorf("create page %q: %w", pp.page.Title, err)
			}
		}
		// One summary activity entry for the whole import run, not per task/page:
		// a project import can bring in hundreds of items and per-item entries
		// would flood the Activity feed the way normal TASK_CREATED entries do
		// for interactive creation.
		if len(imp.tasks) > 0 || len(imp.pages) > 0 {
			if err := imp.h.writeActivityTx(tx, imp.projectID, "", imp.actorID, "PROJECT_IMPORTED", map[string]any{
				"tasks": len(imp.tasks),
				"pages": len(imp.pages),
			}); err != nil {
				return fmt.Errorf("write import activity: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		imp.removeKeys(writtenKeys)
		return err
	}
	// Imported tasks populate the project's boards; broadcast one project-scoped
	// refresh (taskID empty — bulk import) after the transaction commits so any
	// board viewers pick up the new tasks without a manual reload.
	if len(imp.tasks) > 0 {
		imp.h.publishBoardEvent(imp.projectID, "", imp.actorID, "PROJECT_IMPORTED")
	}
	return nil
}

// copyZipEntryToStorage streams one archive entry into attachment storage.
// Returns ("", 0, nil) when the entry exceeds the per-file limit.
func (imp *projectImporter) copyZipEntryToStorage(zf *zip.File) (string, int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rc.Close() }()
	key, err := NewStorageKey()
	if err != nil {
		return "", 0, err
	}
	limit := imp.maxUpload()
	written, err := imp.h.storage.Write(key, io.LimitReader(rc, limit+1))
	if err != nil {
		return "", 0, err
	}
	if written > limit {
		_ = imp.h.storage.Remove(key)
		return "", 0, nil
	}
	return key, written, nil
}

func (imp *projectImporter) removeKeys(keys []string) {
	for _, k := range keys {
		_ = imp.h.storage.Remove(k)
	}
}

// finalizeCounts derives the result counters from the final plan (persist may
// have dropped oversized files).
func (imp *projectImporter) finalizeCounts() {
	for _, pt := range imp.tasks {
		imp.result.Tasks++
		imp.result.Comments += len(pt.comments)
		imp.result.Links += len(pt.links)
		for _, pa := range pt.attachments {
			imp.result.Attachments++
			if pa.zipFile != nil {
				imp.result.Files++
			}
		}
	}
	imp.result.Relations = len(imp.relations)
	imp.result.Pages = len(imp.pages)
	imp.result.Releases = len(imp.releases)
	imp.result.Sprints = len(imp.sprints)
	imp.result.Boards = len(imp.boards)
	imp.result.Categories = len(imp.categories)
	imp.result.Templates = len(imp.templates)
}

// importedEstimationUnit maps a manifest's estimation unit onto a valid stored
// value. Missing (older exports) and unrecognized values both fall back to
// NONE: an import must never switch estimation on for a unit this build does
// not understand, and NONE is the safe, invisible default.
// importedStoryPoints / importedEstimateHours keep an imported estimate only
// when it would also have been accepted through PATCH /tasks/{id}: on an
// estimable leaf type and inside the valid range. Anything else imports as
// unestimated.
func importedStoryPoints(taskType string, p *int) *int {
	if p == nil || !EstimableTaskType(taskType) || ValidateStoryPoints(p) != nil {
		return nil
	}
	return p
}

func importedEstimateHours(taskType string, h *float64) *float64 {
	if h == nil || !EstimableTaskType(taskType) || ValidateEstimateHours(h) != nil {
		return nil
	}
	return h
}

func importedEstimationUnit(s string) string {
	if ValidEstimationUnit(s) {
		return s
	}
	return EstimationUnitNone
}

// importedBoardLaneLimit maps a manifest's board lane limit onto a valid stored
// value. Absent (an export predating the field) and out-of-range both fall back
// to the default rather than to 0 — 0 means "draw every card", which is a
// deliberate choice a project makes, not something an old archive should
// silently acquire by omission.
func importedBoardLaneLimit(n *int) int {
	if n == nil || !ValidBoardLaneLimit(*n) {
		return DefaultBoardLaneLimit
	}
	return *n
}
