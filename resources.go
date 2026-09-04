// Resources: shared, exclusive things agents contend for — an Android phone on a USB
// hub, a GPU, a staging environment, an API quota. agistry does not own or enforce
// access to any of them; it records who holds what so lanes stop interrupting each
// other. A lease is advisory. It prevents collisions; it does not prove anything about
// the resource's state, so verification stays the caller's job.
//
// Two ideas carry the design:
//
//  1. A lease, not a lock. It expires. The dominant failure is not contention, it is
//     an agent that crashes mid-hold and deadlocks the fleet. A lease dies two ways:
//     its own deadline passes, OR its holder's session goes 'gone'. The second is the
//     strong one and is why this lives in agistry rather than a lock file — only the
//     registry knows the holder died. Alive holder -> held; dead holder -> free.
//
//  2. The note is worth more than the lock. Every lease carries an opaque note that
//     agistry stores and never interprets (an installed bundle hash, a job id, a
//     reason). It survives release, so a lane arriving at a *free* resource can still
//     see what the last holder left on it.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

var (
	resourceTTL  int64 // silence after which a resource is presumed gone
	leaseMaxHold int64 // server-wide fallback cap on a single hold
)

const maxResourceIDLen = 200

// validResourceID keeps ids addressable from a shell and a URL. The id should be the
// key the tooling already uses (an adb serial, a GPU uuid), not a friendly name.
func validResourceID(s string) bool {
	if s == "" || len(s) > maxResourceIDLen {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// maxHoldFor resolves the cap for one resource: its own value when set, else the
// server-wide default. A 6h cap for phones running full e2e suites and a 15m cap for
// a scratch emulator are the same mechanism.
func maxHoldFor(resMax int64) int64 {
	if resMax > 0 {
		return resMax
	}
	return leaseMaxHold
}

type leaseView struct {
	Holder     string `json:"holder"`
	HolderTask string `json:"holder_task,omitempty"`
	HolderRole string `json:"holder_role,omitempty"`
	HolderHost string `json:"holder_host,omitempty"`
	Note       string `json:"note,omitempty"`
	AcquiredAt int64  `json:"acquired_at"`
	ExpiresAt  int64  `json:"expires_at"`
	ExpiresIn  int64  `json:"expires_in,omitempty"`
	ReleasedAt int64  `json:"released_at,omitempty"`
	ReleasedBy string `json:"released_by,omitempty"`
}

type resourceRow struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Name         string     `json:"name,omitempty"`
	Host         string     `json:"host,omitempty"`
	Meta         any        `json:"meta,omitempty"`
	Source       string     `json:"source"`
	MaxHold      int64      `json:"max_hold_seconds"`
	State        string     `json:"state"`
	RegisteredAt int64      `json:"registered_at"`
	LastSeen     int64      `json:"last_seen"`
	Held         bool       `json:"held"`
	Lease        *leaseView `json:"lease,omitempty"`      // active hold, if any
	LastLease    *leaseView `json:"last_lease,omitempty"` // most recent finished hold
}

// decodeMeta returns stored meta as parsed JSON when it is valid JSON, else as the raw
// string. Callers put whatever they want here; agistry never reads inside it.
func decodeMeta(s string) any {
	if s == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

// POST /resources {id, kind, name, host, meta, max_hold_seconds}
// Upsert + liveness refresh, mirroring /register for agents. Providers re-post
// periodically; a resource that stops being refreshed ages out to 'gone'.
//
// The server never discovers resources itself: a phone is plugged into a *host*, not
// into the registry, so only something running on that host can see it. Discovery is
// the provider's job; agistry just holds the board.
func handleResourceRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      string          `json:"id"`
		Kind    string          `json:"kind"`
		Name    string          `json:"name"`
		Host    string          `json:"host"`
		Meta    json.RawMessage `json:"meta"`
		MaxHold int64           `json:"max_hold_seconds"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if !validResourceID(in.ID) {
		bad(w, "id required (no spaces, <= 200 chars)")
		return
	}
	if in.MaxHold < 0 {
		bad(w, "max_hold_seconds must be >= 0 (0 = server default)")
		return
	}
	if err := upsertResource(in.ID, in.Kind, in.Name, in.Host, string(in.Meta), "api", in.MaxHold, now()); err != nil {
		fail(w, err)
		return
	}
	// Report the cap now in force, not the one in the request: a partial re-register
	// keeps the stored value, and echoing the request would understate it.
	var stored int64
	_ = db.QueryRow(`SELECT max_hold FROM resources WHERE id=?`, in.ID).Scan(&stored)
	ok(w, map[string]any{"status": "registered", "id": in.ID, "max_hold_seconds": maxHoldFor(stored)})
}

// upsertResource inserts or refreshes one resource. A refresh revives a resource that
// had aged out, so unplugging and replugging a phone heals without operator action.
func upsertResource(id, kind, name, host, meta, source string, maxHold, t int64) error {
	_, err := db.Exec(`
INSERT INTO resources(id, kind, name, host, meta, source, max_hold, state, registered_at, last_seen)
VALUES(?, ?, ?, ?, ?, ?, ?, 'online', ?, ?)
ON CONFLICT(id) DO UPDATE SET
  -- Only overwrite what the caller actually supplied. "provide <id> <kind>" is a
  -- documented form with name and max-hold optional, and a refresh in that shape must
  -- not wipe what the provider published -- silently dropping a phone from a 6h cap
  -- back to the server default would shorten every later hold on it.
  kind     = CASE WHEN excluded.kind     <> '' THEN excluded.kind     ELSE resources.kind     END,
  name     = CASE WHEN excluded.name     <> '' THEN excluded.name     ELSE resources.name     END,
  host     = CASE WHEN excluded.host     <> '' THEN excluded.host     ELSE resources.host     END,
  meta     = CASE WHEN excluded.meta     <> '' THEN excluded.meta     ELSE resources.meta     END,
  max_hold = CASE WHEN excluded.max_hold >  0  THEN excluded.max_hold ELSE resources.max_hold END,
  source=excluded.source, state='online', last_seen=excluded.last_seen`,
		id, kind, name, host, meta, source, maxHold, t, t)
	return err
}

// POST /resources/deregister {id} — retire a resource explicitly instead of waiting
// for it to age out. Any active lease on it is released.
func handleResourceDeregister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(w, r, &in); err != nil || in.ID == "" {
		bad(w, "id required")
		return
	}
	t := now()
	if _, err := db.Exec(`UPDATE resources SET state='gone', last_seen=? WHERE id=?`, t, in.ID); err != nil {
		fail(w, err)
		return
	}
	if _, err := db.Exec(`
UPDATE leases SET released_at=?, released_by='resource-gone'
WHERE resource_id=? AND released_at IS NULL`, t, in.ID); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]string{"status": "deregistered", "id": in.ID})
}

// GET /resources?kind=&host=&id=&free=1&held=1&all=1
// The board: every resource, whether it is held, by whom, until when, and what the
// last holder left behind. Default hides 'gone' resources; all=1 includes them.
func handleResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	for _, f := range []struct{ param, col string }{{"kind", "kind"}, {"host", "host"}, {"id", "id"}} {
		if v := q.Get(f.param); v != "" {
			where = append(where, f.col+"=?")
			args = append(args, v)
		}
	}
	if q.Get("all") != "1" {
		where = append(where, "state!='gone'")
	}
	rows, err := db.Query(`
SELECT id, kind, name, host, meta, source, max_hold, state, registered_at, last_seen
FROM resources WHERE `+strings.Join(where, " AND ")+` ORDER BY kind, id`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	list := []resourceRow{}
	for rows.Next() {
		var x resourceRow
		var meta string
		if err := rows.Scan(&x.ID, &x.Kind, &x.Name, &x.Host, &meta, &x.Source, &x.MaxHold,
			&x.State, &x.RegisteredAt, &x.LastSeen); err != nil {
			fail(w, err)
			return
		}
		x.Meta = decodeMeta(meta)
		x.MaxHold = maxHoldFor(x.MaxHold)
		list = append(list, x)
	}
	if err := rows.Err(); err != nil {
		fail(w, err)
		return
	}

	t := now()
	out := []resourceRow{}
	for i := range list {
		x := list[i]
		x.Lease = activeLease(x.ID, t)
		x.Held = x.Lease != nil
		x.LastLease = lastFinishedLease(x.ID, t)
		if q.Get("free") == "1" && x.Held {
			continue
		}
		if q.Get("held") == "1" && !x.Held {
			continue
		}
		out = append(out, x)
	}
	ok(w, map[string]any{"resources": out, "count": len(out)})
}

// activeLease returns the lease currently in force on a resource, or nil. A row that
// exists but has expired, or whose holder has gone, is not in force — the reaper will
// tidy it, and a claim reclaims it inline, so readers must not wait for either.
func activeLease(resourceID string, t int64) *leaseView {
	var l leaseView
	err := db.QueryRow(`
SELECT l.holder, COALESCE(a.task,''), COALESCE(a.role,''), COALESCE(a.host,''),
       l.note, l.acquired_at, l.expires_at
FROM leases l LEFT JOIN agents a ON a.session_id = l.holder
WHERE l.resource_id=? AND l.released_at IS NULL AND l.expires_at > ?
  AND l.holder IN (SELECT session_id FROM agents WHERE state <> 'gone')`,
		resourceID, t).Scan(&l.Holder, &l.HolderTask, &l.HolderRole, &l.HolderHost,
		&l.Note, &l.AcquiredAt, &l.ExpiresAt)
	if err != nil {
		return nil
	}
	l.ExpiresIn = l.ExpiresAt - t
	return &l
}

// lastFinishedLease is the durable record: what the previous holder left on the
// resource. It is the answer to "my build may not be resident any more — what is?"
//
// "Finished" means no longer in force, which includes a row still marked standing
// whose deadline has passed or whose holder has died. Reading it that way rather than
// trusting released_at keeps the board truthful in the window before the reaper tidies
// up — otherwise a lane arriving right after a holder crashed would be shown the state
// left by the holder before it.
func lastFinishedLease(resourceID string, t int64) *leaseView {
	var l leaseView
	err := db.QueryRow(`
SELECT holder, note, acquired_at, expires_at, COALESCE(released_at,0),
       CASE WHEN released_at IS NOT NULL THEN released_by
            WHEN expires_at <= ?                THEN 'expired'
            ELSE 'holder-gone' END
FROM leases
WHERE resource_id=?
  AND (released_at IS NOT NULL
       OR expires_at <= ?
       OR holder NOT IN (SELECT session_id FROM agents WHERE state <> 'gone'))
ORDER BY id DESC LIMIT 1`, t, resourceID, t).
		Scan(&l.Holder, &l.Note, &l.AcquiredAt, &l.ExpiresAt, &l.ReleasedAt, &l.ReleasedBy)
	if err != nil {
		return nil
	}
	return &l
}

// POST /resources/claim {resource_id, session_id, ttl_seconds, note}
//
// Succeeds if the resource is free, or if the standing lease is stale (expired, or its
// holder's session is gone) — a stale lease is reclaimed inline and the response says
// what it displaced, so the caller knows what it is about to overwrite. There is
// deliberately no `steal`: reclaiming a dead holder needs no ceremony, and taking a
// resource from a *live* holder is a conversation, not an API call. That case returns
// 409 with the holder's identity so the caller can message them via /send.
func handleResourceClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ResourceID string `json:"resource_id"`
		SessionID  string `json:"session_id"`
		TTL        int64  `json:"ttl_seconds"`
		Note       string `json:"note"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.ResourceID == "" || in.SessionID == "" {
		bad(w, "resource_id and session_id required")
		return
	}
	in.SessionID = resolveSession(in.SessionID)
	if !liveSession(in.SessionID) {
		bad(w, "session_id is not a live agent — register before claiming")
		return
	}
	t := now()

	tx, err := db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	// Everything below must go through tx: the pool is capped at one connection, so a
	// stray db.* call inside an open transaction would deadlock.
	defer func() { _ = tx.Rollback() }()

	var state string
	var resMax int64
	if err := tx.QueryRow(`SELECT state, max_hold FROM resources WHERE id=?`, in.ResourceID).
		Scan(&state, &resMax); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown resource: " + in.ResourceID})
		return
	}
	if state == "gone" {
		bad(w, "resource is gone (provider stopped refreshing it): "+in.ResourceID)
		return
	}

	hardCap := maxHoldFor(resMax)
	ttlSec := in.TTL
	capped := false
	if ttlSec <= 0 {
		ttlSec = hardCap
	}
	if ttlSec > hardCap {
		ttlSec, capped = hardCap, true
	}

	// Record what a stale lease was holding before clearing it.
	var displaced *leaseView
	var dv leaseView
	if err := tx.QueryRow(`
SELECT holder, note, acquired_at, expires_at FROM leases
WHERE resource_id=? AND released_at IS NULL`, in.ResourceID).
		Scan(&dv.Holder, &dv.Note, &dv.AcquiredAt, &dv.ExpiresAt); err == nil {
		displaced = &dv
	}

	// A re-claim by the CURRENT holder is a refresh, not a conflict. The CLI retries on
	// a 5s curl timeout, so a committed claim whose response was lost would otherwise
	// come back as 409 naming the caller's own session — and an agent following "ask,
	// never force" would hand back a device it actually owns.
	var ownID int64
	if err := tx.QueryRow(`
SELECT id FROM leases
WHERE resource_id=? AND holder=? AND released_at IS NULL AND expires_at > ?`,
		in.ResourceID, in.SessionID, t).Scan(&ownID); err == nil {
		newExp := t + ttlSec
		if hardStop := dv.AcquiredAt + hardCap; dv.AcquiredAt > 0 && newExp > hardStop {
			newExp = hardStop
		}
		if _, err := tx.Exec(`
UPDATE leases SET expires_at=?, note=CASE WHEN ?<>'' THEN ? ELSE note END
WHERE id=?`, newExp, in.Note, in.Note, ownID); err != nil {
			fail(w, err)
			return
		}
		if err := tx.Commit(); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{
			"status": "acquired", "resource_id": in.ResourceID, "holder": in.SessionID,
			"acquired_at": dv.AcquiredAt, "expires_at": newExp, "expires_in": newExp - t,
			"max_hold_seconds": hardCap, "refreshed": true,
		})
		return
	}

	// Clear the standing lease only if it is stale. A live hold survives and the
	// INSERT below then trips the unique index.
	if _, err := tx.Exec(`
UPDATE leases
SET released_at=?,
    released_by=CASE WHEN expires_at<=? THEN 'expired' ELSE 'holder-gone' END
WHERE resource_id=? AND released_at IS NULL
  AND (expires_at<=? OR holder NOT IN (SELECT session_id FROM agents WHERE state <> 'gone'))`,
		t, t, in.ResourceID, t); err != nil {
		fail(w, err)
		return
	}

	_, err = tx.Exec(`
INSERT INTO leases(resource_id, holder, note, acquired_at, expires_at)
VALUES(?, ?, ?, ?, ?)`, in.ResourceID, in.SessionID, in.Note, t, t+ttlSec)
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback()
			h := activeLease(in.ResourceID, t)
			conflict(w, map[string]any{
				"error":       "held",
				"resource_id": in.ResourceID,
				"holder":      h,
				"hint":        "holder is live — message them (agistry send) and ask them to release; do not force",
			})
			return
		}
		fail(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}

	resp := map[string]any{
		"status":           "acquired",
		"resource_id":      in.ResourceID,
		"holder":           in.SessionID,
		"acquired_at":      t,
		"expires_at":       t + ttlSec,
		"expires_in":       ttlSec,
		"max_hold_seconds": hardCap,
	}
	if capped {
		resp["capped"] = true
	}
	// What was here before me. On a clean handback this is the last holder's parting
	// note; on a reclaim it is the hold we just displaced. Either way it is the answer
	// to "is my build still resident, or must I reinstall?" — returned here because
	// claim time is when the caller needs it, not one extra round trip later.
	if prev := lastFinishedLease(in.ResourceID, t); prev != nil {
		resp["previous"] = prev
	}
	if displaced != nil {
		// The hold we took was standing but stale (expired, or its holder died) rather
		// than handed back — so nobody tidied up, and state may be mid-flight.
		resp["reclaimed"] = true
	}
	ok(w, resp)
}

