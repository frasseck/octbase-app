// Package seed inserts deterministic demo data into the database on startup.
// Every call is a full upsert: existing rows are reset to their seed state so
// the demo environment stays predictable across restarts and test runs.
// Enabled by setting OCTBASE_DEMO_MODE=true.
package seed

import (
	"database/sql"
	"log/slog"
	"time"
)

const (
	SuperAdminUserID   = "00000000-0000-0000-0000-000000000010"
	DemoUserID         = "00000000-0000-0000-0000-000000000001"
	ProjectID          = "00000000-0000-0000-0000-000000000101"
	TaskID             = "00000000-0000-0000-0000-000000000201"
	TaskID2            = "00000000-0000-0000-0000-000000000202"
	CommentID          = "00000000-0000-0000-0000-000000000211"
	CommentReplyID     = "00000000-0000-0000-0000-000000000212"
	LinkID             = "00000000-0000-0000-0000-000000000221"
	AttachmentID       = "00000000-0000-0000-0000-000000000231"
	RelationID         = "00000000-0000-0000-0000-000000000241"
	CategoryID         = "00000000-0000-0000-0000-000000000251"
	TemplateID         = "00000000-0000-0000-0000-000000000261"
	BoardID            = "00000000-0000-0000-0000-000000000301"
	ColumnPlannedID    = "00000000-0000-0000-0000-000000000311"
	ColumnInProgressID = "00000000-0000-0000-0000-000000000312"
	ColumnReviewID     = "00000000-0000-0000-0000-000000000313"
	ColumnDoneID       = "00000000-0000-0000-0000-000000000314"
	ReleaseID          = "00000000-0000-0000-0000-000000000401"
	PageID             = "00000000-0000-0000-0000-000000000501"
	PageRevisionID     = "00000000-0000-0000-0000-000000000511"
	RepositoryID       = "00000000-0000-0000-0000-000000000601"
	BranchID           = "00000000-0000-0000-0000-000000000611"
	ActivityID         = "00000000-0000-0000-0000-000000000701"
)

// The seeded demo credentials. They are public surface — README.md,
// octbase-api/README.md, the dev-stack / testing / run-octbase /
// frontend-testing skills, the Playwright conftest, both SPAs' demo-login
// helpers and scripts/simulate_agile_project.py all name them — so treat a
// change here as a change to all of those.
//
// They MUST satisfy shared.ValidatePassword, and seed_password_test.go asserts
// exactly that plus the hash-matches-plaintext pairing below. They did not, for
// a long time: "demo1234" and "super1234" are 8 characters against a 12-
// character policy, so the seeded accounts held passwords the application
// itself would refuse to set. The cost was not cosmetic — the change-password
// success path was untestable in the browser, because a test that changed the
// demo password could not change it back (422, policy) and every later test in
// the run then failed to log in. The policy is not enforced retroactively,
// which is the only reason the old seed worked at login at all.
const (
	SuperAdminPassword = "superpass1234"
	DemoUserPassword   = "demopass1234"
)

// bcrypt hashes of the two constants above (cost 12). Precomputed rather than
// derived at boot so seeding stays cheap and the SQL stays a plain upsert;
// seed_password_test.go is what keeps them honest.
const (
	superAdminPasswordHash = "$2a$12$Mhw/VAOcPQWgjFcngcgjtu8hUU9V2UUSZjNRo/8bTyQ1B2Rkha1EO"
	demoUserPasswordHash   = "$2a$12$wVnhxyaJdkeUC8Mc4/wj/uKz2CAWt0jjjS0m997d9t7OlTZOMxMkW"
)

