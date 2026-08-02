package auth

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/shared"
)

// ChangePassword handles POST /api/v1/auth/change-password (authenticated).
//
// A signed-in user rotates their own password by proving the current one. Until
// now the only way to a new password was the emailed reset-token flow, so a user
// who simply wanted to change a known password had no path at all.
//
// Every OTHER session is revoked — a password change should log out other
// devices, mirroring ResetPassword — while the caller's own session is
// re-established so they stay signed in on this device.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "currentPassword is required", "currentPassword")
		return
	}
	if req.NewPassword == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "newPassword is required", "newPassword")
		return
	}
	if msg := shared.ValidatePassword(req.NewPassword); msg != "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", msg, "newPassword")
		return
	}
	if req.NewPassword == req.CurrentPassword {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "new password must differ from the current password", "newPassword")
		return
	}

	// Load the stored hash. A disabled/deleted account cannot rotate its password
	// (the auth middleware already rejects disabled accounts; re-checked here so
	// this handler is safe on its own). The wrong-current-password and
	// no-usable-account cases return the same code so the response reveals nothing
	// beyond "that didn't work".
	var passwordHash, status string
	var isActive bool
	err := h.db.QueryRow(
		`SELECT COALESCE(password_hash, ''), status, is_active FROM users WHERE id = $1`, userID,
	).Scan(&passwordHash, &status, &isActive)
	if errors.Is(err, sql.ErrNoRows) {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if status == "disabled" || status == "deleted" || !isActive || passwordHash == "" ||
		!VerifyPassword(passwordHash, req.CurrentPassword) {
		h.audit.Write(userID, auditlog.ActionUserPasswordChangeFailed, "auth", userID,
			`{"reason":"current_password_invalid"}`, clientIP(r), r.UserAgent())
		shared.WriteError(w, http.StatusUnauthorized, "CURRENT_PASSWORD_INVALID", "current password is incorrect")
		return
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`,
			newHash, userID,
		); err != nil {
			return err
		}
		// End every existing session; a rotated password logs out other devices.
		_, err := tx.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
		return err
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// The DELETE above also dropped the caller's own refresh token; re-issue one
	// so the user who just changed their password stays signed in on this device.
	if err := h.issueRefreshCookie(w, userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(userID, auditlog.ActionUserPasswordChanged, "auth", userID,
		"", clientIP(r), r.UserAgent())
	shared.NoCache(w)
	w.WriteHeader(http.StatusNoContent)
}
