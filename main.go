// Command agistry is a lightweight, fault-tolerant registry + mailbox for
// coordinating agent processes (e.g. Claude Code instances): who is working on
// which task, in which role, and a durable mailbox for handing off between them.
//
// Single static binary, SQLite (WAL) for durable state, a TTL reaper for
// self-healing liveness, and an embedded read-only web dashboard.
//
// Schema policy: initSchema is authoritative and greenfield (CREATE TABLE … IF NOT
// EXISTS, no in-repo migrations). State is disposable — agents reconcile their
// identity on every heartbeat, liveness self-heals via TTL, delivered messages GC.
// A schema change ships as a clean cutover: stop / delete the db file / restart.
package main

import (
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

//go:embed web/index.html
var uiHTML string

var (
	db         *sql.DB
	token      string
	ttl        int64
	pendingTTL int64
	seq        uint64
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB cap on request bodies
	maxTaskLen   = 40       // a task is a short grouping tag, not a description
)

func now() int64 { return time.Now().Unix() }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func main() {
	addr := env("REGISTRY_ADDR", ":7070")
	dbPath := env("REGISTRY_DB", "registry.db")
	token = os.Getenv("REGISTRY_TOKEN")
	ttl = envInt("REGISTRY_TTL_SECONDS", 600)
	pendingTTL = envInt("REGISTRY_PENDING_TTL_SECONDS", 604800) // 7d: pending handoffs may wait days

	var err error
	db, err = sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // single writer; serializes all access (no read concurrency by design)
	if err := initSchema(); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	go reaper()

	if token == "" {
		log.Println("WARNING: REGISTRY_TOKEN unset — auth disabled (dev only)")
	}
	log.Printf("agistry listening on %s (db=%s ttl=%ds pending_ttl=%ds)", addr, dbPath, ttl, pendingTTL)
	srv := &http.Server{
		Addr:              addr,
		Handler:           routes(),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	log.Fatal(srv.ListenAndServe())
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", auth(handleRegister))
	mux.HandleFunc("/assign", auth(handleAssign))
	mux.HandleFunc("/heartbeat", auth(handleHeartbeat))
	mux.HandleFunc("/deregister", auth(handleDeregister))
	mux.HandleFunc("/agents", auth(handleAgents))
	mux.HandleFunc("/send", auth(handleSend))
	mux.HandleFunc("/inbox", auth(handleInbox))
	mux.HandleFunc("/ack", auth(handleAck))
	mux.HandleFunc("/messages", auth(handleMessages))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("/", handleUI) // unauthenticated page shell; data calls carry the token
	return mux
}

func initSchema() error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS agents (
  session_id    TEXT PRIMARY KEY,
  task          TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT '',
  cwd           TEXT NOT NULL DEFAULT '',
  host          TEXT NOT NULL DEFAULT '',
  state         TEXT NOT NULL DEFAULT 'unassigned',
  registered_at INTEGER NOT NULL DEFAULT 0,
  last_seen     INTEGER NOT NULL DEFAULT 0
);
-- single owner per (task,role) among live, fully-assigned agents; lets the DB enforce
-- the invariant atomically instead of a racy check-then-act. Stubs (empty task/role)
-- and gone agents are excluded so they never collide.
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_taskrole
  ON agents(task, role) WHERE state <> 'gone' AND task <> '' AND role <> '';
CREATE TABLE IF NOT EXISTS messages (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  msg_id           TEXT UNIQUE,
  to_session       TEXT NOT NULL DEFAULT '',
  to_task          TEXT NOT NULL DEFAULT '',
  to_role          TEXT NOT NULL DEFAULT '',
  from_session     TEXT NOT NULL DEFAULT '',
  body             TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL DEFAULT 0,
  delivered_at     INTEGER,  -- NULL = pending (not yet delivered)
  dead_lettered_at INTEGER   -- NULL = not dead-lettered; set when a pending msg expires unclaimed
);
CREATE INDEX IF NOT EXISTS idx_msg_pending ON messages(to_session, to_task, to_role)
  WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
`)
	return err
}

// ---- middleware + helpers ----

func auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := r.Header.Get("X-Registry-Token")
			if got == "" {
				got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h(w, r)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any)       { writeJSON(w, http.StatusOK, v) }
func bad(w http.ResponseWriter, msg string) { writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg}) }
func conflict(w http.ResponseWriter, v any) { writeJSON(w, http.StatusConflict, v) }
func fail(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// validTaskTag enforces that a task is a short grouping tag (id/slug), not a
// description or status update — long free-text tasks make TASK:role addressing
// unusable and clutter the dashboard.
func validTaskTag(s string) bool {
	if len(s) > maxTaskLen {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func newMsgID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(atomic.AddUint64(&seq, 1), 36)
}

// ---- handlers ----

// POST /register {session_id, cwd, host}
// Creates an identity stub. Idempotent. Never clobbers an existing role/task.
func handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Host      string `json:"host"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.SessionID == "" {
		bad(w, "session_id required")
		return
	}
	t := now()
	_, err := db.Exec(`
INSERT INTO agents(session_id, cwd, host, state, registered_at, last_seen)
VALUES(?, ?, ?, 'unassigned', ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  cwd=excluded.cwd, host=excluded.host, last_seen=excluded.last_seen`,
		in.SessionID, in.Cwd, in.Host, t, t)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"status": "registered", "session_id": in.SessionID})
}

