package workmanagement

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	neturl "net/url"
	"strings"
	"time"
)

// jiraDateFormat is the Jira CSV date/time format used for timestamps and comment headers.
// Example output: "31/Mar/24 6:49 AM"
const jiraDateFormat = "02/Jan/06 3:04 PM"

// jiraDueDateFormat is the Jira CSV date-only format used for the "Due date" column.
const jiraDueDateFormat = "02/Jan/06"

// internalDueDateLayout is the layout used internally for due-date strings.
const internalDueDateLayout = "2006-01-02"

// maxImportRows caps the number of data rows accepted in a single import.
const maxImportRows = 5000

// maxImportCSVBytes caps the raw upload size for a Jira CSV import, applied
// before any row is parsed. CSV text is far less dense than the ZIP project
// export, so this is deliberately smaller than maxImportZipBytes but kept in
// the same order of magnitude.
const maxImportCSVBytes = 20 << 20 // 20 MiB

// jiraFixedHeaders lists the fixed (non-repeating) Jira CSV column headers in order.
var jiraFixedHeaders = []string{
	"Issue Key",
	"Project Key",
	"Summary",
	"Issue Type",
	"Status",
	"Priority",
	"Assignee",
	"Reporter",
	"Description",
	"Created",
	"Updated",
	"Due date",
	"Labels",
}

// statusToJira maps internal task statuses to Jira status names.
var statusToJira = map[string]string{
	"PLANNED":     "To Do",
	"IN_PROGRESS": "In Progress",
	"IN_REVIEW":   "In Review",
	"DONE":        "Done",
	"ARCHIVED":    "Done",
}

// jiraToStatus maps Jira status names to internal task statuses.
var jiraToStatus = map[string]string{
	"to do":       StatusPlanned,
	"open":        StatusPlanned,
	"backlog":     StatusPlanned,
	"in progress": StatusInProgress,
	"in review":   StatusInReview,
	"done":        StatusDone,
	"closed":      StatusDone,
	"resolved":    StatusDone,
}

// priorityToJira maps internal priorities to Jira priority names.
var priorityToJira = map[string]string{
	"LOW":      "Low",
	"MEDIUM":   "Medium",
	"HIGH":     "High",
	"CRITICAL": "Critical",
	"BLOCKER":  "Blocker",
}

// jiraToPriority maps Jira priority names to internal priorities.
var jiraToPriority = map[string]string{
	"lowest":   PriorityLow,
	"low":      PriorityLow,
	"medium":   PriorityMedium,
	"high":     PriorityHigh,
	"highest":  PriorityCritical,
	"critical": PriorityCritical,
	"blocker":  PriorityBlocker,
}

// typeToJira maps internal task types to Jira issue types. THEME and
// INITIATIVE use the names of Jira's premium-plan hierarchy levels.
var typeToJira = map[string]string{
	"TASK":       "Task",
	"STORY":      "Story",
	"EPIC":       "Epic",
	"SUBTASK":    "Sub-task",
	"INITIATIVE": "Initiative",
	"THEME":      "Theme",
}

// jiraToType maps Jira issue types to internal task types. Bugs and sub-tasks
// import as plain TASKs: BUG is no longer a type of its own, and the CSV
// carries no parent reference, so an imported SUBTASK could never satisfy the
// subtask-requires-parent rule.
var jiraToType = map[string]string{
	"task":     TaskTypeTask,
	"sub-task": TaskTypeTask,
	"bug":      TaskTypeTask,
	"story":    TaskTypeStory,
	"epic":     TaskTypeEpic,
}

// toJiraProjectKey converts an internal project slug to a Jira-safe project key
// (max 10 uppercase ASCII letters/digits). Falls back to "PROJ" if nothing valid remains.
// ToJiraProjectKey converts a project slug to a valid Jira project key.
// It strips non-alphanumeric characters, uppercases the result, truncates to
// 10 characters, and falls back to "PROJ" if nothing remains.
func ToJiraProjectKey(slug string) string {
	return toJiraProjectKey(slug)
}

func toJiraProjectKey(slug string) string {
	upper := strings.ToUpper(slug)
	var out []rune
	for _, c := range upper {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
			if len(out) == 10 {
				break
			}
		}
	}
	if len(out) == 0 {
		return "PROJ"
	}
	return string(out)
}

