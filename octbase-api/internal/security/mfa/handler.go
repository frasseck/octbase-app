package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/shared"
)

// totpIssuer names the account in the authenticator app.
const totpIssuer = "Octbase"

// Handler serves the personal MFA enrollment/verification endpoints. All
// routes are self-service (shared.GetUserID scopes every call) — a user can
// only enroll/disable their own MFA, never another user's.
type Handler struct {
	db    *sql.DB
	repo  *Repo
	audit *auditlog.Repo
}

// NewHandler creates a new mfa Handler.
func NewHandler(db *sql.DB, repo *Repo, audit *auditlog.Repo) *Handler {
	return &Handler{db: db, repo: repo, audit: audit}
}

// RegisterRoutes registers the MFA management endpoints that require a full
// session (disable and recovery-code regeneration both re-check the password).
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/api/v1/users/me/mfa/disable", h.Disable)
	r.Post("/api/v1/users/me/mfa/recovery-codes/regenerate", h.RegenerateRecoveryCodes)
}

// RegisterEnrollmentRoutes registers enroll/confirm. These are mounted under a
// middleware that accepts either a full access token (voluntary enrollment) or
// a scoped MFA-enrollment token (forced enrollment at login), so a user the
// deployment requires to set up MFA can complete it without a session.
func (h *Handler) RegisterEnrollmentRoutes(r chi.Router) {
	r.Post("/api/v1/users/me/mfa/enroll", h.Enroll)
	r.Post("/api/v1/users/me/mfa/confirm", h.Confirm)
}

// Enroll begins (or restarts) enrollment: generates a new TOTP secret and
// returns its otpauth:// URI for client-side QR rendering, plus the raw
// secret as a manual-entry fallback. Enrollment is not active until Confirm
// succeeds, so calling this again before confirming simply replaces the
// pending secret.
// accountActive reports whether the user is neither disabled nor deleted. The
// MFA enroll/confirm route group authenticates via EnrollmentOrAccessMiddleware,
// which (unlike the main authed group) does not run shared.LoadUserGlobalRole,
// so these handlers check account status themselves rather than trust a still-
// valid access token from a since-disabled account.
func (h *Handler) accountActive(userID string) bool {
	var status string
	if err := h.db.QueryRow(`SELECT status FROM users WHERE id = $1`, userID).Scan(&status); err != nil {
		return false
	}
	return status != "disabled" && status != "deleted"
}

