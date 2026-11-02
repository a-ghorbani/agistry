package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeSlack stands in for the Slack Web API (and the Socket Mode websocket, when a
// test installs onSocket). No test talks to real Slack.
type fakeSlack struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []slackCall
	ts       int
	fail     map[string]string // method -> Slack error code to return
	limited  int               // the next N calls get a 429
	onSocket func(*websocket.Conn)
}

type slackCall struct {
	Method string
	Body   map[string]any
	Auth   string
}

func newFakeSlack(t *testing.T) (*fakeSlack, *slackRelay) {
	t.Helper()
	f := &fakeSlack{fail: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f, newSlackRelay("xoxb-test", "xapp-test", "C1", f.srv.URL+"/api/")
}

func (f *fakeSlack) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/socket" {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.onSocket(c)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	raw, _ := io.ReadAll(r.Body)
	body := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &body)
	} else {
		form, _ := url.ParseQuery(string(raw))
		for k := range form {
			body[k] = form.Get(k)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.limited > 0 {
		f.limited--
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	f.calls = append(f.calls, slackCall{method, body, r.Header.Get("Authorization")})
	if code := f.fail[method]; code != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
		return
	}
	out := map[string]any{"ok": true}
	switch method {
	case "chat.postMessage":
		f.ts++
		out["ts"] = "100." + strconv.Itoa(f.ts)
	case "apps.connections.open":
		out["url"] = "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/socket"
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeSlack) take() []slackCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func text(c slackCall) string { s, _ := c.Body["text"].(string); return s }

func reply(user, body, threadTS, ts string) slackEnvelope {
	ev := map[string]any{"type": "message", "channel": "C1", "user": user, "text": body, "ts": ts}
	if threadTS != "" {
		ev["thread_ts"] = threadTS
	}
	p, _ := json.Marshal(map[string]any{"event": ev})
	return slackEnvelope{Type: "events_api", EnvelopeID: "e-" + ts, Payload: p}
}

func pendingToOperator(t *testing.T) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE to_session='operator' AND delivered_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One thread per sender task, started by a header naming the sender; each message is
// posted in it (and shown in the channel), then acked.
func TestSlackDeliversOperatorMailIntoTaskThreads(t *testing.T) {
	setup(t)
	f, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	joinAgent(t, "s2", "T", "reviewer")
	joinAgent(t, "s3", "", "")
	send(t, "s1", "operator", "Drop <legacy> path? default: keep it")
	send(t, "s2", "operator", "second from the same task")
	send(t, "s3", "operator", "from a session with no task")
	s.drainOnce(now())

	c := f.take()
	if len(c) != 5 {
		t.Fatalf("want 2 headers + 3 messages, got %d: %v", len(c), c)
	}
	if c[0].Body["thread_ts"] != nil || !strings.Contains(text(c[0]), "T:impl") || c[0].Auth != "Bearer xoxb-test" {
		t.Fatalf("first post should be a header naming the sender, with the bot token: %v", c[0])
	}
	if c[1].Body["thread_ts"] != "100.1" || c[1].Body["reply_broadcast"] != true || c[1].Body["channel"] != "C1" ||
		!strings.Contains(text(c[1]), "Drop &lt;legacy&gt; path? default: keep it") || !strings.Contains(text(c[1]), "T:impl") {
		t.Fatalf("message should be posted, escaped, in the task thread: %v", c[1].Body)
	}
	if c[2].Body["thread_ts"] != "100.1" {
		t.Fatalf("same task should reuse its thread: %v", c[2].Body)
	}
	if c[3].Body["thread_ts"] != nil || !strings.Contains(text(c[3]), "s3") || c[4].Body["thread_ts"] != "100.4" {
		t.Fatalf("a sender without a task gets a thread of its own: %v / %v", c[3].Body, c[4].Body)
	}
	if n := pendingToOperator(t); n != 0 {
		t.Fatalf("delivered messages should be acked, %d pending", n)
	}
	s.drainOnce(now())
	if c := f.take(); len(c) != 0 {
		t.Fatalf("nothing should be posted twice: %v", c)
	}
}

// Slack being down must not lose mail: the message stays pending until Slack accepts
// it, and retries back off instead of hammering.
func TestSlackAcksOnlyAfterSlackAccepts(t *testing.T) {
	setup(t)
	f, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "operator", "first")
	send(t, "s1", "operator", "second")
	f.fail["chat.postMessage"] = "service_unavailable"
	t0 := now()
	s.drainOnce(t0)
	if n := pendingToOperator(t); n != 2 {
		t.Fatalf("failed delivery must leave mail pending, %d pending", n)
	}
	f.take()
	s.drainOnce(t0 + 30)
	if c := f.take(); len(c) != 0 {
		t.Fatalf("a failure should back off, not retry at once: %v", c)
	}
	delete(f.fail, "chat.postMessage")
	s.drainOnce(t0 + 61)
	if n := pendingToOperator(t); n != 0 {
		t.Fatalf("mail should go out after the backoff, %d pending", n)
	}
	c := f.take()
	if len(c) != 3 || !strings.Contains(text(c[1]), "first") || !strings.Contains(text(c[2]), "second") {
		t.Fatalf("want header, first, second in order: %v", c)
	}
}

func TestSlackRateLimitRetriesAfter(t *testing.T) {
	setup(t)
	f, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "operator", "q?")
	f.limited = 2
	s.drainOnce(now())
	if n := pendingToOperator(t); n != 0 {
		t.Fatalf("a 429 should be retried after Retry-After, %d pending", n)
	}
}

