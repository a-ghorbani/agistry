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
	ttl = 600
	pendingTTL = 604800
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

func TestSendUnknownSessionRejected(t *testing.T) {
	setup(t)
	// no agent named "ghost" — a session target that resolves to nobody is a typo,
	// not late binding, so it must be rejected rather than queued to a black hole.
	code, m := do(t, "POST", "/send", `{"to":"ghost","from":"t","msg":"into the void"}`)
	if code != 400 || m["status"] == "queued" {
		t.Fatalf("unknown session should be rejected, got code=%d resp=%v", code, m)
	}
}

func TestSendLateBindingTaskRole(t *testing.T) {
	setup(t)
	// addressing a not-yet-joined TASK:role is the durable late-binding feature —
	// it must queue, then deliver once someone joins that role.
	if code, m := do(t, "POST", "/send", `{"to":"FUTURE:impl","from":"t","msg":"pick me up"}`); code != 200 || m["status"] != "queued" {
		t.Fatalf("late-binding send should queue, got code=%d resp=%v", code, m)
	}
	do(t, "POST", "/register", `{"session_id":"late"}`)
	do(t, "POST", "/assign", `{"session_id":"late","task":"FUTURE","role":"impl"}`)
	if _, m := do(t, "GET", "/inbox?session_id=late", ""); m["count"].(float64) != 1 {
		t.Fatalf("joiner should receive the late-bound message, got %v", m["count"])
	}
}

func TestInboxNoSessionLeak(t *testing.T) {
	setup(t)
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

func TestAssignClobberGuard(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"S"}`)
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","task":"T1","role":"a"}`); code != 200 {
		t.Fatalf("first assign should succeed, got %d", code)
	}
	// re-declaring the SAME identity is idempotent (the reconciling heartbeat relies on this)
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","task":"T1","role":"a"}`); code != 200 {
		t.Fatalf("idempotent re-assign of same identity should succeed, got %d", code)
	}
	// changing to a DIFFERENT identity without force is refused (subagent-clobber guard)
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","task":"T2","role":"b"}`); code != 409 {
		t.Fatalf("silent identity change should 409, got %d", code)
	}
	// with force it goes through
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","task":"T2","role":"b","force":true}`); code != 200 {
		t.Fatalf("forced identity change should succeed, got %d", code)
	}
}

func TestAssignTaskValidation(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"S"}`)
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","role":"impl","task":"a task with spaces"}`); code != 400 {
		t.Fatalf("task with spaces should be rejected, got %d", code)
	}
	long := strings.Repeat("x", 41)
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","role":"impl","task":"`+long+`"}`); code != 400 {
		t.Fatalf("over-long task should be rejected, got %d", code)
	}
	if code, _ := do(t, "POST", "/assign", `{"session_id":"S","role":"impl","task":"POC-31"}`); code != 200 {
		t.Fatalf("short tag should be accepted, got %d", code)
	}
}

func TestHeartbeatRecreatesStubAfterWipe(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"X","cwd":"/x"}`)
	// simulate a registry wipe while the session keeps running
	if _, err := db.Exec(`DELETE FROM agents`); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if code, m := do(t, "POST", "/heartbeat", `{"session_id":"X","cwd":"/x"}`); code != 200 || m["status"] != "alive" {
		t.Fatalf("heartbeat after wipe should re-create + report alive, got code=%d resp=%v", code, m)
	}
	if _, m := do(t, "GET", "/agents", ""); m["count"].(float64) != 1 {
		t.Fatalf("session should reappear after heartbeat, got %v agents", m["count"])
	}
}

func TestInboxPeekAndAck(t *testing.T) {
	setup(t)
	do(t, "POST", "/register", `{"session_id":"X"}`)
	do(t, "POST", "/send", `{"to":"X","from":"y","msg":"m1","msg_id":"a1"}`)
	do(t, "POST", "/send", `{"to":"X","from":"y","msg":"m2","msg_id":"a2"}`)

	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 2 {
		t.Fatalf("peek should return 2, got %v", m["count"])
	}
	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 2 {
		t.Fatalf("peek must not consume; got %v on second peek", m["count"])
	}
	do(t, "POST", "/ack", `{"session_id":"X","msg_ids":["a1"]}`)
	if _, m := do(t, "GET", "/inbox?peek=1&session_id=X", ""); m["count"].(float64) != 1 {
		t.Fatalf("after acking a1, peek should be 1, got %v", m["count"])
	}
	if _, m := do(t, "GET", "/inbox?session_id=X", ""); m["count"].(float64) != 1 {
		t.Fatalf("remaining message should still deliver, got %v", m["count"])
	}
}

func TestDeadLetterUnclaimedPending(t *testing.T) {
	setup(t)
	pendingTTL = 1000
	do(t, "POST", "/register", `{"session_id":"X"}`)
	do(t, "POST", "/send", `{"to":"FUTURE:nobody","from":"y","msg":"unclaimed","msg_id":"old1"}`)
	// age it well past the pending TTL
	if _, err := db.Exec(`UPDATE messages SET created_at=1 WHERE msg_id='old1'`); err != nil {
		t.Fatalf("age msg: %v", err)
	}
	reapOnce(now())

	// dead-lettered: not silently deleted — still visible in the feed, flagged
	_, m := do(t, "GET", "/messages", "")
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("dead-lettered message should remain visible, got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["dead_lettered"].(float64) != 1 {
		t.Fatalf("message should be flagged dead_lettered, got %v", msgs[0])
	}
	// and it must NOT deliver to a late joiner anymore
	do(t, "POST", "/register", `{"session_id":"j"}`)
	do(t, "POST", "/assign", `{"session_id":"j","task":"FUTURE","role":"nobody"}`)
	if _, m := do(t, "GET", "/inbox?session_id=j", ""); m["count"].(float64) != 0 {
		t.Fatalf("dead-lettered message must not be delivered, got %v", m["count"])
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
