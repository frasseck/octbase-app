package workmanagement_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
	"github.com/octbase/octbase-api/internal/workmanagement"
)

// ---- unit tests (no DB) ----

func TestFormatCommentValue_WithTimestamp(t *testing.T) {
	val := workmanagement.FormatCommentValue("2024-03-31T06:49:00Z", "557057:abc-123", "Fix the bug")
	// Date part: "31/Mar/24 6:49 AM", author, then text.
	if !strings.HasPrefix(val, "31/Mar/24") {
		t.Errorf("unexpected comment value: %q", val)
	}
	if !strings.Contains(val, "557057:abc-123") {
		t.Errorf("missing author in comment value: %q", val)
	}
	if !strings.HasSuffix(val, "Fix the bug") {
		t.Errorf("missing text in comment value: %q", val)
	}
}

func TestFormatCommentValue_FallbackOnBadTimestamp(t *testing.T) {
	val := workmanagement.FormatCommentValue("not-a-time", "alice@example.com", "Some text")
	if val != "alice@example.com;Some text" {
		t.Errorf("unexpected fallback value: %q", val)
	}
}

func TestParseCommentValue_ValidJiraFormat(t *testing.T) {
	raw := "31/Mar/24 6:49 AM;557057:abc-123;Fix the bug; with semicolons inside"
	dateStr, author, text, ok := workmanagement.ParseCommentValue(raw)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if dateStr != "31/Mar/24 6:49 AM" {
		t.Errorf("dateStr = %q, want \"31/Mar/24 6:49 AM\"", dateStr)
	}
	if author != "557057:abc-123" {
		t.Errorf("author = %q, want \"557057:abc-123\"", author)
	}
	if text != "Fix the bug; with semicolons inside" {
		t.Errorf("text = %q, want \"Fix the bug; with semicolons inside\"", text)
	}
}

func TestParseCommentValue_PlainText(t *testing.T) {
	raw := "just a plain comment"
	_, _, text, ok := workmanagement.ParseCommentValue(raw)
	if ok {
		t.Error("expected ok=false for plain text")
	}
	if text != raw {
		t.Errorf("text = %q, want %q", text, raw)
	}
}

func TestParseCommentValue_TwoSegmentsOnly(t *testing.T) {
	// Only two semicolons — not a valid Jira comment format.
	raw := "31/Mar/24 6:49 AM;557057:abc-123"
	_, _, text, ok := workmanagement.ParseCommentValue(raw)
	if ok {
		t.Error("expected ok=false for two-segment value")
	}
	if text != raw {
		t.Errorf("text = %q, want %q", text, raw)
	}
}

func TestToJiraProjectKey(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"my-project", "MYPROJECT"},
		{"hello-world-2024", "HELLOWORLD"},
		{"---", "PROJ"},
		{"short", "SHORT"},
		{"a", "A"},
	}
	for _, tc := range cases {
		got := workmanagement.ToJiraProjectKey(tc.slug)
		if got != tc.want {
			t.Errorf("ToJiraProjectKey(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestParseJiraDueDate(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"31/Mar/24", "2024-03-31"},
		{"2024-03-31", "2024-03-31"},
		{"", ""},
		{"not-a-date", ""},
	}
	for _, tc := range cases {
		got := workmanagement.ParseJiraDueDate(tc.input)
		if got != tc.want {
			t.Errorf("ParseJiraDueDate(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---- integration tests ----

func TestExportJiraCSV_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Export Test")
	tid := testutil.MustCreateTask(t, srv, pid, "First Task")

	// Add a comment to the task.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "Hello, world!"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Add a second task without comments.
	testutil.MustCreateTask(t, srv, pid, "Second Task")

	// Export.
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+pid+"/export/jira-csv", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	csvResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	defer func() { _ = csvResp.Body.Close() }()

	if csvResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(csvResp.Body)
		t.Fatalf("export status = %d, body: %s", csvResp.StatusCode, body)
	}
	ct := csvResp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}

	body, _ := io.ReadAll(csvResp.Body)
	csvContent := string(body)

	// Header row must contain the fixed columns.
	for _, col := range []string{"Summary", "Issue Key", "Project Key", "Status", "Priority", "Comment"} {
		if !strings.Contains(csvContent, col) {
			t.Errorf("CSV missing column %q", col)
		}
	}

	// Both tasks must appear by title.
	if !strings.Contains(csvContent, "First Task") {
		t.Error("CSV missing 'First Task'")
	}
	if !strings.Contains(csvContent, "Second Task") {
		t.Error("CSV missing 'Second Task'")
	}
	if !strings.Contains(csvContent, "Hello, world!") {
		t.Error("CSV missing comment text")
	}
}

func TestExportJiraCSV_EmptyProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Empty Project")

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+pid+"/export/jira-csv", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	csvResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	defer func() { _ = csvResp.Body.Close() }()
	testutil.AssertStatus(t, &http.Response{StatusCode: csvResp.StatusCode}, http.StatusOK)

	body, _ := io.ReadAll(csvResp.Body)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only), got %d", len(lines))
	}
}

