package scmintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Provider identifiers stored on RepositoryConnection.Provider.
const (
	ProviderGitHub    = "GITHUB"
	ProviderGitLab    = "GITLAB"
	ProviderBitbucket = "BITBUCKET"
	// ProviderFake is the default offline provider used by seed data, the dev
	// stack, and tests. It performs no network calls.
	ProviderFake = "FAKE_GITLAB"
)

// Provider talks to a remote source-control host. Implementations are selected
// per RepositoryConnection by NewProvider.
type Provider interface {
	// ValidateRepository confirms the repository exists and returns its default
	// branch name.
	ValidateRepository(ctx context.Context) (defaultBranch string, err error)
	// CreateBranch creates branchName from the HEAD of fromBranch.
	CreateBranch(ctx context.Context, branchName, fromBranch string) error
	// BranchExists reports whether branchName already exists on the remote. It
	// lets the API distinguish "the branch is already there" (so it can be
	// linked) from "the name was rejected as invalid".
	BranchExists(ctx context.Context, branchName string) (bool, error)
	// CreatePullRequest opens a pull/merge request and returns its identifiers.
	CreatePullRequest(ctx context.Context, in PullRequestInput) (*PullRequest, error)
}

// ProviderError carries a stable error code and the HTTP status the API should
// return when a provider operation fails.
type ProviderError struct {
	Code    string
	Status  int
	Message string
}

func (e *ProviderError) Error() string { return e.Message }

// Stable error codes surfaced to API clients.
const (
	CodeRepoNotFound  = "SCM_REPO_NOT_FOUND"
	CodeAuthFailed    = "SCM_AUTH_FAILED"
	CodeBranchExists  = "SCM_BRANCH_EXISTS"
	CodeProviderError = "SCM_PROVIDER_ERROR"
	CodeNotConfigured = "SCM_NOT_CONFIGURED"
	// CodeURLNotAllowed is returned when a user-supplied SCM base URL uses an
	// unsupported scheme or targets a disallowed (internal) address.
	CodeURLNotAllowed = "SCM_URL_NOT_ALLOWED"
)

// defaultHTTPClient is used when NewProvider is given a nil client. It carries
// the SSRF egress guard (see ssrf.go), so every real provider request refuses to
// reach loopback/private/link-local hosts even when the base URL is attacker
// supplied.
var defaultHTTPClient = newGuardedHTTPClient()

// NewProvider builds the Provider for a connection. FAKE_GITLAB (and an empty
// provider) map to the offline fake; an unknown provider is an error.
func NewProvider(conn *RepositoryConnection, token string, client *http.Client) (Provider, error) {
	if client == nil {
		client = defaultHTTPClient
	}
	owner, repo, fullPath, err := splitRepoURL(conn.RepositoryURL)
	base := strings.TrimRight(conn.APIBaseURL, "/")
	// Reject a user-supplied base URL that uses a bad scheme or a literal
	// internal address before it is ever dialed. Hostnames resolving to internal
	// IPs are caught at dial time by the guarded client. The offline fake makes
	// no network calls, so it is exempt.
	if conn.Provider != ProviderFake && conn.Provider != "" {
		if perr := checkOutboundURL(base, allowPrivateEgress); perr != nil {
			return nil, perr
		}
	}
	switch conn.Provider {
	case ProviderGitHub:
		if err != nil {
			return nil, err
		}
		if base == "" {
			base = "https://api.github.com"
		}
		return &githubProvider{base: base, owner: owner, repo: repo, token: token, client: client}, nil
	case ProviderGitLab:
		if err != nil {
			return nil, err
		}
		if base == "" {
			base = "https://gitlab.com"
		}
		return &gitlabProvider{base: base, project: fullPath, token: token, oauth: conn.AuthKind == authKindOAuth, client: client}, nil
	case ProviderBitbucket:
		if err != nil {
			return nil, err
		}
		if base == "" {
			base = "https://api.bitbucket.org"
		}
		return &bitbucketProvider{base: base, workspace: owner, repo: repo, token: token, client: client}, nil
	case ProviderFake, "":
		return &fakeProvider{defaultBranch: conn.DefaultBranch}, nil
	default:
		return nil, fmt.Errorf("unsupported SCM provider %q", conn.Provider)
	}
}

