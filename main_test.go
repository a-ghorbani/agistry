package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) {
	t.Helper()
	token = ""
	dbPath := filepath.Join(t.TempDir(), "test.db")
	var err error
	db, err = sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := initSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Registry-Token", token)
	}
	rr := httptest.NewRecorder()
	routes().ServeHTTP(rr, req)
	var m map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	return rr.Code, m
}

func TestRegisterAssignAgents(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"s1","cwd":"/w/TASK-1"}`)
	do(t, "POST", "/assign", `{"session_id":"s1","task":"TASK-1","role":"reviewer"}`)

	code, m := do(t, "GET", "/agents?task=TASK-1", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if m["count"].(float64) != 1 {
		t.Fatalf("want 1 agent, got %v", m["count"])
	}
	a := m["agents"].([]any)[0].(map[string]any)
	if a["role"] != "reviewer" || a["state"] != "active" {
		t.Fatalf("unexpected agent: %v", a)
	}
}

func TestSendInboxDrainsOnce(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"impl"}`)
	do(t, "POST", "/assign", `{"session_id":"impl","task":"TASK-1","role":"implementer"}`)
	do(t, "POST", "/send", `{"to":"TASK-1:implementer","from":"reviewer","msg":"go"}`)

	if _, m := do(t, "GET", "/inbox?session_id=impl", ""); m["count"].(float64) != 1 {
		t.Fatalf("first drain: want 1, got %v", m["count"])
	}
	if _, m := do(t, "GET", "/inbox?session_id=impl", ""); m["count"].(float64) != 0 {
		t.Fatalf("second drain: want 0, got %v", m["count"])
	}
}

func TestSendIdempotentOnMsgID(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"i2"}`)
	do(t, "POST", "/assign", `{"session_id":"i2","task":"T","role":"r"}`)
	do(t, "POST", "/send", `{"to":"T:r","msg":"a","msg_id":"dup"}`)
	do(t, "POST", "/send", `{"to":"T:r","msg":"a","msg_id":"dup"}`)

	if _, m := do(t, "GET", "/inbox?session_id=i2", ""); m["count"].(float64) != 1 {
		t.Fatalf("want 1 after dedup, got %v", m["count"])
	}
}

func TestInboxNoSessionLeak(t *testing.T) {
	setup(t)
	// A is the intended recipient; B is registered but has not joined a role yet
	// (so task="" role=""), which previously wildcard-matched session-targeted messages.
	do(t, "POST", "/register", `{"session_id":"A","cwd":"/x"}`)
	do(t, "POST", "/register", `{"session_id":"B","cwd":"/y"}`)
	do(t, "POST", "/send", `{"to":"A","from":"t","msg":"for A only"}`)

	if _, m := do(t, "GET", "/inbox?session_id=B", ""); m["count"].(float64) != 0 {
		t.Fatalf("leak: role-less B received a message addressed to session A (count=%v)", m["count"])
	}
	if _, m := do(t, "GET", "/inbox?session_id=A", ""); m["count"].(float64) != 1 {
		t.Fatalf("A should have its message, got %v", m["count"])
	}
}

func TestAuthRejectsWithoutToken(t *testing.T) {
	setup(t)
	token = "secret"
	defer func() { token = "" }()

	req := httptest.NewRequest("GET", "/agents", nil) // no token header
	rr := httptest.NewRecorder()
	routes().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("no token: want 401, got %d", rr.Code)
	}

	code, _ := do(t, "GET", "/agents", "") // do() sets the header
	if code != 200 {
		t.Fatalf("with token: want 200, got %d", code)
	}
}