// POST /resources/renew {resource_id, session_id, ttl_seconds}
// Extends a hold, never past acquired_at + the resource's cap — otherwise renewal
// would defeat the cap and an agent that is alive but has moved on could sit on a
// phone forever. Hitting the cap is the signal to re-claim deliberately.
func handleResourceRenew(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ResourceID string `json:"resource_id"`
		SessionID  string `json:"session_id"`
		TTL        int64  `json:"ttl_seconds"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.ResourceID == "" || in.SessionID == "" {
		bad(w, "resource_id and session_id required")
		return
	}
	in.SessionID = resolveSession(in.SessionID)
	t := now()

	// One transaction, and every read inside it goes through tx: the pool holds a single
	// connection, so a stray db.* call here would deadlock.
	tx, err := db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	// The holder-liveness clause matters as much as the deadline: once a session is
	// gone the board already shows the resource free and another agent can take it, so
	// renewing in that window would leave two agents believing they hold one device.
	var leaseID, acquiredAt, expiresAt, resMax int64
	err = tx.QueryRow(`
SELECT l.id, l.acquired_at, l.expires_at, r.max_hold
FROM leases l JOIN resources r ON r.id = l.resource_id
WHERE l.resource_id=? AND l.holder=? AND l.released_at IS NULL AND l.expires_at > ?
  AND l.holder IN (SELECT session_id FROM agents WHERE state <> 'gone')`,
		in.ResourceID, in.SessionID, t).Scan(&leaseID, &acquiredAt, &expiresAt, &resMax)
	if err != nil {
		_ = tx.Rollback()
		conflict(w, map[string]any{
			"error":       "not held by you",
			"resource_id": in.ResourceID,
			"holder":      activeLease(in.ResourceID, t),
			"hint":        "the lease lapsed or was reclaimed — claim again before continuing",
		})
		return
	}

	hardCap := maxHoldFor(resMax)
	// An omitted ttl means "as long as I am allowed", which lands exactly on the hard
	// stop. That is the ordinary case, so it is not a clamp — `capped` is reserved for
	// a ttl the caller actually asked for and did not get, otherwise the flag fires on
	// every default renew and stops carrying information.
	explicit := in.TTL > 0
	ttlSec := in.TTL
	if !explicit {
		ttlSec = hardCap
	}
	newExp := t + ttlSec
	hardStop := acquiredAt + hardCap
	capped := false
	if newExp > hardStop {
		newExp = hardStop
		capped = explicit
	}
	if newExp < expiresAt {
		newExp = expiresAt // never shorten a hold by renewing it
	}

	res, err := tx.Exec(`UPDATE leases SET expires_at=? WHERE id=? AND released_at IS NULL`, newExp, leaseID)
	if err != nil {
		fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The reaper released it between the select and the update. Reporting success
		// here would be the same lie the liveness clause above exists to prevent.
		_ = tx.Rollback()
		conflict(w, map[string]any{
			"error":       "not held by you",
			"resource_id": in.ResourceID,
			"holder":      activeLease(in.ResourceID, t),
			"hint":        "the lease was released while renewing — claim again before continuing",
		})
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}

	resp := map[string]any{
		"status": "renewed", "resource_id": in.ResourceID,
		"expires_at": newExp, "expires_in": newExp - t, "max_hold_seconds": hardCap,
	}
	if capped {
		resp["capped"] = true
	}
	// Warn only when the wall is actually close, so the warning still means something.
	if remaining := hardStop - t; remaining <= 600 {
		resp["at_max_hold"] = true
		resp["hint"] = "approaching this resource's max hold — release and re-claim if you need longer"
	}
	ok(w, resp)
}

// POST /resources/release {resource_id, session_id, note}
// Ends a hold. A note given here overwrites the lease note, which is the point at
// which a holder records what it left on the resource for whoever arrives next.
func handleResourceRelease(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ResourceID string `json:"resource_id"`
		SessionID  string `json:"session_id"`
		Note       string `json:"note"`
	}
	if err := readJSON(w, r, &in); err != nil {
		bad(w, "invalid json")
		return
	}
	if in.ResourceID == "" || in.SessionID == "" {
		bad(w, "resource_id and session_id required")
		return
	}
	in.SessionID = resolveSession(in.SessionID)
	t := now()
	res, err := db.Exec(`
UPDATE leases
SET released_at=?, released_by='holder', note=CASE WHEN ?<>'' THEN ? ELSE note END
WHERE resource_id=? AND holder=? AND released_at IS NULL`,
		t, in.Note, in.Note, in.ResourceID, in.SessionID)
	if err != nil {
		fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Idempotent: releasing something you no longer hold is not an error, it is a
		// no-op worth reporting, because a lapsed hold means someone else may be on it.
		ok(w, map[string]any{
			"status": "not-held", "resource_id": in.ResourceID,
			"holder": activeLease(in.ResourceID, t),
		})
		return
	}
	ok(w, map[string]string{"status": "released", "resource_id": in.ResourceID})
}

// reapResources ages out silent resources and tidies leases that are no longer in
// force. Claim already reclaims a stale lease inline, so this is hygiene (and keeps
// the board honest for readers), not a correctness dependency.
func reapResources(t int64) {
	// Config-seeded resources have no provider to refresh them; the server keeps them alive.
	if _, err := db.Exec(`UPDATE resources SET last_seen=? WHERE source='config' AND state='online'`, t); err != nil {
		log.Printf("reaper resources config refresh: %v", err)
	}
	if _, err := db.Exec(`UPDATE resources SET state='gone' WHERE state!='gone' AND last_seen < ?`, t-resourceTTL); err != nil {
		log.Printf("reaper resources: %v", err)
	}
	if _, err := db.Exec(`
UPDATE leases SET released_at=?, released_by='expired'
WHERE released_at IS NULL AND expires_at <= ?`, t, t); err != nil {
		log.Printf("reaper leases expired: %v", err)
	}
	if _, err := db.Exec(`
UPDATE leases SET released_at=?, released_by='holder-gone'
WHERE released_at IS NULL
  AND holder NOT IN (SELECT session_id FROM agents WHERE state <> 'gone')`, t); err != nil {
		log.Printf("reaper leases holder-gone: %v", err)
	}
	// Keep lease history bounded, but keep the most recent hold per resource forever —
	// that row is the durable record of what was left on the resource.
	if _, err := db.Exec(`
DELETE FROM leases
WHERE released_at IS NOT NULL AND released_at < ?
  AND id NOT IN (SELECT MAX(id) FROM leases WHERE released_at IS NOT NULL GROUP BY resource_id)`,
		t-leaseHistoryTTL); err != nil {
		log.Printf("reaper lease gc: %v", err)
	}
}

const leaseHistoryTTL = 7 * 24 * 3600

// loadResourceFile seeds resources from a JSON file for things with no provider to
// announce them — a staging URL, an API quota, a rack of machines that are simply
// always there. Anything discoverable (a phone on a USB hub) should be posted by a
// provider on its own host instead, so it appears and disappears with reality.
func loadResourceFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var seed []struct {
		ID      string          `json:"id"`
		Kind    string          `json:"kind"`
		Name    string          `json:"name"`
		Host    string          `json:"host"`
		Meta    json.RawMessage `json:"meta"`
		MaxHold int64           `json:"max_hold_seconds"`
	}
	if err := json.Unmarshal(b, &seed); err != nil {
		return err
	}
	t := now()
	for _, s := range seed {
		if !validResourceID(s.ID) {
			log.Printf("resources file: skipping invalid id %q", s.ID)
			continue
		}
		if err := upsertResource(s.ID, s.Kind, s.Name, s.Host, string(s.Meta), "config", s.MaxHold, t); err != nil {
			return err
		}
	}
	log.Printf("seeded %d resource(s) from %s", len(seed), path)
	return nil
}
