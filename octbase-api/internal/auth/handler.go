package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/security/mfa"
	"github.com/octbase/octbase-api/internal/shared"
)

const refreshCookieName = "refresh_token"

// refreshPresentCookieName is a non-HttpOnly companion to the refresh cookie.
// The refresh cookie itself is invisible to JS, so the SPA cannot tell whether
// a session is restorable before probing /auth/refresh — and an unconditional
// probe logs a 401 for every logged-out visitor. This marker is set with the
// same expiry as the refresh cookie (so the browser auto-deletes it when the
// session lapses) and lets the client skip the probe when no session exists.
// It carries no secret, only the boolean "a refresh cookie may be present".
const refreshPresentCookieName = "refresh_present"

// defaultSessionTimeout caps how long a session may stay alive without
// activity. The refresh token is the session: each refresh rotates it and
// re-extends the window by this duration, so an active client keeps going,
// but after this long without a refresh the token expires and the user must
// sign in again. This is the server-side backstop for the 60-minute idle
// timeout the frontend also enforces. Override with OCTBASE_JWT_REFRESH_TTL.
const defaultSessionTimeout = time.Hour

// invitationTTL is how long a created invitation link remains acceptable.
// Kept as a named constant (rather than an env-overridable function like
// mfaChallengeTTLFromEnv) because the invitation email body hardcodes
// "This invitation expires in 7 days" — making the TTL configurable would
// desync it from the text sent to the invitee.
const invitationTTL = 7 * 24 * time.Hour

// mfaEnrollmentTTL is how long a forced-enrollment token is valid — long
// enough to scan a QR code and enter a first TOTP code, short enough that a
// leaked token is quickly useless. It grants only the enroll/confirm endpoints.
const mfaEnrollmentTTL = 15 * time.Minute

// MFA enforcement modes for OCTBASE_REQUIRE_MFA.
const (
	RequireMFAOff    = "off"    // MFA optional (default)
	RequireMFAAdmins = "admins" // required for ADMIN and SUPER_ADMIN
	RequireMFAAll    = "all"    // required for every account
)

// errUserLimitReached signals from inside the accept-invitation transaction
// that every seat allowed by OCTBASE_MAX_USERS is taken; mapped to
// 403 USER_LIMIT_REACHED.
var errUserLimitReached = errors.New("user limit reached")

// mfaVerifyMaxAttempts/-Window bound second-factor guesses per account at
// POST /auth/mfa/verify. The challenge token is stateless by design, so this
// in-process fixed window is the only per-account cap on code guessing (the
// per-IP limiter on the public auth group is the other, coarser layer).
const (
	mfaVerifyMaxAttempts = 10
	mfaVerifyWindow      = 5 * time.Minute
)

// Handler handles all auth-related HTTP routes.
type Handler struct {
	db              *sql.DB
	provider        Provider
	tokens          *RefreshTokenRepo
	invitations     *InvitationRepo
	audit           *auditlog.Repo
	mailer          mailer.Mailer
	jwtSecret       string
	accessTTL       time.Duration
	refreshTTL      time.Duration
	mfaChallengeTTL time.Duration
	mfaRepo         *mfa.Repo
	// maxUsers caps how many user accounts the installation may hold
	// (including the admin); 0 or negative means unlimited. Enforced when an
	// invitation is created and again, authoritatively, when it is accepted
	// (an invitation may outlive free seats). See WithUserLimit.
	maxUsers int
	// requireMFA is the enforcement mode (RequireMFAOff/Admins/All). When a
	// login succeeds for an in-scope account that has no MFA yet, Login returns
	// an enrollment challenge instead of a session. Default off. See WithRequireMFA.
	requireMFA string
}