// A reply in a sender's thread goes to its task:role, so a session that replaced the
// sender gets it.
func TestSlackReplyReachesSenderRole(t *testing.T) {
	setup(t)
	_, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "operator", "Drop the legacy path? default: keep it")
	s.drainOnce(now())
	do(t, "POST", "/deregister", `{"session_id":"s1"}`)
	joinAgent(t, "s2", "T", "impl")

	s.handleEnvelope(reply("U9", "drop it &amp; delete &lt;legacy&gt;", "100.1", "200.1"))
	_, m := do(t, "GET", "/inbox?session_id=s2", "")
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("replacement session should get the reply: %v", m)
	}
	x := msgs[0].(map[string]any)
	if x["body"] != "drop it & delete <legacy>" || x["from"] != "operator" {
		t.Fatalf("reply should arrive verbatim from the operator: %v", x)
	}
}

// A sender with no role can only be answered by session id; a thread shared by a task
// answers whoever in it wrote last.
func TestSlackReplyTargets(t *testing.T) {
	setup(t)
	_, s := newFakeSlack(t)
	joinAgent(t, "s3", "", "")
	joinAgent(t, "s1", "T", "impl")
	joinAgent(t, "s2", "T", "reviewer")
	send(t, "s3", "operator", "no role here")
	send(t, "s1", "operator", "from impl")
	send(t, "s2", "operator", "from reviewer")
	s.drainOnce(now()) // threads: s3 -> 100.1, T -> 100.3

	s.handleEnvelope(reply("U9", "to the stub", "100.1", "200.1"))
	s.handleEnvelope(reply("U9", "to the latest in T", "100.3", "200.2"))
	if got := inboxBodies(t, "s3"); len(got) != 1 || got[0] != "to the stub" {
		t.Fatalf("roleless sender should be answered by session id: %v", got)
	}
	if got := inboxBodies(t, "s2"); len(got) != 1 || got[0] != "to the latest in T" {
		t.Fatalf("the task thread should answer its latest sender: %v", got)
	}
	if got := inboxBodies(t, "s1"); len(got) != 0 {
		t.Fatalf("earlier sender should not get it: %v", got)
	}
}

