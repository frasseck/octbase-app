// Package testutil provides shared helpers for handler integration tests.
package testutil

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/octbase/octbase-api/internal/activity"
	"github.com/octbase/octbase-api/internal/admin"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/dashboard"
	"github.com/octbase/octbase-api/internal/docs"
	"github.com/octbase/octbase-api/internal/identityaccess"
	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/scmintegration"
	"github.com/octbase/octbase-api/internal/security/mfa"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/usermgmt"
	"github.com/octbase/octbase-api/internal/workmanagement"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Fixed user IDs used across tests.
const (
	// SuperAdminUserID is a seeded Super Admin for privilege tests.
	SuperAdminUserID = "00000000-0000-0000-0000-000000000010"

	// DemoUserID is a seeded ADMIN user — can create projects.
	DemoUserID = "00000000-0000-0000-0000-000000000001"

	// SecondUserID is a seeded USER — can be added to projects.
	SecondUserID = "00000000-0000-0000-0000-000000000002"

	// GuestUserID is a seeded GUEST — read-only.
	GuestUserID = "00000000-0000-0000-0000-000000000003"

	// DisabledUserID is a seeded disabled account.
	DisabledUserID = "00000000-0000-0000-0000-000000000004"

	TestJWTSecret = "test-secret-do-not-use-in-production"

	// TestUserPassword is the plaintext password seeded (as a bcrypt hash) on the
	// SuperAdmin/Demo/Second accounts, for flows that require password re-auth.
	TestUserPassword = "test-user-password-123"
)

// TokenForUser returns a short-lived JWT for the given userID.
func TokenForUser(userID string) string {
	token, _ := auth.IssueAccessToken(userID, TestJWTSecret, time.Hour)
	return token
}

