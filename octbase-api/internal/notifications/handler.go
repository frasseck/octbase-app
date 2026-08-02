package notifications

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler serves notification endpoints.
type Handler struct {
	db   *sql.DB
	repo *Repo
}

// NewHandler creates a new notifications Handler.
func NewHandler(db *sql.DB, repo *Repo) *Handler { return &Handler{db: db, repo: repo} }

// RegisterRoutes registers notification endpoints.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/users/me/notifications", h.List)
	r.Post("/api/v1/users/me/notifications/read-all", h.MarkAllRead)
	r.Patch("/api/v1/users/me/notifications/{id}", h.MarkRead)
	r.Get("/api/v1/users/me/notification-preferences", h.GetPreferences)
	r.Patch("/api/v1/users/me/notification-preferences", h.UpdatePreference)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	pg := shared.ParsePagination(r)
	unreadOnly := r.URL.Query().Get("unreadOnly") == "true"
	ns, err := h.repo.List(userID, unreadOnly, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	count, _ := h.repo.UnreadCount(userID)
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": ns,
		"unreadCount":   count,
	})
}

func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if err := h.repo.MarkAllRead(userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	id := chi.URLParam(r, "id")
	var req struct {
		IsRead bool `json:"isRead"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{"isRead": true}, nil, &req) {
		return
	}
	if err := h.repo.SetRead(id, userID, req.IsRead); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"isRead": req.IsRead})
}

func (h *Handler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	prefs, err := h.repo.GetPreferences(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, prefs)
}

func (h *Handler) UpdatePreference(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	var req struct {
		Kind  string `json:"kind"`
		InApp bool   `json:"inApp"`
		Email bool   `json:"email"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"kind": true, "inApp": true, "email": true,
	}, nil, &req) {
		return
	}
	// The kind was unvalidated, so any string could be stored — including a
	// retired kind (release_due) or a typo, both of which then sit in the table
	// forever affecting nothing while looking like a live setting.
	if !ValidKind(req.Kind) {
		shared.WriteValidationError(w, "INVALID_NOTIFICATION_KIND",
			"unknown notification kind", "kind")
		return
	}
	p := &NotificationPreference{
		UserID: userID,
		Kind:   req.Kind,
		InApp:  req.InApp,
		Email:  req.Email,
	}
	if err := h.repo.UpsertPreference(p); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}
