package main

import "testing"

// resSetup gives every resource test a live agent to hold leases, and short,
// predictable caps so expiry can be exercised without sleeping.
func resSetup(t *testing.T, sessions ...string) {
	t.Helper()
	setup(t)
	resourceTTL = 900
	leaseMaxHold = 3600
	for _, s := range sessions {
		do(t, "POST", "/register", `{"session_id":"`+s+`","host":"lab"}`)
		do(t, "POST", "/assign", `{"session_id":"`+s+`","task":"T","role":"`+s+`"}`)
	}
}

func addPhone(t *testing.T, id string, maxHold int64) {
	t.Helper()
	body := `{"id":"` + id + `","kind":"android-device","name":"pixel","host":"lab","meta":{"model":"pixel-7"}`
	if maxHold > 0 {
		body += `,"max_hold_seconds":` + itoa(maxHold)
	}
	code, _ := do(t, "POST", "/resources/register", body+`}`)
	if code != 200 {
		t.Fatalf("register resource: status %d", code)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func TestResourceRegisterAndList(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:R5CT21", 0)

	code, m := do(t, "GET", "/resources", "")
	if code != 200 || m["count"].(float64) != 1 {
		t.Fatalf("status %d count %v", code, m["count"])
	}
	r := m["resources"].([]any)[0].(map[string]any)
	if r["id"] != "adb:R5CT21" || r["kind"] != "android-device" || r["host"] != "lab" {
		t.Fatalf("unexpected resource: %v", r)
	}
	if r["held"] != false {
		t.Fatalf("fresh resource should be free: %v", r["held"])
	}
	// meta round-trips as structured JSON, uninterpreted
	meta, okc := r["meta"].(map[string]any)
	if !okc || meta["model"] != "pixel-7" {
		t.Fatalf("meta not preserved: %v", r["meta"])
	}
	// no explicit cap -> server default surfaces
	if r["max_hold_seconds"].(float64) != 3600 {
		t.Fatalf("want default cap 3600, got %v", r["max_hold_seconds"])
	}
}

func TestClaimThenSecondClaimConflicts(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)

	code, m := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:abc"}`)
	if code != 200 || m["status"] != "acquired" {
		t.Fatalf("first claim: status %d %v", code, m)
	}

	code, m = do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2"}`)
	if code != 409 {
		t.Fatalf("second claim should conflict, got %d %v", code, m)
	}
	h := m["holder"].(map[string]any)
	// the 409 must carry enough identity to message the holder — that is the whole point
	if h["holder"] != "s1" || h["holder_role"] != "s1" || h["holder_task"] != "T" {
		t.Fatalf("conflict lacks addressable holder: %v", h)
	}
	if h["note"] != "sha256:abc" {
		t.Fatalf("holder note not surfaced: %v", h)
	}
}

func TestClaimReclaimsWhenHolderGone(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:old"}`)

	// s1 dies (SessionEnd hook, crash, or the TTL reaper) — the lease must not outlive it
	do(t, "POST", "/deregister", `{"session_id":"s1"}`)

	code, m := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2","note":"sha256:new"}`)
	if code != 200 {
		t.Fatalf("claim over dead holder should succeed, got %d %v", code, m)
	}
	// and it must say what it displaced, so the caller knows what it is overwriting
	if m["reclaimed"] != true {
		t.Fatalf("taking over a stale hold must be flagged as a reclaim: %v", m)
	}
	prev, okc := m["previous"].(map[string]any)
	if !okc || prev["holder"] != "s1" || prev["note"] != "sha256:old" {
		t.Fatalf("reclaim must report the displaced hold: %v", m["previous"])
	}
}

func TestExpiredLeaseIsReclaimable(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)

	// age the lease past its deadline; holder is still perfectly alive
	if _, err := db.Exec(`UPDATE leases SET expires_at=? WHERE resource_id='adb:1'`, now()-1); err != nil {
		t.Fatal(err)
	}
	code, m := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2"}`)
	if code != 200 {
		t.Fatalf("expired lease should be reclaimable, got %d %v", code, m)
	}
	// the board must show the new holder, not the stale one
	_, l := do(t, "GET", "/resources", "")
	r := l["resources"].([]any)[0].(map[string]any)
	if r["lease"].(map[string]any)["holder"] != "s2" {
		t.Fatalf("board still shows stale holder: %v", r["lease"])
	}
}

func TestLiveHolderIsNeverStolen(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)

	// repeated attempts must not erode the hold — there is no force flag by design
	for i := 0; i < 3; i++ {
		if code, _ := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2"}`); code != 409 {
			t.Fatalf("attempt %d: live hold was breached (%d)", i, code)
		}
	}
	if l := activeLease("adb:1", now()); l == nil || l.Holder != "s1" {
		t.Fatalf("holder changed under contention: %v", l)
	}
}