// NewHandler creates a new auth Handler. jwtSecret is the validated signing
// secret resolved once at the composition root (main), so the handler never
// falls back to a default key on its own.
func NewHandler(db *sql.DB, provider Provider, tokens *RefreshTokenRepo, invitations *InvitationRepo, audit *auditlog.Repo, mlr mailer.Mailer, jwtSecret string) *Handler {
	if mlr == nil {
		mlr = mailer.New()
	}
	return &Handler{
		db:              db,
		provider:        provider,
		tokens:          tokens,
		invitations:     invitations,
		audit:           audit,
		mailer:          mlr,
		jwtSecret:       jwtSecret,
		accessTTL:       accessTTL(),
		refreshTTL:      refreshTTL(),
		mfaChallengeTTL: mfaChallengeTTLFromEnv(),
	}
}

// WithMFA wires the MFA repo so Login can check whether an account has MFA
// enabled and Verify can check a submitted TOTP/recovery code. Kept as a
// post-construction setter (like workmanagement's WithAttachmentStorage)
// rather than a NewHandler parameter, since existing test helpers construct
// Handler directly without needing the MFA path.
func (h *Handler) WithMFA(repo *mfa.Repo) *Handler {
	h.mfaRepo = repo
	return h
}

// WithRequireMFA sets the MFA enforcement mode (RequireMFAOff/Admins/All).
// Unrecognized values are treated as off. A post-construction setter like
// WithMFA; enforcement additionally needs WithMFA to have wired the repo.
func (h *Handler) WithRequireMFA(mode string) *Handler {
	switch mode {
	case RequireMFAAdmins, RequireMFAAll:
		h.requireMFA = mode
	default:
		h.requireMFA = RequireMFAOff
	}
	return h
}

// mfaEnrollmentRequired reports whether an account with the given global role
// must set up MFA before it can obtain a session under the current policy.
func (h *Handler) mfaEnrollmentRequired(globalRole string) bool {
	switch h.requireMFA {
	case RequireMFAAll:
		return true
	case RequireMFAAdmins:
		return globalRole == "ADMIN" || globalRole == "SUPER_ADMIN"
	default:
		return false
	}
}

// WithUserLimit sets the installation-wide account limit (OCTBASE_MAX_USERS)
// enforced on invitation creation and acceptance. Values <= 0 disable the
// limit. A post-construction setter like WithMFA, for the same reason.
func (h *Handler) WithUserLimit(n int) *Handler {
	h.maxUsers = n
	return h
}

// RegisterPublicRoutes registers routes that don't require authentication.
func (h *Handler) RegisterPublicRoutes(r chi.Router) {
	r.Post("/api/v1/auth/login", h.Login)
	r.Post("/api/v1/auth/mfa/verify", h.VerifyMFA)
	r.Post("/api/v1/auth/refresh", h.Refresh)
	r.Post("/api/v1/auth/logout", h.Logout)
	r.Post("/api/v1/auth/forgot-password", h.ForgotPassword)
	r.Post("/api/v1/auth/reset-password", h.ResetPassword)
	r.Get("/api/v1/invitations/{token}", h.GetInvitation)
	r.Post("/api/v1/invitations/{token}/accept", h.AcceptInvitation)
}

