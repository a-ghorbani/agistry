// Slack relay: delivers messages addressed to the operator into a Slack channel and
// sends the human's thread replies back as messages from the operator. Optional — it
// runs only when SLACK_BOT_TOKEN, SLACK_APP_TOKEN and SLACK_CHANNEL are all set.
//
// It is the operator's delivery path, the way the channel and plugins are an agent's:
// it drains the operator's mailbox and marks a message delivered only once Slack has
// accepted it, so a Slack outage leaves messages pending in the mailbox, not lost.
//
// It talks to Slack in Socket Mode, so agistry needs no public URL: the app token opens
// a websocket that carries thread replies in, and the bot token posts over HTTPS.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type slackRelay struct {
	botToken, appToken string
	channel            string // channel id (C…/G…): events name channels by id
	api                string // Slack Web API base URL; tests point it at a fake
	client             *http.Client
	kickc              chan struct{}

	// backoff after a failed post; only the worker touches these
	failures int
	retryAt  int64
}

func newSlackRelay(botToken, appToken, channel, api string) *slackRelay {
	return &slackRelay{
		botToken: botToken, appToken: appToken, channel: channel, api: api,
		client: &http.Client{Timeout: 15 * time.Second},
		kickc:  make(chan struct{}, 1),
	}
}

// startSlack starts the relay when it is configured, and says why when it is half
// configured, since a missing variable otherwise looks like Slack ignoring you.
func startSlack() {
	bot, app, ch := os.Getenv("SLACK_BOT_TOKEN"), os.Getenv("SLACK_APP_TOKEN"), os.Getenv("SLACK_CHANNEL")
	if bot == "" || app == "" || ch == "" {
		if bot != "" || app != "" || ch != "" {
			log.Println("Slack relay off: needs all of SLACK_BOT_TOKEN, SLACK_APP_TOKEN and SLACK_CHANNEL")
		}
		return
	}
	s := newSlackRelay(bot, app, ch, "https://slack.com/api/")
	operatorNotify = s.kick
	go s.worker()
	go s.socketLoop(context.Background())
	log.Printf("Slack relay on: messages to %s go to channel %s", operatorID, ch)
}

func (s *slackRelay) kick() {
	select {
	case s.kickc <- struct{}{}:
	default:
	}
}

// ---- Web API ----

// call POSTs one Web API method: form-encoded for url.Values, JSON otherwise. A 429 is
// retried after Retry-After a few times; anything else is the caller's to retry. Errors
// name the method and Slack's error code, never the token.
func (s *slackRelay) call(method, tok string, body any, out any) error {
	for attempt := 0; ; attempt++ {
		var payload, ct string
		if form, isForm := body.(url.Values); isForm {
			payload, ct = form.Encode(), "application/x-www-form-urlencoded"
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			payload, ct = string(b), "application/json; charset=utf-8"
		}
		req, err := http.NewRequest(http.MethodPost, s.api+method, strings.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			time.Sleep(retryAfter(resp.Header.Get("Retry-After")))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
		}
		var env struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("%s: bad response", method)
		}
		if !env.OK {
			return fmt.Errorf("%s: %s", method, env.Error)
		}
		if out != nil {
			return json.Unmarshal(raw, out)
		}
		return nil
	}
}

func retryAfter(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n < 0 {
		n = 1
	}
	if n > 30 {
		n = 30
	}
	return time.Duration(n) * time.Second
}

// post sends a message, threaded under threadTS when given and then also shown in the
// channel, so a new message in an old thread is not missed.
func (s *slackRelay) post(threadTS, text string) (string, error) {
	msg := map[string]any{"channel": s.channel, "text": text, "unfurl_links": false}
	if threadTS != "" {
		msg["thread_ts"] = threadTS
		msg["reply_broadcast"] = true
	}
	var out struct {
		TS string `json:"ts"`
	}
	err := s.call("chat.postMessage", s.botToken, msg, &out)
	return out.TS, err
}

// slackEsc escapes the three characters Slack's mrkdwn treats as control characters.
func slackEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// ---- delivery: the operator's mailbox -> Slack ----

