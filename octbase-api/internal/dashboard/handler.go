package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler serves the personal settings dashboard's preferences endpoint.
type Handler struct {
	repo *Repo
}

// NewHandler creates a new dashboard Handler.
func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

// RegisterRoutes registers dashboard endpoints. Self-service only — a user
// reads/writes their own preferences, never another user's (shared.GetUserID
// scopes every call), so no role check is needed.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/users/me/preferences", h.GetPreferences)
	r.Patch("/api/v1/users/me/preferences", h.UpdatePreferences)
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

func (h *Handler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	var req struct {
		Language string `json:"language"`
		Theme    string `json:"theme"`
		// Terminology is a pointer so an omitted field is distinguishable from an
		// empty one: clients that predate it (the mobile app, a cached frontend)
		// PATCH only language and theme, and must not have their vocabulary reset
		// — or be rejected — for saying nothing about it.
		Terminology *string `json:"terminology"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"language": true, "theme": true, "terminology": true,
	}, nil, &req) {
		return
	}
	if !IsValidLanguage(req.Language) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PREFERENCE_VALUE", "unsupported language")
		return
	}
	if !IsValidTheme(req.Theme) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PREFERENCE_VALUE", "unsupported theme")
		return
	}
	terminology := ""
	if req.Terminology != nil {
		if !IsValidTerminology(*req.Terminology) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PREFERENCE_VALUE", "unsupported terminology")
			return
		}
		terminology = *req.Terminology
	} else {
		current, err := h.repo.GetPreferences(userID)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		terminology = current.Terminology
	}
	p := &Preferences{UserID: userID, Language: req.Language, Theme: req.Theme, Terminology: terminology}
	if err := h.repo.UpsertPreferences(p); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, p)
}
