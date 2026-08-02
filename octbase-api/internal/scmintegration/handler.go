package scmintegration

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// ActivityWriter is a minimal interface for recording activity.
type ActivityWriter interface {
	Write(projectID, taskID, actorID, actType string, params map[string]any) error
}

// Handler holds SCM integration HTTP handlers.
type Handler struct {
	db          *sql.DB
	repos       *RepositoryConnectionRepo
	branches    *BranchReferenceRepo
	oauthStates *OAuthStateRepo
	activity    ActivityWriter
	httpClient  *http.Client
}

func NewHandler(db *sql.DB, repos *RepositoryConnectionRepo, branches *BranchReferenceRepo, activity ActivityWriter) *Handler {
	return &Handler{
		db: db, repos: repos, branches: branches, oauthStates: NewOAuthStateRepo(db),
		activity: activity, httpClient: defaultHTTPClient,
	}
}

// buildProvider resolves a connection's (possibly refreshed) access token and
// returns a ready provider client.
func (h *Handler) buildProvider(ctx context.Context, rc *RepositoryConnection) (Provider, error) {
	token, err := h.resolveToken(ctx, rc)
	if err != nil {
		return nil, err
	}
	return NewProvider(rc, token, h.httpClient)
}

// resolveToken decrypts the access token, refreshing it first when an OAuth
// token has expired and a refresh token is available. Errors are *ProviderError
// so callers can use writeProviderError.
func (h *Handler) resolveToken(ctx context.Context, rc *RepositoryConnection) (string, error) {
	token, derr := shared.DecryptSecret(rc.AccessToken)
	if derr != nil {
		return "", &ProviderError{Code: CodeNotConfigured, Status: http.StatusBadRequest, Message: "access token decryption is not configured"}
	}
	if rc.AuthKind != authKindOAuth || !tokenExpired(rc.TokenExpiresAt) || rc.RefreshToken == "" {
		return token, nil
	}
	tok, err := h.refreshConnection(ctx, rc)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// refreshConnection rotates an OAuth access token via its refresh token and
// persists the new credentials, returning the fresh access token.
func (h *Handler) refreshConnection(ctx context.Context, rc *RepositoryConnection) (string, error) {
	refreshTok, derr := shared.DecryptSecret(rc.RefreshToken)
	if derr != nil {
		return "", &ProviderError{Code: CodeNotConfigured, Status: http.StatusBadRequest, Message: "refresh token decryption is not configured"}
	}
	cfg, ok := loadOAuthConfig(rc.Provider)
	if !ok {
		return "", &ProviderError{Code: CodeNotConfigured, Status: http.StatusBadRequest, Message: "OAuth app is not configured for this provider"}
	}
	tok, err := cfg.refresh(ctx, h.httpClient, refreshTok)
	if err != nil {
		return "", err
	}
	if err := h.applyToken(rc, tok); err != nil {
		return "", &ProviderError{Code: CodeNotConfigured, Status: http.StatusBadRequest, Message: err.Error()}
	}
	return tok.AccessToken, nil
}

// applyToken encrypts and stores rotated OAuth tokens on the connection.
func (h *Handler) applyToken(rc *RepositoryConnection, tok *oauthToken) error {
	enc, err := shared.EncryptSecret(tok.AccessToken)
	if err != nil {
		return err
	}
	rc.AccessToken = enc
	if tok.RefreshToken != "" {
		renc, rerr := shared.EncryptSecret(tok.RefreshToken)
		if rerr != nil {
			return rerr
		}
		rc.RefreshToken = renc
	}
	if tok.ExpiresIn > 0 {
		rc.TokenExpiresAt = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339)
	} else {
		rc.TokenExpiresAt = ""
	}
	rc.AuthKind = authKindOAuth
	rc.UpdatedAt = shared.Now()
	return h.repos.Update(rc)
}

// tokenExpired reports whether an RFC3339 expiry is within 60s of now. An empty
// expiry means the token does not expire (e.g. a GitHub classic OAuth token).
func tokenExpired(expiresAt string) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return time.Now().UTC().Add(60 * time.Second).After(t)
}