func TestSlackIgnoresBotsEditsTopLevelOtherThreadsAndRedelivery(t *testing.T) {
	setup(t)
	f, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "operator", "hello")
	s.drainOnce(now())
	f.take()

	ev := func(fields map[string]any) slackEnvelope {
		base := map[string]any{"type": "message", "channel": "C1", "user": "U9", "text": "x", "ts": "200.9", "thread_ts": "100.1"}
		for k, v := range fields {
			base[k] = v
		}
		p, _ := json.Marshal(map[string]any{"event": base})
		return slackEnvelope{Type: "events_api", Payload: p}
	}
	s.handleEnvelope(ev(map[string]any{"bot_id": "B1", "ts": "200.1"}))
	s.handleEnvelope(ev(map[string]any{"subtype": "message_changed", "ts": "200.2"}))
	s.handleEnvelope(ev(map[string]any{"channel": "C2", "ts": "200.3"}))
	s.handleEnvelope(reply("U9", "top-level chatter", "", "200.4"))
	s.handleEnvelope(reply("U9", "some other thread", "999.1", "200.5"))
	if got := inboxBodies(t, "s1"); len(got) != 0 {
		t.Fatalf("ignored messages were delivered: %v", got)
	}
	// Slack redelivers an event it thinks went unacked; it must queue once.
	s.handleEnvelope(reply("U9", "yes", "100.1", "200.6"))
	s.handleEnvelope(reply("U9", "yes", "100.1", "200.6"))
	if got := inboxBodies(t, "s1"); len(got) != 1 {
		t.Fatalf("a redelivered reply should queue once: %v", got)
	}
	if c := f.take(); len(c) != 0 {
		t.Fatalf("nothing here should post to Slack: %v", c)
	}
}

// The real Socket Mode loop against a fake websocket: it opens the connection with the
// app token, acks each envelope by id, acts on the event, and returns on `disconnect`.
func TestSlackSocketModeAcksAndHandles(t *testing.T) {
	setup(t)
	f, s := newFakeSlack(t)
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "operator", "over the socket?")
	s.drainOnce(now())

	acks := make(chan string, 4)
	f.onSocket = func(c *websocket.Conn) {
		ctx := context.Background()
		write := func(v any) {
			b, _ := json.Marshal(v)
			_ = c.Write(ctx, websocket.MessageText, b)
		}
		write(map[string]any{"type": "hello"})
		e := reply("U9", "yes, socket", "100.1", "300.1")
		write(map[string]any{"type": e.Type, "envelope_id": "env-1", "payload": json.RawMessage(e.Payload)})
		if _, data, err := c.Read(ctx); err == nil {
			var a map[string]string
			_ = json.Unmarshal(data, &a)
			acks <- a["envelope_id"]
		}
		write(map[string]any{"type": "disconnect", "reason": "refresh_requested"})
		_, _, _ = c.Read(ctx)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	healthy, err := s.socketOnce(ctx)
	if !healthy || err != nil {
		t.Fatalf("socketOnce should end cleanly on disconnect: healthy=%v err=%v", healthy, err)
	}
	select {
	case id := <-acks:
		if id != "env-1" {
			t.Fatalf("ack carried the wrong envelope id: %q", id)
		}
	case <-ctx.Done():
		t.Fatal("envelope was never acked")
	}
	var open []slackCall
	for _, c := range f.take() {
		if c.Method == "apps.connections.open" {
			open = append(open, c)
		}
	}
	if len(open) != 1 || open[0].Auth != "Bearer xapp-test" {
		t.Fatalf("connection must be opened with the app token: %v", open)
	}
	// the envelope is handled on its own goroutine
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM messages WHERE from_session='operator' AND body='yes, socket'`).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("event from the socket was not handled")
		}
	}
}

// Queuing mail to the operator wakes the relay rather than waiting for its tick.
func TestSendToOperatorWakesRelay(t *testing.T) {
	setup(t)
	_, s := newFakeSlack(t)
	operatorNotify = s.kick
	t.Cleanup(func() { operatorNotify = func() {} })
	joinAgent(t, "s1", "T", "impl")
	send(t, "s1", "T:other", "not for the operator")
	select {
	case <-s.kickc:
		t.Fatal("mail to an agent should not wake the relay")
	default:
	}
	send(t, "s1", "operator", "for the human")
	select {
	case <-s.kickc:
	default:
		t.Fatal("mail to the operator should wake the relay")
	}
}

func inboxBodies(t *testing.T, sid string) []string {
	t.Helper()
	_, m := do(t, "GET", "/inbox?session_id="+sid, "")
	out := []string{}
	for _, x := range m["messages"].([]any) {
		out = append(out, x.(map[string]any)["body"].(string))
	}
	return out
}