// jiraIssueKey returns the best available Jira issue key for a task.
// Prefers an already-set ExternalRef (e.g. the original Jira key from a prior import),
// falls back to "<PROJECT_KEY>-<seqNumber>", then "<PROJECT_KEY>-<shortID>".
func jiraIssueKey(projectKey string, t Task) string {
	if t.ExternalRef != nil && *t.ExternalRef != "" {
		return *t.ExternalRef
	}
	if t.SeqNumber != nil {
		return fmt.Sprintf("%s-%d", projectKey, *t.SeqNumber)
	}
	short := t.ID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s-%s", projectKey, strings.ToUpper(short))
}

// formatJiraTimestamp converts an RFC3339 internal timestamp to Jira CSV format.
// Returns the original string unchanged if parsing fails.
func formatJiraTimestamp(rfcTime string) string {
	t, err := time.Parse(time.RFC3339, rfcTime)
	if err != nil {
		return rfcTime
	}
	return t.UTC().Format(jiraDateFormat)
}

// parseJiraTimestamp converts a Jira CSV timestamp back to RFC3339 UTC.
// Returns the current time as RFC3339 if parsing fails.
func parseJiraTimestamp(jiraTime string) string {
	if jiraTime == "" {
		return ""
	}
	t, err := time.Parse(jiraDateFormat, strings.TrimSpace(jiraTime))
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// formatJiraDueDate converts an internal YYYY-MM-DD due date to Jira date format.
func formatJiraDueDate(date string) string {
	if date == "" {
		return ""
	}
	t, err := time.Parse(internalDueDateLayout, date)
	if err != nil {
		return date
	}
	return t.Format(jiraDueDateFormat)
}

// ParseJiraDueDate converts a Jira CSV due date back to the internal YYYY-MM-DD format.
// Accepts Jira format (02/Jan/06) and ISO 8601 (2006-01-02); returns "" on failure.
func ParseJiraDueDate(date string) string { return parseJiraDueDate(date) }

// parseJiraDueDate converts a Jira CSV due date back to the internal YYYY-MM-DD format.
// Accepts Jira format (02/Jan/06) and ISO 8601 (2006-01-02); returns "" on failure.
func parseJiraDueDate(date string) string {
	if date == "" {
		return ""
	}
	if t, err := time.Parse(jiraDueDateFormat, strings.TrimSpace(date)); err == nil {
		return t.Format(internalDueDateLayout)
	}
	if _, err := time.Parse(internalDueDateLayout, strings.TrimSpace(date)); err == nil {
		return strings.TrimSpace(date)
	}
	return ""
}

// FormatCommentValue formats a comment in the Jira CSV comment format.
// See formatCommentValue for details.
func FormatCommentValue(createdAt, authorIdentifier, text string) string {
	return formatCommentValue(createdAt, authorIdentifier, text)
}

// formatCommentValue formats a comment in the Jira CSV comment format:
//
//	"DD/Mon/YY h:mm AM/PM;authorIdentifier;commentText"
//
// authorIdentifier can be an email or a Jira account ID.
// Falls back to plain text when the timestamp cannot be parsed.
func formatCommentValue(createdAt, authorIdentifier, text string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		if authorIdentifier != "" {
			return authorIdentifier + ";" + text
		}
		return text
	}
	return fmt.Sprintf("%s;%s;%s", t.UTC().Format(jiraDateFormat), authorIdentifier, text)
}

// ParseCommentValue parses a Jira-format comment value.
// See parseCommentValue for details.
func ParseCommentValue(val string) (dateStr, author, text string, ok bool) {
	return parseCommentValue(val)
}

// parseCommentValue parses a Jira-format comment value.
// Returns (dateStr, author, text, ok=true) when the "date;author;text" format is
// recognised; otherwise returns ("", "", rawValue, false).
// SplitN(..., 3) ensures the text segment is never truncated by embedded semicolons.
func parseCommentValue(val string) (dateStr, author, text string, ok bool) {
	parts := strings.SplitN(val, ";", 3)
	if len(parts) != 3 {
		return "", "", val, false
	}
	if _, err := time.Parse(jiraDateFormat, strings.TrimSpace(parts[0])); err != nil {
		return "", "", val, false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), parts[2], true
}

// jiraCommentDateToRFC converts a parsed Jira comment date string to RFC3339 UTC.
func jiraCommentDateToRFC(jiraDate string) string {
	t, err := time.Parse(jiraDateFormat, jiraDate)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// formatAttachmentValue formats an attachment in the Jira CSV attachment format:
//
//	"DD/Mon/YY h:mm AM/PM;authorIdentifier;filename;URL"
//
// The author is empty for URL-backed attachments created by an import (we do
// not track an author for external links); Jira tolerates an empty field.
func formatAttachmentValue(createdAt, authorIdentifier, filename, url string) string {
	dateStr := ""
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		dateStr = t.UTC().Format(jiraDateFormat)
	}
	return fmt.Sprintf("%s;%s;%s;%s", dateStr, authorIdentifier, filename, url)
}

