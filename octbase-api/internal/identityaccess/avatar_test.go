package identityaccess_test

import (
	"bytes"
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

func uploadAvatar(t *testing.T, srv *httptest.Server, userID, filename string, data []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/users/me/avatar", &buf)
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

func TestAvatarUploadServeRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	up := uploadAvatar(t, srv, testutil.DemoUserID, "me.png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusOK)
	var body map[string]any
	testutil.DecodeJSON(t, up, &body)
	if body["avatarUpdatedAt"] == nil || body["avatarUpdatedAt"] == "" {
		t.Fatalf("expected avatarUpdatedAt in upload response, got %v", body)
	}

	// /users/me reflects the avatar.
	me := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, me, http.StatusOK)
	var meBody map[string]any
	testutil.DecodeJSON(t, me, &meBody)
	if meBody["avatarUpdatedAt"] == nil {
		t.Error("expected avatarUpdatedAt on /users/me after upload")
	}

	// GET the image back.
	dl := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/"+testutil.DemoUserID+"/avatar", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, dl, http.StatusOK)
	if ct := dl.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if dl.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	got, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if !bytes.Equal(got, pngBytes) {
		t.Errorf("served bytes differ from uploaded (%d vs %d)", len(got), len(pngBytes))
	}
}

func TestAvatarRejectsNonImage(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := uploadAvatar(t, srv, testutil.DemoUserID, "notes.txt", []byte("this is plainly not an image at all"))
	testutil.AssertStatus(t, resp, http.StatusUnsupportedMediaType)
	_ = resp.Body.Close()
}

func TestAvatarGetNotFoundWhenNone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/"+testutil.DemoUserID+"/avatar", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestAvatarDelete(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	up := uploadAvatar(t, srv, testutil.DemoUserID, "me.png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusOK)
	_ = up.Body.Close()

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/users/me/avatar", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNoContent)
	_ = del.Body.Close()

	// Now gone.
	dl := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/"+testutil.DemoUserID+"/avatar", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, dl, http.StatusNotFound)
	_ = dl.Body.Close()

	me := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, testutil.DemoUserID)
	var meBody map[string]any
	testutil.DecodeJSON(t, me, &meBody)
	if _, present := meBody["avatarUpdatedAt"]; present {
		t.Error("avatarUpdatedAt should be omitted after delete")
	}
}

func TestAvatarAppearsInMembersList(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Team")

	up := uploadAvatar(t, srv, testutil.DemoUserID, "me.png", pngBytes)
	testutil.AssertStatus(t, up, http.StatusOK)
	_ = up.Body.Close()

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/members", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var members []map[string]any
	testutil.DecodeJSON(t, resp, &members)
	found := false
	for _, m := range members {
		if m["userId"] == testutil.DemoUserID {
			found = true
			if m["avatarUpdatedAt"] == nil {
				t.Error("expected the demo member's avatarUpdatedAt to be set in the members list")
			}
		}
	}
	if !found {
		t.Fatal("demo user not found in members list")
	}
}

func TestAvatarRequiresAuth(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/"+testutil.DemoUserID+"/avatar", nil, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}
