package workmanagement_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ── Per-user storage quota (OCTBASE_MAX_USER_STORAGE_MB) ─────────────────────
// Quotas are set in raw bytes relative to len(pngBytes) so each test pins down
// exactly which of the two checks fires: the pre-write rejection of an already
// exhausted quota, or the post-write rejection of a file that no longer fits.

func assertQuotaExceeded(t *testing.T, resp *http.Response) {
	t.Helper()
	testutil.AssertStatus(t, resp, http.StatusRequestEntityTooLarge)
	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "STORAGE_QUOTA_EXCEEDED" {
		t.Errorf("code = %v, want STORAGE_QUOTA_EXCEEDED", body["code"])
	}
}

func TestUploadQuota_FileNoLongerFits(t *testing.T) {
	db := testutil.NewTestDB(t)
	// Room for one PNG but not two.
	srv := testutil.NewTestServer(t, db, testutil.WithUserStorageQuota(int64(2*len(pngBytes)-1)))
	pid := testutil.MustCreateProject(t, srv, "Quota")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "one.png", "image/png", pngBytes)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	resp = uploadFile(t, srv, tid, testutil.DemoUserID, "two.png", "image/png", pngBytes)
	assertQuotaExceeded(t, resp)
}

func TestUploadQuota_ExhaustedRejectsBeforeWrite(t *testing.T) {
	db := testutil.NewTestDB(t)
	// Quota exactly one PNG: the first upload consumes it fully, the second is
	// rejected by the cheap pre-write check.
	srv := testutil.NewTestServer(t, db, testutil.WithUserStorageQuota(int64(len(pngBytes))))
	pid := testutil.MustCreateProject(t, srv, "Quota")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := uploadFile(t, srv, tid, testutil.DemoUserID, "one.png", "image/png", pngBytes)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, resp, &att)

	resp = uploadFile(t, srv, tid, testutil.DemoUserID, "two.png", "image/png", pngBytes)
	assertQuotaExceeded(t, resp)

	// Deleting the attachment frees the quota again.
	resp = testutil.Do(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%s/attachments/%s", tid, att["id"]), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	resp = uploadFile(t, srv, tid, testutil.DemoUserID, "three.png", "image/png", pngBytes)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}

func TestUploadQuota_NoQuotaByDefault(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db) // no WithUserStorageQuota → unlimited
	pid := testutil.MustCreateProject(t, srv, "Quota")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	for i := 0; i < 3; i++ {
		resp := uploadFile(t, srv, tid, testutil.DemoUserID, fmt.Sprintf("f%d.png", i), "image/png", pngBytes)
		testutil.AssertStatus(t, resp, http.StatusCreated)
		_ = resp.Body.Close()
	}
}