// POST /assign {session_id, task, role, force}
// Declares the semantic identity (task + role). Idempotent upsert on session_id.
// Refuses to silently change an already-declared identity without force (stops a
// subagent from clobbering its parent's registration), and the DB enforces a single
// owner per (task,role).
func handleAssign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Task      string `json:"task"`
		Role      string `json:"role"`
		Force     bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.SessionID == "" || in.Role == "" {
		bad(w, "session_id and role required")
		return
	}
	if in.Task != "" && !validTaskTag(in.Task) {
		bad(w, "task must be a short tag (≤40 chars, no spaces) — a work-item id or slug like POC-31, not a description; put status/details in a message")
		return
	}

	// clobber guard: don't silently overwrite a different existing identity.
	var curTask, curRole string
	_ = db.QueryRow(`SELECT task, role FROM agents WHERE session_id=?`, in.SessionID).Scan(&curTask, &curRole)
	if (curTask != "" || curRole != "") && (curTask != in.Task || curRole != in.Role) && !in.Force {
		conflict(w, map[string]any{
			"error":   "session already registered with a different identity; pass force=true to change it",
			"current": curTask + ":" + curRole,
		})
		return
	}

	// single owner per task:role — reject if another live session already holds it
	// (nice held_by error; the unique index below is the race-proof backstop).
	if in.Task != "" {
		var other string
		_ = db.QueryRow(`SELECT session_id FROM agents WHERE task=? AND role=? AND state<>'gone' AND session_id<>?`,
			in.Task, in.Role, in.SessionID).Scan(&other)
		if other != "" {
			conflict(w, map[string]any{"error": "role already held on this task", "held_by": other})
			return
		}
	}
	t := now()
	_, err := db.Exec(`
INSERT INTO agents(session_id, task, role, state, registered_at, last_seen)
VALUES(?, ?, ?, 'active', ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  task=excluded.task, role=excluded.role, state='active', last_seen=excluded.last_seen`,
		in.SessionID, in.Task, in.Role, t, t)
	if err != nil {
		if isUniqueViolation(err) {
			conflict(w, map[string]any{"error": "role already held on this task"})
			return
		}
		fail(w, err)
		return
	}
	ok(w, map[string]any{"status": "assigned", "task": in.Task, "role": in.Role})
}