// Run upserts all seed records to their canonical state. Safe to call on every
// startup — existing rows are reset, missing rows are inserted.
// Retries the initial connection ping to tolerate a brief network-setup window
// when the container starts before the DNS for the DB hostname has settled.
func Run(db *sql.DB) error {
	slog.Info("running seed")
	for i := range 10 {
		if err := db.Ping(); err == nil {
			break
		}
		wait := time.Duration(i+1) * time.Second
		slog.Info("seed waiting for db", "attempt", i+1, "wait", wait)
		time.Sleep(wait)
	}

	now := "2024-01-01T00:00:00Z"
	// The activity entry alone gets a startup timestamp: the fixed 2024 date is
	// already outside the GDPR activity retention window (internal/retention),
	// so the purge that runs right after seeding would delete the entry and the
	// demo Activity view (which UI tests assert on) would come up empty. The
	// entry's ID, type and message stay deterministic, and the upsert re-stamps
	// it on every restart so it keeps outrunning the daily purge.
	activityNow := time.Now().UTC().Format(time.RFC3339)

	stmts := []string{
		// Super admin: SUPER_ADMIN role, password SuperAdminPassword. mfa_enabled is
		// pinned to false (not just left at its column default): this account
		// is logged into directly by the frontend, the mobile app, and most
		// of the Playwright/pytest suite, so it must never require a TOTP
		// code locally — MFA stays fully opt-in and this upsert guards
		// against someone flipping it on for the seed data later by mistake.
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,is_admin,mfa_enabled,created_at,updated_at) VALUES ('` + SuperAdminUserID + `','super@octbase.dev','Super Admin','` + superAdminPasswordHash + `','SUPER_ADMIN','active',true,true,false,'` + now + `','` + now + `')
		 ON CONFLICT (id) DO UPDATE SET email=EXCLUDED.email, display_name=EXCLUDED.display_name, password_hash=EXCLUDED.password_hash, global_role=EXCLUDED.global_role, status=EXCLUDED.status, is_active=EXCLUDED.is_active, is_admin=EXCLUDED.is_admin, mfa_enabled=EXCLUDED.mfa_enabled`,

		// Demo user: ADMIN role, password DemoUserPassword. Same mfa_enabled=false
		// pin as the super admin above, for the same reason.
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,is_admin,mfa_enabled,created_at,updated_at) VALUES ('` + DemoUserID + `','demo@octbase.dev','Demo User','` + demoUserPasswordHash + `','ADMIN','active',true,true,false,'` + now + `','` + now + `')
		 ON CONFLICT (id) DO UPDATE SET email=EXCLUDED.email, display_name=EXCLUDED.display_name, password_hash=EXCLUDED.password_hash, global_role=EXCLUDED.global_role, status=EXCLUDED.status, is_active=EXCLUDED.is_active, is_admin=EXCLUDED.is_admin, mfa_enabled=EXCLUDED.mfa_enabled`,

		// Project — include created_by_user_id
		`INSERT INTO projects (id,name,slug,abbreviation,description,visibility,status,created_by_user_id,created_at,updated_at,version) VALUES ('` + ProjectID + `','Demo Project','demo-project','DP','A demonstration project','PUBLIC','ACTIVE','` + DemoUserID + `','` + now + `','` + now + `',1)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, abbreviation=EXCLUDED.abbreviation, status=EXCLUDED.status, visibility=EXCLUDED.visibility, version=EXCLUDED.version`,

		// Membership — creator is PROJECT_OWNER
		`INSERT INTO memberships (id,project_id,user_id,role,assigned_by_user_id,created_at,updated_at) VALUES ('00000000-0000-0000-0000-000000000001','` + ProjectID + `','` + DemoUserID + `','PROJECT_OWNER','` + DemoUserID + `','` + now + `','` + now + `')
		 ON CONFLICT (id) DO UPDATE SET role=EXCLUDED.role`,

		// Category
		`INSERT INTO task_categories (id,project_id,name,description,color,created_at,updated_at) VALUES ('` + CategoryID + `','` + ProjectID + `','Feature','New features','blue','` + now + `','` + now + `')
		 ON CONFLICT (id) DO NOTHING`,

		// Template
		// The template stays named "Bug Report" but instantiates a plain TASK:
		// BUG is no longer a task type (bugs are tasks in the
		// EPIC → STORY → TASK → SUBTASK hierarchy). Upsert task_type so
		// databases seeded before the hierarchy migration converge.
		`INSERT INTO task_templates (id,project_id,name,title_template,description_template,task_type,priority,created_at,updated_at) VALUES ('` + TemplateID + `','` + ProjectID + `','Bug Report','[BUG] ','Steps to reproduce:\n1. \n\nExpected:\nActual:','TASK','HIGH','` + now + `','` + now + `')
		 ON CONFLICT (id) DO UPDATE SET task_type=EXCLUDED.task_type`,

		// Release — reset status so close/reopen tests start predictably
		`INSERT INTO releases (id,project_id,name,goal,due_date,status,created_at,updated_at,version) VALUES ('` + ReleaseID + `','` + ProjectID + `','v1.0 Launch','Ship first version','2024-06-01','PLANNED','` + now + `','` + now + `',1)
		 ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, version=EXCLUDED.version`,

		// Board
		`INSERT INTO boards (id,project_id,name,is_default,created_at,updated_at) VALUES ('` + BoardID + `','` + ProjectID + `','Main Board',1,'` + now + `','` + now + `')
		 ON CONFLICT (id) DO NOTHING`,

		// Board columns
		`INSERT INTO board_columns (id,board_id,name,status,position,created_at,updated_at) VALUES ('` + ColumnPlannedID + `','` + BoardID + `','Planned','PLANNED',0,'` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO board_columns (id,board_id,name,status,position,created_at,updated_at) VALUES ('` + ColumnInProgressID + `','` + BoardID + `','In Progress','IN_PROGRESS',1,'` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO board_columns (id,board_id,name,status,position,created_at,updated_at) VALUES ('` + ColumnReviewID + `','` + BoardID + `','Review','IN_REVIEW',2,'` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO board_columns (id,board_id,name,status,position,created_at,updated_at) VALUES ('` + ColumnDoneID + `','` + BoardID + `','Done','DONE',3,'` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Tasks — reset mutable fields so tests start from a known board position
		`INSERT INTO tasks (id,project_id,title,description,task_type,status,priority,assignee_id,reporter_id,reviewer_id,release_id,board_column_id,board_rank,seq_number,created_at,updated_at,version)
		 VALUES ('` + TaskID + `','` + ProjectID + `','Implement user authentication','Add JWT-based auth to the API','TASK','IN_PROGRESS','HIGH','` + DemoUserID + `','` + DemoUserID + `',NULL,'` + ReleaseID + `','` + ColumnInProgressID + `',1000,1,'` + now + `','` + now + `',1)
		 ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, priority=EXCLUDED.priority, assignee_id=EXCLUDED.assignee_id, reviewer_id=EXCLUDED.reviewer_id, release_id=EXCLUDED.release_id, board_column_id=EXCLUDED.board_column_id, board_rank=EXCLUDED.board_rank, seq_number=EXCLUDED.seq_number, version=EXCLUDED.version`,

		`INSERT INTO tasks (id,project_id,title,description,task_type,status,priority,assignee_id,reporter_id,reviewer_id,release_id,board_column_id,board_rank,seq_number,created_at,updated_at,version)
		 VALUES ('` + TaskID2 + `','` + ProjectID + `','Write API documentation','Document all endpoints using OpenAPI','TASK','PLANNED','MEDIUM',NULL,'` + DemoUserID + `',NULL,NULL,'` + ColumnPlannedID + `',2000,2,'` + now + `','` + now + `',1)
		 ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, priority=EXCLUDED.priority, assignee_id=EXCLUDED.assignee_id, reviewer_id=EXCLUDED.reviewer_id, release_id=EXCLUDED.release_id, board_column_id=EXCLUDED.board_column_id, board_rank=EXCLUDED.board_rank, seq_number=EXCLUDED.seq_number, version=EXCLUDED.version`,

		// Per-project task counter — seed it to the highest seeded seq_number (2) so
		// the first UI-created task gets DP-3, not a duplicate DP-1/DP-2. Without
		// this, NextSeqNumber lazily starts the counter at 1 and collides with the
		// seeded tasks above. GREATEST keeps re-seeds monotonic.
		`INSERT INTO project_task_counters (project_id, last_seq) VALUES ('` + ProjectID + `',2)
		 ON CONFLICT (project_id) DO UPDATE SET last_seq = GREATEST(project_task_counters.last_seq, EXCLUDED.last_seq)`,

		// Comment (top-level) and a threaded reply from another user so the demo
		// shows both the resolved author name and the reply tree.
		`INSERT INTO task_comments (id,task_id,author_id,text,created_at,updated_at) VALUES ('` + CommentID + `','` + TaskID + `','` + DemoUserID + `','Working on this now','` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO task_comments (id,task_id,author_id,parent_id,text,created_at,updated_at) VALUES ('` + CommentReplyID + `','` + TaskID + `','` + SuperAdminUserID + `','` + CommentID + `','Thanks — ping me if you need a review.','2024-01-01T00:05:00Z','2024-01-01T00:05:00Z') ON CONFLICT (id) DO NOTHING`,

		// Link
		`INSERT INTO task_links (id,task_id,url,title,created_at) VALUES ('` + LinkID + `','` + TaskID + `','https://jwt.io','JWT Reference','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Attachment
		`INSERT INTO task_attachments (id,task_id,filename,content_type,size_bytes,external_url,created_at) VALUES ('` + AttachmentID + `','` + TaskID + `','auth-diagram.png','image/png',12345,'https://example.com/auth-diagram.png','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Relation
		`INSERT INTO task_relations (id,source_task_id,target_task_id,relation_type,created_at) VALUES ('` + RelationID + `','` + TaskID + `','` + TaskID2 + `','BLOCKS','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Page — reset content so page edit tests start from known state
		`INSERT INTO pages (id,project_id,title,slug,content,rendered_html,status,created_at,updated_at,version)
		 VALUES ('` + PageID + `','` + ProjectID + `','Getting Started','getting-started','= Getting Started\n\nWelcome to the demo project.\n\n== Authentication\n\nSee TASK-` + TaskID + ` for auth implementation.','<div class="asciidoc-content"><h1>Getting Started</h1><p>Welcome to the demo project.</p><h2>Authentication</h2><p>See TASK-` + TaskID + ` for auth implementation.</p></div>','PUBLISHED','` + now + `','` + now + `',1)
		 ON CONFLICT (id) DO UPDATE SET content=EXCLUDED.content, rendered_html=EXCLUDED.rendered_html, status=EXCLUDED.status, version=EXCLUDED.version`,

		// Page revision
		`INSERT INTO page_revisions (id,page_id,content,message,author_id,created_at) VALUES ('` + PageRevisionID + `','` + PageID + `','= Getting Started\n\nInitial content.','Initial publish','` + DemoUserID + `','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Repository connection
		`INSERT INTO repository_connections (id,project_id,provider,display_name,repository_url,default_branch,created_at,updated_at) VALUES ('` + RepositoryID + `','` + ProjectID + `','FAKE_GITLAB','Demo Repo','https://gitlab.example.com/demo/octbase','main','` + now + `','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Branch reference
		`INSERT INTO branch_references (id,task_id,repository_id,branch_name,branch_type,created_at) VALUES ('` + BranchID + `','` + TaskID + `','` + RepositoryID + `','feature/user-auth','feature','` + now + `') ON CONFLICT (id) DO NOTHING`,

		// Activity. payload_json carries the params the TASK_CREATED translation
		// interpolates ("Created task \"{{title}}\""); an empty object renders the
		// placeholder verbatim in the Activity view. The conflict clause refreshes
		// it too, so stacks seeded before this was fixed repair themselves.
		`INSERT INTO activity_entries (id,project_id,task_id,actor_user_id,type,message,payload_json,created_at) VALUES ('` + ActivityID + `','` + ProjectID + `','` + TaskID + `','` + DemoUserID + `','TASK_CREATED','Task created: Implement user authentication','{"title":"Implement user authentication"}','` + activityNow + `') ON CONFLICT (id) DO UPDATE SET created_at=EXCLUDED.created_at, payload_json=EXCLUDED.payload_json`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			slog.Warn("seed statement failed", "error", err, "stmt", stmt[:min(50, len(stmt))])
		}
	}

	slog.Info("seed complete")
	return nil
}