func TestTTLClampedToResourceCap(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 21600) // 6h cap: a full e2e suite

	// asking for more than the cap is clamped, not refused
	code, m := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","ttl_seconds":99999}`)
	if code != 200 || m["capped"] != true {
		t.Fatalf("over-cap claim should clamp: %d %v", code, m)
	}
	if got := m["expires_in"].(float64); got != 21600 {
		t.Fatalf("want clamp to 21600, got %v", got)
	}
	// a request within the cap is honoured exactly
	do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s1"}`)
	_, m = do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","ttl_seconds":600}`)
	if m["expires_in"].(float64) != 600 || m["capped"] == true {
		t.Fatalf("in-cap claim altered: %v", m)
	}
}

func TestRenewCannotOutrunTheCap(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 3600)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","ttl_seconds":60}`)

	// pretend the hold started an hour ago: renewal must land on the hard stop, so an
	// agent that is alive but has moved on cannot sit on the phone forever
	if _, err := db.Exec(`UPDATE leases SET acquired_at=? WHERE resource_id='adb:1'`, now()-3000); err != nil {
		t.Fatal(err)
	}
	code, m := do(t, "POST", "/resources/renew", `{"resource_id":"adb:1","session_id":"s1","ttl_seconds":3600}`)
	if code != 200 || m["capped"] != true {
		t.Fatalf("renew past cap should be capped: %d %v", code, m)
	}
	if got := m["expires_in"].(float64); got > 600 {
		t.Fatalf("renew outran the cap: expires_in=%v", got)
	}
}

func TestRenewAfterLosingLeaseFails(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)
	do(t, "POST", "/deregister", `{"session_id":"s1"}`)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2"}`)

	// s1 comes back and blindly renews — it must be told it no longer holds the phone
	do(t, "POST", "/register", `{"session_id":"s1"}`)
	code, m := do(t, "POST", "/resources/renew", `{"resource_id":"adb:1","session_id":"s1"}`)
	if code != 409 {
		t.Fatalf("renew of a lost lease must fail, got %d %v", code, m)
	}
	if m["holder"].(map[string]any)["holder"] != "s2" {
		t.Fatalf("should name the current holder: %v", m["holder"])
	}
}

func TestReleaseRecordsNoteForNextHolder(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","note":"claiming"}`)
	do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s1","note":"left build sha256:deadbeef resident"}`)

	code, m := do(t, "GET", "/resources", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	r := m["resources"].([]any)[0].(map[string]any)
	if r["held"] != false {
		t.Fatalf("should be free after release")
	}
	// the durable record: a lane arriving at a FREE phone still learns what is on it
	ll := r["last_lease"].(map[string]any)
	if ll["note"] != "left build sha256:deadbeef resident" || ll["released_by"] != "holder" {
		t.Fatalf("last_lease lost the record: %v", ll)
	}
}

func TestReleaseByNonHolderIsNoop(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)

	code, m := do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s2"}`)
	if code != 200 || m["status"] != "not-held" {
		t.Fatalf("want not-held, got %d %v", code, m)
	}
	if l := activeLease("adb:1", now()); l == nil || l.Holder != "s1" {
		t.Fatalf("a stranger's release freed the resource: %v", l)
	}
}

func TestClaimRequiresLiveAgentAndKnownResource(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 0)

	if code, _ := do(t, "POST", "/resources/claim", `{"resource_id":"nope","session_id":"s1"}`); code != 404 {
		t.Fatalf("unknown resource should 404, got %d", code)
	}
	if code, _ := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"ghost"}`); code != 400 {
		t.Fatalf("unregistered session should 400, got %d", code)
	}
}

func TestResourceAgesOutAndComesBack(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 0)

	// provider stops refreshing (USB unplugged)
	if _, err := db.Exec(`UPDATE resources SET last_seen=? WHERE id='adb:1'`, now()-resourceTTL-1); err != nil {
		t.Fatal(err)
	}
	reapResources(now())
	if _, m := do(t, "GET", "/resources", ""); m["count"].(float64) != 0 {
		t.Fatalf("stale resource should be hidden: %v", m)
	}
	if code, _ := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`); code != 400 {
		t.Fatalf("claiming a gone resource should fail, got %d", code)
	}
	// replugged: the provider posts again and it revives without operator action
	addPhone(t, "adb:1", 0)
	if _, m := do(t, "GET", "/resources", ""); m["count"].(float64) != 1 {
		t.Fatalf("resource should revive on refresh: %v", m)
	}
}

func TestDeregisterResourceReleasesLease(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)

	do(t, "POST", "/resources/deregister", `{"id":"adb:1"}`)
	if l := activeLease("adb:1", now()); l != nil {
		t.Fatalf("retiring a resource must drop its lease: %v", l)
	}
}