// POST /heartbeat {session_id, cwd, host}
// Asserts liveness. If the session is unknown (e.g. the registry db was wiped), it
// re-creates a stub so a still-running session reappears; the client's reconciling
// /assign restores its role.
func handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Host      string `json:"host"`
	}
	if err := readJSON(w, r, &in); err != nil || in.SessionID == "" {
		bad(w, "session_id required")
		return
	}
	t := now()
	res, err := db.Exec(`
UPDATE agents
SET last_seen=?, state=CASE WHEN role!='' THEN 'active' ELSE 'unassigned' END
WHERE session_id=?`, t, in.SessionID)
	if err != nil {
		fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := db.Exec(`
INSERT INTO agents(session_id, cwd, host, state, registered_at, last_seen)
VALUES(?, ?, ?, 'unassigned', ?, ?)
ON CONFLICT(session_id) DO UPDATE SET last_seen=excluded.last_seen`,
			in.SessionID, in.Cwd, in.Host, t, t); err != nil {
			fail(w, err)
			return
		}
	}
	ok(w, map[string]string{"status": "alive"})
}

// POST /deregister {session_id}
func handleDeregister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if err := readJSON(w, r, &in); err != nil || in.SessionID == "" {
		bad(w, "session_id required")
		return
	}
	if _, err := db.Exec(`UPDATE agents SET state='gone', last_seen=? WHERE session_id=?`, now(), in.SessionID); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]string{"status": "deregistered"})
}

type agentRow struct {
	SessionID    string `json:"session_id"`
	Task         string `json:"task"`
	Role         string `json:"role"`
	Cwd          string `json:"cwd"`
	Host         string `json:"host"`
	State        string `json:"state"`
	RegisteredAt int64  `json:"registered_at"`
	LastSeen     int64  `json:"last_seen"`
}

// GET /agents?task=&role=&state=&all=1
// Default hides 'gone' entries; pass all=1 to include them.
func handleAgents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	if v := q.Get("task"); v != "" {
		where = append(where, "task=?")
		args = append(args, v)
	}
	if v := q.Get("role"); v != "" {
		where = append(where, "role=?")
		args = append(args, v)
	}
	if v := q.Get("state"); v != "" {
		where = append(where, "state=?")
		args = append(args, v)
	} else if q.Get("all") != "1" {
		where = append(where, "state!='gone'")
	}
	rows, err := db.Query(`
SELECT session_id, task, role, cwd, host, state, registered_at, last_seen
FROM agents WHERE `+strings.Join(where, " AND ")+` ORDER BY task, role`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []agentRow{}
	for rows.Next() {
		var a agentRow
		if err := rows.Scan(&a.SessionID, &a.Task, &a.Role, &a.Cwd, &a.Host, &a.State, &a.RegisteredAt, &a.LastSeen); err != nil {
			fail(w, err)
			return
		}
		out = append(out, a)
	}
	ok(w, map[string]any{"agents": out, "count": len(out)})
}

// resolveSession returns the full session_id for an exact id, or for an unambiguous
// prefix of a live agent — so a human can address the short id shown in the dashboard
// (e.g. `to: "8edb7472"`). Falls back to the input unchanged if there is no unique
// live match (the caller then rejects it rather than queue to nobody).
func resolveSession(s string) string {
	var exact string
	_ = db.QueryRow(`SELECT session_id FROM agents WHERE session_id=?`, s).Scan(&exact)
	if exact != "" {
		return exact
	}
	rows, err := db.Query(`SELECT session_id FROM agents WHERE state<>'gone' AND session_id LIKE ?`, s+"%")
	if err != nil {
		return s
	}
	defer rows.Close()
	var match string
	n := 0
	for rows.Next() {
		var x string
		if rows.Scan(&x) == nil {
			match = x
			n++
		}
	}
	if n == 1 {
		return match
	}
	return s
}

// liveSession reports whether a non-gone agent with this exact session_id exists.
func liveSession(sid string) bool {
	var x string
	_ = db.QueryRow(`SELECT session_id FROM agents WHERE session_id=? AND state<>'gone'`, sid).Scan(&x)
	return x != ""
}