// writeProviderError translates a ProviderError into the standard error
// response, defaulting to a 502 SCM_PROVIDER_ERROR for unexpected failures.
func writeProviderError(w http.ResponseWriter, err error) {
	var pe *ProviderError
	if errors.As(err, &pe) {
		shared.WriteError(w, pe.Status, pe.Code, pe.Message)
		return
	}
	shared.WriteError(w, http.StatusBadGateway, CodeProviderError, err.Error())
}

// isRealProvider reports whether the provider performs live network calls.
func isRealProvider(provider string) bool {
	return provider != "" && provider != ProviderFake
}

// memberGuard checks project membership and returns the role. SUPER_ADMIN
// bypasses membership and is treated as PROJECT_ADMIN.
// validRepoURL reports whether a repository URL is acceptable for a real
// provider: an http(s) URL with a host. repositoryUrl is stored metadata and is
// not itself dialed (apiBaseUrl is, guarded by checkOutboundURL), but rejecting
// exotic schemes (file:, gopher:, ...) keeps stored connections clean.
func validRepoURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (h *Handler) memberGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	return shared.ProjectMemberGuard(h.db, w, r, projectID)
}

// taskProjectGuard looks up the project for a task and runs memberGuard.
func (h *Handler) taskProjectGuard(w http.ResponseWriter, r *http.Request, taskID string) (string, string, bool) {
	projectID, err := h.branches.FindTaskProjectID(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return "", "", false
	}
	if projectID == "" {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return "", "", false
	}
	role, ok := h.memberGuard(w, r, projectID)
	return projectID, role, ok
}

// taskProjectWriterGuard is taskProjectGuard plus the writer-role check and
// the 409 PROJECT_ARCHIVED freeze, for branch/PR mutations addressed by task.
func (h *Handler) taskProjectWriterGuard(w http.ResponseWriter, r *http.Request, taskID string) (string, string, bool) {
	projectID, role, ok := h.taskProjectGuard(w, r, taskID)
	if !ok {
		return "", "", false
	}
	if !shared.RequireWriterOr403(w, role) {
		return "", "", false
	}
	if !shared.RequireProjectWritable(h.db, w, r, projectID) {
		return "", "", false
	}
	return projectID, role, true
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/api/v1/projects/{projectId}/repository-connections", h.CreateRepoConnection)
	r.Get("/api/v1/projects/{projectId}/repository-connections", h.ListRepoConnections)
	r.Patch("/api/v1/repository-connections/{repositoryId}", h.UpdateRepoConnection)
	r.Post("/api/v1/tasks/{taskId}/branches", h.CreateBranch)
	r.Get("/api/v1/tasks/{taskId}/branches", h.ListBranches)
	r.Delete("/api/v1/repository-connections/{repositoryId}", h.DeleteRepoConnection)
	r.Delete("/api/v1/tasks/{taskId}/branches/{branchId}", h.DeleteBranch)
	r.Post("/api/v1/tasks/{taskId}/branches/{branchId}/pull-request", h.CreatePullRequest)
	r.Get("/api/v1/repository-connections/{repositoryId}/oauth/authorize", h.OAuthAuthorize)
	r.Post("/api/v1/repository-connections/{repositoryId}/oauth/refresh", h.OAuthRefresh)
}

// RegisterPublicRoutes registers routes that must not require a JWT — the OAuth
// callback is reached via the provider's browser redirect and is authenticated
// by the one-time state parameter instead.
func (h *Handler) RegisterPublicRoutes(r chi.Router) {
	r.Get("/api/v1/oauth/{provider}/callback", h.OAuthCallback)
}