// RegisterRoutes registers auth routes that require authentication.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/auth/me", h.Me)
	r.Post("/api/v1/auth/change-password", h.ChangePassword)
	r.Post("/api/v1/admin/invitations", h.CreateInvitation)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}

	user, err := h.provider.Login(r.Context(), req.Email, req.Password)
	if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUserInactive) {
		h.audit.Write("", auditlog.ActionLoginFailed, "auth", "",
			fmt.Sprintf(`{"email":%q}`, req.Email), clientIP(r), r.UserAgent())
		shared.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// MFA gate: a password match alone is not enough to issue real tokens for
	// an account with MFA enabled. Issue a short-lived, single-purpose
	// challenge token instead — see jwt.go's mfaChallengeIssuer for how this
	// stays stateless (no server-side "pending login" record).
	if h.mfaRepo != nil {
		mfaEnabled, err := h.mfaRepo.IsEnabled(user.ID)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if mfaEnabled {
			challengeToken, err := IssueMFAChallengeToken(user.ID, h.jwtSecret, h.mfaChallengeTTL)
			if err != nil {
				shared.WriteServerError(w, r, err)
				return
			}
			shared.NoCache(w)
			shared.WriteJSON(w, http.StatusOK, map[string]any{
				"mfaRequired":    true,
				"challengeToken": challengeToken,
				"expiresIn":      int(h.mfaChallengeTTL.Seconds()),
			})
			return
		}

		// Enforcement: the deployment requires MFA for this account but it has
		// none yet. Issue a scoped enrollment token (no session) so the client
		// can complete setup; the user logs in again once MFA is active.
		if h.mfaEnrollmentRequired(user.GlobalRole) {
			enrollToken, err := IssueMFAEnrollmentToken(user.ID, h.jwtSecret, mfaEnrollmentTTL)
			if err != nil {
				shared.WriteServerError(w, r, err)
				return
			}
			shared.NoCache(w)
			shared.WriteJSON(w, http.StatusOK, map[string]any{
				"mfaEnrollmentRequired": true,
				"enrollmentToken":       enrollToken,
				"expiresIn":             int(mfaEnrollmentTTL.Seconds()),
			})
			return
		}
	}

	accessToken, err := IssueAccessToken(user.ID, h.jwtSecret, h.accessTTL)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	if err := h.issueRefreshCookie(w, user.ID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// No email in the metadata: the user ID identifies the account, and audit
	// rows outlive a later GDPR erasure. (LOGIN_FAILED above keeps the attempted
	// email — there is no user ID for a failed attempt — bounded by the
	// retention purge.)
	h.audit.Write(user.ID, auditlog.ActionLoginSuccess, "auth", user.ID,
		"", clientIP(r), r.UserAgent())
	h.touchLastLogin(user.ID)

	shared.NoCache(w)
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken": accessToken,
		"expiresIn":   int(h.accessTTL.Seconds()),
	})
}

// touchLastLogin bumps users.last_login_at without blocking the response. It
// runs only after a *completed* authentication — for MFA accounts that means
// after the second factor at VerifyMFA, not on the bare password match in
// Login — so the timestamp keeps meaning "last successful login" for the
// admin user list (usermgmt.ManagedUser).
func (h *Handler) touchLastLogin(userID string) {
	go func() {
		if _, err := h.db.Exec(`UPDATE users SET last_login_at = $1 WHERE id = $2`, time.Now(), userID); err != nil {
			slog.Error("update last_login_at failed", "error", err)
		}
	}()
}

