package workmanagement_test

import (
	"archive/zip"
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
	"unicode/utf8"

	"github.com/octbase/octbase-api/internal/testutil"
)

// doExportProject downloads the project export ZIP and returns its raw bytes.
func doExportProject(t *testing.T, srv *httptest.Server, projectID string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/projects/"+projectID+"/export", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, body: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "-export.zip") {
		t.Errorf("Content-Disposition = %q, want *-export.zip", cd)
	}
	return body
}

// doImportProject posts a project export archive; returns the decoded result.
// wantStatus lets error-path tests assert non-200 responses.
func doImportProject(t *testing.T, srv *httptest.Server, projectID, userID string, archive []byte, query string, wantStatus int) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="export.zip"`)
	h.Set("Content-Type", "application/zip")
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(archive); err != nil {
		t.Fatalf("write archive to form: %v", err)
	}
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/projects/"+projectID+"/import"+query, &buf)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(userID))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("import status = %d, want %d, body: %s", resp.StatusCode, wantStatus, body)
	}
	var result map[string]interface{}
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
			t.Fatalf("decode import response: %v (body: %s)", err, body)
		}
	}
	return result
}

// buildTransferFixture creates a project with one task carrying a comment
// thread, a link, an uploaded file, an external attachment, plus a parent and
// child page (the child references the task). Returns projectID, taskID.
func buildTransferFixture(t *testing.T, srv *httptest.Server) (string, string) {
	t.Helper()
	pid := testutil.MustCreateProject(t, srv, "Transfer Source")
	tid := testutil.MustCreateTask(t, srv, pid, "Exported Task")

	// Comment + threaded reply.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "Top-level comment"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var comment map[string]interface{}
	testutil.DecodeJSON(t, resp, &comment)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "A reply", "parentId": comment["id"].(string)}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Link.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/links",
		map[string]string{"url": "https://example.com/spec", "title": "Spec"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Uploaded file (real PNG so the sniffer accepts it on export and import).
	up := uploadFile(t, srv, tid, testutil.DemoUserID, "shot.png", "image/png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusCreated)
	_ = up.Body.Close()

	// External attachment.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]string{"filename": "design doc", "externalUrl": "https://example.com/doc"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Parent page + child page referencing the task.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]interface{}{"title": "Handbook", "content": "= Handbook\n\nWelcome."}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var parentPage map[string]interface{}
	testutil.DecodeJSON(t, resp, &parentPage)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]interface{}{
			"title":        "Task Notes",
			"content":      "See TASK-" + tid + " for details.",
			"parentPageId": parentPage["id"].(string),
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	return pid, tid
}

func TestProjectExportImport_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID, srcTID := buildTransferFixture(t, srv)
	archive := doExportProject(t, srv, srcPID)

	// The archive must contain the manifest and the uploaded file bytes.
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("export is not a valid zip: %v", err)
	}
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["project.json"] {
		t.Fatal("archive missing project.json")
	}
	var fileEntry string
	for n := range names {
		if strings.HasPrefix(n, "files/") {
			fileEntry = n
		}
	}
	if fileEntry == "" {
		t.Fatal("archive missing files/ entry for the uploaded attachment")
	}
	mf, _ := zr.Open("project.json")
	manifestBytes, _ := io.ReadAll(mf)
	_ = mf.Close()
	var manifest map[string]interface{}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest["format"] != "octbase-project-export" {
		t.Errorf("manifest format = %v", manifest["format"])
	}
	if !strings.Contains(string(manifestBytes), srcTID) {
		t.Error("manifest does not contain the exported task ID")
	}
	if !strings.Contains(string(manifestBytes), "demo@octbase.dev") {
		t.Error("manifest does not reference users by email")
	}

	// Import into a fresh project.
	dstPID := testutil.MustCreateProject(t, srv, "Transfer Target")
	result := doImportProject(t, srv, dstPID, testutil.DemoUserID, archive, "", http.StatusOK)

	assertCount := func(key string, want float64) {
		t.Helper()
		if got, _ := result[key].(float64); got != want {
			t.Errorf("%s = %v, want %v (result: %v)", key, result[key], want, result)
		}
	}
	assertCount("tasks", 1)
	assertCount("comments", 2)
	assertCount("links", 1)
	assertCount("attachments", 2)
	assertCount("files", 1)
	assertCount("pages", 2)
	assertCount("skipped", 0)

	// Task restored with a new ID and intact fields.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task in target project, got %d", len(tasks))
	}
	newTask := tasks[0]
	newTID := newTask["id"].(string)
	if newTID == srcTID {
		t.Error("imported task kept its old ID")
	}
	if newTask["title"] != "Exported Task" {
		t.Errorf("title = %v", newTask["title"])
	}

	// Comment thread restored (reply points at the new parent comment).
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+newTID+"/comments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var comments []map[string]interface{}
	testutil.DecodeJSON(t, resp, &comments)
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	var topID string
	var replyParent interface{}
	for _, c := range comments {
		if c["text"] == "Top-level comment" {
			topID = c["id"].(string)
		}
		if c["text"] == "A reply" {
			replyParent = c["parentId"]
		}
	}
	if topID == "" || replyParent != topID {
		t.Errorf("comment thread not remapped: top=%q replyParent=%v", topID, replyParent)
	}

	// Attachments restored; the uploaded file is downloadable with its bytes.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+newTID+"/attachments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var atts []map[string]interface{}
	testutil.DecodeJSON(t, resp, &atts)
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(atts))
	}
	var uploadedID string
	for _, a := range atts {
		if a["filename"] == "shot.png" {
			uploadedID = a["id"].(string)
		}
	}
	if uploadedID == "" {
		t.Fatal("uploaded attachment not found after import")
	}
	dlReq, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/tasks/"+newTID+"/attachments/"+uploadedID+"/content", nil)
	dlReq.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	dl, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	dlBytes, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if dl.StatusCode != http.StatusOK || !bytes.Equal(dlBytes, pngBytes) {
		t.Errorf("re-imported file differs (status %d, %d bytes, want %d)", dl.StatusCode, len(dlBytes), len(pngBytes))
	}

	// Pages restored: hierarchy intact and TASK reference remapped.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/pages", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var pages []map[string]interface{}
	testutil.DecodeJSON(t, resp, &pages)
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	byTitle := make(map[string]map[string]interface{})
	for _, p := range pages {
		byTitle[p["title"].(string)] = p
	}
	parent, child := byTitle["Handbook"], byTitle["Task Notes"]
	if parent == nil || child == nil {
		t.Fatalf("pages missing after import: %v", byTitle)
	}
	if child["parentPageId"] != parent["id"] {
		t.Errorf("page hierarchy not remapped: parentPageId=%v want %v", child["parentPageId"], parent["id"])
	}
	content, _ := child["content"].(string)
	if !strings.Contains(content, "TASK-"+newTID) {
		t.Errorf("page content not remapped to new task ID: %q", content)
	}
	if strings.Contains(content, srcTID) {
		t.Errorf("page content still references the old task ID: %q", content)
	}
}

func TestProjectImport_DryRun(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID, _ := buildTransferFixture(t, srv)
	archive := doExportProject(t, srv, srcPID)

	dstPID := testutil.MustCreateProject(t, srv, "DryRun Target")
	result := doImportProject(t, srv, dstPID, testutil.DemoUserID, archive, "?dryRun=true", http.StatusOK)
	if result["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", result["dryRun"])
	}
	if got, _ := result["tasks"].(float64); got != 1 {
		t.Errorf("tasks = %v, want 1", result["tasks"])
	}

	// Nothing persisted.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 0 {
		t.Errorf("dry run persisted %d tasks", len(tasks))
	}
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/pages", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var pages []map[string]interface{}
	testutil.DecodeJSON(t, resp, &pages)
	if len(pages) != 0 {
		t.Errorf("dry run persisted %d pages", len(pages))
	}
}

func TestProjectImport_ViewerForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Viewer Import")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	archive := makeArchive(t, `{"format":"octbase-project-export","formatVersion":1,"tasks":[]}`)
	result := doImportProject(t, srv, pid, testutil.SecondUserID, archive, "", http.StatusForbidden)
	if result["code"] != "FORBIDDEN" {
		t.Errorf("code = %v, want FORBIDDEN", result["code"])
	}
}

func TestProjectImport_InvalidArchive(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Bad Archive")
	result := doImportProject(t, srv, pid, testutil.DemoUserID, []byte("this is not a zip"), "", http.StatusBadRequest)
	if result["code"] != "IMPORT_ARCHIVE_INVALID" {
		t.Errorf("code = %v, want IMPORT_ARCHIVE_INVALID", result["code"])
	}
}

func TestProjectImport_WrongFormat(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Wrong Format")

	archive := makeArchive(t, `{"format":"something-else","formatVersion":1}`)
	result := doImportProject(t, srv, pid, testutil.DemoUserID, archive, "", http.StatusBadRequest)
	if result["code"] != "IMPORT_FORMAT_UNSUPPORTED" {
		t.Errorf("code = %v, want IMPORT_FORMAT_UNSUPPORTED", result["code"])
	}

	archive = makeArchive(t, `{"format":"octbase-project-export","formatVersion":99}`)
	result = doImportProject(t, srv, pid, testutil.DemoUserID, archive, "", http.StatusBadRequest)
	if result["code"] != "IMPORT_FORMAT_UNSUPPORTED" {
		t.Errorf("code = %v, want IMPORT_FORMAT_UNSUPPORTED", result["code"])
	}
}

func TestProjectImport_SkipsInvalidTasksAndDisallowedFiles(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Partial Import")

	// One valid task, one without a title, plus an attachment whose bytes are
	// a script (disallowed type) — the task and the valid parts must survive.
	manifest := `{
	  "format": "octbase-project-export",
	  "formatVersion": 1,
	  "tasks": [
	    {"id":"aaaaaaaa-0000-0000-0000-000000000001","title":"Valid Task","taskType":"BUG","status":"IN_PROGRESS","priority":"HIGH",
	     "attachments":[{"id":"bbbbbbbb-0000-0000-0000-000000000001","filename":"evil.sh","contentType":"application/x-sh","file":"files/evil"}]},
	    {"id":"aaaaaaaa-0000-0000-0000-000000000002","title":"   "}
	  ]
	}`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mw, _ := zw.Create("project.json")
	_, _ = mw.Write([]byte(manifest))
	fw, _ := zw.Create("files/evil")
	_, _ = fw.Write([]byte("#!/bin/sh\necho pwned\n"))
	_ = zw.Close()

	result := doImportProject(t, srv, pid, testutil.DemoUserID, buf.Bytes(), "", http.StatusOK)
	if got, _ := result["tasks"].(float64); got != 1 {
		t.Errorf("tasks = %v, want 1", result["tasks"])
	}
	if got, _ := result["skipped"].(float64); got != 1 {
		t.Errorf("skipped = %v, want 1", result["skipped"])
	}
	if got, _ := result["files"].(float64); got != 0 {
		t.Errorf("files = %v, want 0 (disallowed type must be skipped)", result["files"])
	}
	warnings := fmt.Sprintf("%v", result["warnings"])
	if !strings.Contains(warnings, "not allowed") {
		t.Errorf("expected a file-type warning, got: %s", warnings)
	}

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	// Legacy exports may still carry BUG; the import converts it to TASK.
	if tasks[0]["status"] != "IN_PROGRESS" || tasks[0]["priority"] != "HIGH" || tasks[0]["taskType"] != "TASK" {
		t.Errorf("imported task fields wrong: %v", tasks[0])
	}
}

// doImportProjectAsNew posts a project export archive to the
// create-project-from-export endpoint; returns the decoded result.
func doImportProjectAsNew(t *testing.T, srv *httptest.Server, userID string, archive []byte, query string, wantStatus int) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="export.zip"`)
	h.Set("Content-Type", "application/zip")
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(archive); err != nil {
		t.Fatalf("write archive to form: %v", err)
	}
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/projects/import"+query, &buf)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(userID))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("import request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("import status = %d, want %d, body: %s", resp.StatusCode, wantStatus, body)
	}
	var result map[string]interface{}
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
			t.Fatalf("decode import response: %v (body: %s)", err, body)
		}
	}
	return result
}