func (h *Handler) CreateRepoConnection(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	var req struct {
		Provider      string `json:"provider"`
		DisplayName   string `json:"displayName"`
		RepositoryURL string `json:"repositoryUrl"`
		DefaultBranch string `json:"defaultBranch"`
		APIBaseURL    string `json:"apiBaseUrl"`
		AccessToken   string `json:"accessToken"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if req.Provider == "" {
		req.Provider = ProviderFake
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if isRealProvider(req.Provider) && req.RepositoryURL != "" && !validRepoURL(req.RepositoryURL) {
		shared.WriteError(w, http.StatusBadRequest, "SCM_URL_NOT_ALLOWED", "repository URL must be an http(s) URL")
		return
	}
	now := shared.Now()
	rc := &RepositoryConnection{
		ID: shared.NewUUID(), ProjectID: projectID, Provider: req.Provider,
		DisplayName: req.DisplayName, RepositoryURL: req.RepositoryURL,
		DefaultBranch: req.DefaultBranch, APIBaseURL: req.APIBaseURL,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	// Validate the repository against the live provider (when a token is given)
	// and adopt its real default branch before persisting.
	if isRealProvider(rc.Provider) && req.AccessToken != "" {
		provider, perr := NewProvider(rc, req.AccessToken, h.httpClient)
		if perr != nil {
			writeProviderError(w, perr)
			return
		}
		defaultBranch, verr := provider.ValidateRepository(r.Context())
		if verr != nil {
			writeProviderError(w, verr)
			return
		}
		if defaultBranch != "" {
			rc.DefaultBranch = defaultBranch
		}
	}
	// Encrypt the access token at rest.
	enc, err := shared.EncryptSecret(req.AccessToken)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, CodeNotConfigured, "access token encryption is not configured")
		return
	}
	rc.AccessToken = enc
	if err := h.repos.Create(rc); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, rc)
}

func (h *Handler) ListRepoConnections(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	rcs, err := h.repos.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	for i := range rcs {
		rcs[i].OAuthAvailable = oauthConfigured(rcs[i].Provider)
	}
	shared.WriteJSON(w, http.StatusOK, rcs)
}

func (h *Handler) UpdateRepoConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "repositoryId")
	rc, err := h.repos.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rc == nil {
		shared.WriteError(w, http.StatusNotFound, "REPO_NOT_FOUND", "Repository connection not found")
		return
	}
	role, ok := h.memberGuard(w, r, rc.ProjectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	var req struct {
		DisplayName   *string `json:"displayName"`
		RepositoryURL *string `json:"repositoryUrl"`
		DefaultBranch *string `json:"defaultBranch"`
		APIBaseURL    *string `json:"apiBaseUrl"`
		AccessToken   *string `json:"accessToken"`
		Version       *int    `json:"version"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"displayName": true, "repositoryUrl": true, "defaultBranch": true,
		"apiBaseUrl": true, "accessToken": true, "version": true,
	}, nil, &req) {
		return
	}
	if req.DisplayName != nil {
		rc.DisplayName = *req.DisplayName
	}
	if req.RepositoryURL != nil {
		rc.RepositoryURL = *req.RepositoryURL
	}
	if isRealProvider(rc.Provider) && rc.RepositoryURL != "" && !validRepoURL(rc.RepositoryURL) {
		shared.WriteError(w, http.StatusBadRequest, "SCM_URL_NOT_ALLOWED", "repository URL must be an http(s) URL")
		return
	}
	if req.DefaultBranch != nil {
		rc.DefaultBranch = *req.DefaultBranch
	}
	if req.APIBaseURL != nil {
		rc.APIBaseURL = *req.APIBaseURL
	}
	if req.AccessToken != nil {
		enc, eerr := shared.EncryptSecret(*req.AccessToken)
		if eerr != nil {
			shared.WriteError(w, http.StatusBadRequest, CodeNotConfigured, "access token encryption is not configured")
			return
		}
		rc.AccessToken = enc
	}
	// An optional client version makes the edit optimistic against the
	// client's own snapshot (see workmanagement.UpdateTask for the same
	// pattern) and also guards against a concurrent background OAuth token
	// rotation (applyToken) overwriting this edit or vice versa.
	if req.Version != nil {
		rc.Version = *req.Version
	}
	rc.UpdatedAt = shared.Now()
	if err := h.repos.Update(rc); err != nil {
		shared.WriteUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, rc)
}

