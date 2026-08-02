package scmintegration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newConn(provider, repoURL, base string) *RepositoryConnection {
	return &RepositoryConnection{Provider: provider, RepositoryURL: repoURL, APIBaseURL: base, DefaultBranch: "main"}
}

func TestSplitRepoURL(t *testing.T) {
	cases := []struct{ in, owner, repo, full string }{
		{"https://github.com/org/repo", "org", "repo", "org/repo"},
		{"https://github.com/org/repo.git", "org", "repo", "org/repo"},
		{"git@gitlab.com:group/sub/repo.git", "group", "repo", "group/sub/repo"},
		{"https://bitbucket.org/team/svc/", "team", "svc", "team/svc"},
	}
	for _, c := range cases {
		o, r, f, err := splitRepoURL(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if o != c.owner || r != c.repo || f != c.full {
			t.Errorf("%s => (%s,%s,%s), want (%s,%s,%s)", c.in, o, r, f, c.owner, c.repo, c.full)
		}
	}
	if _, _, _, err := splitRepoURL("not-a-url"); err == nil {
		t.Errorf("expected error for path-less URL")
	}
}

func TestNewProvider_Unsupported(t *testing.T) {
	if _, err := NewProvider(newConn("SVN", "https://x/y/z", ""), "", nil); err == nil {
		t.Errorf("expected error for unsupported provider")
	}
}

func TestGitHubProvider_Validate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "tok", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	db, err := p.ValidateRepository(context.Background())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if db != "trunk" {
		t.Errorf("default branch = %q, want trunk", db)
	}
}

func TestGitHubProvider_ValidateNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "tok", srv.Client())
	_, err := p.ValidateRepository(context.Background())
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeRepoNotFound {
		t.Fatalf("err = %v, want SCM_REPO_NOT_FOUND", err)
	}
}

func TestGitHubProvider_CreateBranch(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/git/ref/heads/main":
			_, _ = w.Write([]byte(`{"object":{"sha":"abc123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/git/refs":
			posted = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "tok", srv.Client())
	if err := p.CreateBranch(context.Background(), "feature/x", "main"); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	if !posted {
		t.Errorf("expected POST to create ref")
	}
}

func TestBranchExists(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		repoURL  string
		path     string // expected lookup path for branch "feature/x"
	}{
		{"github", ProviderGitHub, "https://github.com/org/repo", "/repos/org/repo/git/ref/heads/feature/x"},
		{"gitlab", ProviderGitLab, "https://gitlab.com/org/repo", "/api/v4/projects/org/repo/repository/branches/feature/x"},
		{"bitbucket", ProviderBitbucket, "https://bitbucket.org/org/repo", "/2.0/repositories/org/repo/refs/branches/feature/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 200 => exists.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.path {
					t.Errorf("path = %s, want %s", r.URL.Path, c.path)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			p, _ := NewProvider(newConn(c.provider, c.repoURL, srv.URL), "tok", srv.Client())
			ok, err := p.BranchExists(context.Background(), "feature/x")
			if err != nil || !ok {
				t.Errorf("exists: ok=%v err=%v, want true/nil", ok, err)
			}
			srv.Close()

			// 404 => absent, no error.
			srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			p, _ = NewProvider(newConn(c.provider, c.repoURL, srv404.URL), "tok", srv404.Client())
			ok, err = p.BranchExists(context.Background(), "feature/x")
			if err != nil || ok {
				t.Errorf("absent: ok=%v err=%v, want false/nil", ok, err)
			}
			srv404.Close()

			// 401 => provider error.
			srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))
			p, _ = NewProvider(newConn(c.provider, c.repoURL, srv401.URL), "tok", srv401.Client())
			if _, err = p.BranchExists(context.Background(), "feature/x"); err == nil {
				t.Errorf("expected error on 401")
			}
			srv401.Close()
		})
	}
}

func TestFakeProvider_BranchExists(t *testing.T) {
	p, _ := NewProvider(newConn(ProviderFake, "https://x/y/z", ""), "", nil)
	if ok, err := p.BranchExists(context.Background(), "anything"); ok || err != nil {
		t.Errorf("fake BranchExists = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestGitHubProvider_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "bad", srv.Client())
	err := p.CreateBranch(context.Background(), "f", "main")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeAuthFailed {
		t.Fatalf("err = %v, want SCM_AUTH_FAILED", err)
	}
}

func TestGitHubProvider_AuthFailedIncludesProviderDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "tok", srv.Client())
	err := p.CreateBranch(context.Background(), "f", "main")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeAuthFailed {
		t.Fatalf("err = %v, want SCM_AUTH_FAILED", err)
	}
	if !strings.Contains(pe.Message, "Resource not accessible by personal access token") {
		t.Errorf("message %q does not include provider detail", pe.Message)
	}
}

func TestProviderMessage(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"message":"Bad credentials"}`, "Bad credentials"},                               // GitHub/GitLab
		{`{"error":"invalid_token","error_description":"token revoked"}`, "token revoked"}, // OAuth-style
		{`{"error":{"message":"branch exists"}}`, "branch exists"},                         // Bitbucket
		{`{"error":"insufficient_scope"}`, "insufficient_scope"},                           // GitLab string error
		{``, ""},
		{`not json`, ""},
		{`{"other":"x"}`, ""},
	}
	for _, c := range cases {
		if got := providerMessage([]byte(c.in)); got != c.want {
			t.Errorf("providerMessage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitLabProvider_CreateBranchExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.EscapedPath(), "/api/v4/projects/group%2Frepo/repository/branches") {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "glpat" {
			t.Errorf("token header = %q", got)
		}
		w.WriteHeader(http.StatusBadRequest) // GitLab: branch already exists
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitLab, "https://gitlab.com/group/repo", srv.URL), "glpat", srv.Client())
	err := p.CreateBranch(context.Background(), "feature/x", "main")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeBranchExists {
		t.Fatalf("err = %v, want SCM_BRANCH_EXISTS", err)
	}
}

