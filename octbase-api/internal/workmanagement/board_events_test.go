package workmanagement_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// capturePublisher records the project-scoped events the workmanagement handler
// broadcasts on mutations, so tests can assert co-workers would see a refresh.
// It is concurrency-safe: Publish runs on the request goroutine while the test
// reads on its own.
type capturePublisher struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	projectID string
	payload   map[string]any
}

func (p *capturePublisher) Publish(projectID string, payload map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{projectID: projectID, payload: payload})
}

// lastOfType returns the most recent captured event whose activityType matches,
// failing the test if none was recorded.
func (p *capturePublisher) lastOfType(t *testing.T, actType string) capturedEvent {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.events) - 1; i >= 0; i-- {
		if p.events[i].payload["activityType"] == actType {
			return p.events[i]
		}
	}
	t.Fatalf("no board event of activityType %q was published (got %d events)", actType, len(p.events))
	return capturedEvent{}
}

// TestBoardEvents_TaskMutationsPublish verifies that task mutations broadcast a
// project-scoped "board.changed" event so other viewers of the board refresh
// automatically. This is the backend half of the board auto-refresh feature.
func TestBoardEvents_TaskMutationsPublish(t *testing.T) {
	db := testutil.NewTestDB(t)
	pub := &capturePublisher{}
	srv := testutil.NewTestServer(t, db, testutil.WithBoardEventPublisher(pub))
	pid := testutil.MustCreateProject(t, srv, "P")

	// Creating a task broadcasts a board.changed event scoped to the project,
	// carrying the actor (for the frontend's skip-own-changes filter) and the
	// new task id (so an open task panel can refresh in place).
	tid := testutil.MustCreateTask(t, srv, pid, "T1")
	ev := pub.lastOfType(t, "TASK_CREATED")
	if ev.projectID != pid {
		t.Fatalf("create event projectID = %q, want %q", ev.projectID, pid)
	}
	if ev.payload["type"] != "board.changed" {
		t.Fatalf("create event type = %v, want board.changed", ev.payload["type"])
	}
	if ev.payload["actorId"] != testutil.DemoUserID {
		t.Fatalf("create event actorId = %v, want %q", ev.payload["actorId"], testutil.DemoUserID)
	}
	if ev.payload["taskId"] != tid {
		t.Fatalf("create event taskId = %v, want %q", ev.payload["taskId"], tid)
	}

	// Moving the task onto a board column broadcasts TASK_MOVED. The move runs in
	// a transaction and publishes only after a successful commit.
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Lane", "PLANNED", 0)
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": col}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	mvEv := pub.lastOfType(t, "TASK_MOVED")
	if mvEv.projectID != pid {
		t.Fatalf("move event projectID = %q, want %q", mvEv.projectID, pid)
	}
	if mvEv.payload["taskId"] != tid {
		t.Fatalf("move event taskId = %v, want %q", mvEv.payload["taskId"], tid)
	}

	// Archiving hides the card from the board. The activity write happens inside
	// a transaction, so the publish is a distinct post-commit call — assert it
	// still fires (a prior gap).
	ar := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, ar, http.StatusOK)
	if ev := pub.lastOfType(t, "TASK_ARCHIVED"); ev.payload["taskId"] != tid {
		t.Fatalf("archive event taskId = %v, want %q", ev.payload["taskId"], tid)
	}

	// Deleting removes the card entirely. Delete only writes an audit entry (no
	// activity row), so its board broadcast is an explicit publish — assert it
	// fires so co-workers' boards drop the card.
	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNoContent)
	if ev := pub.lastOfType(t, "TASK_DELETED"); ev.payload["taskId"] != tid || ev.projectID != pid {
		t.Fatalf("delete event = %+v, want taskId %q project %q", ev.payload, tid, pid)
	}

	// A bulk action changes many cards at once but broadcasts a single
	// project-scoped refresh (no taskId), covering actions like set_priority
	// that write no per-task activity.
	b1 := testutil.MustCreateTask(t, srv, pid, "B1")
	b2 := testutil.MustCreateTask(t, srv, pid, "B2")
	bulk := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks/bulk",
		map[string]any{"action": "set_priority", "taskIds": []string{b1, b2}, "value": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, bulk, http.StatusOK)
	if ev := pub.lastOfType(t, "BULK_set_priority"); ev.projectID != pid || ev.payload["taskId"] != nil {
		t.Fatalf("bulk event = %+v, want project %q and no taskId", ev.payload, pid)
	}
}