func (s *slackRelay) worker() {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		s.drainOnce(now())
		select {
		case <-s.kickc:
		case <-tick.C:
		}
	}
}

type operatorMsg struct {
	msgID, from, task, role, body string
}

// drainOnce posts the operator's pending messages in order and marks each delivered
// once Slack has accepted it. The first failure stops the pass and backs off, so order
// is kept and nothing is marked delivered that Slack did not take.
func (s *slackRelay) drainOnce(t int64) {
	if t < s.retryAt {
		return
	}
	rows, err := db.Query(`
SELECT m.msg_id, m.from_session, COALESCE(a.task, ''), COALESCE(a.role, ''), m.body
FROM messages m LEFT JOIN agents a ON a.session_id = m.from_session
WHERE m.to_session = ? AND m.delivered_at IS NULL AND m.dead_lettered_at IS NULL
ORDER BY m.id LIMIT 50`, operatorID)
	if err != nil {
		log.Printf("slack drain: %v", err)
		return
	}
	// Collect, then close: the pool has one connection, so querying while iterating
	// would deadlock.
	var pending []operatorMsg
	for rows.Next() {
		var m operatorMsg
		if rows.Scan(&m.msgID, &m.from, &m.task, &m.role, &m.body) == nil {
			pending = append(pending, m)
		}
	}
	_ = rows.Close()

	for _, m := range pending {
		if err := s.deliver(m, t); err != nil {
			s.failures++
			backoff := int64(60) << min(s.failures-1, 4) // 1m, 2m, 4m … capped at 15m
			s.retryAt = t + min(backoff, 900)
			log.Printf("slack: delivery to %s failed, %d pending, retry in %ds: %v", operatorID, len(pending), s.retryAt-t, err)
			return
		}
	}
	s.failures, s.retryAt = 0, 0
}

// deliver posts one message into its sender's thread and acks it.
func (s *slackRelay) deliver(m operatorMsg, t int64) error {
	// Replies go back to the task:role, so they reach whoever holds the role by then;
	// a sender without a role can only be answered by session id.
	replyTo := m.from
	if m.role != "" {
		replyTo = m.task + ":" + m.role
	}
	key := m.task
	if key == "" {
		key = m.from
	}
	if key == "" {
		key = "?"
	}
	thread, err := s.thread(key, replyTo)
	if err != nil {
		return err
	}
	who := replyTo
	if who == "" {
		who = "unknown sender"
	}
	if _, err := s.post(thread, "*"+slackEsc(who)+"*\n"+slackEsc(clip(m.body, 38000))); err != nil {
		if strings.Contains(err.Error(), "thread_not_found") {
			// someone deleted the thread's header; the retry starts a new thread
			_, _ = db.Exec(`DELETE FROM operator_threads WHERE channel=? AND key=?`, s.channel, key)
		}
		return err
	}
	if _, err := db.Exec(`UPDATE messages SET delivered_at=? WHERE msg_id=? AND delivered_at IS NULL`, t, m.msgID); err != nil {
		return err
	}
	// the latest sender in a thread is the one a reply answers
	if replyTo != "" {
		_, _ = db.Exec(`UPDATE operator_threads SET reply_to=? WHERE channel=? AND key=?`, replyTo, s.channel, key)
	}
	return nil
}