func TestImportJiraCSV_BasicTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Import Test")

	csvData := `Issue Key,Summary,Issue Type,Status,Priority,Description
PROJ-1,Imported Task One,Task,To Do,High,Description one
PROJ-2,Imported Task Two,Bug,In Progress,Medium,Description two
`
	result := doImportCSV(t, srv, pid, csvData)

	if result["imported"].(float64) != 2 {
		t.Errorf("imported = %v, want 2", result["imported"])
	}
	if result["skipped"].(float64) != 0 {
		t.Errorf("skipped = %v, want 0", result["skipped"])
	}

	// Verify tasks were created via the tasks list endpoint.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}

	// Check field mapping.
	titles := make(map[string]map[string]interface{})
	for _, task := range tasks {
		titles[task["title"].(string)] = task
	}
	t1, ok := titles["Imported Task One"]
	if !ok {
		t.Fatal("'Imported Task One' not found in tasks list")
	}
	if t1["taskType"] != "TASK" {
		t.Errorf("taskType = %v, want TASK", t1["taskType"])
	}
	if t1["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", t1["status"])
	}
	if t1["priority"] != "HIGH" {
		t.Errorf("priority = %v, want HIGH", t1["priority"])
	}
	if t1["externalRef"] != "PROJ-1" {
		t.Errorf("externalRef = %v, want PROJ-1", t1["externalRef"])
	}

	t2, ok := titles["Imported Task Two"]
	if !ok {
		t.Fatal("'Imported Task Two' not found in tasks list")
	}
	// Jira "Bug" imports as a plain TASK since the BUG type was retired.
	if t2["taskType"] != "TASK" {
		t.Errorf("taskType = %v, want TASK", t2["taskType"])
	}
	if t2["status"] != "IN_PROGRESS" {
		t.Errorf("status = %v, want IN_PROGRESS", t2["status"])
	}
}

func TestImportJiraCSV_WithComments(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Comment Import")

	csvData := `Summary,Comment,Comment` + "\n" +
		`Task With Comments,"31/Mar/24 6:49 AM;demo@octbase.dev;First comment","31/Mar/24 7:00 AM;demo@octbase.dev;Second comment"` + "\n"

	result := doImportCSV(t, srv, pid, csvData)
	if result["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1", result["imported"])
	}

	// Find the created task.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	taskID := tasks[0]["id"].(string)

	// Check comments.
	commentsResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID+"/comments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, commentsResp, http.StatusOK)
	var comments []map[string]interface{}
	testutil.DecodeJSON(t, commentsResp, &comments)
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0]["text"] != "First comment" {
		t.Errorf("comment[0].text = %q, want \"First comment\"", comments[0]["text"])
	}
	if comments[1]["text"] != "Second comment" {
		t.Errorf("comment[1].text = %q, want \"Second comment\"", comments[1]["text"])
	}
}

