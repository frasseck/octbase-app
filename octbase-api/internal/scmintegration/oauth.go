package scmintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// oauthConfig holds the per-provider OAuth app settings resolved from the
// environment. ClientID/ClientSecret come from
// OCTBASE_OAUTH_<PROVIDER>_CLIENT_ID / _CLIENT_SECRET; AuthURL/TokenURL default
// to each provider's public endpoints and may be overridden (for self-hosted
// instances) via OCTBASE_OAUTH_<PROVIDER>_AUTH_URL / _TOKEN_URL.
type oauthConfig struct {
	provider     string
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	scope        string
	redirectURI  string
}

// oauthDefaults lists the public authorize/token endpoints and default scopes.
var oauthDefaults = map[string]struct{ authURL, tokenURL, scope string }{
	ProviderGitHub:    {"https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "repo"},
	ProviderGitLab:    {"https://gitlab.com/oauth/authorize", "https://gitlab.com/oauth/token", "api"},
	ProviderBitbucket: {"https://bitbucket.org/site/oauth2/authorize", "https://bitbucket.org/site/oauth2/access_token", "repository pullrequest:write"},
}

// providerPathSegment maps a provider constant to its lowercase URL segment.
func providerPathSegment(provider string) string {
	switch provider {
	case ProviderGitHub:
		return "github"
	case ProviderGitLab:
		return "gitlab"
	case ProviderBitbucket:
		return "bitbucket"
	default:
		return strings.ToLower(provider)
	}
}

// loadOAuthConfig builds the OAuth config for a provider, or returns ok=false
// when the provider is not OAuth-capable or its app credentials are unset.
func loadOAuthConfig(provider string) (oauthConfig, bool) {
	def, known := oauthDefaults[provider]
	if !known {
		return oauthConfig{}, false
	}
	prefix := "OCTBASE_OAUTH_" + provider + "_"
	cfg := oauthConfig{
		provider:     provider,
		clientID:     os.Getenv(prefix + "CLIENT_ID"),
		clientSecret: os.Getenv(prefix + "CLIENT_SECRET"),
		authURL:      envOr(prefix+"AUTH_URL", def.authURL),
		tokenURL:     envOr(prefix+"TOKEN_URL", def.tokenURL),
		scope:        envOr(prefix+"SCOPE", def.scope),
	}
	redirectBase := strings.TrimRight(os.Getenv("OCTBASE_OAUTH_REDIRECT_BASE"), "/")
	cfg.redirectURI = redirectBase + "/api/v1/oauth/" + providerPathSegment(provider) + "/callback"
	if cfg.clientID == "" || cfg.clientSecret == "" {
		return cfg, false
	}
	return cfg, true
}

// oauthConfigured reports whether the server has usable OAuth app credentials
// for the provider.
func oauthConfigured(provider string) bool {
	_, ok := loadOAuthConfig(provider)
	return ok
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// authorizeURL builds the provider consent URL for the given state.
func (c oauthConfig) authorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", c.scope)
	q.Set("state", state)
	return c.authURL + "?" + q.Encode()
}

// oauthToken is the normalized token-endpoint response.
type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// exchangeCode swaps an authorization code for tokens.
func (c oauthConfig) exchangeCode(ctx context.Context, client *http.Client, code string) (*oauthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	return c.postToken(ctx, client, form)
}

// refresh exchanges a refresh token for a fresh access token (token rotation).
func (c oauthConfig) refresh(ctx context.Context, client *http.Client, refreshToken string) (*oauthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.postToken(ctx, client, form)
}

// postToken performs the form POST to the token endpoint. Bitbucket expects the
// client credentials via HTTP Basic auth; GitHub/GitLab accept them in the body.
func (c oauthConfig) postToken(ctx context.Context, client *http.Client, form url.Values) (*oauthToken, error) {
	if c.provider == ProviderBitbucket {
		// credentials sent via basic auth below
	} else {
		form.Set("client_id", c.clientID)
		form.Set("client_secret", c.clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.provider == ProviderBitbucket {
		req.SetBasicAuth(c.clientID, c.clientSecret)
	}
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: err.Error()}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, &ProviderError{Code: CodeAuthFailed, Status: http.StatusBadGateway, Message: fmt.Sprintf("OAuth token endpoint returned status %d", resp.StatusCode)}
	}
	var tok oauthToken
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, &ProviderError{Code: CodeProviderError, Status: http.StatusBadGateway, Message: "decode token response"}
	}
	if tok.AccessToken == "" {
		return nil, &ProviderError{Code: CodeAuthFailed, Status: http.StatusBadGateway, Message: "OAuth token endpoint returned no access token"}
	}
	return &tok, nil
}
