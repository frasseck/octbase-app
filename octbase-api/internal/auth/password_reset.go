package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/shared"
)

// passwordResetTTL is how long a reset link stays valid. Kept as a named
// constant (like invitationTTL) because the reset email body hardcodes
// "This link expires in 60 minutes".
const passwordResetTTL = time.Hour

// forgotPasswordMaxAttempts/-Window bound how many reset emails one IP can
// trigger. This sits on top of the coarse per-IP limiter on the whole public
// auth group: minting tokens and sending email is more expensive than a login
// attempt, and a tight budget also slows enumeration probing.
const (
	forgotPasswordMaxAttempts = 5
	forgotPasswordWindow      = 15 * time.Minute
)

// forgotPasswordResponse is the single body ForgotPassword ever returns with
// 202 — identical for known, unknown, and disabled accounts, so the endpoint
// cannot be used to test whether an email is registered.
var forgotPasswordResponse = map[string]string{
	"message": "If an account with that email exists, a password reset link has been sent.",
}

// ForgotPassword handles POST /api/v1/auth/forgot-password. Public. It always
// answers 202 with the same body; whether a token was minted and mailed is
// deliberately unobservable from the response.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if req.Email == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "email is required", "email")
		return
	}

	if !shared.AllowAttempt("forgot-password:"+shared.ClientIP(r), forgotPasswordMaxAttempts, forgotPasswordWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(forgotPasswordWindow.Seconds())))
		shared.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many password reset requests, please try again later")
		return
	}

	// Look up the account. Any miss (unknown email, disabled, deleted) takes
	// the same code path to the same 202 below.
	var userID, status string
	var isActive bool
	err := h.db.QueryRow(
		`SELECT id, status, is_active FROM users WHERE email = $1`, req.Email,
	).Scan(&userID, &status, &isActive)
	if err == nil && status != "disabled" && status != "deleted" && isActive {
		rawToken, tokenHash := generateToken()
		expiresAt := time.Now().Add(passwordResetTTL)
		// One outstanding token per account: a new request invalidates prior
		// unused links, so a mailbox full of forgotten requests holds exactly
		// one live credential, and repeated requests cannot stockpile tokens.
		if txErr := shared.WithTx(h.db, func(tx *sql.Tx) error {
			if _, err := tx.Exec(
				`DELETE FROM password_reset_tokens WHERE user_id = $1 AND used_at IS NULL`, userID,
			); err != nil {
				return err
			}
			_, err := tx.Exec(
				`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1,$2,$3)`,
				tokenHash, userID, expiresAt,
			)
			return err
		}); txErr != nil {
			// Still answer 202: surfacing a 500 only for registered accounts
			// would reopen the enumeration oracle this endpoint closes.
			slog.Error("password reset token store failed", "error", txErr)
		} else {
			resetURL := fmt.Sprintf("%s/#/reset-password/%s", appBaseURL(), rawToken)
			subject, body := passwordResetEmail(resetURL)
			// Send asynchronously so response latency does not reveal whether an
			// email is being delivered (SMTP round-trips are observable; the DB
			// work above is not). Delivery failure is logged, never surfaced.
			email := req.Email
			go func() {
				if err := h.mailer.Send(context.Background(), email, subject, body); err != nil {
					slog.Error("password reset email send failed", "userId", userID, "error", err)
				}
			}()
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("password reset user lookup failed", "error", err)
	}

	shared.NoCache(w)
	shared.WriteJSON(w, http.StatusAccepted, forgotPasswordResponse)
}

// ResetPassword handles POST /api/v1/auth/reset-password. Public. Exchanges a
// valid, unused, unexpired token for a new password and revokes every refresh
// token of the account — a reset after compromise must end existing sessions.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if req.Token == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "token is required", "token")
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

	// One stable code for unknown, expired, and used tokens — distinguishing
	// them would tell an attacker which guesses were once-valid tokens. A
	// disabled/deleted account rejects the same way for the same reason.
	tokenHash := hashToken(req.Token)
	var userID, status string
	var isActive bool
	err := h.db.QueryRow(
		`SELECT u.id, u.status, u.is_active
		   FROM password_reset_tokens t
		   JOIN users u ON u.id = t.user_id
		  WHERE t.token_hash = $1 AND t.used_at IS NULL AND t.expires_at > now()`,
		tokenHash,
	).Scan(&userID, &status, &isActive)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (status == "disabled" || status == "deleted" || !isActive)) {
		shared.WriteError(w, http.StatusBadRequest, "RESET_TOKEN_INVALID", "invalid or expired reset token")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	passwordHash, err := HashPassword(req.NewPassword)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		// Re-check validity inside the transaction: two concurrent submissions
		// of the same link must not both succeed.
		res, err := tx.Exec(
			`UPDATE password_reset_tokens SET used_at = now()
			  WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`,
			tokenHash,
		)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errResetTokenSpent
		}
		if _, err := tx.Exec(
			`UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`,
			passwordHash, userID,
		); err != nil {
			return err
		}
		// End every existing session; the new password is the only way back in.
		_, err = tx.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
		return err
	}); err != nil {
		if errors.Is(err, errResetTokenSpent) {
			shared.WriteError(w, http.StatusBadRequest, "RESET_TOKEN_INVALID", "invalid or expired reset token")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(userID, auditlog.ActionUserPasswordReset, "user", userID,
		`{"method":"self_service"}`, clientIP(r), r.UserAgent())

	w.WriteHeader(http.StatusNoContent)
}

// errResetTokenSpent signals from inside the reset transaction that the token
// was consumed between the pre-check and the guarded update.
var errResetTokenSpent = errors.New("reset token already spent")

// passwordResetEmail builds the subject and plain-text body for a reset mail.
// Like invitationEmail, a bare URL on its own line serves both the plain and
// the HTML alternative the mailer renders.
func passwordResetEmail(resetURL string) (subject, body string) {
	subject = "Reset your Octbase password"
	body = fmt.Sprintf(
		"A password reset was requested for your Octbase account.\n\n"+
			"Open the link below to choose a new password:\n\n%s\n\n"+
			"This link expires in 60 minutes and can be used once.\n\n"+
			"If you didn't request a reset, you can ignore this email — your password is unchanged.",
		resetURL,
	)
	return subject, body
}

// appBaseURL is the frontend origin used in emailed links (OCTBASE_APP_URL,
// same fallback as the invitation accept link).
func appBaseURL() string {
	if u := os.Getenv("OCTBASE_APP_URL"); u != "" {
		return u
	}
	return "http://localhost:8080"
}
