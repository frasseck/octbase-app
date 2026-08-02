// Package main is the entry point for the Octbase API server.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/octbase/octbase-api/internal/activity"
	"github.com/octbase/octbase-api/internal/admin"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/bootstrap"
	"github.com/octbase/octbase-api/internal/dashboard"
	"github.com/octbase/octbase-api/internal/docs"
	"github.com/octbase/octbase-api/internal/identityaccess"
	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/retention"
	"github.com/octbase/octbase-api/internal/scmintegration"
	"github.com/octbase/octbase-api/internal/security/mfa"
	"github.com/octbase/octbase-api/internal/seed"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/usermgmt"
	"github.com/octbase/octbase-api/internal/webhooks"
	"github.com/octbase/octbase-api/internal/workmanagement"
)

const (
	migrationsPath = "migrations"
	// defaultAppVersion ships as the app version unless overridden by
	// OCTBASE_APP_VERSION. Real version numbers are always stamped per
	// deployment via the env var; a build without one is unstamped and
	// presents itself as "beta" (the frontend tag reads "octbase beta").
	defaultAppVersion = "beta"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "octbase_http_requests_total",
		Help: "Total HTTP requests by method, path, and status.",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "octbase_http_request_duration_seconds",
		Help:    "HTTP request latency by method and path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

func main() {
	// Configure log level from environment.
	level := slog.LevelInfo
	switch os.Getenv("OCTBASE_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	dsn := os.Getenv("OCTBASE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" // #nosec G101 -- dev-only fallback matching the local compose defaults; production sets OCTBASE_DATABASE_URL
	}

	// Migrations need DDL; serving traffic needs only DML. Deployments that
	// provision a least-privilege runtime role point OCTBASE_DATABASE_URL at the
	// restricted role and OCTBASE_MIGRATE_DATABASE_URL at the schema owner, so an
	// app compromise or SQL injection cannot reshape or drop the schema. Unset
	// (the bundled single-container Postgres) keeps the legacy single-URL
	// behaviour: one role does both. See docs/operations.md ("Least-privilege
	// runtime database role") and scripts/db-least-privilege.sql.
	migrateDSN := migrationDSN(dsn, os.Getenv("OCTBASE_MIGRATE_DATABASE_URL"))

	// Server-side statement_timeout backstop for the request pool: a single
	// query that blocks forever (lock wait, runaway scan) is cancelled by
	// PostgreSQL instead of pinning its pooled connection, which is what slowly
	// exhausts the pool and deadlocks the whole API. Default 30s; set to 0 to
	// disable. Deliberately NOT applied to the migration connection below.
	statementTimeout := 30 * time.Second
	if v := os.Getenv("OCTBASE_DB_STATEMENT_TIMEOUT"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d >= 0 {
			statementTimeout = d
		} else {
			slog.Warn("invalid OCTBASE_DB_STATEMENT_TIMEOUT, using default", "value", v, "default", statementTimeout)
		}
	}

	slog.Info("opening database", "statementTimeout", statementTimeout)
	db, err := shared.OpenDB(dsn, shared.WithStatementTimeout(statementTimeout))
	if err != nil {
		slog.Error("failed to open db", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Pool size is configurable so many API instances can share one Postgres
	// (or a pgBouncer) without exhausting max_connections. See
	// docs/hosting-concept.md. Defaults preserve the original single-stack sizing.
	maxOpenConns := 25
	if v := os.Getenv("OCTBASE_DB_MAX_OPEN_CONNS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			maxOpenConns = n
		}
	}
	maxIdleConns := 5
	if v := os.Getenv("OCTBASE_DB_MAX_IDLE_CONNS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			maxIdleConns = n
		}
	}
	if maxIdleConns > maxOpenConns {
		maxIdleConns = maxOpenConns
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	for i := range 10 {
		if err = db.Ping(); err == nil {
			break
		}
		wait := time.Duration(i+1) * time.Second
		slog.Info("database not ready, retrying", "attempt", i+1, "wait", wait)
		time.Sleep(wait)
	}
	if err != nil {
		slog.Error("database unreachable after retries", "error", err)
		os.Exit(1)
	}

	if err := runMigrations(db, dsn, migrateDSN); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Expected migration version is derived from the migration files on disk, so
	// the health check tracks new migrations automatically (no hand-bumped const).
	expectedMigrationVersion, err := shared.LatestMigrationVersion(migrationsPath)
	if err != nil {
		slog.Error("failed to determine expected migration version", "error", err)
		os.Exit(1)
	}

	demoMode := os.Getenv("OCTBASE_DEMO_MODE") == "true"
	if demoMode {
		if err := seed.Run(db); err != nil {
			slog.Warn("seed error", "error", err)
		}
	}

	// Create the installation's first administrator when configured and the
	// users table is still empty (see internal/bootstrap). Fatal on error: the
	// alternative is an instance that passes its health check but that nobody
	// can log into, which is worse than a loud failure at provisioning time.
	// Not run in demo mode — the seed above already put accounts in the table,
	// so the bootstrap would no-op anyway; skipping it keeps the two paths from
	// looking like they interact.
	if !demoMode {
		if err := bootstrap.Run(db, bootstrap.ConfigFromEnv()); err != nil {
			slog.Error("admin bootstrap failed", "error", err)
			os.Exit(1)
		}
	}

	// GDPR storage limitation: purge aged audit/activity rows, expired refresh
	// tokens and stale invitations at startup and then daily (see internal/retention).
	// Cancelled on shutdown below so the purge loop doesn't outlive the process.
	retentionCtx, cancelRetention := context.WithCancel(context.Background())
	defer cancelRetention()
	retention.Start(retentionCtx, db, retention.ConfigFromEnv())

	// Optional-feature flags exposed to the SPA via GET /api/v1/config. These are
	// deployment-level toggles read once here at the composition root (the env is
	// the only adapter), not domain state. Default-on: a feature is enabled unless
	// the operator explicitly opts out with "false", so a missing key ships the
	// full app. See octbase-frontend/js/README.md ("Configuring views & features").
	featureTaskView := os.Getenv("OCTBASE_FEATURE_TASKVIEW") != "false"

	// Deployment edition (OCTBASE_EDITION: TEAM, BUSINESS or ENTERPRISE).
	// Editions gate optional product surface per client; today that is Jira CSV
	// import. Like the feature flags above, a missing value ships the full app
	// (ENTERPRISE).
	edition := editionFromEnv()
	featureJiraCSVImport := jiraImportEnabled(edition)

	appVersion := os.Getenv("OCTBASE_APP_VERSION")
	if appVersion == "" {
		appVersion = defaultAppVersion
	}

	// Installation-wide account limit (OCTBASE_MAX_USERS): how many user
	// accounts the installation may hold, including the admin. Defaults to 5
	// (the smallest bookable package); 0 disables the limit. Enforced on user
	// creation and on invitation create/accept.
	maxUsers := 5
	if v := os.Getenv("OCTBASE_MAX_USERS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			maxUsers = n
		} else {
			slog.Warn("ignoring invalid OCTBASE_MAX_USERS; using default", "value", v, "default", maxUsers)
		}
	}

	// Per-user storage quota (OCTBASE_MAX_USER_STORAGE_MB): total size of all
	// files a single user may have uploaded. Defaults to 512 MB (0.5 GB per
	// user); 0 disables the quota.
	maxUserStorageMB := 512
	if v := os.Getenv("OCTBASE_MAX_USER_STORAGE_MB"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			maxUserStorageMB = n
		} else {
			slog.Warn("ignoring invalid OCTBASE_MAX_USER_STORAGE_MB; using default", "value", v, "default", maxUserStorageMB)
		}
	}

	// Auth setup. A weak/absent signing secret lets anyone forge tokens, so we
	// refuse to start in non-demo deployments rather than degrade to a known key.
	jwtSecret := os.Getenv("OCTBASE_JWT_SECRET")
	if len(jwtSecret) < 32 {
		if !demoMode {
			slog.Error("OCTBASE_JWT_SECRET must be set to at least 32 bytes in production (generate with: openssl rand -base64 32)")
			os.Exit(1)
		}
		jwtSecret = "dev-secret-change-in-production"
		slog.Warn("OCTBASE_JWT_SECRET not set or too short; using dev default (demo mode only)")
	}

	// Fail-closed hardening for production (non-demo) deployments. Each of these
	// silently degraded before: a forgotten flag shipped an insecure default.
	if !demoMode {
		// A refresh cookie without Secure can leak over any plaintext request.
		if os.Getenv("OCTBASE_SECURE_COOKIES") != "true" {
			slog.Error("OCTBASE_SECURE_COOKIES must be 'true' in production (set OCTBASE_DEMO_MODE=true only for local/demo use)")
			os.Exit(1)
		}
		// Reset/invitation links embed OCTBASE_APP_URL; the localhost fallback
		// would email dead links from a real deployment.
		if os.Getenv("OCTBASE_APP_URL") == "" {
			slog.Error("OCTBASE_APP_URL must be set to the real frontend origin in production (used for reset/invitation links)")
			os.Exit(1)
		}
		// Cleartext DB traffic: warn when a non-local database is reached with TLS
		// disabled. Not fatal (the bundled Postgres on the private compose network
		// is a legitimate sslmode=disable case), but must be visible.
		if dsn := os.Getenv("OCTBASE_DATABASE_URL"); strings.Contains(dsn, "sslmode=disable") &&
			!strings.Contains(dsn, "@localhost") && !strings.Contains(dsn, "@127.0.0.1") && !strings.Contains(dsn, "@postgres") {
			slog.Warn("OCTBASE_DATABASE_URL uses sslmode=disable against a non-local host; use sslmode=require/verify-full for external databases")
		}
	}

	// Shared audit log repo.
	auditRepo := auditlog.NewRepo(db)

	// Outbound mail runs off the request goroutine. SMTP is a multi-round-trip
	// conversation with a host we do not control, and every task
	// create/edit/status-change/comment can trigger a notification mail — inline
	// sends made those writes as slow as the relay, and a blackholed relay stalled
	// them past the server's WriteTimeout. One bounded queue with a couple of
	// workers serves every mail-sending handler (auth, notifications, user
	// management); it is drained and shut down below, after the HTTP server stops.
	mailQueue := mailer.NewQueue(mailer.New(), mailer.DefaultWorkers, mailer.DefaultQueueSize)

	emailProvider := auth.NewEmailProvider(db, jwtSecret)
	tokenRepo := auth.NewRefreshTokenRepo(db)
	invitationRepo := auth.NewInvitationRepo(db)
	authHandler := auth.NewHandler(db, emailProvider, tokenRepo, invitationRepo, auditRepo, mailQueue, jwtSecret).WithUserLimit(maxUsers)

	// SSE hub.
	sseHub := sse.NewHub()
	go sseHub.Run()

	// Repos.
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
	dashboardRepo := dashboard.NewRepo(db)
	mfaRepo := mfa.NewRepo(db)
	authHandler.WithMFA(mfaRepo)
	// MFA enforcement (OCTBASE_REQUIRE_MFA: off | admins | all). Default off, so
	// existing deployments are unchanged; when set, a login for an in-scope
	// account without MFA returns a scoped enrollment challenge instead of a
	// session. Enrollment is completed via the enroll/confirm group below.
	requireMFA := os.Getenv("OCTBASE_REQUIRE_MFA")
	authHandler.WithRequireMFA(requireMFA)
	if requireMFA == auth.RequireMFAAdmins || requireMFA == auth.RequireMFAAll {
		slog.Info("MFA enforcement enabled", "mode", requireMFA)
	}

	// Services.
	wmSvc := workmanagement.NewService(db, taskRepo, commentRepo, linkRepo, attachmentRepo, relationRepo, releaseRepo, boardRepo, columnRepo, sprintRepo, templateRepo)
	notifSvc := notifications.NewService(db, notifRepo, sseHub, mailQueue)

	// Handlers.
	iaHandler := identityaccess.NewHandler(db, userRepo, membershipRepo, auditRepo)
	wmHandler := workmanagement.NewHandler(
		db, projectRepo, taskRepo, commentRepo, linkRepo, attachmentRepo,
		relationRepo, boardRepo, columnRepo, extColumnRepo, releaseRepo, sprintRepo, categoryRepo,
		templateRepo, wmSvc, activityRepo, notifSvc, pageRepo, auditRepo,
	)
	// Broadcast project-scoped board/task changes over SSE so co-workers viewing
	// the same board refresh automatically (see workmanagement.BoardEventPublisher).
	wmHandler.WithEventPublisher(sseHub)
	// Attachment file storage (local filesystem volume). Uploads are disabled if
	// the directory cannot be created.
	attachmentsDir := os.Getenv("OCTBASE_ATTACHMENTS_DIR")
	if attachmentsDir == "" {
		attachmentsDir = "/data/attachments"
	}
	maxUploadMB := 10
	if v := os.Getenv("OCTBASE_MAX_UPLOAD_MB"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			maxUploadMB = n
		}
	}
	if store, serr := workmanagement.NewAttachmentStorage(attachmentsDir); serr != nil {
		slog.Warn("attachment storage unavailable; file uploads disabled", "dir", attachmentsDir, "error", serr)
	} else {
		wmHandler.WithAttachmentStorage(store, int64(maxUploadMB)<<20)
		wmHandler.WithUserStorageQuota(int64(maxUserStorageMB) << 20)
		slog.Info("attachment storage ready", "dir", attachmentsDir, "maxUploadMB", maxUploadMB, "maxUserStorageMB", maxUserStorageMB)
	}

	// Docs context surface for the whole-project export/import.
	wmHandler.WithPagePorter(docs.NewPorter(pageRepo, revisionRepo, refRepo))

	docHandler := docs.NewHandler(db, pageRepo, revisionRepo, refRepo, activityRepo)
	scmHandler := scmintegration.NewHandler(db, repoConnRepo, branchRepo, activityRepo)
	actHandler := activity.NewHandler(db, activityRepo)
	adminHandler := admin.NewHandler(db, auditRepo)
	sseHandler := sse.NewHandler(db, sseHub, emailProvider)
	notifHandler := notifications.NewHandler(db, notifRepo)
	dashboardHandler := dashboard.NewHandler(dashboardRepo)
	mfaHandler := mfa.NewHandler(db, mfaRepo, auditRepo)
	webhookHandler := webhooks.NewHandler(db, branchRepo, taskRepo, sseHub)
	auditHandler := auditlog.NewHandler(auditRepo)
	usermgmtHandler := usermgmt.NewHandler(db, usermgmtRepo, auditRepo, mailQueue).WithUserLimit(maxUsers)

	// Router.
	// Trusted reverse-proxy IPs/CIDRs whose X-Forwarded-For we honor. Empty (the
	// default) means no proxy is trusted, so forwarding headers are ignored and
	// cannot be spoofed to bypass rate limiting or forge audit IPs. Set this to
	// the edge proxy's address(es) in production. See docs/operations.md.
	trustedProxies := shared.TrustedProxiesFromEnv()
	if len(trustedProxies) == 0 {
		slog.Info("no trusted proxies configured; ignoring X-Forwarded-For (set OCTBASE_TRUSTED_PROXIES behind a proxy)")
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(shared.RealIP(trustedProxies))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(shared.CORSMiddleware)
	r.Use(shared.SecurityHeaders)
	r.Use(prometheusMiddleware)

	// Metrics & health — public.
	r.Handle("/metrics", promhttp.Handler())
	r.Get("/api/v1/health", healthHandler(db, expectedMigrationVersion, appVersion))
	r.Get("/api/v1/version", versionHandler(appVersion))
	r.Get("/api/v1/meta/enums", enumsHandler)
	r.Get("/api/v1/config", configHandler(featureTaskView, featureJiraCSVImport, edition, appVersion, maxUsers, maxUploadMB, maxUserStorageMB))
	r.Get("/health", healthHandler(db, expectedMigrationVersion, appVersion))

	// OpenAPI docs.
	r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "api/openapi.yaml")
	})
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		// Swagger UI assets are served locally from /swagger-ui (no external CDN).
		// The page still needs 'unsafe-inline' for its inline init script and the
		// inline styles Swagger UI injects at runtime, so this is looser than the
		// strict default-src 'none' set globally in SecurityHeaders — but every
		// source is now first-party.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self'; "+
				"frame-ancestors 'none'; base-uri 'none'")
		http.ServeFile(w, r, "web/docs.html")
	})
	// Locally-vendored Swagger UI assets backing /docs (no CDN dependency).
	r.Get("/swagger-ui/swagger-ui.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/swagger-ui/swagger-ui.css")
	})
	r.Get("/swagger-ui/swagger-ui-bundle.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/swagger-ui/swagger-ui-bundle.js")
	})
	r.Get("/docs/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs", http.StatusMovedPermanently)
	})

	// Public auth routes — rate-limited.
	r.Group(func(r chi.Router) {
		r.Use(shared.RateLimit(120, time.Minute))
		authHandler.RegisterPublicRoutes(r)
	})

	// Webhook endpoints (HMAC-authenticated, not JWT).
	r.Post("/api/v1/webhooks/bitbucket", webhookHandler.HandleBitbucket)
	r.Post("/api/v1/webhooks/github", webhookHandler.HandleGitHub)

	// OAuth callback (authenticated by one-time state, not JWT).
	scmHandler.RegisterPublicRoutes(r)

	// JWT-authenticated routes.
	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(emailProvider))
		r.Use(shared.LoadUserGlobalRole(db)) // loads global_role, rejects disabled accounts
		r.Use(shared.RequireJSON)

		// Auth routes that need a valid user.
		authHandler.RegisterRoutes(r)

		// Legacy admin routes (ADMIN or SUPER_ADMIN).
		r.Group(func(r chi.Router) {
			r.Use(admin.RequireAdmin())
			adminHandler.RegisterRoutes(r)
		})

		// User management (SUPER_ADMIN only, rate-limited to 60/min to resist enumeration).
		r.Group(func(r chi.Router) {
			r.Use(shared.RateLimit(60, time.Minute))
			usermgmtHandler.RegisterRoutes(r)
		})

		// Audit logs (SUPER_ADMIN only — enforced inside the handler).
		auditHandler.RegisterRoutes(r)

		// Domain routes.
		iaHandler.RegisterRoutes(r)
		wmHandler.RegisterRoutes(r)
		docHandler.RegisterRoutes(r)
		scmHandler.RegisterRoutes(r)
		actHandler.RegisterRoutes(r)
		notifHandler.RegisterRoutes(r)
		dashboardHandler.RegisterRoutes(r)
		mfaHandler.RegisterRoutes(r)
	})

	// MFA enroll/confirm — accepts a full access token OR a scoped enrollment
	// token, so a user forced to set up MFA at login (OCTBASE_REQUIRE_MFA) can
	// complete it before they hold a session. No other route uses this
	// middleware, so an enrollment token unlocks nothing else.
	r.Group(func(r chi.Router) {
		r.Use(auth.EnrollmentOrAccessMiddleware(emailProvider, jwtSecret))
		r.Use(shared.RequireJSON)
		mfaHandler.RegisterEnrollmentRoutes(r)
	})

	// Export/import routes (project ZIP + Jira CSV) — separate group without RequireJSON.
	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(emailProvider))
		r.Use(shared.LoadUserGlobalRole(db))
		wmHandler.RegisterProjectTransferRoutes(r)
		wmHandler.RegisterCSVRoutes(r, featureJiraCSVImport)
		// File upload/download: multipart and binary, so not in the RequireJSON group.
		wmHandler.RegisterFileRoutes(r)
		// Profile-picture upload/serve: multipart + raw image bytes, same reason.
		iaHandler.RegisterFileRoutes(r)
	})

	// SSE endpoints — OptionalJWT, supports ?token= for EventSource clients.
	r.Group(func(r chi.Router) {
		r.Use(auth.OptionalJWTMiddleware(emailProvider))
		sseHandler.RegisterRoutes(r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	addr := ":" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("starting server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}

	// Drain queued mail after the HTTP server has stopped accepting requests, so
	// notifications produced by the last in-flight writes still go out and no
	// worker goroutine outlives the process.
	mailCtx, cancelMail := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMail()
	if err := mailQueue.Close(mailCtx); err != nil {
		slog.Warn("mail queue drain incomplete at shutdown", "err", err)
	}
}

// migrationDSN picks the DSN migrations run against: the dedicated owner/DDL URL
// when one is configured, otherwise the runtime URL. Keeping the fallback here
// (rather than at each call site) is what makes the least-privilege split opt-in
// and backward-compatible — a deployment that only sets OCTBASE_DATABASE_URL runs
// exactly as it did before.
func migrationDSN(runtimeDSN, migrateDSN string) string {
	if strings.TrimSpace(migrateDSN) == "" {
		return runtimeDSN
	}
	return migrateDSN
}

// runMigrations applies the migrations, using a separate connection for the
// owner/DDL role when the least-privilege split is configured. The runtime pool
// is deliberately not reused in that case: it is authenticated as the restricted
// role, which by design cannot run DDL.
func runMigrations(runtimeDB *sql.DB, runtimeDSN, migrateDSN string) error {
	if migrateDSN == runtimeDSN {
		return shared.RunMigrations(runtimeDB, migrationsPath)
	}
	slog.Info("running migrations as the dedicated owner role (OCTBASE_MIGRATE_DATABASE_URL)")
	migrateDB, err := shared.OpenDB(migrateDSN)
	if err != nil {
		return err
	}
	// Closed as soon as migrations finish so the privileged role holds no
	// connection for the lifetime of the process.
	defer func() { _ = migrateDB.Close() }()
	return shared.RunMigrations(migrateDB, migrationsPath)
}

func healthHandler(db *sql.DB, expectedMigrationVersion uint, appVersion string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bound every DB touch so the health check can never hang: under pool
		// exhaustion acquiring a connection blocks, and an unbounded Ping here
		// would make /health hang too — hiding the very outage a probe/watchdog
		// needs to see. On timeout we report "degraded" and move on.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		dbStatus := "ok"
		if err := db.PingContext(ctx); err != nil {
			dbStatus = "error"
		}
		stats := db.Stats()
		version, _, _ := shared.MigrationVersionContext(ctx, db)
		status := http.StatusOK
		if dbStatus != "ok" || version != expectedMigrationVersion {
			status = http.StatusServiceUnavailable
		}
		shared.WriteJSON(w, status, map[string]any{
			"status": func() string {
				if status == http.StatusOK {
					return "ok"
				}
				return "degraded"
			}(),
			"db": map[string]any{
				"status":           dbStatus,
				"poolOpen":         stats.OpenConnections,
				"poolIdle":         stats.Idle,
				"migrationVersion": version,
			},
			"version": appVersion,
		})
	}
}

func versionHandler(appVersion string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{
			"version": appVersion,
			"name":    "Octbase API",
		})
	}
}