// VerifyMFA exchanges a login challenge token + a TOTP/recovery code for a
// real access/refresh pair, completing the two-step login started in Login.
func (h *Handler) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChallengeToken string `json:"challengeToken"`
		Code           string `json:"code"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}

	userID, err := ParseMFAChallengeToken(req.ChallengeToken, h.jwtSecret)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, "MFA_CHALLENGE_INVALID", "invalid or expired MFA challenge")
		return
	}

	// Per-account attempt budget, on top of the per-IP limiter on the public
	// auth group: the challenge token is stateless (deliberately — no pending-
	// login store, see docs/architecture.md §4), so nothing else caps how many
	// codes can be guessed for one account within the challenge TTL, and a
	// per-IP limit alone is sidestepped by rotating IPs.
	if !shared.AllowAttempt("mfa-verify:"+userID, mfaVerifyMaxAttempts, mfaVerifyWindow) {
		w.Header().Set("Retry-After", strconv.Itoa(int(mfaVerifyWindow.Seconds())))
		shared.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many verification attempts, please try again later")
		return
	}

	if h.mfaRepo == nil {
		shared.WriteServerError(w, r, errors.New("mfa not configured"))
		return
	}
	ok, err := mfa.ValidateCode(h.mfaRepo, userID, req.Code)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !ok {
		h.audit.Write(userID, auditlog.ActionLoginFailed, "auth", userID,
			`{"reason":"mfa_code_invalid"}`, clientIP(r), r.UserAgent())
		shared.WriteError(w, http.StatusUnauthorized, "MFA_CODE_INVALID", "invalid verification code")
		return
	}

	// Verify the account is still active before issuing real tokens — the
	// password check in Login may be minutes old by the time the code arrives.
	// Mirrors email_provider.Login's gate: status first, legacy is_active as
	// the fallback "for safety", so a deactivation via either column blocks
	// completing a login here just as it blocks starting one.
	var status string
	var isActive bool
	if err := h.db.QueryRow(`SELECT status, is_active FROM users WHERE id = $1`, userID).Scan(&status, &isActive); err != nil ||
		status == "disabled" || status == "deleted" || !isActive {
		h.audit.Write(userID, auditlog.ActionLoginFailed, "auth", userID,
			`{"reason":"account_inactive"}`, clientIP(r), r.UserAgent())
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is inactive")
		return
	}

	accessToken, err := IssueAccessToken(userID, h.jwtSecret, h.accessTTL)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.issueRefreshCookie(w, userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(userID, auditlog.ActionLoginSuccess, "auth", userID, "", clientIP(r), r.UserAgent())
	h.touchLastLogin(userID)

	shared.NoCache(w)
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken": accessToken,
		"expiresIn":   int(h.accessTTL.Seconds()),
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	// Every rejection below clears the cookies (incl. the presence marker) so a
	// dead session stops the client re-probing on each load until natural expiry.
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		clearRefreshCookies(w)
		shared.WriteError(w, http.StatusUnauthorized, "REFRESH_TOKEN_INVALID", "no refresh token")
		return
	}

	hash := hashToken(cookie.Value)
	tokenID, userID, rotated, err := h.tokens.Claim(hash)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		clearRefreshCookies(w)
		shared.WriteError(w, http.StatusUnauthorized, "REFRESH_TOKEN_INVALID", "invalid or expired refresh token")
		return
	}

	// Reuse detection: a token that was already rotated is being replayed, which
	// means the cookie was captured. Revoke every session for the user and reject.
	if rotated {
		if err := h.tokens.DeleteByUser(userID); err != nil {
			slog.Error("refresh token reuse detected but session revocation failed", "userId", userID, "error", err)
		} else {
			slog.Warn("refresh token reuse detected; revoked all sessions", "userId", userID)
		}
		h.audit.Write(userID, auditlog.ActionRefreshReuse, "auth", userID, "", clientIP(r), r.UserAgent())
		clearRefreshCookies(w)
		shared.WriteError(w, http.StatusUnauthorized, "REFRESH_TOKEN_INVALID", "invalid or expired refresh token")
		return
	}

	// Verify the account is still active before issuing a new token.
	var status string
	if err := h.db.QueryRow(`SELECT status FROM users WHERE id = $1`, userID).Scan(&status); err != nil || status == "disabled" || status == "deleted" {
		clearRefreshCookies(w)
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is inactive")
		return
	}

	// Rotate: atomically mark the old token rotated (keep it so a later replay is
	// detected as reuse above) and issue a fresh one. A rotation that affects no
	// row means a concurrent request already rotated this exact token — the same
	// captured-cookie replay the branch above guards against, so fail it closed
	// and revoke the whole family rather than mint a second live session.
	rotatedNow, err := h.tokens.Rotate(tokenID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !rotatedNow {
		if err := h.tokens.DeleteByUser(userID); err != nil {
			slog.Error("refresh token reuse detected but session revocation failed", "userId", userID, "error", err)
		} else {
			slog.Warn("refresh token reuse detected (concurrent rotation); revoked all sessions", "userId", userID)
		}
		h.audit.Write(userID, auditlog.ActionRefreshReuse, "auth", userID, "", clientIP(r), r.UserAgent())
		clearRefreshCookies(w)
		shared.WriteError(w, http.StatusUnauthorized, "REFRESH_TOKEN_INVALID", "invalid or expired refresh token")
		return
	}

	accessToken, err := IssueAccessToken(userID, h.jwtSecret, h.accessTTL)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	if err := h.issueRefreshCookie(w, userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.NoCache(w)
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken": accessToken,
		"expiresIn":   int(h.accessTTL.Seconds()),
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err == nil {
		hash := hashToken(cookie.Value)
		if tokenID, _, err := h.tokens.FindByHash(hash); err == nil {
			_ = h.tokens.Delete(tokenID)
		}
	}
	clearRefreshCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// meResponse is the full current-user payload returned by GET /api/v1/auth/me.
type meResponse struct {
	ID                 string              `json:"id"`
	Email              string              `json:"email"`
	Name               string              `json:"name"`
	DisplayName        string              `json:"displayName"`
	GlobalRole         string              `json:"globalRole"`
	Status             string              `json:"status"`
	MFAEnabled         bool                `json:"mfaEnabled"`
	AvatarUpdatedAt    *string             `json:"avatarUpdatedAt,omitempty"`
	ProjectMemberships []projectMembership `json:"projectMemberships"`
}

type projectMembership struct {
	ProjectID string `json:"projectId"`
	Role      string `json:"role"`
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	var resp meResponse
	var avatarUpdatedAt sql.NullString
	err := h.db.QueryRow(
		`SELECT id, display_name, email, global_role, status, mfa_enabled, avatar_updated_at FROM users WHERE id = $1`, userID,
	).Scan(&resp.ID, &resp.DisplayName, &resp.Email, &resp.GlobalRole, &resp.Status, &resp.MFAEnabled, &avatarUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	resp.Name = resp.DisplayName // alias for backward compat
	if avatarUpdatedAt.Valid {
		resp.AvatarUpdatedAt = &avatarUpdatedAt.String
	}

	// Load project memberships.
	rows, err := h.db.Query(
		`SELECT project_id, role FROM memberships WHERE user_id = $1`, userID,
	)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()
	resp.ProjectMemberships = []projectMembership{}
	for rows.Next() {
		var m projectMembership
		if err := rows.Scan(&m.ProjectID, &m.Role); err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		resp.ProjectMemberships = append(resp.ProjectMemberships, m)
	}
	if err := rows.Err(); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	globalRole := shared.GetGlobalRole(r)

	// Only SUPER_ADMIN and ADMIN (or project owner) may invite.
	var req struct {
		Email     string  `json:"email"`
		ProjectID *string `json:"projectId"`
		Role      string  `json:"role"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if req.Email == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "email is required", "email")
		return
	}

	isSuperAdmin := globalRole == rbac.GlobalSuperAdmin
	isAdmin := globalRole == rbac.GlobalAdmin

	var memberRole string
	if req.ProjectID != nil {
		if req.Role == "" {
			req.Role = rbac.ProjectMember
		}
		if !rbac.IsValidProjectRole(req.Role) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid project role")
			return
		}
		if err := h.db.QueryRow(
			`SELECT role FROM memberships WHERE project_id=$1 AND user_id=$2`,
			*req.ProjectID, userID,
		).Scan(&memberRole); err != nil && !errors.Is(err, sql.ErrNoRows) {
			shared.WriteServerError(w, r, err)
			return
		}
	}

	if !isSuperAdmin && !isAdmin {
		// Non-admins may invite only if they hold project.invite_users for the project.
		if req.ProjectID == nil || !rbac.HasPermission(globalRole, memberRole, rbac.PermProjectInviteUsers) {
			shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only project admins can invite")
			return
		}
	}

	// Granting PROJECT_OWNER requires the actor to already be a PROJECT_OWNER
	// (or SUPER_ADMIN), regardless of global role.
	if req.Role == rbac.ProjectOwner && !isSuperAdmin && memberRole != rbac.ProjectOwner {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only project owners can transfer ownership")
		return
	}

	// Installation-wide seat limit: refuse to create an invitation that could
	// not be accepted right now. Only existing accounts count — pending
	// invitations don't hold a seat, so acceptance stays first-come-first-served
	// and is re-checked authoritatively in AcceptInvitation.
	if h.maxUsers > 0 {
		n, err := shared.CountUsers(h.db)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if n >= h.maxUsers {
			shared.WriteError(w, http.StatusForbidden, "USER_LIMIT_REACHED",
				fmt.Sprintf("this installation is limited to %d user accounts", h.maxUsers))
			return
		}
	}

	rawToken, tokenHash := generateToken()

	inv := &Invitation{
		ID:        shared.NewUUID(),
		Email:     req.Email,
		ProjectID: req.ProjectID,
		Role:      req.Role,
		TokenHash: tokenHash,
		InvitedBy: userID,
		ExpiresAt: time.Now().Add(invitationTTL),
	}
	if err := h.invitations.Store(inv); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	acceptURL := fmt.Sprintf("%s/#/invitations/%s/accept", appBaseURL(), rawToken)

	// Deliver the invitation email. A delivery failure must not fail the request:
	// the acceptURL is still returned so the inviter can share the link manually
	// (the SPA copies it to the clipboard). In dev mode (no SMTP host) the mailer
	// just logs the message.
	var projectName string
	if req.ProjectID != nil {
		_ = h.db.QueryRow(`SELECT name FROM projects WHERE id = $1`, *req.ProjectID).Scan(&projectName)
	}
	subject, body := invitationEmail(projectName, acceptURL)
	if err := h.mailer.Send(r.Context(), req.Email, subject, body); err != nil {
		slog.Error("invitation email send failed", "invitationId", inv.ID, "email", req.Email, "error", err)
	}

	slog.Info("invitation created",
		"invitationId", inv.ID,
		"email", req.Email,
		"invitedBy", userID,
	)

	shared.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":        inv.ID,
		"email":     inv.Email,
		"role":      inv.Role,
		"expiresAt": inv.ExpiresAt,
		"acceptURL": acceptURL,
	})
}

