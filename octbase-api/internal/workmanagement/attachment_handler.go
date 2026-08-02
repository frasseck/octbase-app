package workmanagement

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// RegisterFileRoutes registers the multipart upload and binary download
// endpoints. These must be mounted in a route group WITHOUT the RequireJSON
// middleware (which rejects non-JSON Content-Types), so they are registered
// separately from the main JSON API routes — alongside the CSV routes.
func (h *Handler) RegisterFileRoutes(r chi.Router) {
	r.Post("/api/v1/tasks/{taskId}/attachments/upload", h.UploadAttachment)
	r.Get("/api/v1/tasks/{taskId}/attachments/{attachmentId}/content", h.DownloadAttachment)
}

// allowedUploadTypes is the content-type allowlist for uploaded files. Both the
// declared multipart Content-Type and a byte-sniff of the actual content must
// resolve to a type in this set. Executables and scripts are intentionally
// excluded.
// Note: image/svg+xml is intentionally NOT allowed because SVG can carry script.
var allowedUploadTypes = map[string]bool{
	"image/png":          true,
	"image/jpeg":         true,
	"image/gif":          true,
	"image/webp":         true,
	"application/pdf":    true,
	"text/plain":         true,
	"text/csv":           true,
	"application/zip":    true,
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.ms-excel":      true,
	"application/vnd.ms-powerpoint": true,
}

// imageTypes is the subset of allowed types that may be displayed inline.
var imageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// defaultMaxUploadBytes is used when no explicit limit is configured.
const defaultMaxUploadBytes = 10 << 20 // 10 MiB

// UploadAttachment handles a multipart file upload to a task, persisting the
// bytes under a random storage key and recording metadata. It reuses the same
// writer/membership guard as the rest of the task-mutation endpoints.
func (h *Handler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if t == nil {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	_, ok := h.writerGuard(w, r, t.ProjectID)
	if !ok {
		return
	}
	if h.storage == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "UPLOADS_DISABLED", "file uploads are not configured")
		return
	}

	maxBytes := h.maxUploadBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	// Enforce the size limit early at the body level. http.MaxBytesReader makes
	// reads past the limit fail, so an oversized stream never hits disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil { // #nosec G120 -- body capped by http.MaxBytesReader above
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "uploaded file exceeds the maximum allowed size")
			return
		}
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("file")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "no file part named 'file' in the request")
		return
	}
	defer func() { _ = file.Close() }()

	// Sniff the first 512 bytes to determine the real content type, independent
	// of the (spoofable) declared multipart Content-Type.
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	sniffed := normalizeContentType(http.DetectContentType(head))

	declared := normalizeContentType(header.Header.Get("Content-Type"))
	if declared == "" {
		declared = sniffed
	}

	contentType, allowOK := resolveUploadType(declared, sniffed, header.Filename)
	if !allowOK {
		shared.WriteError(w, http.StatusUnsupportedMediaType, "DISALLOWED_FILE_TYPE", "this file type is not allowed")
		return
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Per-user storage quota. Usage is read once here: the cheap pre-check
	// rejects an already-exhausted quota before any disk write, and the same
	// figure is re-checked against the actual byte count after the streamed
	// write (the part size isn't known until then).
	uploaderID := shared.GetUserID(r)
	var storageUsed int64
	if h.maxUserStorageBytes > 0 {
		used, err := h.attachments.UploadedBytesByUser(uploaderID)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if used >= h.maxUserStorageBytes {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "STORAGE_QUOTA_EXCEEDED", "your uploaded-files storage quota is used up; delete attachments you no longer need")
			return
		}
		storageUsed = used
	}

	storageKey, err := NewStorageKey()
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Re-wrap with the byte limit during the streamed write so a client that
	// lies about the part size still cannot exceed the cap.
	written, err := h.storage.Write(storageKey, io.LimitReader(file, maxBytes+1))
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if written > maxBytes {
		_ = h.storage.Remove(storageKey)
		shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "uploaded file exceeds the maximum allowed size")
		return
	}
	if h.maxUserStorageBytes > 0 && storageUsed+written > h.maxUserStorageBytes {
		_ = h.storage.Remove(storageKey)
		shared.WriteError(w, http.StatusRequestEntityTooLarge, "STORAGE_QUOTA_EXCEEDED", "this file does not fit into your remaining storage quota")
		return
	}

	a := &TaskAttachment{
		ID:          shared.NewUUID(),
		TaskID:      taskID,
		Filename:    sanitizeFilename(header.Filename),
		ContentType: contentType,
		SizeBytes:   written,
		StorageKey:  storageKey,
		UploadedBy:  uploaderID,
		CreatedAt:   shared.Now(),
	}
	if err := h.attachments.Create(a); err != nil {
		_ = h.storage.Remove(storageKey)
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, a)
}