func TestImportJiraCSV_ValidationErrors(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Validation Test")

	long := strings.Repeat("x", 256)
	// A row containing only whitespace is parsed as a single-field record (an
	// entirely empty line is silently skipped by encoding/csv), trims to an
	// empty Summary, and is rejected by validation.
	csvData := fmt.Sprintf("Summary\n \n%s\nValid Task\n", long)

	result := doImportCSV(t, srv, pid, csvData)

	// 2 invalid rows (blank and too-long), 1 valid.
	if result["imported"].(float64) != 1 {
		t.Errorf("imported = %v, want 1", result["imported"])
	}
	if result["skipped"].(float64) != 2 {
		t.Errorf("skipped = %v, want 2", result["skipped"])
	}
	errs := result["errors"].([]interface{})
	if len(errs) != 2 {
		t.Errorf("len(errors) = %d, want 2", len(errs))
	}
}

func TestImportJiraCSV_DryRun(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Dry Run Test")

	csvData := "Summary\nDry Run Task\n"

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/projects/"+pid+"/import/jira-csv?dryRun=true",
		strings.NewReader(csvData))
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	req.Header.Set("Content-Type", "text/csv")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if result["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", result["dryRun"])
	}
	if result["imported"].(float64) != 1 {
		t.Errorf("imported = %v, want 1 (dry-run reflects what would be imported)", result["imported"])
	}

	// Verify nothing was actually persisted.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after dry-run, got %d", len(tasks))
	}
}

func TestImportJiraCSV_MissingSummaryColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Bad CSV Test")
	csvData := "Issue Key,Status\nPROJ-1,To Do\n"

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/projects/"+pid+"/import/jira-csv",
		strings.NewReader(csvData))
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	req.Header.Set("Content-Type", "text/csv")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestJiraCSVRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Create a project with 2 tasks, one with a comment.
	srcPID := testutil.MustCreateProject(t, srv, "Source Project")
	tid1 := testutil.MustCreateTask(t, srv, srcPID, "Round-trip Task One")
	testutil.MustCreateTask(t, srv, srcPID, "Round-trip Task Two")

	commentResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid1+"/comments",
		map[string]string{"text": "A comment with commas, and \"quotes\""}, testutil.DemoUserID)
	testutil.AssertStatus(t, commentResp, http.StatusCreated)
	_ = commentResp.Body.Close()

	// Export source project.
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+srcPID+"/export/jira-csv", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	exportResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = exportResp.Body.Close() }()
	testutil.AssertStatus(t, exportResp, http.StatusOK)
	csvBytes, _ := io.ReadAll(exportResp.Body)

	// Import exported CSV into a new project.
	dstPID := testutil.MustCreateProject(t, srv, "Destination Project")
	result := doImportCSV(t, srv, dstPID, string(csvBytes))

	if result["imported"].(float64) != 2 {
		t.Errorf("round-trip imported = %v, want 2", result["imported"])
	}

	// Verify tasks.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks in destination, got %d", len(tasks))
	}

	// Find the task that had a comment.
	var taskWithComment map[string]interface{}
	for _, task := range tasks {
		if task["title"] == "Round-trip Task One" {
			taskWithComment = task
			break
		}
	}
	if taskWithComment == nil {
		t.Fatal("'Round-trip Task One' not found in destination")
	}

	commentsResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskWithComment["id"].(string)+"/comments",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, commentsResp, http.StatusOK)
	var comments []map[string]interface{}
	testutil.DecodeJSON(t, commentsResp, &comments)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment after round-trip, got %d", len(comments))
	}
	if comments[0]["text"] != `A comment with commas, and "quotes"` {
		t.Errorf("comment text = %q", comments[0]["text"])
	}
}

func TestImportJiraCSV_UnknownColumnsWarned(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Unknown Cols Test")
	csvData := "Summary,CustomField1,AnotherUnknown\nTask A,foo,bar\n"

	result := doImportCSV(t, srv, pid, csvData)

	if result["imported"].(float64) != 1 {
		t.Errorf("imported = %v, want 1", result["imported"])
	}
	warnings, _ := result["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Error("expected at least one warning for unknown columns")
	}
	found := false
	for _, w := range warnings {
		wm := w.(map[string]interface{})
		if strings.Contains(wm["message"].(string), "CustomField1") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning for 'CustomField1' not found in: %v", warnings)
	}
}

