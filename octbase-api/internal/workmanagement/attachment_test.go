package workmanagement_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// pngBytes is a minimal valid 1x1 PNG so http.DetectContentType reports image/png.
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// uploadFile posts a multipart upload to the given task as userID. declaredType
// overrides the part's Content-Type (empty = let multipart default).
func uploadFile(t *testing.T, srv *httptest.Server, taskID, userID, filename, declaredType string, data []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	var fw io.Writer
	var err error
	if declaredType != "" {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename)}
		h["Content-Type"] = []string{declaredType}
		fw, err = mw.CreatePart(h)
	} else {
		fw, err = mw.CreateFormFile("file", filename)
	}
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tasks/"+taskID+"/attachments/upload", &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if userID != "" {
		req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(userID))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do upload: %v", err)
	}
	return resp
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "shot.png", "image/png", pngBytes)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, resp, &att)
	if att["filename"] != "shot.png" {
		t.Errorf("filename = %v", att["filename"])
	}
	if att["contentType"] != "image/png" {
		t.Errorf("contentType = %v", att["contentType"])
	}
	// storageKey must never be serialized.
	if _, ok := att["storageKey"]; ok {
		t.Error("storageKey must not be present in JSON response")
	}
	attID := att["id"].(string)

	// Download round trip.
	dl := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s/content", tid, attID), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, dl, http.StatusOK)
	if ct := dl.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("download content-type = %q", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); cd == "" || cd[:6] != "inline" {
		t.Errorf("expected inline disposition for image, got %q", cd)
	}
	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if !bytes.Equal(body, pngBytes) {
		t.Errorf("downloaded bytes differ from uploaded (%d vs %d)", len(body), len(pngBytes))
	}
}

func TestUploadOversizedRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// 26 MiB > 25 MiB default test limit.
	big := bytes.Repeat([]byte("A"), 26<<20)
	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "big.txt", "text/plain", big)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 413/400 for oversized upload, got %d", resp.StatusCode)
	}
}

func TestUploadDisallowedTypeRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// An ELF/script-ish payload declared as a disallowed type.
	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "evil.sh", "application/x-sh", []byte("#!/bin/sh\nrm -rf /\n"))
	defer func() { _ = resp.Body.Close() }()
	testutil.AssertStatus(t, resp, http.StatusUnsupportedMediaType)
}

func TestUploadTypeMismatchRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// Declared as PNG but the bytes are an HTML/script document.
	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "fake.png", "image/png",
		[]byte("<html><script>alert(1)</script></html>"))
	defer func() { _ = resp.Body.Close() }()
	testutil.AssertStatus(t, resp, http.StatusUnsupportedMediaType)
}

func TestUploadPathTraversalFilenameNeutralized(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "../../../etc/passwd", "text/plain", []byte("hello world"))
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, resp, &att)
	fn := att["filename"].(string)
	if fn == "../../../etc/passwd" || fn == "" {
		t.Errorf("filename not neutralized: %q", fn)
	}
	if fn != "passwd" {
		t.Errorf("expected base filename 'passwd', got %q", fn)
	}
}

func TestNonMemberCannotDownload(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	// PRIVATE project so non-members are not allowed.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Private", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	pid := p["id"].(string)
	tid := testutil.MustCreateTask(t, srv, pid, "Secret Task")

	up := uploadFile(t, srv, tid, testutil.DemoUserID, "shot.png", "image/png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, up, &att)
	attID := att["id"].(string)

	// SecondUser is not a member of this PRIVATE project.
	dl := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s/content", tid, attID), nil, testutil.SecondUserID)
	defer func() { _ = dl.Body.Close() }()
	if dl.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for non-member download, got %d", dl.StatusCode)
	}
}

func TestDeleteTaskRemovesAttachmentFile(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	up := uploadFile(t, srv, tid, testutil.DemoUserID, "shot.png", "image/png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, up, &att)
	attID := att["id"].(string)

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	defer func() { _ = del.Body.Close() }()
	testutil.AssertStatus(t, del, http.StatusNoContent)

	// The content endpoint must now 404 (task gone).
	dl := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s/content", tid, attID), nil, testutil.DemoUserID)
	defer func() { _ = dl.Body.Close() }()
	if dl.StatusCode == http.StatusOK {
		t.Error("expected attachment to be unreachable after task delete")
	}
}

func TestCopyTaskDuplicatesAttachmentFile(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Files")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	up := uploadFile(t, srv, tid, testutil.DemoUserID, "shot.png", "image/png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, up, &att)
	origID := att["id"].(string)

	// Copy the task.
	cp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/copy", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, cp, http.StatusCreated)
	var copied map[string]interface{}
	testutil.DecodeJSON(t, cp, &copied)
	copyID := copied["id"].(string)

	// The copy has its own attachment with a downloadable file.
	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+copyID+"/attachments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, list, http.StatusOK)
	var atts []map[string]interface{}
	testutil.DecodeJSON(t, list, &atts)
	if len(atts) != 1 {
		t.Fatalf("expected 1 copied attachment, got %d", len(atts))
	}
	newID := atts[0]["id"].(string)
	if newID == origID {
		t.Error("copied attachment should have a new id")
	}
	dl := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s/content", copyID, newID), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, dl, http.StatusOK)
	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if !bytes.Equal(body, pngBytes) {
		t.Error("copied attachment bytes differ from original")
	}

	// Deleting the original attachment must NOT affect the copy's file.
	d := testutil.Do(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s", tid, origID), nil, testutil.DemoUserID)
	_ = d.Body.Close()
	dl2 := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s/content", copyID, newID), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, dl2, http.StatusOK)
	_ = dl2.Body.Close()
}

func TestDescriptionXSSSanitizedOnCreateAndUpdate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "XSS")

	create := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "T", "description": `<p>hi</p><script>alert(1)</script><img src=x onerror=alert(2)>`},
		testutil.DemoUserID)
	testutil.AssertStatus(t, create, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, create, &task)
	desc := task["description"].(string)
	if contains(desc, "<script") || contains(desc, "onerror") || contains(desc, "alert(1)") {
		t.Errorf("create description not sanitized: %q", desc)
	}
	if !contains(desc, "<p>hi</p>") {
		t.Errorf("create description lost safe content: %q", desc)
	}
	tid := task["id"].(string)

	upd := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"description": `<a href="javascript:alert(3)">x</a><b>ok</b>`}, testutil.DemoUserID)
	testutil.AssertStatus(t, upd, http.StatusOK)
	var updated map[string]interface{}
	testutil.DecodeJSON(t, upd, &updated)
	udesc := updated["description"].(string)
	if contains(udesc, "javascript:") {
		t.Errorf("update description not sanitized: %q", udesc)
	}
	if !contains(udesc, "ok") {
		t.Errorf("update description lost safe content: %q", udesc)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
