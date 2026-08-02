package workmanagement

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// extractUploadReader bounds the request body to maxBytes and returns an
// io.Reader for the uploaded content plus an optional cleanup func. It
// accepts either multipart/form-data (a file field named formField) or a raw
// body, and is shared by the ZIP project importer and the Jira CSV importer.
//
// The bound is applied to r.Body before any read happens, so it protects
// both the multipart-form-parsing path (which buffers/reads r.Body) and the
// raw-body fallback: a body that exceeds maxBytes surfaces as an
// *http.MaxBytesError from whichever call ends up doing the actual read
// (multipart parsing here, or the caller's own read of the returned reader).
func extractUploadReader(w http.ResponseWriter, r *http.Request, formField string, maxBytes int64) (io.Reader, func(), error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by http.MaxBytesReader above; 32 MiB is the in-memory spill threshold
			return nil, nil, fmt.Errorf("failed to parse multipart form: %w", err)
		}
		f, _, err := r.FormFile(formField)
		if err != nil {
			return nil, nil, fmt.Errorf("missing %q field in multipart form", formField)
		}
		return f, func() { _ = f.Close() }, nil
	}
	return r.Body, nil, nil
}