func (h *Handler) CreateBranch(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	projectID, _, ok := h.taskProjectWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	var req struct {
		RepositoryID string `json:"repositoryId"`
		BranchName   string `json:"branchName"`
		BranchType   string `json:"branchType"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if req.BranchType == "" {
		req.BranchType = "feature"
	}
	// Resolve the target repository connection (scoped to the task's project).
	rc, err := h.repos.FindByIDInProject(req.RepositoryID, projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, rc != nil, "REPO_NOT_FOUND", "repository connection not found") {
		return
	}
	// Create the branch on the remote before recording it locally.
	provider, perr := h.buildProvider(r.Context(), rc)
	if perr != nil {
		writeProviderError(w, perr)
		return
	}
	// When the branch already exists on the remote we link it instead of
	// failing. CodeBranchExists also covers invalid names, so confirm the
	// branch is really there before treating creation-as-link.
	linked := false
	if cerr := provider.CreateBranch(r.Context(), req.BranchName, rc.DefaultBranch); cerr != nil {
		var pe *ProviderError
		if !errors.As(cerr, &pe) || pe.Code != CodeBranchExists {
			writeProviderError(w, cerr)
			return
		}
		exists, xerr := provider.BranchExists(r.Context(), req.BranchName)
		if xerr != nil {
			writeProviderError(w, xerr)
			return
		}
		if !exists {
			// The name was rejected as invalid, not already taken.
			writeProviderError(w, cerr)
			return
		}
		linked = true
	}
	br := &BranchReference{
		ID: shared.NewUUID(), TaskID: taskID, RepositoryID: req.RepositoryID,
		BranchName: req.BranchName, BranchType: req.BranchType, CreatedAt: shared.Now(),
	}
	if err := h.branches.Create(br); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	event := "BRANCH_CREATED"
	if linked {
		event = "BRANCH_LINKED"
	}
	_ = h.activity.Write(projectID, taskID, actorID, event, map[string]any{"branchName": req.BranchName})
	shared.WriteJSON(w, http.StatusCreated, br)
}

func (h *Handler) ListBranches(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if _, _, ok := h.taskProjectGuard(w, r, taskID); !ok {
		return
	}
	brs, err := h.branches.ListByTask(taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, brs)
}

func (h *Handler) DeleteRepoConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "repositoryId")
	rc, err := h.repos.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rc == nil {
		shared.WriteError(w, http.StatusNotFound, "REPO_NOT_FOUND", "repository connection not found")
		return
	}
	role, ok := h.memberGuard(w, r, rc.ProjectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	if err := h.repos.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	branchID := chi.URLParam(r, "branchId")
	if _, _, ok := h.taskProjectWriterGuard(w, r, taskID); !ok {
		return
	}
	// The lookup is scoped to the guarded task named in the URL; a branch that
	// belongs to a different task reads as not-found.
	br, err := h.branches.FindByIDInTask(branchID, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, br != nil, "BRANCH_NOT_FOUND", "Branch reference not found") {
		return
	}
	if err := h.branches.Delete(branchID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreatePullRequest opens a pull/merge request from a task branch into the
// repository's default branch (or an explicit target) and records its identity.
func (h *Handler) CreatePullRequest(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	branchID := chi.URLParam(r, "branchId")
	projectID, _, ok := h.taskProjectWriterGuard(w, r, taskID)
	if !ok {
		return
	}
	br, err := h.branches.FindByIDInTask(branchID, taskID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, br != nil, "BRANCH_NOT_FOUND", "branch reference not found") {
		return
	}
	rc, err := h.repos.FindByIDInProject(br.RepositoryID, projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !shared.RequireFound(w, rc != nil, "REPO_NOT_FOUND", "repository connection not found") {
		return
	}
	var req struct {
		Title        string `json:"title"`
		Body         string `json:"body"`
		TargetBranch string `json:"targetBranch"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	target := req.TargetBranch
	if target == "" {
		target = rc.DefaultBranch
	}
	title := req.Title
	if title == "" {
		title = br.BranchName
	}
	provider, perr := h.buildProvider(r.Context(), rc)
	if perr != nil {
		writeProviderError(w, perr)
		return
	}
	pr, perr := provider.CreatePullRequest(r.Context(), PullRequestInput{
		Title: title, Body: req.Body, SourceBranch: br.BranchName, TargetBranch: target,
	})
	if perr != nil {
		writeProviderError(w, perr)
		return
	}
	status := pr.Status
	if status == "" {
		status = "OPEN"
	}
	if err := h.branches.UpdatePRByID(br.ID, status, pr.URL, pr.Number); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.activity.Write(projectID, taskID, actorID, "PR_CREATED", map[string]any{"branchName": br.BranchName, "prUrl": pr.URL})
	shared.WriteJSON(w, http.StatusCreated, pr)
}

// OAuthAuthorize starts the OAuth flow for a connection, redirecting the browser
// to the provider's consent screen with a one-time state.
func (h *Handler) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "repositoryId")
	rc, err := h.repos.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rc == nil {
		shared.WriteError(w, http.StatusNotFound, "REPO_NOT_FOUND", "repository connection not found")
		return
	}
	role, ok := h.memberGuard(w, r, rc.ProjectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	cfg, configured := loadOAuthConfig(rc.Provider)
	if !configured {
		shared.WriteError(w, http.StatusBadRequest, CodeNotConfigured, "OAuth is not configured for this provider")
		return
	}
	state := shared.NewUUID()
	expires := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	if err := h.oauthStates.Create(&OAuthState{
		State: state, Provider: rc.Provider, RepositoryID: rc.ID, UserID: shared.GetUserID(r), ExpiresAt: expires,
	}, shared.Now()); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Return the consent URL as JSON: this SPA authenticates with a bearer token
	// in a header, so it fetches this endpoint and then navigates the browser to
	// the returned URL (a 302 here would lose the auth header).
	shared.WriteJSON(w, http.StatusOK, map[string]string{"authorizeUrl": cfg.authorizeURL(state)})
}

