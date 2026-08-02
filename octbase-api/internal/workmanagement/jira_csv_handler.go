package workmanagement

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// RegisterCSVRoutes registers the Jira CSV export/import routes.
// These routes are intentionally registered in a separate group without the
// RequireJSON middleware, because the import endpoint accepts multipart/form-data.
//
// importEnabled reflects the deployment edition plus the add-on flag
// (OCTBASE_EDITION / OCTBASE_OPTION_JIRA_IMPORT — the import is included in
// ENTERPRISE, activatable as an additional option in BUSINESS, never in
// TEAM). When false the import route stays registered but answers 403
// FEATURE_DISABLED, a stable code clients can rely on, rather than
// disappearing into a 404.
func (h *Handler) RegisterCSVRoutes(r chi.Router, importEnabled bool) {
	r.Get("/api/v1/projects/{projectId}/export/jira-csv", h.ExportJiraCSV)
	if !importEnabled {
		r.Post("/api/v1/projects/{projectId}/import/jira-csv", func(w http.ResponseWriter, _ *http.Request) {
			shared.WriteError(w, http.StatusForbidden, "FEATURE_DISABLED", "Jira CSV import is not activated for this deployment")
		})
		return
	}
	r.Post("/api/v1/projects/{projectId}/import/jira-csv", h.ImportJiraCSV)
}

