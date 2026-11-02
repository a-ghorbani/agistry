package main

import "testing"

// The operator is always listed and always addressable, so `send operator` never fails
// as a typo, and nothing ages it out.
func TestOperatorAlwaysPresentAndSendable(t *testing.T) {
	setup(t)
	_, m := do(t, "GET", "/agents?agent_kind=human", "")
	if m["count"].(float64) != 1 {
		t.Fatalf("operator not listed: %v", m)
	}
	if a := m["agents"].([]any)[0].(map[string]any); a["session_id"] != "operator" || a["state"] != "active" {
		t.Fatalf("unexpected operator row: %v", a)
	}

	reapOnce(now() + 10*ttl) // long past every TTL
	if !liveSession("operator") {
		t.Fatal("operator was reaped")
	}

	joinAgent(t, "s1", "T", "impl")
	code, r := do(t, "POST", "/send", `{"from":"s1","to":"operator","msg":"Q: drop the legacy path? default: keep it"}`)
	if code != 200 || r["status"] != "queued" {
		t.Fatalf("send to operator rejected: %d %v", code, r)
	}
	_, msgs := do(t, "GET", "/messages", "")
	x := msgs["messages"].([]any)[0].(map[string]any)
	if x["to_identity"] != "operator" || x["from_identity"] != "T:impl" || x["delivered"].(float64) != 0 {
		t.Fatalf("message to operator should wait in the mailbox: %v", x)
	}
}

func TestOperatorIdentityIsReserved(t *testing.T) {
	setup(t)
	for _, c := range []struct{ path, body string }{
		{"/register", `{"session_id":"operator"}`},
		{"/assign", `{"session_id":"operator","task":"T","role":"impl"}`},
		{"/heartbeat", `{"session_id":"operator"}`},
		{"/deregister", `{"session_id":"operator"}`},
	} {
		if code, _ := do(t, "POST", c.path, c.body); code != 400 {
			t.Fatalf("%s as operator should be 400, got %d", c.path, code)
		}
	}
	if !liveSession("operator") {
		t.Fatal("operator should still be live")
	}
}
