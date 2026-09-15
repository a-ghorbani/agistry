// Conversations: the message feed regrouped as two-party chats, for the dashboard.
//
// Messages are mostly addressed TASK:role while the sender is stored only as a session
// id, so the two halves of an exchange look unrelated on the wire:
//
//	8edb7472 -> POC-94:reviewer
//	3fa1c2d0 -> POC-94:implementer
//
// Pairing them needs each sender named the way it is addressed. identitySQL does that
// at read time from the agents table, whose rows survive an agent going gone, so it
// still resolves for finished work.
package main

import (
	"net/http"
	"strconv"
	"unicode/utf8"
)

// messageRetention is how long a finished (delivered or dead-lettered) message stays
// readable. Bodies are short text, so a month of history costs megabytes; the
// dashboard pages it rather than loading it whole.
var messageRetention int64

// identitySQL names both ends of every message. A sender is its agent's task:role, or
// its session id if it has no role or no row. A recipient is the TASK:role it was
// addressed to ("*" for a whole-task wildcard), or for a session-addressed message that
// session's task:role.
//
// Resolution is at read time, so a session that later changed role with --force shows
// its old messages under the new name. Recording identity at send time would avoid
// that, at the cost of a schema cutover.
const identitySQL = `
WITH m AS (
  SELECT x.id, x.msg_id, x.from_session, x.to_session, x.to_task, x.to_role, x.body,
         x.created_at, x.delivered_at, x.dead_lettered_at,
         COALESCE(fa.task, '') AS from_task, COALESCE(fa.role, '') AS from_role,
         CASE WHEN x.from_session = '' THEN '?'
              WHEN COALESCE(fa.role, '') <> '' THEN fa.task || ':' || fa.role
              ELSE x.from_session END AS src,
         CASE WHEN x.to_session = '' THEN x.to_task || ':' || CASE WHEN x.to_role = '' THEN '*' ELSE x.to_role END
              WHEN COALESCE(ta.role, '') <> '' THEN ta.task || ':' || ta.role
              ELSE x.to_session END AS dst
  FROM messages x
  LEFT JOIN agents fa ON fa.session_id = x.from_session
  LEFT JOIN agents ta ON ta.session_id = x.to_session
)`

func clampInt(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

type convRow struct {
	A            string `json:"a"`
	B            string `json:"b"`
	Count        int    `json:"count"`
	Pending      int    `json:"pending"`
	DeadLettered int    `json:"dead_lettered"`
	LastAt       int64  `json:"last_at"`
	LastFrom     string `json:"last_from"`
	LastBody     string `json:"last_body"`
}

// GET /conversations?limit=&q=
// One row per pair of participants, most recent first — enough to draw a chat list
// without shipping message bodies. Only the latest message's body comes back, clipped.
// q filters on participant names (case-insensitive substring).
func handleConversations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), 100, 1, 500)
	filter := q.Get("q")
	rows, err := db.Query(identitySQL+`,
g AS (
  SELECT MIN(src, dst) AS a, MAX(src, dst) AS b, COUNT(*) AS n, MAX(id) AS last_id,
         SUM(CASE WHEN delivered_at IS NULL AND dead_lettered_at IS NULL THEN 1 ELSE 0 END) AS pending,
         SUM(CASE WHEN dead_lettered_at IS NOT NULL THEN 1 ELSE 0 END) AS dead
  FROM m GROUP BY MIN(src, dst), MAX(src, dst)
)
SELECT g.a, g.b, g.n, g.pending, g.dead, m.created_at, m.src, m.body
FROM g JOIN m ON m.id = g.last_id
WHERE ? = '' OR instr(lower(g.a), lower(?)) > 0 OR instr(lower(g.b), lower(?)) > 0
ORDER BY g.last_id DESC LIMIT ?`, filter, filter, filter, limit)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []convRow{}
	for rows.Next() {
		var c convRow
		if err := rows.Scan(&c.A, &c.B, &c.Count, &c.Pending, &c.DeadLettered, &c.LastAt, &c.LastFrom, &c.LastBody); err != nil {
			fail(w, err)
			return
		}
		c.LastBody = clip(c.LastBody, 160)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"conversations": out, "count": len(out)})
}

type threadMsg struct {
	ID           int64  `json:"id"`
	MsgID        string `json:"msg_id"`
	From         string `json:"from"`
	FromIdentity string `json:"from_identity"`
	ToIdentity   string `json:"to_identity"`
	Body         string `json:"body"`
	CreatedAt    int64  `json:"created_at"`
	Delivered    int    `json:"delivered"`
	DeadLettered int    `json:"dead_lettered"`
}

// GET /conversations/thread?a=&b=&before=&limit=
// One page of a two-party conversation covering both directions, oldest first. Pages go
// backwards in time: pass the returned `before` to get the page preceding this one, so a
// long history is only loaded as far back as someone actually scrolls.
func handleConversationThread(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a, b := q.Get("a"), q.Get("b")
	if a == "" || b == "" {
		bad(w, "a and b required (participant names, as returned by /conversations)")
		return
	}
	limit := clampInt(q.Get("limit"), 50, 1, 200)
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	rows, err := db.Query(identitySQL+`
SELECT id, msg_id, from_session, src, dst, body, created_at, delivered_at, dead_lettered_at
FROM m
WHERE ((src = ? AND dst = ?) OR (src = ? AND dst = ?)) AND (? <= 0 OR id < ?)
ORDER BY id DESC LIMIT ?`, a, b, b, a, before, before, limit+1)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []threadMsg{}
	for rows.Next() {
		var t threadMsg
		var deliveredAt, deadAt *int64
		if err := rows.Scan(&t.ID, &t.MsgID, &t.From, &t.FromIdentity, &t.ToIdentity, &t.Body, &t.CreatedAt, &deliveredAt, &deadAt); err != nil {
			fail(w, err)
			return
		}
		if deliveredAt != nil {
			t.Delivered = 1
		}
		if deadAt != nil {
			t.DeadLettered = 1
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	resp := map[string]any{"a": a, "b": b, "messages": out, "has_more": hasMore}
	if len(out) > 0 {
		resp["before"] = out[0].ID
	}
	ok(w, resp)
}