// Deployment editions (OCTBASE_EDITION). Editions gate optional product
// surface per client deployment: Jira CSV import is included in ENTERPRISE
// and bookable as an additional option in BUSINESS (see jiraImportEnabled).
const (
	editionTeam       = "TEAM"
	editionBusiness   = "BUSINESS"
	editionEnterprise = "ENTERPRISE"
)

// jiraImportEnabled decides the Jira CSV import add-on per deployment:
// ENTERPRISE ships it included; BUSINESS must activate it explicitly with
// OCTBASE_OPTION_JIRA_IMPORT=true (an additional bookable option, default
// off — note the opposite stance of the default-on OCTBASE_FEATURE_* flags);
// TEAM can never activate it, so a set flag there is ignored with a warning.
func jiraImportEnabled(edition string) bool {
	opted := strings.EqualFold(strings.TrimSpace(os.Getenv("OCTBASE_OPTION_JIRA_IMPORT")), "true")
	switch edition {
	case editionEnterprise:
		return true
	case editionBusiness:
		return opted
	default:
		if opted {
			slog.Warn("OCTBASE_OPTION_JIRA_IMPORT is set but the Jira import option can only be activated in the BUSINESS edition; ignoring", "edition", edition)
		}
		return false
	}
}

// editionFromEnv reads OCTBASE_EDITION (case-insensitive TEAM, BUSINESS or
// ENTERPRISE). A missing value defaults to ENTERPRISE so an unconfigured
// deployment ships the full app — the same default-on stance as the
// OCTBASE_FEATURE_* flags; an invalid value is logged and treated the same
// way rather than refusing to start.
func editionFromEnv() string {
	raw := strings.TrimSpace(os.Getenv("OCTBASE_EDITION"))
	switch ed := strings.ToUpper(raw); ed {
	case editionTeam, editionBusiness, editionEnterprise:
		return ed
	case "":
		return editionEnterprise
	default:
		slog.Warn("invalid OCTBASE_EDITION, defaulting to ENTERPRISE", "value", raw)
		return editionEnterprise
	}
}