// splitRepoURL extracts owner, repo, and the full namespaced path from a git
// remote URL (https or scp-style ssh), tolerating a trailing ".git".
func splitRepoURL(raw string) (owner, repo, fullPath string, err error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "git@")
	// Drop credentials in "user@host" form left after scheme stripping.
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	i := strings.IndexAny(s, "/:")
	if i < 0 {
		return "", "", "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadRequest, Message: "invalid repository URL"}
	}
	path := strings.Trim(s[i+1:], "/")
	if path == "" {
		return "", "", "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadRequest, Message: "repository URL has no path"}
	}
	parts := strings.Split(path, "/")
	return parts[0], parts[len(parts)-1], path, nil
}

// classifyGet maps a failed read/validation response to a ProviderError,
// enriching the message with the provider's own error text when present (e.g.
// GitHub's "Resource not accessible by personal access token" for a missing
// scope). It consumes the response body.
func classifyGet(resp *http.Response) *ProviderError {
	detail := errDetail(resp)
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ProviderError{Code: CodeAuthFailed, Status: http.StatusBadGateway, Message: withDetail("SCM provider rejected the access token", detail)}
	case http.StatusNotFound:
		return &ProviderError{Code: CodeRepoNotFound, Status: http.StatusNotFound, Message: withDetail("repository not found on the SCM provider", detail)}
	default:
		return &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: withDetail(fmt.Sprintf("SCM provider returned status %d", resp.StatusCode), detail)}
	}
}

// classifyCreate maps a failed branch-creation response to a ProviderError.
// Providers signal an already-existing branch with 400 (GitLab), 409
// (Bitbucket), or 422 (GitHub). It consumes the response body.
func classifyCreate(resp *http.Response) *ProviderError {
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return &ProviderError{Code: CodeBranchExists, Status: http.StatusConflict, Message: withDetail("branch already exists or is invalid", errDetail(resp))}
	default:
		return classifyGet(resp)
	}
}

// classifyExists maps a branch-lookup response to an existence boolean: 200 OK
// means the branch is present, 404 means it is absent, and anything else (auth,
// rate limit, etc.) is a provider error. It consumes the response body.
func classifyExists(resp *http.Response) (bool, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, classifyGet(resp)
	}
}

// withDetail appends a provider-supplied detail to a base error message.
func withDetail(base, detail string) string {
	if detail == "" {
		return base
	}
	return base + ": " + detail
}

// errDetail reads a bounded prefix of a provider error response body, closes it,
// and extracts a human-readable message.
func errDetail(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	_ = resp.Body.Close()
	return providerMessage(data)
}