func TestProjectImportAsNew_CreatesProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID, srcTID := buildTransferFixture(t, srv)
	archive := doExportProject(t, srv, srcPID)

	result := doImportProjectAsNew(t, srv, testutil.DemoUserID, archive, "", http.StatusCreated)
	project, _ := result["project"].(map[string]interface{})
	if project == nil {
		t.Fatalf("response has no project: %v", result)
	}
	newPID, _ := project["id"].(string)
	if newPID == "" || newPID == srcPID {
		t.Fatalf("project id = %q, want a fresh ID", newPID)
	}
	if project["name"] != "Transfer Source" {
		t.Errorf("name = %v, want Transfer Source", project["name"])
	}
	// The source project still owns the slug, so the copy must be de-conflicted.
	if project["slug"] != "transfer-source-2" {
		t.Errorf("slug = %v, want transfer-source-2", project["slug"])
	}
	// Visibility comes from the manifest (the fixture project is PUBLIC).
	if project["visibility"] != "PUBLIC" || project["status"] != "ACTIVE" {
		t.Errorf("visibility/status = %v/%v", project["visibility"], project["status"])
	}
	imported, _ := result["import"].(map[string]interface{})
	if imported == nil {
		t.Fatalf("response has no import summary: %v", result)
	}
	if got, _ := imported["tasks"].(float64); got != 1 {
		t.Errorf("import.tasks = %v, want 1", imported["tasks"])
	}
	if got, _ := imported["pages"].(float64); got != 2 {
		t.Errorf("import.pages = %v, want 2", imported["pages"])
	}

	// The creator is PROJECT_OWNER: content in the new project is reachable and
	// the task got a fresh ID.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+newPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task in new project, got %d", len(tasks))
	}
	if tasks[0]["id"] == srcTID {
		t.Error("imported task kept its old ID")
	}
	if tasks[0]["title"] != "Exported Task" {
		t.Errorf("title = %v", tasks[0]["title"])
	}

	// A second import of the same archive de-conflicts the slug again.
	result = doImportProjectAsNew(t, srv, testutil.DemoUserID, archive, "", http.StatusCreated)
	project2, _ := result["project"].(map[string]interface{})
	if project2 == nil || project2["slug"] != "transfer-source-3" {
		t.Errorf("second import slug = %v, want transfer-source-3", project2["slug"])
	}
}

