package shared

import (
	"encoding/json"
	"net/http"
)

// DecodePatch decodes a PATCH body into a raw-key map, rejects any key not in
// allowed with 400 UNSUPPORTED_FIELD, and unmarshals the surviving keys into
// req. An unrecognized field must fail loudly: decoding straight into a typed
// struct answers 200 for a field it silently dropped, telling the client its
// edit was saved when it was not (the wart UpdateTask, UpdateRelease and
// UpdateSprint each had to close individually).
//
// dedicated maps a field name to the message pointing at the dedicated route
// that does handle it (e.g. "status" → "use POST …/close"); pass nil when no
// field has one. On failure the error response has already been written and
// the caller must return immediately:
//
//	if !shared.DecodePatch(w, r, allowed, dedicated, &req) {
//		return
//	}
func DecodePatch(w http.ResponseWriter, r *http.Request, allowed map[string]bool, dedicated map[string]string, req any) bool {
	var raw map[string]json.RawMessage
	if err := DecodeJSON(r, &raw); err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return false
	}
	for key := range raw {
		if allowed[key] {
			continue
		}
		if msg, ok := dedicated[key]; ok {
			WriteError(w, http.StatusBadRequest, "UNSUPPORTED_FIELD", msg)
			return false
		}
		WriteError(w, http.StatusBadRequest, "UNSUPPORTED_FIELD", "unsupported field: "+key)
		return false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return false
	}
	if err := json.Unmarshal(b, req); err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return false
	}
	return true
}
