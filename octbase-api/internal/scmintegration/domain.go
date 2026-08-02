// Package scmintegration connects projects to source-control repositories and
// links individual tasks to their feature branches.  Repository connections
// store provider, URL, and default branch metadata; branch references record
// which branch was created for a given task.
package scmintegration

// RepositoryConnection domain object.
type RepositoryConnection struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	Provider      string `json:"provider"`
	DisplayName   string `json:"displayName"`
	RepositoryURL string `json:"repositoryUrl"`
	DefaultBranch string `json:"defaultBranch"`
	APIBaseURL    string `json:"apiBaseUrl,omitempty"`
	// AuthKind is "PAT" (default) or "OAUTH".
	AuthKind       string `json:"authKind,omitempty"`
	TokenExpiresAt string `json:"tokenExpiresAt,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	Version        int    `json:"version"`
	// OAuthAvailable is computed per request (not persisted): true when the
	// server has OAuth app credentials configured for this provider, so clients
	// can hide the "Connect with OAuth" action when it would fail.
	OAuthAvailable bool `json:"oauthAvailable"`
	// AccessToken/RefreshToken hold encrypted secrets at rest; never serialized.
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
}

const (
	authKindPAT   = "PAT"
	authKindOAuth = "OAUTH"
)

// PullRequest is the result of opening a pull/merge request.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

// PullRequestInput describes a pull/merge request to open.
type PullRequestInput struct {
	Title        string
	Body         string
	SourceBranch string
	TargetBranch string
}

// BranchReference domain object.
type BranchReference struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"taskId"`
	RepositoryID string  `json:"repositoryId"`
	BranchName   string  `json:"branchName"`
	BranchType   string  `json:"branchType"`
	PRStatus     *string `json:"prStatus,omitempty"`
	PRUrl        *string `json:"prUrl,omitempty"`
	PRNumber     *int    `json:"prNumber,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}
