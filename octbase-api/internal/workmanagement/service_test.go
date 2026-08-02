package workmanagement

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/octbase/octbase-api/internal/shared"
)

// newTestDB opens an isolated PostgreSQL schema for service-level tests.
// It cannot use testutil.NewTestDB because testutil imports workmanagement,
// which would create a cycle.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
		return nil
	}

	_, file, _, _ := runtime.Caller(0)
	migsDir := filepath.Join(filepath.Dir(file), "..", "..", "migrations")

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)

	schema := "tbsvc_" + strings.ReplaceAll(shared.NewUUID(), "-", "")
	if _, err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		_ = db.Close()
		t.Fatalf("set search_path: %v", err)
	}
	for _, name := range []string{
		"001_initial.up.sql", "002_constraints.up.sql", "003_auth.up.sql",
		"004_notifications.up.sql", "005_scm.up.sql", "006_migration.up.sql",
		"007_sprints.up.sql", "008_task_due_date.up.sql",
		"012_board_config.up.sql", "013_attachment_storage.up.sql",
		"014_task_pinned.up.sql", "015_sprint_counts.up.sql",
		"020_comment_threads.up.sql", "021_task_done_at.up.sql",
		"025_version_guards.up.sql", "028_task_hierarchy.up.sql",
		"030_concurrency_guards.up.sql", "034_task_estimation.up.sql",
		"035_sprint_effort_snapshot.up.sql",
	} {
		b, err := os.ReadFile(filepath.Join(migsDir, name))
		if err != nil {
			_ = db.Close()
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			_ = db.Close()
			t.Fatalf("run migration %s: %v", name, err)
		}
	}

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		_ = db.Close()
	})
	return db
}

func newTestService(t *testing.T) (*Service, *TaskRepo, *TaskRelationRepo, *ReleaseRepo, *BoardColumnRepo, *SprintRepo) {
	t.Helper()
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	commentRepo := NewTaskCommentRepo(db)
	linkRepo := NewTaskLinkRepo(db)
	attachmentRepo := NewTaskAttachmentRepo(db)
	relRepo := NewTaskRelationRepo(db)
	msRepo := NewReleaseRepo(db)
	boardRepo := NewBoardRepo(db)
	colRepo := NewBoardColumnRepo(db)
	sprintRepo := NewSprintRepo(db)
	templateRepo := NewTaskTemplateRepo(db)
	return NewService(db, taskRepo, commentRepo, linkRepo, attachmentRepo, relRepo, msRepo, boardRepo, colRepo, sprintRepo, templateRepo),
		taskRepo, relRepo, msRepo, colRepo, sprintRepo
}

func insertProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	_, err := db.Exec(`INSERT INTO projects (id,name,slug,description,visibility,status,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, "Test Project", id, "", "PRIVATE", "ACTIVE", now, now, 1)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func seedTask(t *testing.T, repo *TaskRepo, projectID, id, status string) {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	task := &Task{
		ID: id, ProjectID: projectID, Title: "Test Task",
		TaskType: TaskTypeTask, Status: status, Priority: PriorityMedium,
		BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := repo.Create(task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func seedRelease(t *testing.T, repo *ReleaseRepo, projectID, id string) {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	ms := &Release{
		ID: id, ProjectID: projectID, Name: "Test MS",
		Status: StatusPlanned, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := repo.Create(ms); err != nil {
		t.Fatalf("seed release: %v", err)
	}
}

const (
	projID  = "aaaaaaaa-0000-0000-0000-000000000001"
	taskID1 = "bbbbbbbb-0000-0000-0000-000000000001"
	taskID2 = "bbbbbbbb-0000-0000-0000-000000000002"
	taskID3 = "bbbbbbbb-0000-0000-0000-000000000003"
	msID    = "cccccccc-0000-0000-0000-000000000001"
)

func TestAddRelation_SelfRelation(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	rel := &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID1,
		RelationType: RelationRelatesTo, CreatedAt: "2024-01-01T00:00:00Z",
	}
	err := svc.AddRelation(projID, rel)
	if err == nil {
		t.Fatal("expected error for self-relation, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if de.Code != "TASK_SELF_RELATION" {
		t.Errorf("code = %q, want TASK_SELF_RELATION", de.Code)
	}
}

func TestAddRelation_Duplicate(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	rel := &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationRelatesTo, CreatedAt: "2024-01-01T00:00:00Z",
	}
	if err := svc.AddRelation(projID, rel); err != nil {
		t.Fatalf("first add: %v", err)
	}

	rel2 := &TaskRelation{
		ID: "r2", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationRelatesTo, CreatedAt: "2024-01-01T00:00:00Z",
	}
	err := svc.AddRelation(projID, rel2)
	if err == nil {
		t.Fatal("expected error for duplicate relation, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "TASK_RELATION_DUPLICATE" {
		t.Errorf("expected TASK_RELATION_DUPLICATE, got %v", err)
	}
}

func TestAddRelation_InvalidType(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	// relation_type is plain TEXT with no CHECK constraint, so an unvalidated
	// value would be accepted and read back to clients that switch on it.
	for _, rt := range []string{"", "NOT_A_REAL_TYPE", "relates_to", "BLOCKS "} {
		err := svc.AddRelation(projID, &TaskRelation{
			ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
			RelationType: rt, CreatedAt: "2024-01-01T00:00:00Z",
		})
		if err == nil {
			t.Fatalf("relation type %q: expected error, got nil", rt)
		}
		de, ok := err.(*DomainError)
		if !ok || de.Code != "TASK_RELATION_TYPE_INVALID" {
			t.Errorf("relation type %q: expected TASK_RELATION_TYPE_INVALID, got %v", rt, err)
		}
	}
}

func TestAddRelation_BlockedByCycle(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID3, StatusPlanned)

	// A blocks B, B blocks C — same setup as TestAddRelation_BlocksCycle.
	if err := svc.AddRelation(projID, &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("A blocks B: %v", err)
	}
	if err := svc.AddRelation(projID, &TaskRelation{
		ID: "r2", SourceTaskID: taskID2, TargetTaskID: taskID3,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("B blocks C: %v", err)
	}

	// "A BLOCKED_BY C" is the same cycle as the rejected "C BLOCKS A", just
	// named from the other end: it writes C-BLOCKS-A as its inverse. Checking
	// only the request direction let this through.
	err := svc.AddRelation(projID, &TaskRelation{
		ID: "r3", SourceTaskID: taskID1, TargetTaskID: taskID3,
		RelationType: RelationBlockedBy, CreatedAt: "2024-01-01T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected cycle error for BLOCKED_BY forming a BLOCKS cycle, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "TASK_RELATION_CYCLE" {
		t.Errorf("expected TASK_RELATION_CYCLE, got %v", err)
	}
}

func TestAddRelation_BlockedByNoCycle(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	// The guard must not over-reject: a lone BLOCKED_BY forms no cycle.
	if err := svc.AddRelation(projID, &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlockedBy, CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("A blocked_by B: %v", err)
	}
}

func TestAddRelation_BlocksCycle(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID3, StatusPlanned)

	// A blocks B
	if err := svc.AddRelation(projID, &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("A blocks B: %v", err)
	}
	// B blocks C
	if err := svc.AddRelation(projID, &TaskRelation{
		ID: "r2", SourceTaskID: taskID2, TargetTaskID: taskID3,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("B blocks C: %v", err)
	}
	// C blocks A — would create a cycle
	err := svc.AddRelation(projID, &TaskRelation{
		ID: "r3", SourceTaskID: taskID3, TargetTaskID: taskID1,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "TASK_RELATION_CYCLE" {
		t.Errorf("expected TASK_RELATION_CYCLE, got %v", err)
	}
}

func TestAddRelation_Success(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	rel := &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}
	if err := svc.AddRelation(projID, rel); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}
}

func TestCloseRelease_WithOpenTasks(t *testing.T) {
	svc, taskRepo, _, msRepo, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedRelease(t, msRepo, projID, msID)

	now := "2024-01-01T00:00:00Z"
	msIDStr := msID
	task := &Task{
		ID: taskID1, ProjectID: projID, Title: "Open Task",
		TaskType: TaskTypeTask, Status: StatusPlanned, Priority: PriorityMedium,
		ReleaseID: &msIDStr, BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := taskRepo.Create(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	ms, _ := msRepo.FindByID(msID)
	err := svc.CloseRelease(ms)
	if err == nil {
		t.Fatal("expected error for open tasks, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "RELEASE_HAS_OPEN_TASKS" {
		t.Errorf("expected RELEASE_HAS_OPEN_TASKS, got %v", err)
	}
}

func TestCloseRelease_Success(t *testing.T) {
	svc, _, _, msRepo, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedRelease(t, msRepo, projID, msID)

	ms, _ := msRepo.FindByID(msID)
	if err := svc.CloseRelease(ms); err != nil {
		t.Fatalf("CloseRelease: %v", err)
	}
	updated, _ := msRepo.FindByID(msID)
	if updated.Status != StatusClosed {
		t.Errorf("status = %q, want CLOSED", updated.Status)
	}
}

func TestCloseRelease_DoneTasksAllowed(t *testing.T) {
	svc, taskRepo, _, msRepo, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedRelease(t, msRepo, projID, msID)

	now := "2024-01-01T00:00:00Z"
	msIDStr := msID
	task := &Task{
		ID: taskID1, ProjectID: projID, Title: "Done Task",
		TaskType: TaskTypeTask, Status: StatusDone, Priority: PriorityMedium,
		ReleaseID: &msIDStr, BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := taskRepo.Create(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	ms, _ := msRepo.FindByID(msID)
	if err := svc.CloseRelease(ms); err != nil {
		t.Fatalf("CloseRelease should succeed with only DONE tasks: %v", err)
	}
}

func TestCloseRelease_InReviewBlocked(t *testing.T) {
	svc, taskRepo, _, msRepo, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedRelease(t, msRepo, projID, msID)

	now := "2024-01-01T00:00:00Z"
	msIDStr := msID
	task := &Task{
		ID: taskID1, ProjectID: projID, Title: "Review Task",
		TaskType: TaskTypeTask, Status: StatusInReview, Priority: PriorityMedium,
		ReleaseID: &msIDStr, BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := taskRepo.Create(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	ms, _ := msRepo.FindByID(msID)
	err := svc.CloseRelease(ms)
	if err == nil {
		t.Fatal("expected error for IN_REVIEW task, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "RELEASE_HAS_OPEN_TASKS" {
		t.Errorf("expected RELEASE_HAS_OPEN_TASKS, got %v", err)
	}
}

func TestAddRelation_BlocksSymmetry(t *testing.T) {
	svc, taskRepo, relRepo, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	rel := &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}
	if err := svc.AddRelation(projID, rel); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	// B should have a BLOCKED_BY relation pointing back to A.
	rels, err := relRepo.ListByTask(taskID2)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.SourceTaskID == taskID2 && r.TargetTaskID == taskID1 && r.RelationType == RelationBlockedBy {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BLOCKED_BY relation on task B, got %v", rels)
	}
}

func TestDeleteRelation_RemovesInverse(t *testing.T) {
	svc, taskRepo, relRepo, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)
	seedTask(t, taskRepo, projID, taskID2, StatusPlanned)

	rel := &TaskRelation{
		ID: "r1", SourceTaskID: taskID1, TargetTaskID: taskID2,
		RelationType: RelationBlocks, CreatedAt: "2024-01-01T00:00:00Z",
	}
	if err := svc.AddRelation(projID, rel); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	// Delete scoped to the relation's source task (taskID1); the relation and its
	// inverse must both go.
	deleted, err := svc.DeleteRelation(taskID1, "r1")
	if err != nil {
		t.Fatalf("DeleteRelation: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteRelation reported not deleted, want deleted")
	}

	// Both directions should be gone.
	relsA, _ := relRepo.ListByTask(taskID1)
	if len(relsA) != 0 {
		t.Errorf("expected no relations for A after delete, got %v", relsA)
	}
	relsB, _ := relRepo.ListByTask(taskID2)
	if len(relsB) != 0 {
		t.Errorf("expected no relations for B after delete, got %v", relsB)
	}
}

func insertBoard(t *testing.T, db *sql.DB, projectID, boardID string) {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	_, err := db.Exec(`INSERT INTO boards (id,project_id,name,is_default,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		boardID, projectID, "Test Board", 1, now, now)
	if err != nil {
		t.Fatalf("insert board: %v", err)
	}
}