// ExportJiraCSV exports all tasks of a project as a Jira-compatible CSV file.
//
// Query parameters:
//   - projectKey: override the Jira project key (default: derived from project slug)
//   - userMappings: JSON object mapping local email addresses to Jira account IDs,
//     e.g. {"alice@company.com":"557057:abc-123"}
func (h *Handler) ExportJiraCSV(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.memberGuard(w, r, projectID)
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

	projectKey := toJiraProjectKey(project.Slug)
	if override := strings.TrimSpace(r.URL.Query().Get("projectKey")); override != "" {
		projectKey = override
	}

	userMapping := make(map[string]string)
	if raw := r.URL.Query().Get("userMappings"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &userMapping); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid userMappings JSON: "+err.Error())
			return
		}
	}

	tasks, err := h.tasks.ListAll(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Two batched lookups for the whole project instead of two queries per task.
	// Each map preserves the per-task created_at order of the per-task reads, so
	// the CSV rows are byte-identical to before.
	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	commentsByTask, err := h.comments.ListByTasks(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Only URL-backed attachments round-trip through the CSV; uploaded files live
	// behind authenticated storage and have no public URL.
	attachmentsByTask, err := h.attachments.ListByTasks(taskIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	var rows []exportRow
	var allUserIDs []string

	for _, t := range tasks {
		if t.AssigneeID != nil {
			allUserIDs = append(allUserIDs, *t.AssigneeID)
		}
		if t.ReporterID != nil {
			allUserIDs = append(allUserIDs, *t.ReporterID)
		}
		comments := commentsByTask[t.ID]
		for _, c := range comments {
			allUserIDs = append(allUserIDs, c.AuthorID)
		}
		attachments := attachmentsByTask[t.ID]
		var external []TaskAttachment
		for _, a := range attachments {
			if a.ExternalURL != "" {
				external = append(external, a)
			}
		}
		rows = append(rows, exportRow{task: t, comments: comments, attachments: external})
	}

	userEmails, err := loadUserEmailsForIDs(h.db, allUserIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-tasks.csv"`, projectKey))
	if err := writeJiraCSV(w, projectKey, rows, userEmails, userMapping); err != nil {
		// Response headers already sent — log but cannot write JSON error.
		shared.WriteServerError(w, r, err)
	}
}

// ImportJiraCSV imports tasks from a Jira-compatible CSV file into a project.
//
// The request body must be either:
//   - multipart/form-data with a "file" field containing the CSV, or
//   - a raw CSV body (any Content-Type other than application/json)
//
// Query parameters:
//   - dryRun: "true" to validate without persisting
//   - userMappings: JSON object mapping Jira identifiers to local email addresses,
//     e.g. {"557057:abc-123":"alice@company.com","bob_jira":"bob@company.com"}
//
// The import is all-or-nothing for valid rows: all pre-validated rows are written
// in a single transaction. Rows that fail validation are skipped and reported in
// the response errors list.
func (h *Handler) ImportJiraCSV(w http.ResponseWriter, r *http.Request) {
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

	importMappings := make(map[string]string)
	if raw := r.URL.Query().Get("userMappings"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &importMappings); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid userMappings JSON: "+err.Error())
			return
		}
	}

	csvReader, cleanup, err := extractCSVReader(w, r)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "import file exceeds the maximum allowed size")
			return
		}
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	parsed, err := parseJiraCSVReader(csvReader)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "import file exceeds the maximum allowed size")
			return
		}
		shared.WriteError(w, http.StatusBadRequest, "CSV_PARSE_ERROR", err.Error())
		return
	}

	actorID := shared.GetUserID(r)
	now := shared.Now()

	result, taskRows, err := h.validateImportRows(parsed, projectID, actorID, now, importMappings)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	result.DryRun = dryRun

	if len(parsed.unknownHeaders) > 0 {
		result.addWarning(ImportRowWarning{
			Row:     0,
			Message: "unrecognised columns will be ignored: " + strings.Join(parsed.unknownHeaders, ", "),
		})
	}
	result.finalizeWarnings()

	result.Imported = len(taskRows)
	for _, row := range taskRows {
		result.AttachmentsImported += len(row.attachments)
	}

	if !dryRun && len(taskRows) > 0 {
		if err := h.persistImportRows(projectID, actorID, taskRows, result); err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		// Imported tasks appear on the project's boards; broadcast one
		// project-scoped refresh (taskID empty — this is a bulk, project-level
		// change) after the import transaction commits.
		h.publishBoardEvent(projectID, "", actorID, "TASKS_IMPORTED")
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

// extractCSVReader returns an io.Reader for the CSV body and an optional cleanup func.
// It handles both multipart/form-data uploads (file field) and raw CSV bodies.
// The body is bounded to maxImportCSVBytes before any read is attempted, so
// an oversized upload surfaces as an *http.MaxBytesError from whichever call
// ends up reading it (multipart parsing here, or the CSV parser downstream
// for the raw-body path).
func extractCSVReader(w http.ResponseWriter, r *http.Request) (io.Reader, func(), error) {
	return extractUploadReader(w, r, "file", maxImportCSVBytes)
}

// importTaskRow holds a validated task, its comments, and its URL-backed
// attachments, ready to persist.
type importTaskRow struct {
	task        *Task
	comments    []*TaskComment
	attachments []*TaskAttachment
}

// validateImportRows processes all parsed CSV records and returns the valid rows.
// Invalid rows are recorded in result.Errors and counted in result.Skipped.
func (h *Handler) validateImportRows(
	parsed *parsedCSV,
	projectID, actorID, now string,
	importMappings map[string]string,
) (*ImportResult, []importTaskRow, error) {
	result := &ImportResult{}
	var taskRows []importTaskRow

	// Cache Jira identifier → internal user ID to reduce DB round-trips.
	resolveCache := make(map[string]string)
	warnCache := make(map[string]bool)
	resolve := func(raw string) (string, string, error) {
		if raw == "" {
			return "", "", nil
		}
		if id, hit := resolveCache[raw]; hit {
			return id, "", nil
		}
		id, warn, err := resolveImportIdentifier(h.db, raw, importMappings)
		if err != nil {
			return "", "", err
		}
		resolveCache[raw] = id
		return id, warn, nil
	}

	for rowIdx, record := range parsed.records {
		rowNum := rowIdx + 2 // 1-based; row 1 is the header
		issueKey := csvField(record, parsed.columnIndex, "Issue Key")

		summary := csvField(record, parsed.columnIndex, "Summary")
		if strings.TrimSpace(summary) == "" {
			result.Errors = append(result.Errors, ImportRowError{Row: rowNum, IssueKey: issueKey, Message: "Summary is required"})
			result.Skipped++
			continue
		}
		if len([]rune(summary)) > 255 {
			result.Errors = append(result.Errors, ImportRowError{Row: rowNum, IssueKey: issueKey, Message: "Summary exceeds 255 characters"})
			result.Skipped++
			continue
		}

		// Imported descriptions are attacker-controllable; sanitize against the
		// HTML allowlist so a malicious CSV cannot store XSS payloads.
		description := CleanTaskDescription(csvField(record, parsed.columnIndex, "Description"))
		// Same constrained-HTML upper bound as every other write path (see
		// ValidateTaskInput's DESCRIPTION_TOO_LONG rule in domain.go).
		if len(description) > 50000 {
			result.Errors = append(result.Errors, ImportRowError{Row: rowNum, IssueKey: issueKey, Message: "DESCRIPTION_TOO_LONG: task description must not exceed 50 000 characters"})
			result.Skipped++
			continue
		}
		statusRaw := csvField(record, parsed.columnIndex, "Status")
		priorityRaw := csvField(record, parsed.columnIndex, "Priority")
		issueTypeRaw := csvField(record, parsed.columnIndex, "Issue Type")
		assigneeRaw := csvField(record, parsed.columnIndex, "Assignee")
		reporterRaw := csvField(record, parsed.columnIndex, "Reporter")
		dueDateRaw := csvField(record, parsed.columnIndex, "Due date")
		createdRaw := csvField(record, parsed.columnIndex, "Created")
		updatedRaw := csvField(record, parsed.columnIndex, "Updated")

		// Unmapped values fall back to defaults; each fallback is surfaced in
		// the report so a migration review can spot silently remapped rows.
		status := jiraToStatus[strings.ToLower(statusRaw)]
		if status == "" {
			status = StatusPlanned
			if statusRaw != "" {
				result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey,
					Message: fmt.Sprintf("unknown status %q, defaulting to %s", statusRaw, StatusPlanned)})
			}
		}
		priority := jiraToPriority[strings.ToLower(priorityRaw)]
		if priority == "" {
			priority = PriorityMedium
			if priorityRaw != "" {
				result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey,
					Message: fmt.Sprintf("unknown priority %q, defaulting to %s", priorityRaw, PriorityMedium)})
			}
		}
		taskType := jiraToType[strings.ToLower(issueTypeRaw)]
		if taskType == "" {
			taskType = TaskTypeTask
			if issueTypeRaw != "" {
				result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey,
					Message: fmt.Sprintf("unknown issue type %q, defaulting to %s", issueTypeRaw, TaskTypeTask)})
			}
		}

		assigneeID, assigneeWarn, err := resolve(assigneeRaw)
		if err != nil {
			return nil, nil, err
		}
		reporterID, reporterWarn, err := resolve(reporterRaw)
		if err != nil {
			return nil, nil, err
		}

		for _, warn := range []string{assigneeWarn, reporterWarn} {
			if warn != "" && !warnCache[warn] {
				warnCache[warn] = true
				result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey, Message: warn})
			}
		}

		dueDate := parseJiraDueDate(dueDateRaw)

		// Use CSV timestamps when available; fall back to now.
		createdAt := parseJiraTimestamp(createdRaw)
		if createdAt == "" {
			createdAt = now
		}
		updatedAt := parseJiraTimestamp(updatedRaw)
		if updatedAt == "" {
			updatedAt = now
		}

		var assigneePtr, reporterPtr, dueDatePtr, externalRefPtr *string
		if assigneeID != "" {
			assigneePtr = &assigneeID
		}
		if reporterID != "" {
			reporterPtr = &reporterID
		}
		if dueDate != "" {
			dueDatePtr = &dueDate
		}
		if issueKey != "" {
			externalRefPtr = &issueKey
		}

		taskID := shared.NewUUID()
		task := &Task{
			ID:          taskID,
			ProjectID:   projectID,
			Title:       summary,
			Description: description,
			TaskType:    taskType,
			Status:      status,
			Priority:    priority,
			AssigneeID:  assigneePtr,
			ReporterID:  reporterPtr,
			DueDate:     dueDatePtr,
			ExternalRef: externalRefPtr,
			BoardRank:   1000,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Version:     1,
		}

		var comments []*TaskComment
		for _, cidx := range parsed.commentIndices {
			if cidx >= len(record) {
				continue
			}
			commentVal := strings.TrimSpace(record[cidx])
			if commentVal == "" {
				continue
			}

			dateStr, author, text, isParsed := parseCommentValue(commentVal)

			commentAuthorID := actorID
			if isParsed && author != "" {
				if cid, warn, err := resolve(author); err != nil {
					return nil, nil, err
				} else if cid != "" {
					commentAuthorID = cid
				} else if warn != "" && !warnCache[warn] {
					warnCache[warn] = true
					result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey, Message: "comment author: " + warn})
				}
			}

			commentCreatedAt := now
			if isParsed && dateStr != "" {
				if ts := jiraCommentDateToRFC(dateStr); ts != "" {
					commentCreatedAt = ts
				}
			}

			comments = append(comments, &TaskComment{
				ID:        shared.NewUUID(),
				TaskID:    taskID,
				AuthorID:  commentAuthorID,
				Text:      text,
				CreatedAt: commentCreatedAt,
				UpdatedAt: commentCreatedAt,
			})
		}

		// Attachments become URL-backed records pointing at the original Jira
		// URL. The bytes are deliberately NOT fetched: those URLs require Jira
		// authentication, and fetching caller-supplied URLs server-side would
		// be an SSRF surface. Malformed cells are skipped with a warning.
		var attachments []*TaskAttachment
		for _, aidx := range parsed.attachmentIndices {
			if aidx >= len(record) {
				continue
			}
			attVal := strings.TrimSpace(record[aidx])
			if attVal == "" {
				continue
			}
			attCreatedAt, _, filename, url, reason := parseAttachmentValue(attVal)
			if reason != "" {
				result.addWarning(ImportRowWarning{Row: rowNum, IssueKey: issueKey,
					Message: "malformed attachment cell skipped: " + reason})
				continue
			}
			if attCreatedAt == "" {
				attCreatedAt = now
			}
			attachments = append(attachments, &TaskAttachment{
				ID:          shared.NewUUID(),
				TaskID:      taskID,
				Filename:    filename,
				ExternalURL: url,
				CreatedAt:   attCreatedAt,
			})
		}

		taskRows = append(taskRows, importTaskRow{task: task, comments: comments, attachments: attachments})
	}

	return result, taskRows, nil
}