// OAuthCallback completes the OAuth flow: it validates the one-time state,
// exchanges the code for tokens, stores them encrypted, and redirects back to
// the app (or returns JSON when no app URL is configured).
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	provSeg := chi.URLParam(r, "provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "missing code or state")
		return
	}
	st, err := h.oauthStates.Consume(state)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if st == nil || tokenExpired(st.ExpiresAt) || providerPathSegment(st.Provider) != provSeg {
		shared.WriteError(w, http.StatusBadRequest, "INVALID_STATE", "invalid or expired OAuth state")
		return
	}
	rc, err := h.repos.FindByID(st.RepositoryID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rc == nil {
		shared.WriteError(w, http.StatusNotFound, "REPO_NOT_FOUND", "repository connection not found")
		return
	}
	cfg, configured := loadOAuthConfig(st.Provider)
	if !configured {
		shared.WriteError(w, http.StatusBadRequest, CodeNotConfigured, "OAuth is not configured for this provider")
		return
	}
	tok, perr := cfg.exchangeCode(r.Context(), h.httpClient, code)
	if perr != nil {
		writeProviderError(w, perr)
		return
	}
	if err := h.applyToken(rc, tok); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if appURL := os.Getenv("OCTBASE_APP_URL"); appURL != "" {
		http.Redirect(w, r, appURL, http.StatusFound)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"connected": true})
}

// OAuthRefresh forces rotation of an OAuth connection's access token.
func (h *Handler) OAuthRefresh(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "repositoryId")
	rc, err := h.repos.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rc == nil {
		shared.WriteError(w, http.StatusNotFound, "REPO_NOT_FOUND", "repository connection not found")
		return
	}
	role, ok := h.memberGuard(w, r, rc.ProjectID)
	if !ok {
		return
	}
	if err := shared.RequireOwner(role); err != nil {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	if rc.AuthKind != authKindOAuth || rc.RefreshToken == "" {
		shared.WriteError(w, http.StatusBadRequest, CodeNotConfigured, "connection has no OAuth refresh token")
		return
	}
	if _, rerr := h.refreshConnection(r.Context(), rc); rerr != nil {
		writeProviderError(w, rerr)
		return
	}
	shared.WriteJSON(w, http.StatusOK, rc)
}