// parseAttachmentValue parses a Jira-format attachment value
// ("date;author;filename;URL"). Filenames may themselves contain semicolons
// and are impossible to disambiguate in general, so the fields are assigned
// positionally from the outside in: first two are date/author, the LAST is
// the URL, and everything between is the filename. Returns a non-empty
// reason when the value is malformed; such cells are skipped with a warning
// rather than failing the whole row.
func parseAttachmentValue(val string) (createdAtRFC, author, filename, url, reason string) {
	parts := strings.Split(val, ";")
	if len(parts) < 4 {
		return "", "", "", "", "expected \"date;author;filename;URL\""
	}
	url = strings.TrimSpace(parts[len(parts)-1])
	if !isImportableAttachmentURL(url) {
		return "", "", "", "", fmt.Sprintf("attachment URL %q is not an absolute http(s) URL", url)
	}
	if t, err := time.Parse(jiraDateFormat, strings.TrimSpace(parts[0])); err == nil {
		createdAtRFC = t.UTC().Format(time.RFC3339)
	}
	author = strings.TrimSpace(parts[1])
	filename = strings.TrimSpace(strings.Join(parts[2:len(parts)-1], ";"))
	if filename == "" {
		filename = "attachment"
	}
	return createdAtRFC, author, filename, url, ""
}

// isImportableAttachmentURL accepts only absolute http(s) URLs for imported
// attachment links. Deliberately stricter than safeHref (no mailto, no
// relative paths): the value came from a Jira export and is stored verbatim
// as a clickable external link.
func isImportableAttachmentURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// maxImportWarningDetails caps how many per-row warnings the import report
// returns. The row cap is 5000 and several warnings can arise per row, so an
// unbounded list could dwarf the payload; overflow is summarised in one final
// entry instead.
const maxImportWarningDetails = 200

// ImportResult summarises a CSV import operation.
type ImportResult struct {
	Imported int `json:"imported"`
	// AttachmentsImported counts URL-backed attachment records created (or, on
	// a dry run, that would be created) from repeating Attachment columns.
	AttachmentsImported int                `json:"attachmentsImported"`
	Skipped             int                `json:"skipped"`
	DryRun              bool               `json:"dryRun"`
	Errors              []ImportRowError   `json:"errors,omitempty"`
	Warnings            []ImportRowWarning `json:"warnings,omitempty"`

	// suppressedWarnings counts warnings dropped beyond maxImportWarningDetails.
	suppressedWarnings int
}

// addWarning appends a warning, enforcing the detail cap.
func (r *ImportResult) addWarning(w ImportRowWarning) {
	if len(r.Warnings) >= maxImportWarningDetails {
		r.suppressedWarnings++
		return
	}
	r.Warnings = append(r.Warnings, w)
}

// finalizeWarnings folds any suppressed overflow into one summary entry.
func (r *ImportResult) finalizeWarnings() {
	if r.suppressedWarnings > 0 {
		r.Warnings = append(r.Warnings, ImportRowWarning{
			Row:     0,
			Message: fmt.Sprintf("%d further warnings suppressed (showing the first %d)", r.suppressedWarnings, maxImportWarningDetails),
		})
	}
}

// ImportRowError describes a validation failure for a single CSV row.
type ImportRowError struct {
	Row      int    `json:"row"`
	IssueKey string `json:"issueKey,omitempty"`
	Message  string `json:"message"`
}

// ImportRowWarning describes a non-fatal issue for a single CSV row (or the file overall).
type ImportRowWarning struct {
	Row      int    `json:"row"`
	IssueKey string `json:"issueKey,omitempty"`
	Message  string `json:"message"`
}

// exportRow pairs a task with its comments and URL-backed attachments for the
// CSV writer. Only external-link attachments are exported: uploaded files
// live behind authenticated storage and have no stable public URL.
type exportRow struct {
	task        Task
	comments    []TaskComment
	attachments []TaskAttachment
}