func TestProjectImportAsNew_DryRun(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID, _ := buildTransferFixture(t, srv)
	archive := doExportProject(t, srv, srcPID)

	result := doImportProjectAsNew(t, srv, testutil.DemoUserID, archive, "?dryRun=true", http.StatusOK)
	imported, _ := result["import"].(map[string]interface{})
	if imported == nil || imported["dryRun"] != true {
		t.Fatalf("import.dryRun = %v, want true", result)
	}
	if got, _ := imported["tasks"].(float64); got != 1 {
		t.Errorf("import.tasks = %v, want 1", imported["tasks"])
	}
	// Nothing persisted: the would-be project must not exist.
	project, _ := result["project"].(map[string]interface{})
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+project["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestProjectImportAsNew_NonAdminForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	archive := makeArchive(t, `{"format":"octbase-project-export","formatVersion":1,"project":{"name":"X"},"tasks":[]}`)
	result := doImportProjectAsNew(t, srv, testutil.SecondUserID, archive, "", http.StatusForbidden)
	if result["code"] != "FORBIDDEN" {
		t.Errorf("code = %v, want FORBIDDEN", result["code"])
	}
}

func TestProjectImportAsNew_MissingProjectName(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	archive := makeArchive(t, `{"format":"octbase-project-export","formatVersion":1,"tasks":[]}`)
	result := doImportProjectAsNew(t, srv, testutil.DemoUserID, archive, "", http.StatusBadRequest)
	if result["code"] != "IMPORT_ARCHIVE_INVALID" {
		t.Errorf("code = %v, want IMPORT_ARCHIVE_INVALID", result["code"])
	}
}

// TestProjectExportImport_PlanningRoundTrip covers what used to fall out of the
// archive entirely: releases, sprints, boards with their lanes, task categories
// and task templates — and, for the task, the placement that references them.
// Without this an imported project was a flat backlog.
func TestProjectExportImport_PlanningRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	srcPID := testutil.MustCreateProject(t, srv, "Planning Source")
	relID := testutil.MustCreateRelease(t, srv, srcPID, "1.0.0")

	// Sprint linked to that release.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+srcPID+"/sprints",
		map[string]any{"name": "Sprint 1", "goal": "Ship it", "startDate": "2026-01-05",
			"endDate": "2026-01-19", "releaseId": relID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var sprint map[string]any
	testutil.DecodeJSON(t, resp, &sprint)

	// Board with a custom lane, plus a category and a template.
	boardID := testutil.MustCreateBoard(t, srv, srcPID)
	colID := testutil.MustAddColumn(t, srv, boardID, "Refinement", "REFINEMENT", 0)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+srcPID+"/task-categories",
		map[string]any{"name": "Infrastructure", "description": "Platform work", "color": "blue"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+srcPID+"/task-templates",
		map[string]any{"name": "Bug report", "titleTemplate": "[Bug] ",
			"descriptionTemplate": "<p>Steps</p>", "taskType": "TASK", "priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// A task placed in all three: release, sprint and the custom lane.
	tid := testutil.MustCreateTask(t, srv, srcPID, "Placed Task")
	resp = testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]any{"releaseId": relID, "sprintId": sprint["id"]}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+boardID+"/move-task",
		map[string]any{"taskId": tid, "boardColumnId": colID, "boardRank": 2500}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	archive := doExportProject(t, srv, srcPID)

	dstPID := testutil.MustCreateProject(t, srv, "Planning Target")
	result := doImportProject(t, srv, dstPID, testutil.DemoUserID, archive, "", http.StatusOK)
	// Two boards: the one created above plus the sprint board that creating a
	// sprint provisions.
	for key, want := range map[string]float64{
		"releases": 1, "sprints": 1, "boards": 2, "categories": 1, "templates": 1, "tasks": 1, "skipped": 0,
	} {
		if got, _ := result[key].(float64); got != want {
			t.Errorf("%s = %v, want %v (result: %v)", key, result[key], want, result)
		}
	}

	// The release, sprint, category and template arrived with their content.
	var releases []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/releases", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &releases)
	if len(releases) != 1 || releases[0]["name"] != "1.0.0" {
		t.Fatalf("imported releases = %v", releases)
	}
	var sprints []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/sprints", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &sprints)
	if len(sprints) != 1 || sprints[0]["name"] != "Sprint 1" || sprints[0]["goal"] != "Ship it" {
		t.Fatalf("imported sprints = %v", sprints)
	}
	if sprints[0]["releaseId"] != releases[0]["id"] {
		t.Errorf("sprint releaseId = %v, want the imported release %v", sprints[0]["releaseId"], releases[0]["id"])
	}
	if sprints[0]["startDate"] != "2026-01-05" || sprints[0]["endDate"] != "2026-01-19" {
		t.Errorf("sprint dates = %v / %v", sprints[0]["startDate"], sprints[0]["endDate"])
	}
	var categories []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/task-categories", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &categories)
	if len(categories) != 1 || categories[0]["name"] != "Infrastructure" || categories[0]["color"] != "blue" {
		t.Fatalf("imported categories = %v", categories)
	}
	var templates []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/task-templates", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &templates)
	if len(templates) != 1 || templates[0]["name"] != "Bug report" || templates[0]["priority"] != "HIGH" {
		t.Fatalf("imported templates = %v", templates)
	}

	// The board arrived with its lane, and the sprint board still points at the
	// imported sprint rather than at the source project's one.
	var boards []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &boards)
	var mainBoard, sprintBoard map[string]any
	for _, b := range boards {
		switch b["name"] {
		case "Main Board":
			mainBoard = b
		case "Sprint 1":
			sprintBoard = b
		}
	}
	if mainBoard == nil || sprintBoard == nil {
		t.Fatalf("imported boards = %v, want Main Board and Sprint 1", boards)
	}
	if sprintBoard["sprintId"] != sprints[0]["id"] {
		t.Errorf("sprint board sprintId = %v, want %v", sprintBoard["sprintId"], sprints[0]["id"])
	}
	// The project board list omits the lanes; read the board itself for them.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+mainBoard["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &mainBoard)
	cols, _ := mainBoard["columns"].([]any)
	if len(cols) != 1 {
		t.Fatalf("imported board columns = %v", mainBoard["columns"])
	}
	newCol := cols[0].(map[string]any)
	if newCol["name"] != "Refinement" || newCol["status"] != "REFINEMENT" {
		t.Errorf("imported column = %v", newCol)
	}
	if newCol["id"] == colID {
		t.Error("imported column kept the source ID; every row must be created fresh")
	}

	// ...and the task points at the imported release, sprint and lane, not at
	// the source project's rows.
	var tasks []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("imported tasks = %d, want 1", len(tasks))
	}
	imported := tasks[0]
	if imported["releaseId"] != releases[0]["id"] {
		t.Errorf("task releaseId = %v, want %v", imported["releaseId"], releases[0]["id"])
	}
	if imported["sprintId"] != sprints[0]["id"] {
		t.Errorf("task sprintId = %v, want %v", imported["sprintId"], sprints[0]["id"])
	}
	if imported["boardColumnId"] != newCol["id"] {
		t.Errorf("task boardColumnId = %v, want %v", imported["boardColumnId"], newCol["id"])
	}
	if got, _ := imported["boardRank"].(float64); got != 2500 {
		t.Errorf("task boardRank = %v, want 2500 (the exported rank)", imported["boardRank"])
	}
	// The lane's custom status travels with the lane, so the task keeps it
	// instead of falling back to PLANNED.
	if imported["status"] != "REFINEMENT" {
		t.Errorf("task status = %v, want REFINEMENT", imported["status"])
	}
}

