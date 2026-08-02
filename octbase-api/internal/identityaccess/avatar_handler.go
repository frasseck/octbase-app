package identityaccess

import (
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// maxAvatarBytes caps a profile picture. Avatars live inline in the users row,
// so this is deliberately much smaller than the task-attachment limit.
const maxAvatarBytes = 2 << 20 // 2 MiB

// allowedAvatarTypes is the raster-image allowlist. SVG is intentionally
// excluded because it can carry script — same rule as task attachments.
var allowedAvatarTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// RegisterFileRoutes registers the avatar routes. Upload is multipart and the
// download is raw image bytes, so — like the attachment routes — they must be
// mounted outside the RequireJSON group.
func (h *Handler) RegisterFileRoutes(r chi.Router) {
	r.Post("/api/v1/users/me/avatar", h.UploadAvatar)
	r.Delete("/api/v1/users/me/avatar", h.DeleteAvatar)
	r.Get("/api/v1/users/{userId}/avatar", h.GetAvatar)
}

// UploadAvatar stores the caller's profile picture from a multipart "file" part.
// The declared multipart Content-Type is spoofable, so the real type is decided
// by a byte-sniff and must resolve to an allowed raster image.
func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	// Enforce the size cap at the body level so an oversized stream never buffers.
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes)
	if err := r.ParseMultipartForm(maxAvatarBytes); err != nil { // #nosec G120 -- body capped by http.MaxBytesReader above
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the image exceeds the maximum avatar size (2 MiB)")
			return
		}
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, _, err := r.FormFile("file")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "no file part named 'file' in the request")
		return
	}
	defer func() { _ = file.Close() }()

	// Read the whole (already size-capped) image into memory — avatars are small
	// and stored inline in the DB.
	data, err := io.ReadAll(io.LimitReader(file, maxAvatarBytes+1))
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if len(data) > maxAvatarBytes {
		shared.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the image exceeds the maximum avatar size (2 MiB)")
		return
	}
	if len(data) == 0 {
		shared.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "the uploaded image is empty")
		return
	}

	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	contentType := http.DetectContentType(head)
	if !allowedAvatarTypes[contentType] {
		shared.WriteError(w, http.StatusUnsupportedMediaType, "DISALLOWED_FILE_TYPE", "avatar must be a PNG, JPEG, GIF or WebP image")
		return
	}

	updatedAt, err := h.users.SetAvatar(userID, data, contentType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"avatarUpdatedAt": updatedAt})
}

// GetAvatar streams a user's profile picture. Any authenticated user may view
// any user's avatar — they are shown across the app wherever a person appears.
func (h *Handler) GetAvatar(w http.ResponseWriter, r *http.Request) {
	if shared.GetUserID(r) == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	userID := chi.URLParam(r, "userId")
	data, contentType, updatedAt, found, err := h.users.GetAvatar(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !found {
		shared.WriteError(w, http.StatusNotFound, "AVATAR_NOT_FOUND", "this user has no profile picture")
		return
	}
	w.Header().Set("Content-Type", contentType)
	// Never let a browser sniff a different type than we declare.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The URL carries an ?v=<avatarUpdatedAt> cache-buster, so the bytes for a
	// given URL are immutable and safe to cache hard.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if updatedAt != "" {
		w.Header().Set("ETag", `"`+updatedAt+`"`)
	}
	_, _ = w.Write(data)
}

// DeleteAvatar removes the caller's profile picture (idempotent).
func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	if err := h.users.ClearAvatar(userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