// providerMessage extracts a human-readable error message from a provider's JSON
// error body, tolerating the differing shapes used by GitHub/GitLab ("message"
// or "error"/"error_description") and Bitbucket ("error":{"message":...}).
// Returns "" when nothing useful is found.
func providerMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var p struct {
		Message          string          `json:"message"`
		Error            json.RawMessage `json:"error"`
		ErrorDescription string          `json:"error_description"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	if p.Message != "" {
		return strings.TrimSpace(p.Message)
	}
	if p.ErrorDescription != "" {
		return strings.TrimSpace(p.ErrorDescription)
	}
	if len(p.Error) > 0 {
		var s string
		if json.Unmarshal(p.Error, &s) == nil && s != "" {
			return strings.TrimSpace(s)
		}
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(p.Error, &e) == nil && e.Message != "" {
			return strings.TrimSpace(e.Message)
		}
	}
	return ""
}

// drain reads and closes a response body so the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ---- GitHub ----

type githubProvider struct {
	base, owner, repo, token string
	client                   *http.Client
}

func (p *githubProvider) do(ctx context.Context, method, u string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r) // #nosec G704 -- u passed the SSRF preflight and the client uses the guarded dialer (see shared/httpx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.client.Do(req) // #nosec G704 -- guarded dialer + SSRF preflight upstream
}

func (p *githubProvider) ValidateRepository(ctx context.Context) (string, error) {
	resp, err := p.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s", p.base, p.owner, p.repo), nil)
	if err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", classifyGet(resp)
	}
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: "decode repository response"}
	}
	return out.DefaultBranch, nil
}

func (p *githubProvider) CreateBranch(ctx context.Context, branchName, fromBranch string) error {
	// Resolve the source branch HEAD SHA.
	resp, err := p.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/git/ref/heads/%s", p.base, p.owner, p.repo, url.PathEscape(fromBranch)), nil)
	if err != nil {
		return &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if resp.StatusCode != http.StatusOK {
		return classifyGet(resp)
	}
	_ = json.NewDecoder(resp.Body).Decode(&ref)
	drain(resp)

	body, _ := json.Marshal(map[string]string{"ref": "refs/heads/" + branchName, "sha": ref.Object.SHA})
	cr, err := p.do(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/%s/git/refs", p.base, p.owner, p.repo), body)
	if err != nil {
		return &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(cr)
	if cr.StatusCode != http.StatusCreated {
		return classifyCreate(cr)
	}
	return nil
}

func (p *githubProvider) BranchExists(ctx context.Context, branchName string) (bool, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/git/ref/heads/%s", p.base, p.owner, p.repo, url.PathEscape(branchName))
	resp, err := p.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	return classifyExists(resp)
}

func (p *githubProvider) CreatePullRequest(ctx context.Context, in PullRequestInput) (*PullRequest, error) {
	body, _ := json.Marshal(map[string]string{
		"title": in.Title,
		"head":  in.SourceBranch,
		"base":  in.TargetBranch,
		"body":  in.Body,
	})
	resp, err := p.do(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/%s/pulls", p.base, p.owner, p.repo), body)
	if err != nil {
		return nil, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated {
		return nil, classifyCreate(resp)
	}
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return &PullRequest{Number: out.Number, URL: out.HTMLURL, Status: out.State}, nil
}

// ---- GitLab ----

type gitlabProvider struct {
	base, project, token string
	oauth                bool
	client               *http.Client
}

func (p *gitlabProvider) projectID() string {
	// GitLab accepts the URL-encoded namespaced path as the project ID.
	return strings.ReplaceAll(url.PathEscape(p.project), "/", "%2F")
}

func (p *gitlabProvider) do(ctx context.Context, method, u string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r) // #nosec G704 -- u passed the SSRF preflight and the client uses the guarded dialer (see shared/httpx)
	if err != nil {
		return nil, err
	}
	// OAuth tokens authenticate via Bearer; personal access tokens via PRIVATE-TOKEN.
	if p.oauth {
		req.Header.Set("Authorization", "Bearer "+p.token)
	} else {
		req.Header.Set("PRIVATE-TOKEN", p.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.client.Do(req) // #nosec G704 -- guarded dialer + SSRF preflight upstream
}

func (p *gitlabProvider) ValidateRepository(ctx context.Context) (string, error) {
	resp, err := p.do(ctx, http.MethodGet, fmt.Sprintf("%s/api/v4/projects/%s", p.base, p.projectID()), nil)
	if err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", classifyGet(resp)
	}
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: "decode project response"}
	}
	return out.DefaultBranch, nil
}

func (p *gitlabProvider) CreateBranch(ctx context.Context, branchName, fromBranch string) error {
	u := fmt.Sprintf("%s/api/v4/projects/%s/repository/branches?branch=%s&ref=%s",
		p.base, p.projectID(), url.QueryEscape(branchName), url.QueryEscape(fromBranch))
	resp, err := p.do(ctx, http.MethodPost, u, nil)
	if err != nil {
		return &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated {
		return classifyCreate(resp)
	}
	return nil
}

func (p *gitlabProvider) BranchExists(ctx context.Context, branchName string) (bool, error) {
	u := fmt.Sprintf("%s/api/v4/projects/%s/repository/branches/%s",
		p.base, p.projectID(), url.PathEscape(branchName))
	resp, err := p.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	return classifyExists(resp)
}

func (p *gitlabProvider) CreatePullRequest(ctx context.Context, in PullRequestInput) (*PullRequest, error) {
	body, _ := json.Marshal(map[string]string{
		"source_branch": in.SourceBranch,
		"target_branch": in.TargetBranch,
		"title":         in.Title,
		"description":   in.Body,
	})
	resp, err := p.do(ctx, http.MethodPost, fmt.Sprintf("%s/api/v4/projects/%s/merge_requests", p.base, p.projectID()), body)
	if err != nil {
		return nil, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, classifyCreate(resp)
	}
	var out struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
		State  string `json:"state"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return &PullRequest{Number: out.IID, URL: out.WebURL, Status: out.State}, nil
}