// TestProjectImport_LegacyManifestWithoutPlanning pins the compatibility rule
// that keeps formatVersion at 1: an archive written before the planning
// sections existed still imports, and placement references it cannot resolve
// leave the task unplaced instead of pointing at a stranger's rows.
func TestProjectImport_LegacyManifestWithoutPlanning(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Legacy Target")
	archive := makeArchive(t, `{"format":"octbase-project-export","formatVersion":1,
		"project":{"name":"Legacy"},
		"tasks":[{"id":"old-1","title":"Legacy Task","taskType":"TASK","status":"PLANNED",
			"priority":"MEDIUM","releaseId":"gone-1","sprintId":"gone-2","boardColumnId":"gone-3",
			"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}`)
	result := doImportProject(t, srv, pid, testutil.DemoUserID, archive, "", http.StatusOK)
	for key, want := range map[string]float64{
		"tasks": 1, "releases": 0, "sprints": 0, "boards": 0, "categories": 0, "templates": 0, "skipped": 0,
	} {
		if got, _ := result[key].(float64); got != want {
			t.Errorf("%s = %v, want %v (result: %v)", key, result[key], want, result)
		}
	}

	var tasks []map[string]any
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("imported tasks = %d, want 1", len(tasks))
	}
	for _, field := range []string{"releaseId", "sprintId", "boardColumnId"} {
		if tasks[0][field] != nil {
			t.Errorf("%s = %v, want null: the archive carries no such row", field, tasks[0][field])
		}
	}
}

