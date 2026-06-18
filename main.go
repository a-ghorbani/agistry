// Command agistry is a lightweight, fault-tolerant registry + mailbox for
// coordinating agent processes (e.g. Claude Code instances): who is working on
// which task, in which role, and a durable mailbox for handing off between them.
//
// Single static binary, SQLite (WAL) for durable state, a TTL reaper for
// self-healing liveness, and an embedded read-only web dashboard.
package main

import (
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed web/index.html
var uiHTML string

var (
	db    *sql.DB
	token string
	ttl   int64
	seq   uint64
)

const maxBodyBytes = 1 << 20 // 1 MiB cap on request bodies

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

	var err error
	db, err = sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // serialize writes; WAL gives concurrent reads at this scale
	if err := initSchema(); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	go reaper()

	if token == "" {
		log.Println("WARNING: REGISTRY_TOKEN unset — auth disabled (dev only)")
	}
	log.Printf("agistry listening on %s (db=%s ttl=%ds)", addr, dbPath, ttl)
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
  handle        TEXT NOT NULL DEFAULT '',
  notify        TEXT NOT NULL DEFAULT 'poll',
  state         TEXT NOT NULL DEFAULT 'unassigned',
  registered_at INTEGER NOT NULL DEFAULT 0,
  last_seen     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  msg_id       TEXT UNIQUE,
  to_session   TEXT NOT NULL DEFAULT '',
  to_task      TEXT NOT NULL DEFAULT '',
  to_role      TEXT NOT NULL DEFAULT '',
  from_session TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL DEFAULT 0,
  delivered    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_msg_undelivered ON messages(delivered, to_session, to_task, to_role);
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
func fail(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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

// POST /assign {session_id, task, role, handle, notify}
// Enriches the entry with the semantic role. Idempotent upsert.
func handleAssign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Task      string `json:"task"`
		Role      string `json:"role"`
		Handle    string `json:"handle"`
		Notify    string `json:"notify"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.SessionID == "" || in.Role == "" {
		bad(w, "session_id and role required")
		return
	}
	if in.Notify == "" {
		if in.Handle != "" {
			in.Notify = "push"
		} else {
			in.Notify = "poll"
		}
	}
	t := now()
	_, err := db.Exec(`
INSERT INTO agents(session_id, task, role, handle, notify, state, registered_at, last_seen)
VALUES(?, ?, ?, ?, ?, 'active', ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  task=excluded.task, role=excluded.role, handle=excluded.handle,
  notify=excluded.notify, state='active', last_seen=excluded.last_seen`,
		in.SessionID, in.Task, in.Role, in.Handle, in.Notify, t, t)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"status": "assigned", "task": in.Task, "role": in.Role})
}

// POST /heartbeat {session_id}
func handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if err := readJSON(w, r, &in); err != nil || in.SessionID == "" {
		bad(w, "session_id required")
		return
	}
	// Bump last_seen and revive from 'gone' to the appropriate live state.
	res, err := db.Exec(`
UPDATE agents
SET last_seen=?, state=CASE WHEN role!='' THEN 'active' ELSE 'unassigned' END
WHERE session_id=?`, now(), in.SessionID)
	if err != nil {
		fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown session_id"})
		return
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
	Handle       string `json:"handle"`
	Notify       string `json:"notify"`
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
SELECT session_id, task, role, cwd, host, handle, notify, state, registered_at, last_seen
FROM agents WHERE `+strings.Join(where, " AND ")+` ORDER BY task, role`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []agentRow{}
	for rows.Next() {
		var a agentRow
		if err := rows.Scan(&a.SessionID, &a.Task, &a.Role, &a.Cwd, &a.Host, &a.Handle, &a.Notify, &a.State, &a.RegisteredAt, &a.LastSeen); err != nil {
			fail(w, err)
			return
		}
		out = append(out, a)
	}
	ok(w, map[string]any{"agents": out, "count": len(out)})
}

// POST /send {to, task, role, from, msg, msg_id}
// Target is either `to` ("TASK-x:role" or a session_id) or explicit task+role.
// Idempotent on msg_id. Stores in the mailbox; best-effort push if target wants it.
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
			toSession = in.To
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
	go tryPush(toSession, toTask, toRole) // best-effort wake; mailbox is the source of truth
	ok(w, map[string]any{"status": "queued", "msg_id": in.MsgID})
}

// GET /inbox?session_id=
// Drains undelivered messages addressed to this session or its task:role.
func handleInbox(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		bad(w, "session_id required")
		return
	}
	var task, role string
	_ = db.QueryRow(`SELECT task, role FROM agents WHERE session_id=?`, sid).Scan(&task, &role)
	_, _ = db.Exec(`UPDATE agents SET last_seen=? WHERE session_id=?`, now(), sid) // touch liveness on poll

	rows, err := db.Query(`
SELECT id, msg_id, from_session, body, created_at FROM messages
WHERE delivered=0 AND (
  to_session=? OR
  (to_task=? AND to_role=?) OR
  (to_task=? AND to_role='')
) ORDER BY id`, sid, task, role, task)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	type msg struct {
		ID        int64  `json:"-"`
		MsgID     string `json:"msg_id"`
		From      string `json:"from"`
		Body      string `json:"body"`
		CreatedAt int64  `json:"created_at"`
	}
	out := []msg{}
	ids := []any{}
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.ID, &m.MsgID, &m.From, &m.Body, &m.CreatedAt); err != nil {
			fail(w, err)
			return
		}
		out = append(out, m)
		ids = append(ids, m.ID)
	}
	if len(ids) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		_, _ = db.Exec(`UPDATE messages SET delivered=1 WHERE id IN (`+ph+`)`, ids...)
	}
	ok(w, map[string]any{"messages": out, "count": len(out)})
}

// tryPush is a best-effort doorbell to a push-capable target's handle.
// The mailbox remains the source of truth; failures here are non-fatal.
func tryPush(toSession, toTask, toRole string) {
	var handle, notify string
	var err error
	if toSession != "" {
		err = db.QueryRow(`SELECT handle, notify FROM agents WHERE session_id=? AND state!='gone'`, toSession).Scan(&handle, &notify)
	} else {
		err = db.QueryRow(`SELECT handle, notify FROM agents WHERE task=? AND role=? AND state!='gone' ORDER BY last_seen DESC LIMIT 1`, toTask, toRole).Scan(&handle, &notify)
	}
	if err != nil || notify != "push" || handle == "" {
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodPost, handle, strings.NewReader(`{"event":"mailbox"}`))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// reaper marks agents stale (gone) past the TTL and prunes old delivered messages.
func reaper() {
	for range time.Tick(60 * time.Second) {
		cutoff := now() - ttl
		if _, err := db.Exec(`UPDATE agents SET state='gone' WHERE state!='gone' AND last_seen < ?`, cutoff); err != nil {
			log.Printf("reaper agents: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM messages WHERE delivered=1 AND created_at < ?`, now()-86400); err != nil {
			log.Printf("reaper messages: %v", err)
		}
	}
}

// handleUI serves the dashboard shell. It is intentionally unauthenticated and
// contains no secrets — the page asks for the token and sends it on /agents calls.
func handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(uiHTML))
}
