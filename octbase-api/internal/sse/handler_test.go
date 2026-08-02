package sse_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/testutil"
)

// sseFixture creates a project owned by DemoUserID and an httptest server that
// mounts the SSE routes behind OptionalJWT middleware, exactly as main.go does.
func sseFixture(t *testing.T) (string, *sse.Hub, *httptest.Server) {
	t.Helper()
	db := testutil.NewTestDB(t)
	if db == nil {
		return "", nil, nil
	}
	appSrv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, appSrv, "SSE Proj")

	hub := sse.NewHub()
	go hub.Run()
	provider := auth.NewEmailProvider(db, testutil.TestJWTSecret)
	h := sse.NewHandler(db, hub, provider)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.OptionalJWTMiddleware(provider))
		h.RegisterRoutes(r)
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return pid, hub, ts
}

func get(t *testing.T, ts *httptest.Server, path, bearer, query string) *http.Response {
	t.Helper()
	url := ts.URL + path
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestPresence_MemberOK(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", testutil.TokenForUser(testutil.DemoUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPresence_TokenQueryParam(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	// No Authorization header — auth via ?token= (EventSource fallback).
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", "", "token="+testutil.TokenForUser(testutil.DemoUserID))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPresence_Unauthorized(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", "", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPresence_Forbidden_NotMember(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	// GuestUserID was never added to the project.
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", testutil.TokenForUser(testutil.GuestUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestStream_Unauthorized(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	resp := get(t, ts, "/api/v1/projects/"+pid+"/events", "", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStream_Forbidden_NotMember(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	resp := get(t, ts, "/api/v1/projects/"+pid+"/events", testutil.TokenForUser(testutil.GuestUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestPresence_DisabledAccount_Unauthorized(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	// A disabled account is rejected even with a still-valid access token.
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", testutil.TokenForUser(testutil.DisabledUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStream_DisabledAccount_Unauthorized(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	resp := get(t, ts, "/api/v1/projects/"+pid+"/events", testutil.TokenForUser(testutil.DisabledUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPresence_SuperAdmin_NonMemberOK(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	// SuperAdminUserID is not a member of the project, but a Super Admin has
	// access without a membership row (matching the board/task endpoints).
	resp := get(t, ts, "/api/v1/projects/"+pid+"/presence", testutil.TokenForUser(testutil.SuperAdminUserID), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStream_SuperAdmin_NonMemberOK(t *testing.T) {
	pid, _, ts := sseFixture(t)
	if ts == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Super Admin streams the project despite having no membership row, via the
	// ?token= EventSource fallback.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/v1/projects/"+pid+"/events?token="+testutil.TokenForUser(testutil.SuperAdminUserID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStream_ReceivesPingAndEvent(t *testing.T) {
	pid, hub, ts := sseFixture(t)
	if ts == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/v1/projects/"+pid+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.DemoUserID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	// First the handler writes the ": ping" comment line.
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ": ping") {
		t.Fatalf("expected initial ping, got %q (err=%v)", line, err)
	}

	// Wait until the subscription is registered, then publish.
	deadline := time.Now().Add(2 * time.Second)
	for len(hub.Presence(pid)) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	hub.Publish(pid, map[string]interface{}{"type": "task.updated", "id": "abc"})

	// Read lines until we see the data frame.
	got := ""
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			l, e := reader.ReadString('\n')
			got += l
			if strings.Contains(got, "task.updated") || e != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published event")
	}
	if !strings.Contains(got, `"type":"task.updated"`) {
		t.Fatalf("expected published event in stream, got %q", got)
	}
}