// invitationEmail builds the subject and plain-text body for a project (or
// platform) invitation. The mailer wraps the body in a text/html alternative,
// so a bare URL on its own line is enough for both plain and HTML clients.
func invitationEmail(projectName, acceptURL string) (subject, body string) {
	if projectName != "" {
		subject = fmt.Sprintf("You've been invited to %s on Octbase", projectName)
		body = fmt.Sprintf(
			"You've been invited to join the project \"%s\" on Octbase.\n\n"+
				"Open the link below to accept the invitation and set up your account:\n\n%s\n\n"+
				"This invitation expires in 7 days.",
			projectName, acceptURL,
		)
		return subject, body
	}
	subject = "You've been invited to Octbase"
	body = fmt.Sprintf(
		"You've been invited to Octbase.\n\n"+
			"Open the link below to accept the invitation and set up your account:\n\n%s\n\n"+
			"This invitation expires in 7 days.",
		acceptURL,
	)
	return subject, body
}

func (h *Handler) GetInvitation(w http.ResponseWriter, r *http.Request) {
	rawToken := chi.URLParam(r, "token")
	hash := hashToken(rawToken)

	inv, err := h.invitations.FindByTokenHash(hash)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if inv == nil || inv.AcceptedAt != nil || time.Now().After(inv.ExpiresAt) {
		shared.WriteError(w, http.StatusNotFound, "INVITATION_NOT_FOUND", "invitation not found or expired")
		return
	}

	resp := map[string]any{
		"email": inv.Email,
		"role":  inv.Role,
	}
	if inv.ProjectID != nil {
		var projectName string
		if err := h.db.QueryRow(`SELECT name FROM projects WHERE id = $1`, *inv.ProjectID).Scan(&projectName); err == nil {
			resp["projectName"] = projectName
		}
	}
	var inviterName string
	if err := h.db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, inv.InvitedBy).Scan(&inviterName); err == nil {
		resp["inviterName"] = inviterName
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	rawToken := chi.URLParam(r, "token")
	hash := hashToken(rawToken)

	inv, err := h.invitations.FindByTokenHash(hash)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if inv == nil || inv.AcceptedAt != nil || time.Now().After(inv.ExpiresAt) {
		shared.WriteError(w, http.StatusGone, "INVITATION_EXPIRED", "invitation not found or expired")
		return
	}

	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if req.Name == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "name is required", "name")
		return
	}
	if req.Password == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "password is required", "password")
		return
	}
	if msg := shared.ValidatePassword(req.Password); msg != "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", msg, "password")
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	now := shared.Now()
	userID := shared.NewUUID()

	if err := shared.WithTx(h.db, func(tx *sql.Tx) error {
		// Authoritative seat-limit check: the invitation may have been created
		// while seats were free and accepted after they filled up. Inside the
		// transaction so the count and the insert see a consistent snapshot.
		if h.maxUsers > 0 {
			n, err := shared.CountUsers(tx)
			if err != nil {
				return err
			}
			if n >= h.maxUsers {
				return errUserLimitReached
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO users (id, email, display_name, password_hash, global_role, status, is_active, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,'USER','active',true,$5,$6)`,
			userID, inv.Email, req.Name, passwordHash, now, now,
		); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if inv.ProjectID != nil {
			memberID := shared.NewUUID()
			if _, err := tx.Exec(
				`INSERT INTO memberships (id, project_id, user_id, role, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				memberID, *inv.ProjectID, userID, inv.Role, now, now,
			); err != nil {
				return fmt.Errorf("create membership: %w", err)
			}
		}
		_, err := tx.Exec(`UPDATE invitations SET accepted_at = now() WHERE id = $1`, inv.ID)
		return err
	}); err != nil {
		if errors.Is(err, errUserLimitReached) {
			shared.WriteError(w, http.StatusForbidden, "USER_LIMIT_REACHED",
				fmt.Sprintf("this installation is limited to %d user accounts", h.maxUsers))
			return
		}
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "EMAIL_CONFLICT", "a user with this email already exists")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}

	accessToken, err := IssueAccessToken(userID, h.jwtSecret, h.accessTTL)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.issueRefreshCookie(w, userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.NoCache(w)
	shared.WriteJSON(w, http.StatusCreated, map[string]any{
		"accessToken": accessToken,
		"expiresIn":   int(h.accessTTL.Seconds()),
	})
}

