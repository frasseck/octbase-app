package workmanagement

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Project export/import ("Projekt exportieren"): a project's tasks, comments,
// links, attachments (including uploaded file bytes), task relations, doc
// pages and its planning structure — releases, sprints, boards with their
// columns, task categories and task templates — are bundled into a single ZIP
// archive that a later import can restore into another (or the same) Octbase
// instance.
//
// ZIP layout:
//
//	project.json      — manifest (format marker, project meta, tasks, pages, …)
//	files/<attId>     — raw bytes of each uploaded attachment
//
// Users are referenced by email in the manifest, mirroring the Jira CSV
// export, so an import into an instance with the same accounts reconnects
// authorship; unknown emails degrade gracefully (see project_import.go).
//
// Three things stay out of the archive on purpose:
//
//   - project memberships — identityaccess owns accounts, and an archive that
//     carried a member list would hand out project access on import;
//   - SCM repository connections — they hold provider credentials, which have
//     no business in a file a project member can download;
//   - external board columns, the activity log and the audit log — the first
//     reference columns in *other* projects and cannot be resolved on import,
//     the latter two are instance history rather than project content.
//
// The format marker stays at version 1 while every section added after the
// first release is optional: an older build reads a newer archive and simply
// ignores what it does not know (as it already does for customPriorities and
// estimationUnit), and a newer build reads an older archive because every new
// section is allowed to be absent.
const (
	projectExportFormat  = "octbase-project-export"
	projectExportVersion = 1
	exportManifestName   = "project.json"
	exportFilesPrefix    = "files/"
)

// RegisterProjectTransferRoutes registers the whole-project ZIP export/import
// routes. Like the CSV and file routes they must be mounted WITHOUT the
// RequireJSON middleware (binary download, multipart upload).
func (h *Handler) RegisterProjectTransferRoutes(r chi.Router) {
	r.Get("/api/v1/projects/{projectId}/export", h.ExportProject)
	r.Post("/api/v1/projects/{projectId}/import", h.ImportProject)
	r.Post("/api/v1/projects/import", h.ImportProjectAsNew)
}

// projectExportManifest is the root object stored as project.json in the
// export archive. The format/formatVersion pair is the compatibility contract
// the importer checks before touching anything else.
type projectExportManifest struct {
	Format        string            `json:"format"`
	FormatVersion int               `json:"formatVersion"`
	ExportedAt    string            `json:"exportedAt"`
	Project       exportProjectMeta `json:"project"`
	Tasks         []exportTask      `json:"tasks"`
	Relations     []exportRelation  `json:"relations,omitempty"`
	Pages         []exportPage      `json:"pages,omitempty"`
	// The planning structure a task is placed into. All optional: an archive
	// written before these sections existed simply has none of them, and an
	// import then behaves exactly as it did before.
	Releases   []exportRelease  `json:"releases,omitempty"`
	Sprints    []exportSprint   `json:"sprints,omitempty"`
	Boards     []exportBoard    `json:"boards,omitempty"`
	Categories []exportCategory `json:"categories,omitempty"`
	Templates  []exportTemplate `json:"templates,omitempty"`
}

type exportProjectMeta struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Abbreviation string `json:"abbreviation,omitempty"`
	Description  string `json:"description,omitempty"`
	Visibility   string `json:"visibility,omitempty"`
	// Task settings: the optional hierarchy levels and admin-defined extra
	// priorities. Optional in the manifest (older exports predate them);
	// applied only by the import-as-new path — importing into an existing
	// project never changes its settings.
	ThemeEnabled      bool     `json:"themeEnabled,omitempty"`
	InitiativeEnabled bool     `json:"initiativeEnabled,omitempty"`
	CustomPriorities  []string `json:"customPriorities,omitempty"`
	// EstimationUnit is omitted when NONE, so an export of a project that
	// does not estimate looks exactly like an export made before the field
	// existed — and both import back to NONE.
	EstimationUnit string `json:"estimationUnit,omitempty"`
	// BoardLaneLimit is a pointer, not an omitempty int, because 0 is a real
	// value here ("draw every card") and would otherwise be indistinguishable
	// from an export made before the field existed — which must import as the
	// default, not as unlimited.
	BoardLaneLimit *int `json:"boardLaneLimit,omitempty"`
}