func TestReaperKeepsMostRecentLeaseRecord(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)
	do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:keepme"}`)

	// age the history well past the GC horizon
	if _, err := db.Exec(`UPDATE leases SET released_at=?`, now()-leaseHistoryTTL-1); err != nil {
		t.Fatal(err)
	}
	reapResources(now())

	// GC must not erase the last thing known about the resource
	if ll := lastFinishedLease("adb:1", now()); ll == nil || ll.Note != "sha256:keepme" {
		t.Fatalf("GC destroyed the durable record: %v", ll)
	}
}

func TestFreeAndHeldFilters(t *testing.T) {
	resSetup(t, "s1")
	addPhone(t, "adb:1", 0)
	addPhone(t, "adb:2", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)

	_, m := do(t, "GET", "/resources?free=1", "")
	if m["count"].(float64) != 1 || m["resources"].([]any)[0].(map[string]any)["id"] != "adb:2" {
		t.Fatalf("free filter wrong: %v", m)
	}
	_, m = do(t, "GET", "/resources?held=1", "")
	if m["count"].(float64) != 1 || m["resources"].([]any)[0].(map[string]any)["id"] != "adb:1" {
		t.Fatalf("held filter wrong: %v", m)
	}
	_, m = do(t, "GET", "/resources?kind=android-device", "")
	if m["count"].(float64) != 2 {
		t.Fatalf("kind filter wrong: %v", m)
	}
}

// The exclusivity claim is the whole feature, so prove it under a real race rather
// than trusting the read-then-write to be lucky: many sessions rush one phone and
// exactly one must come away holding it.
func TestConcurrentClaimsElectExactlyOneHolder(t *testing.T) {
	const n = 24
	sessions := make([]string, n)
	for i := range sessions {
		sessions[i] = "s" + itoa(int64(i))
	}
	resSetup(t, sessions...)
	addPhone(t, "adb:1", 0)

	start := make(chan struct{})
	type res struct {
		code int
		who  string
	}
	out := make(chan res, n)
	for _, s := range sessions {
		go func(s string) {
			<-start
			code, _ := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"`+s+`"}`)
			out <- res{code, s}
		}(s)
	}
	close(start)

	winners, conflicts := 0, 0
	var winner string
	for i := 0; i < n; i++ {
		r := <-out
		switch r.code {
		case 200:
			winners++
			winner = r.who
		case 409:
			conflicts++
		default:
			t.Errorf("unexpected status %d for %s", r.code, r.who)
		}
	}
	if winners != 1 {
		t.Fatalf("want exactly 1 winner, got %d (conflicts %d)", winners, conflicts)
	}
	if l := activeLease("adb:1", now()); l == nil || l.Holder != winner {
		t.Fatalf("board disagrees with the winner %q: %v", winner, l)
	}
}

// A lane arriving seconds after a holder crashed must be shown what THAT holder left,
// not what the holder before it left. The board cannot wait for the reaper to say so.
func TestBoardIsTruthfulBeforeTheReaperRuns(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:first"}`)
	do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:first"}`)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2","note":"sha256:second"}`)

	do(t, "POST", "/deregister", `{"session_id":"s2"}`) // crash mid-hold; no reaper tick yet

	_, m := do(t, "GET", "/resources", "")
	r := m["resources"].([]any)[0].(map[string]any)
	if r["held"] != false {
		t.Fatalf("dead holder should not hold the phone: %v", r)
	}
	ll := r["last_lease"].(map[string]any)
	if ll["note"] != "sha256:second" || ll["holder"] != "s2" {
		t.Fatalf("board shows a stale record: %v", ll)
	}
	if ll["released_by"] != "holder-gone" {
		t.Fatalf("want released_by=holder-gone, got %v", ll["released_by"])
	}
}

// Claiming a phone somebody handed back cleanly must still say what they left on it —
// that is the difference between reusing a resident build and reinstalling blind.
func TestClaimReportsWhatThePreviousHolderLeft(t *testing.T) {
	resSetup(t, "s1", "s2")
	addPhone(t, "adb:1", 0)
	do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s1"}`)
	do(t, "POST", "/resources/release", `{"resource_id":"adb:1","session_id":"s1","note":"sha256:resident"}`)

	_, m := do(t, "POST", "/resources/claim", `{"resource_id":"adb:1","session_id":"s2"}`)
	prev, okc := m["previous"].(map[string]any)
	if !okc || prev["note"] != "sha256:resident" || prev["holder"] != "s1" {
		t.Fatalf("claim did not report the previous hold: %v", m)
	}
	// a clean handback is not a reclaim
	if m["reclaimed"] == true {
		t.Fatalf("clean handback must not be flagged as a reclaim: %v", m)
	}
	// and the first claim on a never-used resource reports nothing at all
	resSetup(t, "s3")
	addPhone(t, "adb:9", 0)
	_, m = do(t, "POST", "/resources/claim", `{"resource_id":"adb:9","session_id":"s3"}`)
	if _, exists := m["previous"]; exists {
		t.Fatalf("fresh resource should have no previous hold: %v", m)
	}
}