// ---- Bitbucket ----

type bitbucketProvider struct {
	base, workspace, repo, token string
	client                       *http.Client
}

func (p *bitbucketProvider) do(ctx context.Context, method, u string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r) // #nosec G704 -- u passed the SSRF preflight and the client uses the guarded dialer (see shared/httpx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.client.Do(req) // #nosec G704 -- guarded dialer + SSRF preflight upstream
}

func (p *bitbucketProvider) ValidateRepository(ctx context.Context) (string, error) {
	resp, err := p.do(ctx, http.MethodGet, fmt.Sprintf("%s/2.0/repositories/%s/%s", p.base, p.workspace, p.repo), nil)
	if err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", classifyGet(resp)
	}
	var out struct {
		MainBranch struct {
			Name string `json:"name"`
		} `json:"mainbranch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: "decode repository response"}
	}
	return out.MainBranch.Name, nil
}

func (p *bitbucketProvider) CreateBranch(ctx context.Context, branchName, fromBranch string) error {
	body, _ := json.Marshal(map[string]any{
		"name":   branchName,
		"target": map[string]string{"hash": fromBranch},
	})
	resp, err := p.do(ctx, http.MethodPost, fmt.Sprintf("%s/2.0/repositories/%s/%s/refs/branches", p.base, p.workspace, p.repo), body)
	if err != nil {
		return &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated {
		return classifyCreate(resp)
	}
	return nil
}

func (p *bitbucketProvider) BranchExists(ctx context.Context, branchName string) (bool, error) {
	u := fmt.Sprintf("%s/2.0/repositories/%s/%s/refs/branches/%s",
		p.base, p.workspace, p.repo, url.PathEscape(branchName))
	resp, err := p.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	return classifyExists(resp)
}

func (p *bitbucketProvider) CreatePullRequest(ctx context.Context, in PullRequestInput) (*PullRequest, error) {
	body, _ := json.Marshal(map[string]any{
		"title":       in.Title,
		"description": in.Body,
		"source":      map[string]any{"branch": map[string]string{"name": in.SourceBranch}},
		"destination": map[string]any{"branch": map[string]string{"name": in.TargetBranch}},
	})
	resp, err := p.do(ctx, http.MethodPost, fmt.Sprintf("%s/2.0/repositories/%s/%s/pullrequests", p.base, p.workspace, p.repo), body)
	if err != nil {
		return nil, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, classifyCreate(resp)
	}
	var out struct {
		ID    int    `json:"id"`
		State string `json:"state"`
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return &PullRequest{Number: out.ID, URL: out.Links.HTML.Href, Status: out.State}, nil
}

// ---- Fake (offline) ----

type fakeProvider struct{ defaultBranch string }

func (f *fakeProvider) ValidateRepository(context.Context) (string, error) {
	if f.defaultBranch != "" {
		return f.defaultBranch, nil
	}
	return "main", nil
}

func (f *fakeProvider) CreateBranch(context.Context, string, string) error { return nil }

// The offline fake never reports a pre-existing branch; CreateBranch always
// succeeds, so the link-existing path is never reached for it.
func (f *fakeProvider) BranchExists(context.Context, string) (bool, error) { return false, nil }

func (f *fakeProvider) CreatePullRequest(_ context.Context, in PullRequestInput) (*PullRequest, error) {
	return &PullRequest{Number: 1, URL: "https://scm.fake/pull/1", Status: "OPEN"}, nil
}