func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)

	if !h.accountActive(userID) {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is inactive")
		return
	}

	// Require fresh proof (the account password) to begin voluntary enrollment
	// from an access token. Without this a stolen/short-lived access token alone
	// could bind MFA to an attacker's authenticator — a takeover that survives
	// the victim's password reset. Mirrors the re-auth Disable/Regenerate require.
	// The forced-enrollment-at-login flow authenticates with a scoped enrollment
	// token minted right after a successful password login, so it is exempt.
	if !shared.IsMFAEnrollmentToken(r) {
		var req struct {
			Password string `json:"password"`
		}
		if !shared.DecodeJSONOrBadRequest(w, r, &req) {
			return
		}
		if !h.reauthenticate(userID, req.Password, "") {
			shared.WriteError(w, http.StatusUnauthorized, "REAUTH_REQUIRED", "password confirmation required to enable MFA")
			return
		}
	}

	enabled, err := h.repo.IsEnabled(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if enabled {
		shared.WriteError(w, http.StatusConflict, "MFA_ALREADY_ENABLED", "MFA is already enabled")
		return
	}

	var email string
	if err := h.db.QueryRow(`SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: email})
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	encrypted, err := shared.EncryptMFASecret(key.Secret())
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.repo.UpsertPendingCredential(userID, encrypted); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"otpauthUrl": key.URL(),
		"secret":     key.Secret(),
	})
}

// Confirm activates MFA once the user proves they can generate a valid code
// from the pending secret, and issues one-time recovery codes.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)

	if !h.accountActive(userID) {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is inactive")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}

	cred, err := h.repo.GetCredential(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if cred == nil || !cred.Pending() {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_ENROLLMENT_NOT_FOUND", "no pending MFA enrollment; call enroll first")
		return
	}

	secret, err := shared.DecryptMFASecret(cred.EncryptedSecret)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !totp.Validate(req.Code, secret) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_CODE_INVALID", "invalid verification code")
		return
	}

	codes, hashes, ids := generateRecoveryCodes()
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		if err := h.repo.ConfirmCredential(tx, userID); err != nil {
			return err
		}
		return h.repo.ReplaceRecoveryCodes(tx, userID, ids, hashes)
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Documented deviation from prompts/33 §1: no activity.Write here. The
	// activity log is project-scoped (activity_entries.project_id NOT NULL,
	// ListByProject is its only read path), so a user-level event like "MFA
	// enabled" has no project to attach to. Enable/disable are recorded in the
	// audit log instead; surfacing them in the Activity view needs a schema
	// extension for user-scoped entries first.
	h.audit.Write(userID, auditlog.ActionMFAEnabled, "user", userID, "", shared.ClientIP(r), r.UserAgent())

	shared.WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// Disable turns MFA off. It requires re-proving identity (current password
// or a valid TOTP/recovery code) so a stolen access token alone can't strip
// MFA protection from an account.
func (h *Handler) Disable(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)

	enabled, err := h.repo.IsEnabled(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !enabled {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_NOT_ENABLED", "MFA is not enabled")
		return
	}

	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if !h.reauthenticate(userID, req.Password, req.Code) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_CODE_INVALID", "current password or a valid code is required to disable MFA")
		return
	}

	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		return h.repo.Disable(tx, userID)
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(userID, auditlog.ActionMFADisabled, "user", userID, "", shared.ClientIP(r), r.UserAgent())
	w.WriteHeader(http.StatusNoContent)
}

// RegenerateRecoveryCodes invalidates existing recovery codes and issues a
// fresh batch. Same re-auth requirement as Disable.
func (h *Handler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)

	enabled, err := h.repo.IsEnabled(userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !enabled {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_NOT_ENABLED", "MFA is not enabled")
		return
	}

	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if !h.reauthenticate(userID, req.Password, req.Code) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "MFA_CODE_INVALID", "current password or a valid code is required to regenerate recovery codes")
		return
	}

	codes, hashes, ids := generateRecoveryCodes()
	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		return h.repo.ReplaceRecoveryCodes(tx, userID, ids, hashes)
	}); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(userID, auditlog.ActionMFARecoveryCodesRegenerated, "user", userID, "", shared.ClientIP(r), r.UserAgent())
	shared.WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// reauthenticate confirms the caller is really the account owner via
// whichever of password/code was supplied. An empty password falls through
// to the code check; an empty code with no matching recovery code fails
// closed. Recovery-code verification is checked before TOTP since consuming
// a recovery code needs a DB write either way, and a wrong TOTP code costs
// nothing to also try.
func (h *Handler) reauthenticate(userID, password, code string) bool {
	if password != "" {
		var hash string
		if err := h.db.QueryRow(`SELECT COALESCE(password_hash,'') FROM users WHERE id = $1`, userID).Scan(&hash); err == nil && hash != "" {
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil {
				return true
			}
		}
	}
	ok, err := ValidateCode(h.repo, userID, code)
	return err == nil && ok
}

// generateRecoveryCodes creates RecoveryCodeCount random codes, returning the
// plaintext (shown once to the user), their hashes, and fresh row IDs (all
// same length, same order) for storage.
func generateRecoveryCodes() (codes, hashes, ids []string) {
	for i := 0; i < RecoveryCodeCount; i++ {
		code := randomRecoveryCode()
		codes = append(codes, code)
		hashes = append(hashes, hashRecoveryCode(code))
		ids = append(ids, shared.NewUUID())
	}
	return codes, hashes, ids
}

// randomRecoveryCode returns a 10-character base32 code (no padding), grouped
// for readability, e.g. "ABCDE-FGHIJ". 8 random bytes always base32-encode to
// 13 characters, of which the first 10 are kept.
func randomRecoveryCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return s[:5] + "-" + s[5:10]
}

// hashRecoveryCode normalizes before hashing so a code the user typed
// lowercase, without the hyphen, or with stray spaces still matches the
// stored hash — recovery codes are entered by hand in exactly the scenario
// where the user has lost their authenticator. Generated codes are already
// canonical, so normalizing here keeps generation and verification aligned
// by construction.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// normalizeRecoveryCode maps user input to the canonical "ABCDE-FGHIJ" form:
// uppercase, separators stripped, hyphen re-inserted. Anything that doesn't
// look like a recovery code (e.g. a 6-digit TOTP code) is returned with only
// case/separator cleanup, which cannot collide with a canonical code.
func normalizeRecoveryCode(code string) string {
	cleaned := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, code))
	if len(cleaned) == 10 {
		return cleaned[:5] + "-" + cleaned[5:]
	}
	return cleaned
}