// TestProjectImport_PlanningInvariantsProtected checks the two project-wide
// rules an archive must not be able to break in the target project: one default
// board and one ACTIVE sprint.
func TestProjectImport_PlanningInvariantsProtected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Source: an active sprint and a default board.
	srcPID := testutil.MustCreateProject(t, srv, "Invariant Source")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+srcPID+"/sprints",
		map[string]any{"name": "Source Sprint", "startDate": "2026-03-02", "endDate": "2026-03-16"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var srcSprint map[string]any
	testutil.DecodeJSON(t, resp, &srcSprint)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+srcSprint["id"].(string)+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	srcBoard := testutil.MustCreateBoard(t, srv, srcPID)
	testutil.MustAddColumn(t, srv, srcBoard, "To Do", "PLANNED", 0)
	archive := doExportProject(t, srv, srcPID)

	// Target: already has its own active sprint and default board.
	dstPID := testutil.MustCreateProject(t, srv, "Invariant Target")
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+dstPID+"/sprints",
		map[string]any{"name": "Target Sprint", "startDate": "2026-05-04", "endDate": "2026-05-18"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var dstSprint map[string]any
	testutil.DecodeJSON(t, resp, &dstSprint)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+dstSprint["id"].(string)+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	testutil.MustCreateBoard(t, srv, dstPID)

	doImportProject(t, srv, dstPID, testutil.DemoUserID, archive, "", http.StatusOK)

	var sprints []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/sprints", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &sprints)
	active := 0
	for _, s := range sprints {
		if s["status"] == "ACTIVE" {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active sprints after import = %d, want 1 (the imported one must be demoted)", active)
	}

	var boards []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &boards)
	defaults := 0
	for _, b := range boards {
		if def, _ := b["isDefault"].(bool); def {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("default boards after import = %d, want 1 (the imported one must be demoted)", defaults)
	}
}

// TestProjectImport_HostilePlanningManifest drives the degradation contract for
// the planning sections: a manifest is external input, so a bad value inside it
// is normalised or skipped, never a failed import.
func TestProjectImport_HostilePlanningManifest(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Hostile Target")
	// A 300-character name of 2-byte runes: over the limit, and every cut point
	// past 255 lands mid-rune.
	longName := strings.Repeat("ü", 300)
	manifest := map[string]any{
		"format": "octbase-project-export", "formatVersion": 1,
		"project": map[string]any{"name": "Hostile"},
		"releases": []any{
			map[string]any{"id": "r1", "name": longName, "status": "NOT_A_STATUS", "dueDate": "31.12.2026"},
			map[string]any{"id": "r2", "name": "   "},
		},
		"sprints": []any{
			map[string]any{"id": "s1", "name": "Backwards", "status": "SPRINTING",
				"startDate": "2026-06-30", "endDate": "2026-06-01", "releaseId": "nope",
				"committedCount": -5, "completedEstimate": 12.5},
		},
		"boards": []any{
			map[string]any{"id": "b1", "name": "Board", "isDefault": true, "isSprintBoard": true,
				"sprintId": "gone", "minColumns": 99, "maxColumns": -3, "columns": []any{
					map[string]any{"id": "c1", "name": "Lane", "status": "TRIAGE"},
					map[string]any{"id": "c2", "name": "No status", "status": "   "},
				}},
		},
		"categories": []any{map[string]any{"id": "cat1", "name": "Colourless"}, map[string]any{"name": ""}},
		"templates":  []any{map[string]any{"id": "t1", "name": "Tpl", "taskType": "SUBTASK", "priority": "URGENT"}},
		"tasks": []any{
			map[string]any{"id": "task1", "title": "Triaged", "taskType": "TASK", "status": "TRIAGE",
				"priority": "MEDIUM", "boardColumnId": "c1", "releaseId": "r1", "boardRank": -7,
				"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	result := doImportProject(t, srv, pid, testutil.DemoUserID, makeArchive(t, string(manifestJSON)), "", http.StatusOK)
	for key, want := range map[string]float64{
		"releases": 1, "sprints": 1, "boards": 1, "categories": 1, "templates": 1, "tasks": 1,
	} {
		if got, _ := result[key].(float64); got != want {
			t.Errorf("%s = %v, want %v (result: %v)", key, result[key], want, result)
		}
	}

	var releases []map[string]any
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/releases", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &releases)
	name, _ := releases[0]["name"].(string)
	if len(name) > 255 || !utf8.ValidString(name) {
		t.Errorf("release name = %d bytes, valid UTF-8 = %v; want a rune-safe cut at 255", len(name), utf8.ValidString(name))
	}
	if releases[0]["status"] != "PLANNED" || releases[0]["dueDate"] != nil {
		t.Errorf("release = %v, want PLANNED with no due date", releases[0])
	}

	var sprints []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/sprints", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &sprints)
	if sprints[0]["status"] != "PLANNED" || sprints[0]["startDate"] != nil || sprints[0]["endDate"] != nil {
		t.Errorf("sprint = %v, want PLANNED with the backwards dates dropped", sprints[0])
	}
	if sprints[0]["releaseId"] != nil {
		t.Errorf("sprint releaseId = %v, want null: the archive has no such release", sprints[0]["releaseId"])
	}
	if got, _ := sprints[0]["committedCount"].(float64); got != 0 {
		t.Errorf("committedCount = %v, want 0", sprints[0]["committedCount"])
	}
	if sprints[0]["completedEstimate"] != nil {
		t.Errorf("completedEstimate = %v, want null without an estimate unit", sprints[0]["completedEstimate"])
	}

	var boards []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &boards)
	if isSprintBoard, _ := boards[0]["isSprintBoard"].(bool); isSprintBoard {
		t.Error("board kept isSprintBoard with no sprint to point at")
	}
	if got, _ := boards[0]["minColumns"].(float64); got != 1 {
		t.Errorf("minColumns = %v, want the default 1 after invalid limits", boards[0]["minColumns"])
	}
	var board map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+boards[0]["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &board)
	if cols, _ := board["columns"].([]any); len(cols) != 1 {
		t.Errorf("board lanes = %v, want only the one with a status", board["columns"])
	}

	var templates []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/task-templates", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &templates)
	if templates[0]["taskType"] != "TASK" || templates[0]["priority"] != "MEDIUM" {
		t.Errorf("template = %v, want the defaults for an unusable type/priority", templates[0])
	}
	var categories []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/task-categories", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &categories)
	if categories[0]["color"] != "gray" {
		t.Errorf("category color = %v, want the gray default", categories[0]["color"])
	}

	// The task keeps the lane's custom status (the lane travelled with it) and
	// falls back to the default rank.
	var tasks []map[string]any
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &tasks)
	if tasks[0]["status"] != "TRIAGE" {
		t.Errorf("task status = %v, want the imported lane's TRIAGE", tasks[0]["status"])
	}
	if got, _ := tasks[0]["boardRank"].(float64); got != 1000 {
		t.Errorf("boardRank = %v, want the default 1000 for a negative rank", tasks[0]["boardRank"])
	}
}

// makeArchive builds a one-entry ZIP holding the given project.json content.
func makeArchive(t *testing.T, manifestJSON string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mw, err := zw.Create("project.json")
	if err != nil {
		t.Fatalf("create manifest entry: %v", err)
	}
	if _, err := mw.Write([]byte(manifestJSON)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