// writeJiraCSV writes a Jira-compatible CSV to w.
// userEmails maps internal user IDs to email addresses.
// userMapping maps email addresses to Jira account IDs (overrides when set).
func writeJiraCSV(w io.Writer, projectKey string, rows []exportRow, userEmails, userMapping map[string]string) error {
	maxComments := 0
	maxAttachments := 0
	for _, r := range rows {
		if len(r.comments) > maxComments {
			maxComments = len(r.comments)
		}
		if len(r.attachments) > maxAttachments {
			maxAttachments = len(r.attachments)
		}
	}

	cw := csv.NewWriter(w)

	header := make([]string, len(jiraFixedHeaders))
	copy(header, jiraFixedHeaders)
	for i := 0; i < maxComments; i++ {
		header = append(header, "Comment")
	}
	for i := 0; i < maxAttachments; i++ {
		header = append(header, "Attachment")
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, row := range rows {
		record := buildExportRecord(projectKey, row, userEmails, userMapping, maxComments, maxAttachments)
		for i := range record {
			record[i] = sanitizeCSVCell(record[i])
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}

// buildExportRecord converts a task, its comments, and its URL-backed
// attachments into a CSV record.
func buildExportRecord(projectKey string, row exportRow, userEmails, userMapping map[string]string, commentCols, attachmentCols int) []string {
	t := row.task

	assigneeIdentifier := ""
	if t.AssigneeID != nil {
		assigneeIdentifier = resolveJiraIdentifier(userEmails[*t.AssigneeID], userMapping)
	}
	reporterIdentifier := ""
	if t.ReporterID != nil {
		reporterIdentifier = resolveJiraIdentifier(userEmails[*t.ReporterID], userMapping)
	}

	dueDate := ""
	if t.DueDate != nil && *t.DueDate != "" {
		dueDate = formatJiraDueDate(*t.DueDate)
	}

	status := statusToJira[t.Status]
	if status == "" {
		status = t.Status
	}
	priority := priorityToJira[t.Priority]
	if priority == "" {
		priority = t.Priority
	}
	issueType := typeToJira[t.TaskType]
	if issueType == "" {
		issueType = "Task"
	}

	record := []string{
		jiraIssueKey(projectKey, t),
		projectKey,
		t.Title,
		issueType,
		status,
		priority,
		assigneeIdentifier,
		reporterIdentifier,
		// Descriptions are stored as constrained HTML; export them as plain text
		// so spreadsheets do not display raw markup and a re-import round-trips
		// through the same sanitizer.
		StripHTMLToText(t.Description),
		formatJiraTimestamp(t.CreatedAt),
		formatJiraTimestamp(t.UpdatedAt),
		dueDate,
		"", // Labels — not in domain model
	}

	for i := 0; i < commentCols; i++ {
		if i < len(row.comments) {
			c := row.comments[i]
			authorEmail := userEmails[c.AuthorID]
			authorIdentifier := resolveJiraIdentifier(authorEmail, userMapping)
			// Comments are stored as constrained HTML; export as plain text.
			record = append(record, formatCommentValue(c.CreatedAt, authorIdentifier, StripHTMLToText(c.Text)))
		} else {
			record = append(record, "")
		}
	}

	for i := 0; i < attachmentCols; i++ {
		if i < len(row.attachments) {
			a := row.attachments[i]
			record = append(record, formatAttachmentValue(a.CreatedAt, "", a.Filename, a.ExternalURL))
		} else {
			record = append(record, "")
		}
	}

	return record
}

// sanitizeCSVCell neutralizes spreadsheet formula (CSV) injection: a cell a
// spreadsheet would interpret as a formula (leading =, +, -, @, or a leading
// tab/CR) is prefixed with a single quote so Excel/Sheets/LibreOffice render it
// as literal text. Jira re-import is unaffected — it does not evaluate formulas.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// resolveJiraIdentifier returns the Jira account ID for email if found in userMapping,
// otherwise returns email as-is.
func resolveJiraIdentifier(email string, userMapping map[string]string) string {
	if email == "" {
		return ""
	}
	if jiraID, ok := userMapping[email]; ok && jiraID != "" {
		return jiraID
	}
	return email
}

// parsedCSV holds the result of reading and indexing a Jira CSV header+rows.
type parsedCSV struct {
	records           [][]string
	commentIndices    []int
	attachmentIndices []int
	// columnIndex maps lowercase header names to their column position (first occurrence).
	columnIndex    map[string]int
	unknownHeaders []string
}

// knownJiraHeaderSet lists all column names that the importer understands.
var knownJiraHeaderSet = map[string]bool{
	"issue key":   true,
	"project key": true,
	"summary":     true,
	"issue type":  true,
	"status":      true,
	"priority":    true,
	"assignee":    true,
	"reporter":    true,
	"description": true,
	"created":     true,
	"updated":     true,
	"due date":    true,
	"labels":      true,
	"comment":     true,
	"attachment":  true,
}

// parseJiraCSVReader reads and indexes a Jira-format CSV from r.
func parseJiraCSVReader(r io.Reader) (*parsedCSV, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // allow variable number of fields per row
	cr.LazyQuotes = true    // tolerate common CSV quoting quirks

	allRows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV parse error: %w", err)
	}
	if len(allRows) < 1 {
		return nil, fmt.Errorf("CSV is empty")
	}
	if len(allRows) > maxImportRows+1 {
		return nil, fmt.Errorf("CSV exceeds maximum of %d data rows", maxImportRows)
	}

	header := allRows[0]
	colIdx := make(map[string]int)
	var commentIndices []int
	var attachmentIndices []int
	var unknownHeaders []string

	for i, h := range header {
		norm := strings.ToLower(strings.TrimSpace(h))
		switch {
		case norm == "comment":
			commentIndices = append(commentIndices, i)
		case norm == "attachment":
			attachmentIndices = append(attachmentIndices, i)
		case knownJiraHeaderSet[norm]:
			if _, exists := colIdx[norm]; !exists {
				colIdx[norm] = i
			}
		case norm != "":
			unknownHeaders = append(unknownHeaders, h)
		}
	}

	if _, ok := colIdx["summary"]; !ok {
		return nil, fmt.Errorf("CSV is missing required 'Summary' column")
	}

	return &parsedCSV{
		records:           allRows[1:],
		commentIndices:    commentIndices,
		attachmentIndices: attachmentIndices,
		columnIndex:       colIdx,
		unknownHeaders:    unknownHeaders,
	}, nil
}

// csvField safely retrieves a field by (lowercased) column name from a CSV record.
// Returns "" when the column is absent or the record is too short.
func csvField(record []string, colIdx map[string]int, header string) string {
	idx, ok := colIdx[strings.ToLower(strings.TrimSpace(header))]
	if !ok {
		return ""
	}
	if idx >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[idx])
}

// loadUserEmailsForIDs batch-fetches user emails for a list of internal user IDs.
// Unknown IDs are silently omitted from the result map.
func loadUserEmailsForIDs(db *sql.DB, ids []string) (map[string]string, error) {
	seen := make(map[string]struct{})
	var unique []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	result := make(map[string]string)
	if len(unique) == 0 {
		return result, nil
	}

	// Single batched lookup instead of one round-trip per user ID: pgx's
	// stdlib driver binds a Go []string directly to a Postgres array for
	// `= ANY($1)`.
	rows, err := db.Query(`SELECT id, email FROM users WHERE id = ANY($1)`, unique)
	if err != nil {
		return nil, fmt.Errorf("batch lookup user emails: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, fmt.Errorf("scan user email row: %w", err)
		}
		result[id] = email
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user email rows: %w", err)
	}
	return result, nil
}

// findUserIDByEmail looks up a user's internal ID by email address.
// Returns "" when not found (no error in that case).
func findUserIDByEmail(db *sql.DB, email string) (string, error) {
	if email == "" {
		return "", nil
	}
	var id string
	err := db.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup user by email %q: %w", email, err)
	}
	return id, nil
}