type exportTask struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	TaskType      string `json:"taskType"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	ParentID      string `json:"parentId,omitempty"`
	AssigneeEmail string `json:"assigneeEmail,omitempty"`
	ReporterEmail string `json:"reporterEmail,omitempty"`
	ReviewerEmail string `json:"reviewerEmail,omitempty"`
	DueDate       string `json:"dueDate,omitempty"`
	SeqNumber     *int   `json:"seqNumber,omitempty"`
	ExternalRef   string `json:"externalRef,omitempty"`
	Pinned        bool   `json:"pinned,omitempty"`
	// Where the task sits in the project's planning structure. The three IDs
	// reference the releases/sprints/boards sections by their exported ID; an
	// import resolves them to the freshly created rows and drops a reference
	// the archive does not carry.
	ReleaseID     string `json:"releaseId,omitempty"`
	SprintID      string `json:"sprintId,omitempty"`
	BoardColumnID string `json:"boardColumnId,omitempty"`
	BoardRank     int    `json:"boardRank,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	DoneAt        string `json:"doneAt,omitempty"`
	// Both estimates travel, not just the project's active one: a unit
	// switch is non-destructive in the live project and must stay so across
	// an export/import round trip. Pointers keep "unestimated" (omitted)
	// distinct from a deliberate estimate of 0.
	StoryPoints   *int               `json:"storyPoints,omitempty"`
	EstimateHours *float64           `json:"estimateHours,omitempty"`
	Comments      []exportComment    `json:"comments,omitempty"`
	Links         []exportLink       `json:"links,omitempty"`
	Attachments   []exportAttachment `json:"attachments,omitempty"`
}

type exportComment struct {
	ID          string `json:"id"`
	ParentID    string `json:"parentId,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`
	Text        string `json:"text"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type exportLink struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// exportAttachment describes one task attachment. Uploaded files carry File —
// the path of their bytes inside the archive; external links carry
// ExternalURL instead.
type exportAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	ExternalURL string `json:"externalUrl,omitempty"`
	File        string `json:"file,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type exportRelation struct {
	SourceTaskID string `json:"sourceTaskId"`
	TargetTaskID string `json:"targetTaskId"`
	RelationType string `json:"relationType"`
	CreatedAt    string `json:"createdAt"`
}

type exportRelease struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Goal      string `json:"goal,omitempty"`
	DueDate   string `json:"dueDate,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// exportSprint carries the velocity snapshot alongside the sprint itself: a
// completed sprint whose counts were dropped on import would re-import as a
// sprint that apparently delivered nothing.
type exportSprint struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Goal              string   `json:"goal,omitempty"`
	StartDate         string   `json:"startDate,omitempty"`
	EndDate           string   `json:"endDate,omitempty"`
	Status            string   `json:"status"`
	ReleaseID         string   `json:"releaseId,omitempty"`
	CommittedCount    int      `json:"committedCount,omitempty"`
	CompletedCount    int      `json:"completedCount,omitempty"`
	CommittedEstimate *float64 `json:"committedEstimate,omitempty"`
	CompletedEstimate *float64 `json:"completedEstimate,omitempty"`
	EstimateUnit      string   `json:"estimateUnit,omitempty"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt"`
}

type exportBoard struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	IsDefault     bool                `json:"isDefault,omitempty"`
	MinColumns    int                 `json:"minColumns,omitempty"`
	MaxColumns    int                 `json:"maxColumns,omitempty"`
	IsSprintBoard bool                `json:"isSprintBoard,omitempty"`
	SprintID      string              `json:"sprintId,omitempty"`
	Columns       []exportBoardColumn `json:"columns,omitempty"`
	CreatedAt     string              `json:"createdAt"`
	UpdatedAt     string              `json:"updatedAt"`
}

type exportBoardColumn struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type exportCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type exportTemplate struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	TitleTemplate       string `json:"titleTemplate,omitempty"`
	DescriptionTemplate string `json:"descriptionTemplate,omitempty"`
	TaskType            string `json:"taskType"`
	Priority            string `json:"priority"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

type exportPage struct {
	ID           string `json:"id"`
	ParentPageID string `json:"parentPageId,omitempty"`
	Title        string `json:"title"`
	Slug         string `json:"slug"`
	Content      string `json:"content,omitempty"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// ExportProject streams a re-importable ZIP archive of the whole project:
// tasks with comments, links and attachments (file bytes included), task
// relations, and doc pages.
func (h *Handler) ExportProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
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

	tasks, err := h.tasks.ListAll(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	customPriorities, err := h.priorities.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	priorityNames := make([]string, 0, len(customPriorities))
	for _, cp := range customPriorities {
		priorityNames = append(priorityNames, cp.Name)
	}

	manifest := projectExportManifest{
		Format:        projectExportFormat,
		FormatVersion: projectExportVersion,
		ExportedAt:    shared.Now(),
		Project: exportProjectMeta{
			Name:              project.Name,
			Slug:              project.Slug,
			Abbreviation:      project.Abbreviation,
			Description:       project.Description,
			Visibility:        project.Visibility,
			ThemeEnabled:      project.ThemeEnabled,
			InitiativeEnabled: project.InitiativeEnabled,
			CustomPriorities:  priorityNames,
			EstimationUnit:    project.EstimationUnit,
			BoardLaneLimit:    &project.BoardLaneLimit,
		},
	}

	// fileEntries lists the storage keys to copy into the archive, keyed by
	// their zip path. Only files that are actually openable are referenced from
	// the manifest, so an importer never chases dangling entries.
	type fileEntry struct {
		zipPath    string
		storageKey string
	}
	var fileEntries []fileEntry

	// Three batched lookups for the whole project instead of three queries per
	// task: an export of 500 tasks went from 1500 round trips to 3. Each map keeps
	// the per-task rows in the same created_at order the per-task reads returned.
	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	commentsByTask, err := h.comments.ListByTasks(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	linksByTask, err := h.links.ListByTasks(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	attachmentsByTask, err := h.attachments.ListByTasks(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	var allUserIDs []string
	commentAuthors := make(map[string]string) // comment ID → author ID
	for _, t := range tasks {
		for _, id := range []*string{t.AssigneeID, t.ReporterID, t.ReviewerID} {
			if id != nil {
				allUserIDs = append(allUserIDs, *id)
			}
		}
		comments := commentsByTask[t.ID]
		for _, c := range comments {
			allUserIDs = append(allUserIDs, c.AuthorID)
			commentAuthors[c.ID] = c.AuthorID
		}
		links := linksByTask[t.ID]
		attachments := attachmentsByTask[t.ID]

		et := exportTask{
			ID:            t.ID,
			Title:         t.Title,
			Description:   t.Description,
			TaskType:      t.TaskType,
			Status:        t.Status,
			Priority:      t.Priority,
			SeqNumber:     t.SeqNumber,
			Pinned:        t.Pinned,
			StoryPoints:   t.StoryPoints,
			EstimateHours: t.EstimateHours,
			BoardRank:     t.BoardRank,
			CreatedAt:     t.CreatedAt,
			UpdatedAt:     t.UpdatedAt,
		}
		if t.ParentID != nil {
			et.ParentID = *t.ParentID
		}
		if t.ReleaseID != nil {
			et.ReleaseID = *t.ReleaseID
		}
		if t.SprintID != nil {
			et.SprintID = *t.SprintID
		}
		if t.BoardColumnID != nil {
			et.BoardColumnID = *t.BoardColumnID
		}
		if t.DueDate != nil {
			et.DueDate = *t.DueDate
		}
		if t.ExternalRef != nil {
			et.ExternalRef = *t.ExternalRef
		}
		if t.DoneAt != nil {
			et.DoneAt = *t.DoneAt
		}
		for _, c := range comments {
			ec := exportComment{ID: c.ID, Text: c.Text, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
			if c.ParentID != nil {
				ec.ParentID = *c.ParentID
			}
			et.Comments = append(et.Comments, ec)
		}
		for _, l := range links {
			et.Links = append(et.Links, exportLink{URL: l.URL, Title: l.Title, CreatedAt: l.CreatedAt})
		}
		for _, a := range attachments {
			ea := exportAttachment{
				ID:          a.ID,
				Filename:    a.Filename,
				ContentType: a.ContentType,
				SizeBytes:   a.SizeBytes,
				ExternalURL: a.ExternalURL,
				CreatedAt:   a.CreatedAt,
			}
			if a.IsUpload() && h.storage != nil {
				// Probe now so the manifest only references files the archive
				// will really contain.
				if f, err := h.storage.Open(a.StorageKey); err == nil {
					_ = f.Close()
					ea.File = exportFilesPrefix + a.ID
					fileEntries = append(fileEntries, fileEntry{zipPath: ea.File, storageKey: a.StorageKey})
				}
			}
			et.Attachments = append(et.Attachments, ea)
		}
		manifest.Tasks = append(manifest.Tasks, et)
	}

	relations, err := h.relations.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for _, rel := range relations {
		manifest.Relations = append(manifest.Relations, exportRelation{
			SourceTaskID: rel.SourceTaskID,
			TargetTaskID: rel.TargetTaskID,
			RelationType: rel.RelationType,
			CreatedAt:    rel.CreatedAt,
		})
	}

	if err := h.exportPlanning(&manifest, projectID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	if h.pagePorter != nil {
		pages, err := h.pagePorter.ListByProject(projectID)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		for _, p := range pages {
			ep := exportPage{
				ID:        p.ID,
				Title:     p.Title,
				Slug:      p.Slug,
				Content:   p.Content,
				Status:    p.Status,
				CreatedAt: p.CreatedAt,
				UpdatedAt: p.UpdatedAt,
			}
			if p.ParentPageID != nil {
				ep.ParentPageID = *p.ParentPageID
			}
			manifest.Pages = append(manifest.Pages, ep)
		}
	}

	// Resolve user IDs to emails and fill the per-task identity fields.
	userEmails, err := loadUserEmailsForIDs(h.db, allUserIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for i, t := range tasks {
		if t.AssigneeID != nil {
			manifest.Tasks[i].AssigneeEmail = userEmails[*t.AssigneeID]
		}
		if t.ReporterID != nil {
			manifest.Tasks[i].ReporterEmail = userEmails[*t.ReporterID]
		}
		if t.ReviewerID != nil {
			manifest.Tasks[i].ReviewerEmail = userEmails[*t.ReviewerID]
		}
	}
	for ti := range manifest.Tasks {
		for ci := range manifest.Tasks[ti].Comments {
			if authorID, ok := commentAuthors[manifest.Tasks[ti].Comments[ci].ID]; ok {
				manifest.Tasks[ti].Comments[ci].AuthorEmail = userEmails[authorID]
			}
		}
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-export.zip"`, project.Slug))

	// From here on the response is streaming: headers are already sent, so a
	// failure can only be logged (via WriteServerError), not turned into a
	// clean JSON error — same limitation as the CSV export.
	zw := zip.NewWriter(w)
	mw, err := zw.Create(exportManifestName)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for _, fe := range fileEntries {
		src, err := h.storage.Open(fe.storageKey)
		if err != nil {
			// Probed OK above but gone now; nothing sane to substitute mid-stream.
			shared.WriteServerError(w, r, err)
			return
		}
		dst, err := zw.Create(fe.zipPath)
		if err == nil {
			_, err = io.Copy(dst, src)
		}
		_ = src.Close()
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		shared.WriteServerError(w, r, err)
	}
}

// exportPlanning fills the manifest's planning sections: releases, sprints,
// boards with their columns, task categories and task templates. Without them
// an imported project is a flat backlog — the tasks arrive, but every release,
// sprint and board card they belonged to is gone.
//
// Board *external* columns are skipped: they are read-only views of columns in
// other projects, so there is nothing an import into a different instance could
// point them at.
func (h *Handler) exportPlanning(manifest *projectExportManifest, projectID string) error {
	releases, err := h.releases.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, rel := range releases {
		er := exportRelease{
			ID:        rel.ID,
			Name:      rel.Name,
			Goal:      rel.Goal,
			Status:    rel.Status,
			CreatedAt: rel.CreatedAt,
			UpdatedAt: rel.UpdatedAt,
		}
		if rel.DueDate != nil {
			er.DueDate = *rel.DueDate
		}
		manifest.Releases = append(manifest.Releases, er)
	}

	sprints, err := h.sprints.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, s := range sprints {
		es := exportSprint{
			ID:                s.ID,
			Name:              s.Name,
			Goal:              s.Goal,
			Status:            s.Status,
			CommittedCount:    s.CommittedCount,
			CompletedCount:    s.CompletedCount,
			CommittedEstimate: s.CommittedEstimate,
			CompletedEstimate: s.CompletedEstimate,
			CreatedAt:         s.CreatedAt,
			UpdatedAt:         s.UpdatedAt,
		}
		if s.StartDate != nil {
			es.StartDate = *s.StartDate
		}
		if s.EndDate != nil {
			es.EndDate = *s.EndDate
		}
		if s.ReleaseID != nil {
			es.ReleaseID = *s.ReleaseID
		}
		if s.EstimateUnit != nil {
			es.EstimateUnit = *s.EstimateUnit
		}
		manifest.Sprints = append(manifest.Sprints, es)
	}

	boards, err := h.boards.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, b := range boards {
		eb := exportBoard{
			ID:            b.ID,
			Name:          b.Name,
			IsDefault:     b.IsDefault,
			MinColumns:    b.MinColumns,
			MaxColumns:    b.MaxColumns,
			IsSprintBoard: b.IsSprintBoard,
			CreatedAt:     b.CreatedAt,
			UpdatedAt:     b.UpdatedAt,
		}
		if b.SprintID != nil {
			eb.SprintID = *b.SprintID
		}
		cols, err := h.columns.ListByBoard(b.ID)
		if err != nil {
			return err
		}
		for _, c := range cols {
			eb.Columns = append(eb.Columns, exportBoardColumn{
				ID:        c.ID,
				Name:      c.Name,
				Status:    c.Status,
				Position:  c.Position,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			})
		}
		manifest.Boards = append(manifest.Boards, eb)
	}

	categories, err := h.categories.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, c := range categories {
		manifest.Categories = append(manifest.Categories, exportCategory{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
			Color:       c.Color,
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
		})
	}

	templates, err := h.templates.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, t := range templates {
		manifest.Templates = append(manifest.Templates, exportTemplate{
			ID:                  t.ID,
			Name:                t.Name,
			TitleTemplate:       t.TitleTemplate,
			DescriptionTemplate: t.DescriptionTemplate,
			TaskType:            t.TaskType,
			Priority:            t.Priority,
			CreatedAt:           t.CreatedAt,
			UpdatedAt:           t.UpdatedAt,
		})
	}
	return nil
}
