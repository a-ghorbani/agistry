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

func TestSendResolvesSessionPrefix(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"abcd1234-full-0001","cwd":"/x"}`)
	do(t, "POST", "/send", `{"to":"abcd1234","from":"t","msg":"hi via prefix"}`)
	if _, m := do(t, "GET", "/inbox?session_id=abcd1234-full-0001", ""); m["count"].(float64) != 1 {
		t.Fatalf("prefix send should reach the full session, got %v", m["count"])
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

func TestMessagesFeedDoesNotConsume(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"X"}`)
	do(t, "POST", "/send", `{"to":"X","from":"y","msg":"feed test"}`)

	if _, m := do(t, "GET", "/messages", ""); m["count"].(float64) < 1 {
		t.Fatalf("feed should list the message, got %v", m["count"])
	}
	// the feed must not mark anything delivered — the recipient still gets it
	if _, m := do(t, "GET", "/inbox?session_id=X", ""); m["count"].(float64) != 1 {
		t.Fatalf("feed consumed the message (inbox=%v)", m["count"])
	}
}

func TestSendSelfIgnored(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"S"}`)
	do(t, "POST", "/assign", `{"session_id":"S","task":"T","role":"impl"}`)

	if _, m := do(t, "POST", "/send", `{"to":"S","from":"S","msg":"to myself"}`); m["status"] != "self-ignored" {
		t.Fatalf("send to own session id should be self-ignored, got %v", m["status"])
	}
	if _, m := do(t, "POST", "/send", `{"to":"T:impl","from":"S","msg":"to my own role"}`); m["status"] != "self-ignored" {
		t.Fatalf("send to own task:role should be self-ignored, got %v", m["status"])
	}
	if _, m := do(t, "GET", "/inbox?session_id=S", ""); m["count"].(float64) != 0 {
		t.Fatalf("self messages must not be delivered, inbox=%v", m["count"])
	}
}

func TestJoinRejectsDuplicateRole(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"A"}`)
	do(t, "POST", "/register", `{"session_id":"B"}`)

	if code, _ := do(t, "POST", "/assign", `{"session_id":"A","task":"T","role":"impl"}`); code != 200 {
		t.Fatalf("first join should succeed, got %d", code)
	}
	if code, _ := do(t, "POST", "/assign", `{"session_id":"B","task":"T","role":"impl"}`); code != 409 {
		t.Fatalf("second join of same task:role should 409, got %d", code)
	}
	if code, _ := do(t, "POST", "/assign", `{"session_id":"A","task":"T","role":"impl"}`); code != 200 {
		t.Fatalf("re-join by the holder should succeed, got %d", code)
	}
}

func TestInboxPeekAndAck(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"X"}`)
	do(t, "POST", "/send", `{"to":"X","from":"y","msg":"m1","msg_id":"a1"}`)
	do(t, "POST", "/send", `{"to":"X","from":"y","msg":"m2","msg_id":"a2"}`)

	// peek returns both WITHOUT consuming — twice
	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 2 {
		t.Fatalf("peek should return 2, got %v", m["count"])
	}
	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 2 {
		t.Fatalf("peek must not consume; got %v on second peek", m["count"])
	}
	// ack only a1 → peek now returns just a2
	do(t, "POST", "/ack", `{"session_id":"X","msg_ids":["a1"]}`)
	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 1 {
		t.Fatalf("after acking a1, peek should be 1, got %v", m["count"])
	}
	// the unacked one still delivers via normal (consuming) inbox
	if _, m := do(t, "GET", "/inbox?session_id=X", ""); m["count"].(float64) != 1 {
		t.Fatalf("remaining message should still deliver, got %v", m["count"])
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