// configHandler serves the public, non-secret runtime configuration the SPA
// needs at boot — the optional-feature flags, the deployment edition and the
// app version (all captured once from the environment at startup; the response
// carries no secrets), so it is a public route alongside the enum metadata.
// The SPA's version tag reads "version" here rather than calling
// /api/v1/version separately, since it already fetches /config at boot.
func configHandler(taskView, jiraCSVImport bool, edition, appVersion string, maxUsers, maxUploadMB, maxUserStorageMB int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]any{
			"features": map[string]bool{
				"taskView":      taskView,
				"jiraCsvImport": jiraCSVImport,
			},
			"edition": edition,
			"version": appVersion,
			// Installation limits, for display purposes only — every limit is
			// enforced server-side. 0 means unlimited.
			"limits": map[string]int{
				"maxUsers":         maxUsers,
				"maxUploadMb":      maxUploadMB,
				"maxUserStorageMb": maxUserStorageMB,
			},
		})
	}
}

func enumsHandler(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"taskStatuses":   []string{"PLANNED", "IN_PROGRESS", "IN_REVIEW", "DONE", "ARCHIVED"},
		"taskPriorities": []string{"LOW", "MEDIUM", "HIGH", "CRITICAL", "BLOCKER"},
		// THEME and INITIATIVE are opt-in per project: the effective set for a
		// project derives from its themeEnabled/initiativeEnabled flags.
		"taskTypes":     []string{"TASK", "STORY", "EPIC", "SUBTASK", "INITIATIVE", "THEME"},
		"globalRoles":   rbac.ValidGlobalRoles(),
		"projectRoles":  rbac.ValidProjectRoles(),
		"roles":         rbac.ValidProjectRoles(), // backward compat
		"relationTypes": []string{"RELATES_TO", "BLOCKS", "BLOCKED_BY", "DUPLICATES"},
		"visibilities":  []string{"PUBLIC", "PRIVATE"},
		// The per-project effort-estimation unit. NONE is the default: a
		// project carries no estimates until an owner picks a unit.
		"estimationUnits": workmanagement.ValidEstimationUnits(),
		"releaseStatuses": []string{"PLANNED", "CLOSED"},
		"pageStatuses":    []string{"DRAFT", "PUBLISHED", "ARCHIVED"},
		"branchTypes":     []string{"feature", "bugfix", "hotfix", "release"},
		"scmProviders":    []string{"FAKE_GITLAB", "GITHUB", "BITBUCKET"},
	})
}

func prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Use a constant label for unmatched routes (e.g. 404 probing) instead of
		// the raw request path — that path is attacker-controlled and would
		// otherwise let a scanner grow the metric's label cardinality without bound.
		path := chi.RouteContext(r.Context()).RoutePattern()
		if path == "" {
			path = "unmatched"
		}
		status := http.StatusText(ww.Status())
		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}
