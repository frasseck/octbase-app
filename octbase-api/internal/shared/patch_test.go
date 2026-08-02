package shared

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doPatch(t *testing.T, body string, allowed map[string]bool, dedicated map[string]string, req any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
	DecodePatch(w, r, allowed, dedicated, req)
	return w
}

func TestDecodePatch(t *testing.T) {
	allowed := map[string]bool{"name": true, "version": true}
	dedicated := map[string]string{"status": "use the dedicated route"}

	t.Run("unknown field is rejected with UNSUPPORTED_FIELD", func(t *testing.T) {
		var req struct{ Name *string }
		w := doPatch(t, `{"nonsense":"x"}`, allowed, dedicated, &req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != "UNSUPPORTED_FIELD" {
			t.Fatalf("code = %q, want UNSUPPORTED_FIELD", body["code"])
		}
		if !strings.Contains(body["message"], "nonsense") {
			t.Fatalf("message should name the field, got %q", body["message"])
		}
	})

	t.Run("dedicated-route field gets the pointer message", func(t *testing.T) {
		var req struct{ Name *string }
		w := doPatch(t, `{"status":"CLOSED"}`, allowed, dedicated, &req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["message"] != "use the dedicated route" {
			t.Fatalf("message = %q", body["message"])
		}
	})

	t.Run("allowed fields decode into the request struct", func(t *testing.T) {
		var req struct {
			Name    *string `json:"name"`
			Version *int    `json:"version"`
		}
		w := doPatch(t, `{"name":"n","version":3}`, allowed, dedicated, &req)
		if w.Code != http.StatusOK { // recorder default: nothing written
			t.Fatalf("unexpected error response: %d %s", w.Code, w.Body.String())
		}
		if req.Name == nil || *req.Name != "n" || req.Version == nil || *req.Version != 3 {
			t.Fatalf("decoded %+v", req)
		}
	})

	t.Run("invalid JSON is a 400 BAD_REQUEST", func(t *testing.T) {
		var req struct{}
		w := doPatch(t, `{`, allowed, nil, &req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != "BAD_REQUEST" {
			t.Fatalf("code = %q, want BAD_REQUEST", body["code"])
		}
	})

	t.Run("type mismatch on an allowed field is a 400, not a silent drop", func(t *testing.T) {
		var req struct {
			Version *int `json:"version"`
		}
		w := doPatch(t, `{"version":"not-a-number"}`, allowed, nil, &req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
}
