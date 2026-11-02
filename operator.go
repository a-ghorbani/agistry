// The operator: the human, as a participant. Agents ask it things the way they message
// each other — `send operator "<question>"` — and its replies come back as ordinary
// messages from `operator`. It is a reserved agent row that is always present and never
// reaped, so a send to it is never rejected as a typo, and anything sent to it waits in
// the mailbox like any other message until something delivers it: the Slack relay
// (slack.go) when configured, otherwise the dashboard's conversation view shows it.
package main

import "net/http"

// operatorID is the operator's session id, REGISTRY_OPERATOR to rename it.
var operatorID = "operator"

// ensureOperator (re)asserts the operator's row: live, kind human, no task or role.
// Run at start and on every reaper pass, so nothing can leave it gone.
func ensureOperator(t int64) error {
	_, err := db.Exec(`
INSERT INTO agents(session_id, agent_kind, state, registered_at, last_seen)
VALUES(?, 'human', 'active', ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  task='', role='', agent_kind='human', state='active', last_seen=excluded.last_seen`,
		operatorID, t, t)
	return err
}

// reservedSession refuses an agent that tries to register, join or leave as the operator.
func reservedSession(w http.ResponseWriter, sid string) bool {
	if sid != operatorID {
		return false
	}
	bad(w, "'"+operatorID+"' is reserved for the human operator")
	return true
}