func TestGitLabProvider_Validate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Frepo" {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitLab, "https://gitlab.com/group/repo", srv.URL), "glpat", srv.Client())
	db, err := p.ValidateRepository(context.Background())
	if err != nil || db != "main" {
		t.Errorf("gitlab validate = %q, %v", db, err)
	}
}

func TestBitbucketProvider_CreateBranch(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/2.0/repositories/team/svc/refs/branches" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		posted = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderBitbucket, "https://bitbucket.org/team/svc", srv.URL), "tok", srv.Client())
	if err := p.CreateBranch(context.Background(), "feature/x", "main"); err != nil {
		t.Fatalf("bitbucket create branch: %v", err)
	}
	if !posted {
		t.Errorf("expected POST to create branch")
	}
}

func TestBitbucketProvider_Validate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2.0/repositories/team/svc" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"mainbranch":{"name":"develop"}}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderBitbucket, "https://bitbucket.org/team/svc", srv.URL), "tok", srv.Client())
	db, err := p.ValidateRepository(context.Background())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if db != "develop" {
		t.Errorf("default branch = %q, want develop", db)
	}
}

func TestGitHubProvider_CreatePullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/org/repo/pulls" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/org/repo/pull/7","state":"open"}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", srv.URL), "tok", srv.Client())
	pr, err := p.CreatePullRequest(context.Background(), PullRequestInput{Title: "t", SourceBranch: "feature/x", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create PR: %v", err)
	}
	if pr.Number != 7 || pr.Status != "open" || pr.URL == "" {
		t.Errorf("pr = %+v", pr)
	}
}

func TestGitLabProvider_CreatePullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.EscapedPath(), "/merge_requests") {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iid":3,"web_url":"https://gitlab.com/group/repo/-/merge_requests/3","state":"opened"}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderGitLab, "https://gitlab.com/group/repo", srv.URL), "glpat", srv.Client())
	pr, err := p.CreatePullRequest(context.Background(), PullRequestInput{Title: "t", SourceBranch: "feature/x", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create MR: %v", err)
	}
	if pr.Number != 3 || pr.Status != "opened" {
		t.Errorf("pr = %+v", pr)
	}
}

func TestBitbucketProvider_CreatePullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2.0/repositories/team/svc/pullrequests" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12,"state":"OPEN","links":{"html":{"href":"https://bitbucket.org/team/svc/pull-requests/12"}}}`))
	}))
	defer srv.Close()
	p, _ := NewProvider(newConn(ProviderBitbucket, "https://bitbucket.org/team/svc", srv.URL), "tok", srv.Client())
	pr, err := p.CreatePullRequest(context.Background(), PullRequestInput{Title: "t", SourceBranch: "feature/x", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("create PR: %v", err)
	}
	if pr.Number != 12 || pr.URL == "" {
		t.Errorf("pr = %+v", pr)
	}
}

func TestGitLabProvider_OAuthUsesBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauthtok" {
			t.Errorf("auth = %q, want Bearer", r.Header.Get("Authorization"))
		}
		if r.Header.Get("PRIVATE-TOKEN") != "" {
			t.Errorf("PRIVATE-TOKEN should not be set for OAuth")
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer srv.Close()
	conn := newConn(ProviderGitLab, "https://gitlab.com/group/repo", srv.URL)
	conn.AuthKind = "OAUTH"
	p, _ := NewProvider(conn, "oauthtok", srv.Client())
	if _, err := p.ValidateRepository(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestFakeProvider(t *testing.T) {
	p, err := NewProvider(newConn(ProviderFake, "https://x/y/z", ""), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := p.ValidateRepository(context.Background())
	if err != nil || db != "main" {
		t.Errorf("fake validate = %q, %v", db, err)
	}
	if err := p.CreateBranch(context.Background(), "f", "main"); err != nil {
		t.Errorf("fake create branch: %v", err)
	}
	pr, err := p.CreatePullRequest(context.Background(), PullRequestInput{Title: "t", SourceBranch: "f", TargetBranch: "main"})
	if err != nil || pr == nil || pr.URL == "" {
		t.Errorf("fake create PR = %+v, %v", pr, err)
	}
}