// isSelfTarget reports whether a message from `from` is addressed back to itself —
// either directly (to_session == from) or to a task:role that `from` currently holds.
func isSelfTarget(from, toSession, toTask, toRole string) bool {
	if toSession != "" {
		return toSession == from
	}
	if toRole != "" {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM agents WHERE session_id=? AND task=? AND role=?`, from, toTask, toRole).Scan(&n)
		return n > 0
	}
	return false
}

// POST /send {to, task, role, from, msg, msg_id}
// Target is either `to` ("TASK-x:role" or a session_id / unique prefix) or explicit task+role.
// Idempotent on msg_id. Stores in the mailbox (the source of truth).
//
// A session target must resolve to a live agent (else it's a typo → rejected). A
// TASK:role target is NOT required to be live: addressing a not-yet-joined role is
// durable late binding, the headline feature — it waits until someone joins.
func handleSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To    string `json:"to"`
		Task  string `json:"task"`
		Role  string `json:"role"`
		From  string `json:"from"`
		Msg   string `json:"msg"`
		MsgID string `json:"msg_id"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	toSession, toTask, toRole := "", in.Task, in.Role
	if in.To != "" {
		if strings.Contains(in.To, ":") {
			parts := strings.SplitN(in.To, ":", 2)
			toTask, toRole = parts[0], parts[1]
		} else {
			toSession = resolveSession(in.To)
			if !liveSession(toSession) {
				bad(w, "no live agent matches session '"+in.To+"' — check the id, or address TASK:role")
				return
			}
		}
	}
	if toSession == "" && toRole == "" {
		bad(w, "need a target: `to` (session or TASK:role), or task+role")
		return
	}
	if in.Msg == "" {
		bad(w, "msg required")
		return
	}
	// no self-delivery: a session must never message itself (by id, or its own
	// task:role) — that caused the pipeline feedback loop.
	if in.From != "" && isSelfTarget(in.From, toSession, toTask, toRole) {
		ok(w, map[string]any{"status": "self-ignored"})
		return
	}
	if in.MsgID == "" {
		in.MsgID = newMsgID()
	}
	_, err := db.Exec(`
INSERT OR IGNORE INTO messages(msg_id, to_session, to_task, to_role, from_session, body, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		in.MsgID, toSession, toTask, toRole, in.From, in.Msg, now())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"status": "queued", "msg_id": in.MsgID})
}

type inboxMsg struct {
	MsgID     string `json:"msg_id"`
	From      string `json:"from"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// GET /inbox?session_id=&peek=1
// Returns messages addressed to this session or its task:role. The default path
// consumes atomically (UPDATE … RETURNING in one statement — no read-then-mark race);
// peek=1 returns without consuming (the channel peeks, then /ack's only what it
// actually pushed — at-least-once).
func handleInbox(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		bad(w, "session_id required")
		return
	}
	var task, role string
	_ = db.QueryRow(`SELECT task, role FROM agents WHERE session_id=?`, sid).Scan(&task, &role)
	_, _ = db.Exec(`UPDATE agents SET last_seen=? WHERE session_id=?`, now(), sid) // touch liveness on poll

	// A message matches if addressed directly to the session, or (only for task-addressed
	// messages, which carry no to_session) to its task with a matching role or a whole-task
	// wildcard. Requiring to_task<>'' stops a session-targeted message (empty task/role)
	// from leaking to every not-yet-joined session (also empty task/role).
	const match = `(
  to_session=?
  OR (to_session='' AND to_task<>'' AND to_task=? AND (to_role=? OR to_role=''))
)`
	const cols = `msg_id, from_session, body, created_at`
	const pending = `delivered_at IS NULL AND dead_lettered_at IS NULL`

	var rows *sql.Rows
	var err error
	if r.URL.Query().Get("peek") == "1" {
		rows, err = db.Query(`SELECT `+cols+` FROM messages WHERE `+pending+` AND `+match+` ORDER BY id`, sid, task, role)
	} else {
		// atomic dequeue: select and mark delivered in one statement.
		rows, err = db.Query(`UPDATE messages SET delivered_at=`+strconv.FormatInt(now(), 10)+
			` WHERE `+pending+` AND `+match+` RETURNING `+cols, sid, task, role)
	}
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []inboxMsg{}
	for rows.Next() {
		var m inboxMsg
		if err := rows.Scan(&m.MsgID, &m.From, &m.Body, &m.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		out = append(out, m)
	}
	ok(w, map[string]any{"messages": out, "count": len(out)})
}

// POST /ack {session_id, msg_ids:[...]}
// Marks specific messages delivered — used by the channel after a successful push.
func handleAck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string   `json:"session_id"`
		MsgIDs    []string `json:"msg_ids"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if len(in.MsgIDs) == 0 {
		ok(w, map[string]any{"acked": 0})
		return
	}
	args := make([]any, len(in.MsgIDs)+1)
	args[0] = now()
	for i, id := range in.MsgIDs {
		args[i+1] = id
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(in.MsgIDs)), ",")
	res, err := db.Exec(`UPDATE messages SET delivered_at=? WHERE delivered_at IS NULL AND msg_id IN (`+ph+`)`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	n, _ := res.RowsAffected()
	ok(w, map[string]any{"acked": n})
}

// GET /messages?limit=N
// Read-only recent message feed for the dashboard. Does NOT consume anything.
func handleMessages(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := db.Query(`
SELECT msg_id, from_session, to_session, to_task, to_role, body, delivered_at, dead_lettered_at, created_at
FROM messages ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type msg struct {
		MsgID        string `json:"msg_id"`
		From         string `json:"from"`
		ToSession    string `json:"to_session"`
		ToTask       string `json:"to_task"`
		ToRole       string `json:"to_role"`
		Body         string `json:"body"`
		Delivered    int    `json:"delivered"`
		DeadLettered int    `json:"dead_lettered"`
		CreatedAt    int64  `json:"created_at"`
	}
	out := []msg{}
	for rows.Next() {
		var m msg
		var deliveredAt, deadAt sql.NullInt64
		if err := rows.Scan(&m.MsgID, &m.From, &m.ToSession, &m.ToTask, &m.ToRole, &m.Body, &deliveredAt, &deadAt, &m.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		if deliveredAt.Valid {
			m.Delivered = 1
		}
		if deadAt.Valid {
			m.DeadLettered = 1
		}
		out = append(out, m)
	}
	ok(w, map[string]any{"messages": out, "count": len(out)})
}

// reapOnce performs one self-healing pass: age out silent agents, dead-letter pending
// messages that were never claimed (visible, not deleted, so a sender can see the
// handoff was never picked up), and GC long-settled messages.
func reapOnce(t int64) {
	if _, err := db.Exec(`UPDATE agents SET state='gone' WHERE state!='gone' AND last_seen < ?`, t-ttl); err != nil {
		log.Printf("reaper agents: %v", err)
	}
	if _, err := db.Exec(`UPDATE messages SET dead_lettered_at=? WHERE delivered_at IS NULL AND dead_lettered_at IS NULL AND created_at < ?`, t, t-pendingTTL); err != nil {
		log.Printf("reaper dead-letter: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM messages WHERE delivered_at IS NOT NULL AND delivered_at < ?`, t-86400); err != nil {
		log.Printf("reaper delivered gc: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM messages WHERE dead_lettered_at IS NOT NULL AND dead_lettered_at < ?`, t-pendingTTL); err != nil {
		log.Printf("reaper dead-letter gc: %v", err)
	}
}

func reaper() {
	for range time.Tick(60 * time.Second) {
		reapOnce(now())
	}
}

// handleUI serves the dashboard shell. It is intentionally unauthenticated and
// contains no secrets — the page asks for the token and sends it on data calls.
func handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if dir := os.Getenv("AGISTRY_WEB_DIR"); dir != "" { // dev mode: serve from disk for fast UI iteration
		if b, err := os.ReadFile(filepath.Join(dir, "index.html")); err == nil {
			_, _ = w.Write(b)
			return
		}
	}
	_, _ = w.Write([]byte(uiHTML))
}
