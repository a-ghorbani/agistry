package main

import (
	"strconv"
	"testing"
	"unicode/utf8"
)

func joinAgent(t *testing.T, sid, task, role string) {
	t.Helper()
	do(t, "POST", "/register", `{"session_id":"`+sid+`","host":"lab"}`)
	if role != "" {
		do(t, "POST", "/assign", `{"session_id":"`+sid+`","task":"`+task+`","role":"`+role+`"}`)
	}
}

func send(t *testing.T, from, to, msg string) {
	t.Helper()
	if code, m := do(t, "POST", "/send", `{"from":"`+from+`","to":"`+to+`","msg":"`+msg+`"}`); code != 200 {
		t.Fatalf("send %s->%s: %d %v", from, to, code, m)
	}
}

func conversations(t *testing.T, q string) []map[string]any {
	t.Helper()
	code, m := do(t, "GET", "/conversations"+q, "")
	if code != 200 {
		t.Fatalf("/conversations: %d %v", code, m)
	}
	out := []map[string]any{}
	for _, c := range m["conversations"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func threadBodies(m map[string]any) []string {
	out := []string{}
	for _, x := range m["messages"].([]any) {
		out = append(out, x.(map[string]any)["body"].(string))
	}
	return out
}

// Every view pairs messages by name, so the feed itself must say who sent each one —
// including a sender that has since gone, which is most senders in practice.
func TestMessagesFeedNamesSenderEvenAfterItLeaves(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	send(t, "s1", "T:impl", "review done")
	do(t, "POST", "/deregister", `{"session_id":"s1"}`)

	_, m := do(t, "GET", "/messages", "")
	x := m["messages"].([]any)[0].(map[string]any)
	if x["from_identity"] != "T:reviewer" || x["to_identity"] != "T:impl" {
		t.Fatalf("feed does not name both ends: %v", x)
	}
	if x["from_task"] != "T" || x["from_role"] != "reviewer" || x["id"].(float64) <= 0 {
		t.Fatalf("feed missing sender fields or id: %v", x)
	}
}

// The core of the chat view: a message and its reply travel as session -> TASK:role in
// both directions, and a direct session-addressed message joins the same conversation.
func TestConversationPairsBothDirections(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	joinAgent(t, "s2", "T", "impl")
	send(t, "s1", "T:impl", "please fix the null check")
	send(t, "s2", "T:reviewer", "fixed in abc123")
	send(t, "s1", "s2", "approved")
	do(t, "POST", "/deregister", `{"session_id":"s2"}`) // replies stay attributed after leaving

	cs := conversations(t, "")
	if len(cs) != 1 {
		t.Fatalf("both directions should be one conversation, got %d: %v", len(cs), cs)
	}
	c := cs[0]
	if c["a"] != "T:impl" || c["b"] != "T:reviewer" || c["count"].(float64) != 3 {
		t.Fatalf("unexpected conversation: %v", c)
	}
	if c["last_body"] != "approved" || c["last_from"] != "T:reviewer" {
		t.Fatalf("latest message wrong: %v", c)
	}

	_, m := do(t, "GET", "/conversations/thread?a=T:reviewer&b=T:impl", "")
	got := threadBodies(m)
	want := []string{"please fix the null check", "fixed in abc123", "approved"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("thread should hold both directions oldest first: %v", got)
	}
	if m["messages"].([]any)[1].(map[string]any)["from_identity"] != "T:impl" {
		t.Fatalf("reply not attributed to its sender: %v", m["messages"])
	}
}

func TestConversationsSeparatePairsMostRecentFirst(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	joinAgent(t, "s3", "U", "pm")
	send(t, "s1", "T:impl", "older")
	if _, err := db.Exec(`UPDATE messages SET created_at = created_at - 100`); err != nil {
		t.Fatal(err)
	}
	send(t, "s3", "T:reviewer", "newer")

	cs := conversations(t, "")
	if len(cs) != 2 {
		t.Fatalf("want 2 conversations, got %d", len(cs))
	}
	if cs[0]["last_body"] != "newer" {
		t.Fatalf("most recent conversation should come first: %v", cs)
	}
	// filter by participant name, case-insensitive
	if f := conversations(t, "?q=PM"); len(f) != 1 || f[0]["last_body"] != "newer" {
		t.Fatalf("q filter wrong: %v", f)
	}
}

// A sender that never declared a role has nothing better than its session id; it must
// still get a conversation rather than being dropped.
func TestConversationSenderWithoutRoleKeepsSessionID(t *testing.T) {
	setup(t)
	joinAgent(t, "stub1", "", "")
	send(t, "stub1", "T:impl", "hello from a stub")
	cs := conversations(t, "")
	if len(cs) != 1 || cs[0]["a"] != "T:impl" || cs[0]["b"] != "stub1" {
		t.Fatalf("roleless sender should be named by session id: %v", cs)
	}
}

func TestConversationCountsAndClippedPreview(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	send(t, "s1", "T:impl", "first")
	if _, err := db.Exec(`UPDATE messages SET dead_lettered_at = ?`, now()); err != nil {
		t.Fatal(err)
	}
	long := ""
	for i := 0; i < 400; i++ {
		long += "x"
	}
	send(t, "s1", "T:impl", long)

	c := conversations(t, "")[0]
	if c["count"].(float64) != 2 || c["pending"].(float64) != 1 || c["dead_lettered"].(float64) != 1 {
		t.Fatalf("counts wrong: %v", c)
	}
	// the list must not ship whole bodies
	if n := utf8.RuneCountInString(c["last_body"].(string)); n != 161 {
		t.Fatalf("preview should be clipped to 160 runes plus an ellipsis, got %d", n)
	}
}

// A month of history must not arrive in one response: pages go backwards from the
// newest message and stop cleanly at the start.
func TestConversationThreadPagesBackwards(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	for i := 0; i < 7; i++ {
		send(t, "s1", "T:impl", "m"+strconv.Itoa(i))
	}
	send(t, "s1", "T:other", "not in this conversation")

	_, p1 := do(t, "GET", "/conversations/thread?a=T:reviewer&b=T:impl&limit=3", "")
	if b := threadBodies(p1); len(b) != 3 || b[0] != "m4" || b[2] != "m6" || p1["has_more"] != true {
		t.Fatalf("newest page wrong: %v has_more=%v", b, p1["has_more"])
	}
	before := strconv.FormatInt(int64(p1["before"].(float64)), 10)
	_, p2 := do(t, "GET", "/conversations/thread?a=T:reviewer&b=T:impl&limit=3&before="+before, "")
	if b := threadBodies(p2); len(b) != 3 || b[0] != "m1" || b[2] != "m3" || p2["has_more"] != true {
		t.Fatalf("second page wrong: %v", b)
	}
	before = strconv.FormatInt(int64(p2["before"].(float64)), 10)
	_, p3 := do(t, "GET", "/conversations/thread?a=T:reviewer&b=T:impl&limit=3&before="+before, "")
	if b := threadBodies(p3); len(b) != 1 || b[0] != "m0" || p3["has_more"] != false {
		t.Fatalf("last page wrong: %v has_more=%v", b, p3["has_more"])
	}
}

func TestConversationThreadRequiresBothParticipants(t *testing.T) {
	setup(t)
	if code, _ := do(t, "GET", "/conversations/thread?a=T:reviewer", ""); code != 400 {
		t.Fatalf("missing b should be 400, got %d", code)
	}
}

// Delivered and dead-lettered messages are kept for the same retention window. The old
// 24h limit on delivered messages meant history held only the messages nobody read.
func TestRetentionKeepsFinishedMessagesForTheWindow(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	joinAgent(t, "s2", "T", "impl")
	send(t, "s1", "T:impl", "read by impl")
	do(t, "GET", "/inbox?session_id=s2", "") // delivers it
	send(t, "s1", "FUTURE:nobody", "never claimed")
	if _, err := db.Exec(`UPDATE messages SET dead_lettered_at = ? WHERE body = 'never claimed'`, now()); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		_, m := do(t, "GET", "/messages", "")
		return len(m["messages"].([]any))
	}

	// ten days old: past the old 24h and 7d limits, inside 30 days
	if _, err := db.Exec(`UPDATE messages SET delivered_at = ? WHERE delivered_at IS NOT NULL`, now()-10*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE messages SET dead_lettered_at = ? WHERE dead_lettered_at IS NOT NULL`, now()-10*86400); err != nil {
		t.Fatal(err)
	}
	reapOnce(now())
	if n := count(); n != 2 {
		t.Fatalf("messages inside the retention window were deleted: %d left", n)
	}

	// past the window: both kinds go
	if _, err := db.Exec(`UPDATE messages SET delivered_at = ? WHERE delivered_at IS NOT NULL`, now()-31*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE messages SET dead_lettered_at = ? WHERE dead_lettered_at IS NOT NULL`, now()-31*86400); err != nil {
		t.Fatal(err)
	}
	reapOnce(now())
	if n := count(); n != 0 {
		t.Fatalf("messages past the retention window were kept: %d left", n)
	}
}

// The agent panel lists exactly the conversations one agent took part in. The filter
// must match a participant name exactly: "T:reviewer" must not pull in "T:reviewer2".
func TestConversationsWithParticipantIsExact(t *testing.T) {
	setup(t)
	joinAgent(t, "s1", "T", "reviewer")
	joinAgent(t, "s2", "T", "reviewer2")
	send(t, "s1", "T:impl", "to impl")
	send(t, "s2", "T:impl", "from reviewer2")
	send(t, "s1", "U:pm", "to pm")

	cs := conversations(t, "?with=T:reviewer")
	if len(cs) != 2 {
		t.Fatalf("want the 2 conversations T:reviewer took part in, got %d: %v", len(cs), cs)
	}
	for _, c := range cs {
		if c["a"] != "T:reviewer" && c["b"] != "T:reviewer" {
			t.Fatalf("conversation without the participant leaked in: %v", c)
		}
	}
}