// thread returns the thread for a sender key, starting it with a header line naming the
// sender the first time.
func (s *slackRelay) thread(key, replyTo string) (string, error) {
	var ts string
	_ = db.QueryRow(`SELECT thread_ts FROM operator_threads WHERE channel=? AND key=?`, s.channel, key).Scan(&ts)
	if ts != "" {
		return ts, nil
	}
	who := replyTo
	if who == "" {
		who = key
	}
	ts, err := s.post("", "Messages from *"+slackEsc(who)+"* — reply in this thread to answer.")
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO operator_threads(channel, key, thread_ts, reply_to) VALUES(?, ?, ?, ?)`,
		s.channel, key, ts, replyTo)
	return ts, err
}

// ---- replies: Slack -> the sender's mailbox ----

type slackEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
}

type slackMessage struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	BotID    string `json:"bot_id"`
	User     string `json:"user"`
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
}

func (s *slackRelay) handleEnvelope(e slackEnvelope) {
	if e.Type != "events_api" {
		return
	}
	var p struct {
		Event slackMessage `json:"event"`
	}
	if json.Unmarshal(e.Payload, &p) == nil {
		s.onMessage(p.Event)
	}
}

// onMessage turns a human's reply in a sender's thread into a message from the
// operator to that sender. Bots (this one included), edits and other subtypes, other
// channels, top-level messages and unknown threads are ignored. The msg_id is derived
// from the Slack message, so a redelivered event queues nothing twice.
func (s *slackRelay) onMessage(ev slackMessage) {
	if ev.Type != "message" || ev.Subtype != "" || ev.BotID != "" || ev.User == "" ||
		ev.Channel != s.channel || ev.ThreadTS == "" {
		return
	}
	var replyTo string
	if db.QueryRow(`SELECT reply_to FROM operator_threads WHERE channel=? AND thread_ts=?`, ev.Channel, ev.ThreadTS).Scan(&replyTo) != nil {
		return // not one of ours
	}
	// Slack sends &, < and > as entities; the agent should get what the human typed.
	text := strings.TrimSpace(strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(ev.Text))
	if text == "" {
		return
	}
	if replyTo == "" {
		s.reply(ev.ThreadTS, "This thread has no sender to reply to.")
		return
	}
	toSession, toTask, toRole := replyTo, "", ""
	if strings.Contains(replyTo, ":") {
		parts := strings.SplitN(replyTo, ":", 2)
		toSession, toTask, toRole = "", parts[0], parts[1]
	}
	if _, err := db.Exec(`
INSERT OR IGNORE INTO messages(msg_id, to_session, to_task, to_role, from_session, body, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		"slack-"+ev.Channel+"-"+ev.TS, toSession, toTask, toRole, operatorID, text, now()); err != nil {
		log.Printf("slack reply: %v", err)
		s.reply(ev.ThreadTS, "Could not queue that reply; try again.")
	}
}

func (s *slackRelay) reply(threadTS, text string) {
	msg := map[string]any{"channel": s.channel, "thread_ts": threadTS, "text": text}
	if err := s.call("chat.postMessage", s.botToken, msg, nil); err != nil {
		log.Printf("slack reply: %v", err)
	}
}

// ---- Socket Mode ----

// socketLoop keeps one Socket Mode connection up for the life of the process. A
// connection that got as far as hello reconnects after a second (Slack's `disconnect`
// asks for exactly that); repeated failures back off to a minute.
func (s *slackRelay) socketLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		healthy, err := s.socketOnce(ctx)
		if err != nil {
			log.Printf("slack socket: %v", err)
		}
		if healthy {
			backoff = time.Second
		} else if backoff *= 2; backoff > time.Minute {
			backoff = time.Minute
		}
		time.Sleep(backoff)
	}
}

// socketOnce runs one connection until it drops or Slack asks to reconnect. It reports
// whether the connection got as far as Slack's hello.
func (s *slackRelay) socketOnce(ctx context.Context) (bool, error) {
	var open struct {
		URL string `json:"url"`
	}
	if err := s.call("apps.connections.open", s.appToken, url.Values{}, &open); err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, _, err := websocket.Dial(ctx, open.URL, nil)
	if err != nil {
		// the URL carries a connection ticket; keep it out of the log
		return false, errors.New(strings.ReplaceAll(err.Error(), open.URL, "<socket url>"))
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(4 << 20)

	// A half-dead TCP connection never errors a Read; a failed ping is how we notice.
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	hello := false
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return hello, err
		}
		var e slackEnvelope
		if json.Unmarshal(data, &e) != nil {
			continue
		}
		// Ack before handling: Slack wants it within 3 seconds and redelivers otherwise.
		if e.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]string{"envelope_id": e.EnvelopeID})
			if err := c.Write(ctx, websocket.MessageText, ack); err != nil {
				return hello, err
			}
		}
		switch e.Type {
		case "hello":
			hello = true
		case "disconnect":
			_ = c.Close(websocket.StatusNormalClosure, "")
			return true, nil
		case "events_api":
			go s.handleEnvelope(e)
		}
	}
}