// persistImportRows writes all validated task rows and their comments to the database
// in a single transaction. SeqNumbers are assigned inside the transaction to avoid
// wasting counters on dry-run calls. One summary activity entry is recorded for the
// whole run (not per task) so a bulk import of hundreds of rows doesn't flood the
// project Activity feed the way individual TASK_CREATED entries would.
func (h *Handler) persistImportRows(projectID, actorID string, rows []importTaskRow, result *ImportResult) error {
	return shared.WithTx(h.db, func(tx *sql.Tx) error {
		for _, row := range rows {
			seq, err := NextSeqNumber(tx, row.task.ProjectID)
			if err != nil {
				return fmt.Errorf("seq number for task %q: %w", row.task.Title, err)
			}
			row.task.SeqNumber = &seq

			if err := h.tasks.CreateTx(tx, row.task); err != nil {
				return fmt.Errorf("create task %q: %w", row.task.Title, err)
			}
			for _, c := range row.comments {
				if err := h.comments.CreateTx(tx, c); err != nil {
					return fmt.Errorf("create comment for task %q: %w", row.task.Title, err)
				}
			}
			for _, a := range row.attachments {
				if err := h.attachments.CreateTx(tx, a); err != nil {
					return fmt.Errorf("create attachment for task %q: %w", row.task.Title, err)
				}
			}
		}
		// One summary entry for the whole run so the migration event is
		// visible in the Activity view without flooding it per task.
		return h.writeActivityTx(tx, projectID, "", actorID, "TASKS_IMPORTED", map[string]any{
			"count":       len(rows),
			"attachments": result.AttachmentsImported,
			"skipped":     result.Skipped,
			"warnings":    len(result.Warnings),
			"source":      "jira_csv",
		})
	})
}