// DownloadAttachment streams an uploaded attachment's bytes. It enforces the
// same task-visibility guard as every other task read, so an attachment is never
// reachable by someone who cannot see the task. This endpoint also backs inline
// image rendering.
func (h *Handler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskGuard(w, r, taskID); !ok {
		return
	}
	if h.storage == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "UPLOADS_DISABLED", "file uploads are not configured")
		return
	}
	id := chi.URLParam(r, "attachmentId")
	a, err := h.attachments.FindByIDInTask(id, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, a != nil && a.IsUpload(), "ATTACHMENT_NOT_FOUND", "attachment not found") {
		return
	}
	f, err := h.storage.Open(a.StorageKey)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "ATTACHMENT_NOT_FOUND", "attachment file missing")
		return
	}
	defer func() { _ = f.Close() }()

	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// Prevent browsers from sniffing a different type than we declare.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Images may render inline (for the preview); everything else downloads.
	disposition := "attachment"
	if imageTypes[ct] {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+"; filename=\""+contentDispositionFilename(a.Filename)+"\"")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, a.Filename, time.Time{}, f)
}

// resolveUploadType decides the stored content type from the declared type, the
// sniffed type, and the filename extension, and reports whether it is allowed.
// Both signals must agree closely: a declared allowed type that sniffs to
// something disallowed (and not a benign text/zip case) is rejected.
func resolveUploadType(declared, sniffed, filename string) (string, bool) {
	// http.DetectContentType reports "application/octet-stream" for many office
	// formats and "text/plain" for CSV; trust the declared type when the sniff
	// is one of these generic values and the declared type is on the allowlist.
	generic := sniffed == "application/octet-stream" || sniffed == "text/plain; charset=utf-8" || sniffed == "text/plain"

	if allowedUploadTypes[declared] {
		if strings.HasPrefix(declared, "image/") || declared == "application/pdf" {
			// For images/PDF the byte-sniff must POSITIVELY confirm the declared
			// type. A generic sniff (text/plain, octet-stream) is not enough:
			// http.DetectContentType reports an SVG/XML/HTML body as text/plain,
			// so trusting the declared image type there would let a script-bearing
			// SVG be stored (and, with a different download path, served) as an
			// image. Real PNG/JPEG/GIF/WEBP/PDF all sniff to their own type.
			if sniffed != declared {
				return "", false
			}
			return declared, true
		}
		// Office/text/zip families: http.DetectContentType is legitimately generic
		// for these, so accept the declared type unless the sniff actively
		// contradicts it with a different concrete type.
		if !generic && sniffed != declared && !zipFamily(declared, sniffed) {
			return "", false
		}
		return declared, true
	}
	// Declared type not allowed (or empty): fall back to the sniffed type if it
	// is itself allowed (e.g. a real PNG sent with a wrong/missing declared type).
	if allowedUploadTypes[sniffed] {
		return sniffed, true
	}
	// Last resort: extension-based for office docs that sniff as zip.
	if zipFamily("", sniffed) {
		if t := mime.TypeByExtension(filepath.Ext(filename)); allowedUploadTypes[normalizeContentType(t)] {
			return normalizeContentType(t), true
		}
	}
	return "", false
}

// zipFamily reports whether the office/zip detection edge applies: modern office
// documents are zip archives, so a sniff of application/zip is consistent with a
// declared docx/xlsx/pptx/zip type.
func zipFamily(declared, sniffed string) bool {
	if sniffed != "application/zip" {
		return false
	}
	switch declared {
	case "",
		"application/zip",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	}
	return false
}

// normalizeContentType lowercases and strips parameters except for the charset
// on text/plain which DetectContentType emits.
func normalizeContentType(ct string) string {
	ct = strings.TrimSpace(strings.ToLower(ct))
	if ct == "text/plain; charset=utf-8" {
		return ct
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

// sanitizeFilename reduces a user-supplied filename to its base name and strips
// path separators and control characters. The result is for display/download
// only and is always HTML-escaped on render; it is never used to build the
// on-disk path.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	// Drop any path-traversal remnants and control characters.
	name = strings.ReplaceAll(name, "..", "")
	var b strings.Builder
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteRune(c)
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, "/.")
	if out == "" {
		return "file"
	}
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

// contentDispositionFilename escapes quotes/backslashes/CR/LF for safe inclusion
// in a Content-Disposition header.
func contentDispositionFilename(name string) string {
	r := strings.NewReplacer("\\", "_", "\"", "_", "\r", "_", "\n", "_")
	return r.Replace(name)
}
