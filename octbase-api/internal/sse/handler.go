package sse

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var sseConnectionsGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "octbase_sse_connections",
	Help: "Current number of open SSE connections.",
})

// Handler exposes SSE and presence endpoints.
type Handler struct {
	db       *sql.DB
	hub      *Hub
	provider auth.Provider
}

// NewHandler creates a new SSE Handler.
func NewHandler(db *sql.DB, hub *Hub, provider auth.Provider) *Handler {
	return &Handler{db: db, hub: hub, provider: provider}
}

// RegisterRoutes registers SSE routes. These are registered inside the JWT
// middleware group but also accept ?token= for EventSource clients that cannot
// send custom headers.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/projects/{projectId}/events", h.Stream)
	r.Get("/api/v1/projects/{projectId}/presence", h.Presence)
}

// resolveUser returns the user ID from the JWT context (set by JWTMiddleware)
// or, as a fallback for EventSource clients, from the ?token query parameter.
func (h *Handler) resolveUser(r *http.Request) (string, *http.Request) {
	if uid := shared.GetUserID(r); uid != "" {
		return uid, r
	}
	if rawToken := r.URL.Query().Get("token"); rawToken != "" {
		if uid, err := h.provider.ValidateToken(rawToken); err == nil && uid != "" {
			ctx := context.WithValue(r.Context(), shared.UserIDKey, uid)
			return uid, r.WithContext(ctx)
		}
	}
	return "", r
}

// errAccountDisabled is returned by authorizeProject for a disabled account,
// so a still-valid access token cannot keep a stream open after deactivation.
var errAccountDisabled = errors.New("account disabled")

// authorizeProject reports whether userID may access the project's stream. It
// mirrors workmanagement.memberGuard: a Super Admin has access without a
// membership row. The global role and account status are looked up here rather
// than read from the request context because the SSE routes use OptionalJWT and
// do not run the LoadUserGlobalRole middleware — including its disabled-account
// rejection, which is re-enforced here. Returns errAccountDisabled for a
// disabled account, ErrNotMember when access is denied, or a wrapped DB error.
func (h *Handler) authorizeProject(projectID, userID string) error {
	var globalRole, status string
	if err := h.db.QueryRow(`SELECT global_role, status FROM users WHERE id = $1`, userID).Scan(&globalRole, &status); err != nil {
		return err
	}
	if status == "disabled" || status == "deleted" {
		return errAccountDisabled
	}
	if globalRole == rbac.GlobalSuperAdmin {
		return nil
	}
	_, err := shared.RequireProjectMember(h.db, projectID, userID)
	return err
}

// Stream opens a text/event-stream for the project. Accepts authentication
// via Bearer token (Authorization header) or ?token= query parameter so that
// browser EventSource clients, which cannot set headers, can connect.
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	userID, r := h.resolveUser(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	err := h.authorizeProject(projectID, userID)
	if errors.Is(err, errAccountDisabled) {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
		return
	}
	if errors.Is(err, shared.ErrNotMember) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		shared.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "streaming not supported")
		return
	}

	// The server is configured with a global WriteTimeout, which sets an absolute
	// write deadline on every response. For this long-lived stream that would
	// forcibly close the connection (browsers then log a failed-connection error
	// and reconnect), so clear the deadline for the lifetime of the stream.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	c := h.hub.Subscribe(projectID, userID)
	defer h.hub.Unsubscribe(c)

	sseConnectionsGauge.Inc()
	defer sseConnectionsGauge.Dec()

	_, _ = w.Write([]byte(": ping\n\n"))
	flusher.Flush()

	// Send a periodic comment as a keepalive so idle connections aren't dropped
	// by intermediaries (proxies, the server IdleTimeout) during quiet periods.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case msg, ok := <-c.Chan():
			if !ok {
				return
			}
			_, _ = w.Write(msg)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// Presence returns connected viewers for the project.
func (h *Handler) Presence(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	userID, _ := h.resolveUser(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	err := h.authorizeProject(projectID, userID)
	if errors.Is(err, errAccountDisabled) {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
		return
	}
	if errors.Is(err, shared.ErrNotMember) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	viewers := h.hub.Presence(projectID)
	shared.WriteJSON(w, http.StatusOK, map[string]any{"viewers": viewers})
}