// issueRefreshCookie generates a refresh token, stores it, and sets the cookie.
func (h *Handler) issueRefreshCookie(w http.ResponseWriter, userID string) error {
	rawToken, tokenHash := generateToken()
	tokenID := shared.NewUUID()
	expiresAt := time.Now().Add(h.refreshTTL)

	if err := h.tokens.Store(tokenID, userID, tokenHash, expiresAt); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    rawToken,
		Path:     "/api/v1/auth/refresh",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteStrictMode,
	})
	// Companion presence marker, readable by JS so the client can gate its
	// refresh probe. Same expiry as the refresh cookie so the two rise and fall
	// together; Path "/" so it is visible to document.cookie on every page.
	http.SetCookie(w, &http.Cookie{
		Name:     refreshPresentCookieName,
		Value:    "1",
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: false,
		Secure:   secureCookies(),
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// clearRefreshCookies expires both the HttpOnly refresh cookie and its presence
// marker. Called on logout and whenever Refresh rejects a request, so a dead
// session stops triggering the client's bootstrap refresh probe.
func clearRefreshCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth/refresh",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshPresentCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   secureCookies(),
		SameSite: http.SameSiteStrictMode,
	})
}

func generateToken() (raw, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	return raw, hash
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func accessTTL() time.Duration {
	if s := os.Getenv("OCTBASE_JWT_ACCESS_TTL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 15 * time.Minute
}

func refreshTTL() time.Duration {
	if s := os.Getenv("OCTBASE_JWT_REFRESH_TTL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return defaultSessionTimeout
}

// mfaChallengeTTLFromEnv controls how long a caller has to submit their TOTP/
// recovery code after a password check before having to log in again.
func mfaChallengeTTLFromEnv() time.Duration {
	if s := os.Getenv("OCTBASE_MFA_CHALLENGE_TTL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 5 * time.Minute
}

// secureCookies returns true when OCTBASE_SECURE_COOKIES=true.
// Set this in production where TLS is terminated by the reverse proxy.
// Leave unset (or false) for local HTTP development.
func secureCookies() bool {
	return os.Getenv("OCTBASE_SECURE_COOKIES") == "true"
}

// clientIP extracts the real IP for audit logging. It relies on shared.RealIP
// having already resolved r.RemoteAddr from trusted-proxy headers, so it never
// reads the spoofable X-Forwarded-For directly.
func clientIP(r *http.Request) string {
	return shared.ClientIP(r)
}