// TestImportJiraCSV_DisabledEdition_Integration drives the full router of a
// TEAM-edition deployment (OCTBASE_EDITION=TEAM, Jira CSV import off): the route
// still exists but answers 403 FEATURE_DISABLED for an authenticated project
// member, and the auth middleware still rejects anonymous callers first. The
// export endpoint is not edition-gated and must keep working.
func TestImportJiraCSV_DisabledEdition_Integration(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db, testutil.WithJiraCSVImportDisabled())

	pid := testutil.MustCreateProject(t, srv, "Edition TEAM Test")
	csvData := "Summary\nShould not be imported\n"

	// Authenticated member → 403 with the stable FEATURE_DISABLED code.
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/projects/"+pid+"/import/jira-csv",
		strings.NewReader(csvData))
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	req.Header.Set("Content-Type", "text/csv")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	var errBody map[string]interface{}
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "FEATURE_DISABLED" {
		t.Errorf("code = %v, want FEATURE_DISABLED", errBody["code"])
	}

	// Anonymous caller → 401 from the JWT middleware before the gate.
	anonReq, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/projects/"+pid+"/import/jira-csv",
		strings.NewReader(csvData))
	anonReq.Header.Set("Content-Type", "text/csv")
	anonResp, err := http.DefaultClient.Do(anonReq)
	if err != nil {
		t.Fatalf("anonymous import: %v", err)
	}
	testutil.AssertStatus(t, anonResp, http.StatusUnauthorized)

	// Nothing was imported.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}

	// Export stays available regardless of edition.
	expReq, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+pid+"/export/jira-csv", nil)
	expReq.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	expResp, err := http.DefaultClient.Do(expReq)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = expResp.Body.Close() }()
	testutil.AssertStatus(t, expResp, http.StatusOK)
}

func TestExportJiraCSV_ProjectKeyOverride(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Key Override Test")
	testutil.MustCreateTask(t, srv, pid, "Some Task")

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+pid+"/export/jira-csv?projectKey=MYKEY",
		nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	testutil.AssertStatus(t, resp, http.StatusOK)

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "MYKEY") {
		t.Error("exported CSV does not contain overridden project key MYKEY")
	}
}