// NewTestDB opens an isolated PostgreSQL schema with the full schema applied.
// Requires TEST_DATABASE_URL env var; skips the test if not set.
func NewTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
		return nil
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)

	schemaID := strings.ReplaceAll(shared.NewUUID(), "-", "")
	schema := "tbtest_" + schemaID
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		_ = db.Close()
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		_ = db.Close()
		t.Fatalf("set search_path: %v", err)
	}

	for _, sql := range readMigrations(t) {
		if _, err := db.Exec(sql); err != nil {
			_ = db.Close()
			t.Fatalf("run migration: %v", err)
		}
	}

	now := shared.Now()

	// A known password (hash) on the ADMIN/USER seed accounts so tests can drive
	// flows that require password re-authentication (e.g. MFA enrollment).
	pwHash, err := bcrypt.GenerateFromPassword([]byte(TestUserPassword), 12)
	if err != nil {
		_ = db.Close()
		t.Fatalf("hash test password: %v", err)
	}

	// Super Admin
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'superadmin@test.dev','Super Admin',$4,'SUPER_ADMIN','active',true,$2,$3)`,
		SuperAdminUserID, now, now, string(pwHash),
	); err != nil {
		_ = db.Close()
		t.Fatalf("seed super admin: %v", err)
	}

	// Demo user — ADMIN (can create projects)
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'demo@octbase.dev','Demo User',$4,'ADMIN','active',true,$2,$3)`,
		DemoUserID, now, now, string(pwHash),
	); err != nil {
		_ = db.Close()
		t.Fatalf("seed demo user: %v", err)
	}

	// Second user — USER
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'second@octbase.dev','Second User',$4,'USER','active',true,$2,$3)`,
		SecondUserID, now, now, string(pwHash),
	); err != nil {
		_ = db.Close()
		t.Fatalf("seed second user: %v", err)
	}

	// Guest user
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'guest@octbase.dev','Guest User','GUEST','active',true,$2,$3)`,
		GuestUserID, now, now,
	); err != nil {
		_ = db.Close()
		t.Fatalf("seed guest user: %v", err)
	}

	// Disabled user — ADMIN but disabled
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'disabled@octbase.dev','Disabled User','$2a$12$disabled_hash_placeholder','ADMIN','disabled',false,$2,$3)`,
		DisabledUserID, now, now,
	); err != nil {
		_ = db.Close()
		t.Fatalf("seed disabled user: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		_ = db.Close()
	})
	return db
}

// RouterOption customizes NewTestRouter/NewTestServer for feature-gate tests.
type RouterOption func(*routerConfig)

type routerConfig struct {
	jiraCSVImportEnabled bool
	maxUsers             int
	userStorageQuota     int64
	requireMFA           string
	mailer               mailer.Mailer
	boardPublisher       workmanagement.BoardEventPublisher
}

// WithMailer wires a custom mailer into the auth handler, so tests can capture
// outbound email (e.g. the password-reset link) instead of the stdout dev-mode
// fallback.
func WithMailer(m mailer.Mailer) RouterOption {
	return func(c *routerConfig) { c.mailer = m }
}

// WithRequireMFA wires the router like a deployment with OCTBASE_REQUIRE_MFA=mode
// ("admins" or "all"), for MFA-enforcement tests. The default ("") leaves
// enforcement off so unrelated tests log in without an MFA setup step.
func WithRequireMFA(mode string) RouterOption {
	return func(c *routerConfig) { c.requireMFA = mode }
}

// WithJiraCSVImportDisabled wires the router like a deployment without the
// Jira import add-on (TEAM edition, or BUSINESS without
// OCTBASE_OPTION_JIRA_IMPORT=true): the Jira CSV import route answers
// 403 FEATURE_DISABLED.
func WithJiraCSVImportDisabled() RouterOption {
	return func(c *routerConfig) { c.jiraCSVImportEnabled = false }
}

// WithUserLimit wires the router like a deployment with OCTBASE_MAX_USERS=n,
// for seat-limit tests. The default (0) leaves the limit off so the seeded
// fixture users never trip it in unrelated tests.
func WithUserLimit(n int) RouterOption {
	return func(c *routerConfig) { c.maxUsers = n }
}

// WithUserStorageQuota wires the router like a deployment with a per-user
// attachment storage quota of maxBytes (OCTBASE_MAX_USER_STORAGE_MB), for
// quota tests. The default (0) leaves the quota off.
func WithUserStorageQuota(maxBytes int64) RouterOption {
	return func(c *routerConfig) { c.userStorageQuota = maxBytes }
}

// WithBoardEventPublisher injects a real-time board-event publisher into the
// workmanagement handler so tests can capture the project-scoped events emitted
// on mutations (see workmanagement.BoardEventPublisher). When unset the router
// wires the router's own sse.Hub, matching production (main.go).
func WithBoardEventPublisher(pub workmanagement.BoardEventPublisher) RouterOption {
	return func(c *routerConfig) { c.boardPublisher = pub }
}

// NewTestRouter builds the full chi router wired with all routes against db.
// Exposed (alongside NewTestServer) so contract tests can walk the route tree.
func NewTestRouter(t *testing.T, db *sql.DB, opts ...RouterOption) chi.Router {
	t.Helper()

	cfg := routerConfig{jiraCSVImportEnabled: true}
	for _, opt := range opts {
		opt(&cfg)
	}

	auditRepo := auditlog.NewRepo(db)
	activityRepo := activity.NewRepo(db)
	userRepo := identityaccess.NewUserRepo(db)
	membershipRepo := identityaccess.NewMembershipRepo(db)
	projectRepo := workmanagement.NewProjectRepo(db)
	taskRepo := workmanagement.NewTaskRepo(db)
	commentRepo := workmanagement.NewTaskCommentRepo(db)
	linkRepo := workmanagement.NewTaskLinkRepo(db)
	attachmentRepo := workmanagement.NewTaskAttachmentRepo(db)
	relationRepo := workmanagement.NewTaskRelationRepo(db)
	boardRepo := workmanagement.NewBoardRepo(db)
	columnRepo := workmanagement.NewBoardColumnRepo(db)
	extColumnRepo := workmanagement.NewBoardExternalColumnRepo(db)
	releaseRepo := workmanagement.NewReleaseRepo(db)
	sprintRepo := workmanagement.NewSprintRepo(db)
	categoryRepo := workmanagement.NewTaskCategoryRepo(db)
	templateRepo := workmanagement.NewTaskTemplateRepo(db)
	pageRepo := docs.NewPageRepo(db)
	revisionRepo := docs.NewPageRevisionRepo(db)
	refRepo := docs.NewPageReferenceRepo(db)
	repoConnRepo := scmintegration.NewRepositoryConnectionRepo(db)
	branchRepo := scmintegration.NewBranchReferenceRepo(db)
	notifRepo := notifications.NewRepo(db)
	usermgmtRepo := usermgmt.NewRepo(db)

	hub := sse.NewHub()
	go hub.Run()

	wmSvc := workmanagement.NewService(db, taskRepo, commentRepo, linkRepo, attachmentRepo, relationRepo, releaseRepo, boardRepo, columnRepo, sprintRepo, templateRepo)
	notifSvc := notifications.NewService(db, notifRepo, hub, nil)

	iaHandler := identityaccess.NewHandler(db, userRepo, membershipRepo, auditRepo)
	wmHandler := workmanagement.NewHandler(
		db, projectRepo, taskRepo, commentRepo, linkRepo, attachmentRepo,
		relationRepo, boardRepo, columnRepo, extColumnRepo, releaseRepo, sprintRepo, categoryRepo,
		templateRepo, wmSvc, activityRepo, notifSvc, pageRepo, auditRepo,
	)
	// Attachment storage in a per-test temp dir so upload/download tests work.
	if store, err := workmanagement.NewAttachmentStorage(t.TempDir()); err == nil {
		wmHandler.WithAttachmentStorage(store, 25<<20)
	}
	wmHandler.WithUserStorageQuota(cfg.userStorageQuota)
	// Broadcast board/task changes over SSE, mirroring main.go. Tests may inject
	// a capturing publisher via WithBoardEventPublisher; otherwise use the hub.
	if cfg.boardPublisher != nil {
		wmHandler.WithEventPublisher(cfg.boardPublisher)
	} else {
		wmHandler.WithEventPublisher(hub)
	}
	wmHandler.WithPagePorter(docs.NewPorter(pageRepo, revisionRepo, refRepo))
	docHandler := docs.NewHandler(db, pageRepo, revisionRepo, refRepo, activityRepo)
	scmHandler := scmintegration.NewHandler(db, repoConnRepo, branchRepo, activityRepo)
	actHandler := activity.NewHandler(db, activityRepo)
	adminHandler := admin.NewHandler(db, auditRepo)
	notifHandler := notifications.NewHandler(db, notifRepo)
	auditHandler := auditlog.NewHandler(auditRepo)
	usermgmtHandler := usermgmt.NewHandler(db, usermgmtRepo, auditRepo, nil).WithUserLimit(cfg.maxUsers)
	dashboardHandler := dashboard.NewHandler(dashboard.NewRepo(db))
	mfaRepo := mfa.NewRepo(db)
	mfaHandler := mfa.NewHandler(db, mfaRepo, auditRepo)

	provider := auth.NewEmailProvider(db, TestJWTSecret)
	tokenRepo := auth.NewRefreshTokenRepo(db)
	invRepo := auth.NewInvitationRepo(db)
	authHandler := auth.NewHandler(db, provider, tokenRepo, invRepo, auditRepo, cfg.mailer, TestJWTSecret).
		WithMFA(mfaRepo).
		WithUserLimit(cfg.maxUsers).
		WithRequireMFA(cfg.requireMFA)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(shared.CORSMiddleware)

	r.Get("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterPublicRoutes(r)
	scmHandler.RegisterPublicRoutes(r)

	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(provider))
		r.Use(shared.LoadUserGlobalRole(db))
		r.Use(shared.RequireJSON)

		authHandler.RegisterRoutes(r)

		r.Group(func(r chi.Router) {
			r.Use(admin.RequireAdmin())
			adminHandler.RegisterRoutes(r)
		})

		usermgmtHandler.RegisterRoutes(r)
		auditHandler.RegisterRoutes(r)
		iaHandler.RegisterRoutes(r)
		wmHandler.RegisterRoutes(r)
		docHandler.RegisterRoutes(r)
		scmHandler.RegisterRoutes(r)
		actHandler.RegisterRoutes(r)
		notifHandler.RegisterRoutes(r)
		dashboardHandler.RegisterRoutes(r)
		mfaHandler.RegisterRoutes(r)
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.EnrollmentOrAccessMiddleware(provider, TestJWTSecret))
		r.Use(shared.RequireJSON)
		mfaHandler.RegisterEnrollmentRoutes(r)
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(provider))
		r.Use(shared.LoadUserGlobalRole(db))
		wmHandler.RegisterProjectTransferRoutes(r)
		wmHandler.RegisterCSVRoutes(r, cfg.jiraCSVImportEnabled)
		wmHandler.RegisterFileRoutes(r)
		iaHandler.RegisterFileRoutes(r)
	})

	return r
}

// NewTestServer creates a full httptest.Server wired with all routes against db.
func NewTestServer(t *testing.T, db *sql.DB, opts ...RouterOption) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewTestRouter(t, db, opts...))
	t.Cleanup(srv.Close)
	return srv
}

// Do performs an HTTP request against srv. userID is used to generate a JWT
// Bearer token if non-empty.
func Do(t *testing.T, srv *httptest.Server, method, path string, body any, userID string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		token := TokenForUser(userID)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// DecodeJSON decodes resp.Body into v, closing the body afterwards.
func DecodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// AssertStatus fails the test if resp.StatusCode != want.
func AssertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("expected status %d, got %d; body: %s", want, resp.StatusCode, body)
	}
}

// MustCreateProject creates a project as DemoUserID (ADMIN) and returns its id.
func MustCreateProject(t *testing.T, srv *httptest.Server, name string) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": name, "visibility": "PUBLIC"}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var p map[string]any
	DecodeJSON(t, resp, &p)
	return p["id"].(string)
}

// MustCreateTask creates a task and returns its id.
func MustCreateTask(t *testing.T, srv *httptest.Server, projectID, title string) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", projectID),
		map[string]string{"title": title}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var task map[string]any
	DecodeJSON(t, resp, &task)
	return task["id"].(string)
}

// MustCreateBoard creates a default board and returns its id.
func MustCreateBoard(t *testing.T, srv *httptest.Server, projectID string) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/boards", projectID),
		map[string]any{"name": "Main Board", "isDefault": true}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var b map[string]any
	DecodeJSON(t, resp, &b)
	return b["id"].(string)
}

// MustCreateNonDefaultBoard creates a second, non-default board and returns its
// id. Use it when a test needs a board that the project's default-board lookup
// must NOT find — MustCreateBoard marks every board it makes as the default, so
// two calls to it leave which board is "the" default up to the LIMIT 1.
func MustCreateNonDefaultBoard(t *testing.T, srv *httptest.Server, projectID, name string) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/boards", projectID),
		map[string]any{"name": name, "isDefault": false}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var b map[string]any
	DecodeJSON(t, resp, &b)
	return b["id"].(string)
}

// MustAddColumn adds a board column and returns its id.
func MustAddColumn(t *testing.T, srv *httptest.Server, boardID, name, status string, pos int) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/boards/%s/columns", boardID),
		map[string]any{"name": name, "status": status, "position": pos}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var c map[string]any
	DecodeJSON(t, resp, &c)
	return c["id"].(string)
}

// MustCreateRelease creates a release and returns its id.
func MustCreateRelease(t *testing.T, srv *httptest.Server, projectID, name string) string {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/releases", projectID),
		map[string]string{"name": name, "goal": "Test goal"}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	var m map[string]any
	DecodeJSON(t, resp, &m)
	return m["id"].(string)
}

// MustAddMember adds a user to a project with the given role.
func MustAddMember(t *testing.T, srv *httptest.Server, projectID, userID, role string) {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", projectID),
		map[string]string{"userId": userID, "role": role}, DemoUserID)
	AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}

// MustAddMemberAs adds a member using the specified actor.
func MustAddMemberAs(t *testing.T, srv *httptest.Server, projectID, userID, role, actorID string) {
	t.Helper()
	resp := Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", projectID),
		map[string]string{"userId": userID, "role": role}, actorID)
	AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}

// NewActivityRepo returns an activity.Repo for test helper use.
func NewActivityRepo(db *sql.DB) *activity.Repo {
	return activity.NewRepo(db)
}

// SetUserGlobalRole updates a test user's global role directly in the DB.
func SetUserGlobalRole(t *testing.T, db *sql.DB, userID, role string) {
	t.Helper()
	if !rbac.IsValidGlobalRole(role) {
		t.Fatalf("invalid global role: %s", role)
	}
	if _, err := db.Exec(`UPDATE users SET global_role=$1 WHERE id=$2`, role, userID); err != nil {
		t.Fatalf("set global role: %v", err)
	}
}

// readMigrations reads all `*.up.sql` migration files from the migrations
// directory, sorted by their numeric filename prefix. Reading the directory
// (rather than a hardcoded list) keeps the test schema in lock-step with new
// migrations automatically.
func readMigrations(t *testing.T) []string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // zero-padded numeric prefixes sort lexically in order
	var sqls []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(base, f)) // #nosec G304 -- test-only helper reading fixtures from a fixed list
		if err != nil {
			t.Fatalf("read migration %s: %v", f, err)
		}
		sqls = append(sqls, string(b))
	}
	return sqls
}