// resolveImportIdentifier resolves a raw Jira CSV user string (email, Jira account ID, or
// username) to an internal user ID using a lookup sequence:
//  1. importMappings[rawValue] → treat value as local email → look up by email
//  2. rawValue looks like an email → look up by email directly
//  3. Returns "" without error when the user is not found
func resolveImportIdentifier(db *sql.DB, raw string, importMappings map[string]string) (userID string, warned string, err error) {
	if raw == "" {
		return "", "", nil
	}
	// Check explicit mapping first.
	if mapped, ok := importMappings[raw]; ok && mapped != "" {
		id, err := findUserIDByEmail(db, mapped)
		if err != nil {
			return "", "", err
		}
		if id != "" {
			return id, "", nil
		}
		return "", fmt.Sprintf("mapped email %q for %q not found in users table", mapped, raw), nil
	}
	// Try direct email lookup.
	if strings.Contains(raw, "@") {
		id, err := findUserIDByEmail(db, raw)
		if err != nil {
			return "", "", err
		}
		if id != "" {
			return id, "", nil
		}
		return "", fmt.Sprintf("user %q not found in users table", raw), nil
	}
	// Could be a Jira account ID or username — cannot resolve without mapping.
	return "", fmt.Sprintf("cannot resolve %q (not an email; add to userMappings)", raw), nil
}