const boardID = "dddddddd-0000-0000-0000-000000000001"

func TestAddBoardColumn_UniqueStatus(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	insertBoard(t, svc.db, projID, boardID)

	now := "2024-01-01T00:00:00Z"
	col1 := &BoardColumn{
		ID: "c1", BoardID: boardID, Name: "Planned", Status: StatusPlanned, Position: 0,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.AddBoardColumn(col1); err != nil {
		t.Fatalf("first AddBoardColumn: %v", err)
	}

	col2 := &BoardColumn{
		ID: "c2", BoardID: boardID, Name: "Also Planned", Status: StatusPlanned, Position: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	err := svc.AddBoardColumn(col2)
	if err == nil {
		t.Fatal("expected COLUMN_STATUS_DUPLICATE error, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "COLUMN_STATUS_DUPLICATE" {
		t.Errorf("expected COLUMN_STATUS_DUPLICATE, got %v", err)
	}
}

func seedSprint(t *testing.T, repo *SprintRepo, projectID, id string) *Sprint {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	s := &Sprint{
		ID: id, ProjectID: projectID, Name: "Sprint 1",
		Status: SprintStatusPlanned, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := repo.Create(s); err != nil {
		t.Fatalf("seed sprint: %v", err)
	}
	return s
}

const sprintID = "eeeeeeee-0000-0000-0000-000000000001"

func TestStartSprint_Success(t *testing.T) {
	svc, _, _, _, _, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	sp := seedSprint(t, sprintRepo, projID, sprintID)

	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("StartSprint: %v", err)
	}
	updated, _ := sprintRepo.FindByID(sprintID)
	if updated.Status != SprintStatusActive {
		t.Errorf("status = %q, want ACTIVE", updated.Status)
	}
}

func TestStartSprint_BlockedByActiveSprint(t *testing.T) {
	svc, _, _, _, _, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	sp1 := seedSprint(t, sprintRepo, projID, sprintID)
	sp2 := seedSprint(t, sprintRepo, projID, "eeeeeeee-0000-0000-0000-000000000002")

	if err := svc.StartSprint(sp1); err != nil {
		t.Fatalf("start first sprint: %v", err)
	}
	err := svc.StartSprint(sp2)
	if err == nil {
		t.Fatal("expected error when starting second sprint, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "SPRINT_ALREADY_ACTIVE" {
		t.Errorf("expected SPRINT_ALREADY_ACTIVE, got %v", err)
	}
}

func TestCopyTask_CopiesSubResources(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)
	seedTask(t, taskRepo, projID, taskID1, StatusPlanned)

	cp, err := svc.CopyTask(taskID1, "actor-1")
	if err != nil {
		t.Fatalf("CopyTask: %v", err)
	}
	if cp.ID == taskID1 {
		t.Error("copy should have a new ID")
	}
	if cp.Status != StatusPlanned {
		t.Errorf("copy status = %q, want PLANNED", cp.Status)
	}
	if cp.Title != "Copy of Test Task" {
		t.Errorf("copy title = %q, want 'Copy of Test Task'", cp.Title)
	}
	if cp.BoardRank != DefaultBoardRank {
		t.Errorf("copy BoardRank = %d, want %d", cp.BoardRank, DefaultBoardRank)
	}
	found, err := taskRepo.FindByID(cp.ID)
	if err != nil || found == nil {
		t.Fatalf("copy not persisted: %v", err)
	}
}

func TestCopyTask_SourceNotFound(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)

	_, err := svc.CopyTask("nonexistent-id", "actor-1")
	if err == nil {
		t.Fatal("expected error for missing source task, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "TASK_NOT_FOUND" {
		t.Errorf("expected TASK_NOT_FOUND DomainError, got %v", err)
	}
}

func TestInstantiateTemplate_CreatesTask(t *testing.T) {
	svc, taskRepo, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)

	now := "2024-01-01T00:00:00Z"
	tmpl := &TaskTemplate{
		ID: "tmpl-1", ProjectID: projID, Name: "Bug Template",
		TitleTemplate: "Bug: fix me", DescriptionTemplate: "steps to reproduce",
		TaskType: TaskTypeStory, Priority: PriorityHigh,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := svc.db.Exec(
		`INSERT INTO task_templates (id,project_id,name,title_template,description_template,task_type,priority,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		tmpl.ID, tmpl.ProjectID, tmpl.Name, tmpl.TitleTemplate, tmpl.DescriptionTemplate, tmpl.TaskType, tmpl.Priority, now, now,
	); err != nil {
		t.Fatalf("insert template: %v", err)
	}

	task, err := svc.InstantiateTemplate("tmpl-1", "actor-1", "")
	if err != nil {
		t.Fatalf("InstantiateTemplate: %v", err)
	}
	if task.Title != "Bug: fix me" {
		t.Errorf("title = %q, want 'Bug: fix me'", task.Title)
	}
	if task.TaskType != TaskTypeStory {
		t.Errorf("taskType = %q, want STORY", task.TaskType)
	}
	if task.Status != StatusPlanned {
		t.Errorf("status = %q, want PLANNED", task.Status)
	}
	found, err := taskRepo.FindByID(task.ID)
	if err != nil || found == nil {
		t.Fatalf("task not persisted: %v", err)
	}
}

func TestInstantiateTemplate_NotFound(t *testing.T) {
	svc, _, _, _, _, _ := newTestService(t)
	insertProject(t, svc.db, projID)

	_, err := svc.InstantiateTemplate("no-such-template", "actor-1", "")
	if err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code != "TEMPLATE_NOT_FOUND" {
		t.Errorf("expected TEMPLATE_NOT_FOUND DomainError, got %v", err)
	}
}

func TestCompleteSprint_MovesOpenTasksToBacklog(t *testing.T) {
	svc, taskRepo, _, _, _, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	sp := seedSprint(t, sprintRepo, projID, sprintID)

	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("start sprint: %v", err)
	}

	now := "2024-01-01T00:00:00Z"
	spID := sprintID
	openTask := &Task{
		ID: taskID1, ProjectID: projID, Title: "Open Task",
		TaskType: TaskTypeTask, Status: StatusPlanned, Priority: PriorityMedium,
		SprintID: &spID, BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	doneTask := &Task{
		ID: taskID2, ProjectID: projID, Title: "Done Task",
		TaskType: TaskTypeTask, Status: StatusDone, Priority: PriorityMedium,
		SprintID: &spID, BoardRank: DefaultBoardRank, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := taskRepo.Create(openTask); err != nil {
		t.Fatalf("create open task: %v", err)
	}
	if err := taskRepo.Create(doneTask); err != nil {
		t.Fatalf("create done task: %v", err)
	}

	if _, err := svc.CompleteSprint(sp, EstimationUnitNone); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}

	updated, _ := sprintRepo.FindByID(sprintID)
	if updated.Status != SprintStatusCompleted {
		t.Errorf("sprint status = %q, want COMPLETED", updated.Status)
	}

	open, _ := taskRepo.FindByID(taskID1)
	if open.SprintID != nil {
		t.Errorf("open task should have no sprint, got %v", *open.SprintID)
	}
	done, _ := taskRepo.FindByID(taskID2)
	if done.SprintID == nil || *done.SprintID != sprintID {
		t.Errorf("done task should retain sprint, got %v", done.SprintID)
	}
}

func seedDefaultBoardColumns(t *testing.T, colRepo *BoardColumnRepo, bID string, lanes [][2]string) {
	t.Helper()
	now := "2024-01-01T00:00:00Z"
	for i, l := range lanes {
		c := &BoardColumn{ID: fmt.Sprintf("%s-c%d", bID, i), BoardID: bID, Name: l[0], Status: l[1], Position: i, CreatedAt: now, UpdatedAt: now}
		if err := colRepo.Create(c); err != nil {
			t.Fatalf("seed column %s: %v", l[0], err)
		}
	}
}

func TestStartSprint_ProvisionsSprintBoardFromDefault(t *testing.T) {
	svc, _, _, _, colRepo, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	insertBoard(t, svc.db, projID, boardID) // is_default=1
	seedDefaultBoardColumns(t, colRepo, boardID, [][2]string{{"To Do", StatusPlanned}, {"Doing", StatusInProgress}})
	sp := seedSprint(t, sprintRepo, projID, sprintID)

	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("StartSprint: %v", err)
	}

	sb, err := NewBoardRepo(svc.db).FindBySprint(sprintID)
	if err != nil || sb == nil {
		t.Fatalf("sprint board not created: %v", err)
	}
	if !sb.IsSprintBoard || sb.IsDefault {
		t.Errorf("flags wrong: isSprintBoard=%v isDefault=%v", sb.IsSprintBoard, sb.IsDefault)
	}
	if sb.SprintID == nil || *sb.SprintID != sprintID {
		t.Errorf("sprint link wrong: %v", sb.SprintID)
	}
	cols, _ := colRepo.ListByBoard(sb.ID)
	if len(cols) != 2 {
		t.Fatalf("want 2 copied lanes, got %d", len(cols))
	}
	if cols[0].Name != "To Do" || cols[0].Status != StatusPlanned {
		t.Errorf("lane0 = %+v", cols[0])
	}
	if cols[1].Name != "Doing" || cols[1].Status != StatusInProgress {
		t.Errorf("lane1 = %+v", cols[1])
	}
}

func TestStartSprint_SprintBoardFallbackTemplate(t *testing.T) {
	svc, _, _, _, colRepo, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	// No default board: the sprint board falls back to the Scrum template.
	sp := seedSprint(t, sprintRepo, projID, sprintID)
	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("StartSprint: %v", err)
	}
	sb, _ := NewBoardRepo(svc.db).FindBySprint(sprintID)
	if sb == nil {
		t.Fatal("sprint board not created")
	}
	cols, _ := colRepo.ListByBoard(sb.ID)
	if len(cols) != 4 {
		t.Errorf("want 4 fallback Scrum lanes, got %d", len(cols))
	}
}

func TestStartSprint_BoardProvisioningIsIdempotent(t *testing.T) {
	svc, _, _, _, _, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	sp := seedSprint(t, sprintRepo, projID, sprintID)
	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := svc.StartSprint(sp); err != nil { // same (already active) sprint
		t.Fatalf("second start: %v", err)
	}
	var n int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM boards WHERE sprint_id=$1`, sprintID).Scan(&n); err != nil {
		t.Fatalf("count boards: %v", err)
	}
	if n != 1 {
		t.Errorf("want exactly 1 sprint board, got %d", n)
	}
}

func TestCompleteSprint_RemovesSprintBoard(t *testing.T) {
	svc, _, _, _, _, sprintRepo := newTestService(t)
	insertProject(t, svc.db, projID)
	sp := seedSprint(t, sprintRepo, projID, sprintID)
	if err := svc.StartSprint(sp); err != nil {
		t.Fatalf("start: %v", err)
	}
	boards := NewBoardRepo(svc.db)
	if sb, _ := boards.FindBySprint(sprintID); sb == nil {
		t.Fatal("expected a sprint board after start")
	}
	if _, err := svc.CompleteSprint(sp, EstimationUnitNone); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if sb, _ := boards.FindBySprint(sprintID); sb != nil {
		t.Error("sprint board should be removed after complete")
	}
	// Idempotent: removing again is a no-op.
	if _, err := svc.RemoveSprintBoard(sprintID); err != nil {
		t.Errorf("RemoveSprintBoard should be idempotent, got %v", err)
	}
}

func TestSprintRepo_FindOverlapping(t *testing.T) {
	db := newTestDB(t)
	insertProject(t, db, projID)
	repo := NewSprintRepo(db)
	mk := func(id, start, end, status string) {
		s := &Sprint{ID: id, ProjectID: projID, Name: id, StartDate: &start, EndDate: &end, Status: status, CreatedAt: "x", UpdatedAt: "x", Version: 1}
		if err := repo.Create(s); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("s1", "2024-01-01", "2024-01-14", SprintStatusPlanned)
	mk("done", "2024-02-01", "2024-02-14", SprintStatusCompleted)

	if o, _ := repo.FindOverlapping(projID, "2024-01-10", "2024-01-20", ""); o == nil {
		t.Error("expected overlap with s1")
	}
	if o, _ := repo.FindOverlapping(projID, "2024-01-14", "2024-01-20", ""); o == nil {
		t.Error("expected inclusive boundary overlap with s1")
	}
	if o, _ := repo.FindOverlapping(projID, "2024-01-15", "2024-01-31", ""); o != nil {
		t.Errorf("did not expect overlap, got %s", o.ID)
	}
	if o, _ := repo.FindOverlapping(projID, "2024-01-01", "2024-01-14", "s1"); o != nil {
		t.Error("a sprint must not overlap itself (excludeID)")
	}
	// Completed sprints never block new ones.
	if o, _ := repo.FindOverlapping(projID, "2024-02-05", "2024-02-10", ""); o != nil {
		t.Errorf("completed sprint should not block, got %s", o.ID)
	}
}