// doImportCSV sends a CSV string as a multipart upload to the import endpoint and
// returns the decoded JSON response body.
func doImportCSV(t *testing.T, srv *httptest.Server, projectID, csvContent string) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Write CSV file part with explicit Content-Type.
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="import.csv"`)
	h.Set("Content-Type", "text/csv")
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fmt.Fprint(fw, csvContent); err != nil {
		t.Fatalf("write CSV to form: %v", err)
	}
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/projects/"+projectID+"/import/jira-csv",
		&buf)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("import status = %d, body: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	return result
}

// ---- attachment import/export (§3 adoption gaps) ----

func TestImportJiraCSV_Attachments(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Attachment Import")

	// Two attachment cells: one valid Jira-format value (filename containing a
	// semicolon, resolved outside-in), one malformed (no usable URL) that must
	// be skipped with a warning instead of failing the row.
	csvData := `Issue Key,Summary,Attachment,Attachment` + "\n" +
		`PROJ-9,Task With Attachments,"31/Mar/24 6:49 AM;alice@example.com;design;v2.pdf;https://jira.example.com/secure/attachment/10001/design.pdf","31/Mar/24 6:50 AM;bob@example.com;broken.txt;not-a-url"` + "\n"

	result := doImportCSV(t, srv, pid, csvData)

	if result["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1", result["imported"])
	}
	if result["attachmentsImported"].(float64) != 1 {
		t.Errorf("attachmentsImported = %v, want 1", result["attachmentsImported"])
	}

	// The malformed cell surfaces as a warning carrying row + issue key.
	warnings, _ := result["warnings"].([]interface{})
	foundMalformed := false
	for _, w := range warnings {
		wm := w.(map[string]interface{})
		if strings.Contains(wm["message"].(string), "malformed attachment") {
			foundMalformed = true
			if wm["issueKey"] != "PROJ-9" {
				t.Errorf("warning issueKey = %v, want PROJ-9", wm["issueKey"])
			}
		}
	}
	if !foundMalformed {
		t.Errorf("no malformed-attachment warning in: %v", warnings)
	}

	// The valid cell became a URL-backed attachment record.
	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	attResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tasks[0]["id"].(string)+"/attachments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, attResp, http.StatusOK)
	var atts []map[string]interface{}
	testutil.DecodeJSON(t, attResp, &atts)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0]["filename"] != "design;v2.pdf" {
		t.Errorf("filename = %v, want design;v2.pdf", atts[0]["filename"])
	}
	if atts[0]["externalUrl"] != "https://jira.example.com/secure/attachment/10001/design.pdf" {
		t.Errorf("externalUrl = %v", atts[0]["externalUrl"])
	}
}

func TestJiraCSVRoundTrip_Attachments(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID := testutil.MustCreateProject(t, srv, "Attachment Source")
	tid := testutil.MustCreateTask(t, srv, srcPID, "Task With Link")

	// One URL-backed attachment (exported) and one plain metadata record
	// without a URL (must not produce an Attachment cell).
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]any{"filename": "spec.pdf", "externalUrl": "https://jira.example.com/secure/attachment/42/spec.pdf"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]any{"filename": "no-url.txt"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/projects/"+srcPID+"/export/jira-csv", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	exportResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = exportResp.Body.Close() }()
	testutil.AssertStatus(t, exportResp, http.StatusOK)
	csvBytes, _ := io.ReadAll(exportResp.Body)
	csvContent := string(csvBytes)

	if !strings.Contains(csvContent, "Attachment") {
		t.Fatalf("export missing Attachment column: %s", csvContent)
	}
	if !strings.Contains(csvContent, "https://jira.example.com/secure/attachment/42/spec.pdf") {
		t.Errorf("export missing attachment URL: %s", csvContent)
	}
	if strings.Contains(csvContent, "no-url.txt") {
		t.Errorf("export must not include attachments without a URL: %s", csvContent)
	}

	// Re-import into a fresh project: the link must survive the round-trip.
	dstPID := testutil.MustCreateProject(t, srv, "Attachment Destination")
	result := doImportCSV(t, srv, dstPID, csvContent)
	if result["attachmentsImported"].(float64) != 1 {
		t.Fatalf("round-trip attachmentsImported = %v, want 1", result["attachmentsImported"])
	}

	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	attResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tasks[0]["id"].(string)+"/attachments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, attResp, http.StatusOK)
	var atts []map[string]interface{}
	testutil.DecodeJSON(t, attResp, &atts)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment after round-trip, got %d", len(atts))
	}
	if atts[0]["filename"] != "spec.pdf" {
		t.Errorf("filename = %v, want spec.pdf", atts[0]["filename"])
	}
	if atts[0]["externalUrl"] != "https://jira.example.com/secure/attachment/42/spec.pdf" {
		t.Errorf("externalUrl = %v", atts[0]["externalUrl"])
	}
}

func TestImportJiraCSV_UnknownStatusWarned(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Unknown Status Test")
	csvData := "Issue Key,Summary,Status\nPROJ-3,Oddly Statused,Blocked Forever\n"

	result := doImportCSV(t, srv, pid, csvData)
	if result["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1", result["imported"])
	}

	warnings, _ := result["warnings"].([]interface{})
	found := false
	for _, w := range warnings {
		wm := w.(map[string]interface{})
		if strings.Contains(wm["message"].(string), "unknown status") && wm["issueKey"] == "PROJ-3" {
			found = true
		}
	}
	if !found {
		t.Errorf("no unknown-status warning with issue key in: %v", warnings)
	}

	tasksResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, tasksResp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, tasksResp, &tasks)
	if len(tasks) != 1 || tasks[0]["status"] != "PLANNED" {
		t.Errorf("expected 1 task with fallback status PLANNED, got %v", tasks)
	}
}
