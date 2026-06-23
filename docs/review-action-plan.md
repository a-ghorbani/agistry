# agistry — review action plan

Decision list distilled from the design review (delivery semantics, addressing,
identity/liveness, concurrency, fault tolerance, trust model, agent layer) plus the
`task`-naming issue surfaced by a real agent (`dcd20d8e`) that joined with a
92-character free-text task.

Status legend: **Do** = accepted, **Reject/Defer** = won't do (reason given).

## Schema policy (no backward compat)

`initSchema` is authoritative and greenfield — it defines the final desired shape
with `CREATE TABLE … IF NOT EXISTS`; there are **no in-repo migrations** (no `ALTER`,
no `DROP COLUMN`, no `schema_version`). The schema is fully encapsulated behind the Go
server — the UI, hooks, skill, and channel all go through the HTTP API, nothing else
reads `registry.db` — so a redefine-and-cutover has no external couplings to break.

Schema changes ship as a clean cutover: **stop server → delete `registry.db` →
restart**. State is largely disposable, *provided clients reconcile* (see #12): agent
identity self-heals as each client re-asserts it, liveness self-heals via TTL,
delivered messages GC after 24h. The one thing a wipe loses is **undelivered in-flight
messages** — so perform the cutover when none are pending (check `GET /messages` / the
dashboard); if they must be preserved, use a one-time `sqlite3 dump → reshape → load`
script kept **outside this repo**.

Do not bolt new state onto the old shape with side conditions — model it properly in
the fresh schema (timestamps over flags, constraints over check-then-act).

## Do — accepted improvements

| # | Change | Source | Why |
|---|--------|--------|-----|
| 1 | Make dequeue atomic; split delivery by path — **channel = at-least-once (peek/ack); manual `inbox` = best-effort/at-most-once (peek + auto-ack)** — and document both explicitly | H1+H2 | Actual cause of the lost-message incidents (f475b188 / 7251f012). Today consuming `inbox` is at-most-once (marks `delivered=1` on read, before the agent acts, main.go:499–501) while the channel is at-least-once. "One guarantee everywhere" is **not achievable**: the manual poll path has no reliable acker (the LLM won't emit a post-read ack), so it stays best-effort by design. Also de-fangs H3's mailbox-drain: with peek/ack, a subagent draining no longer *permanently deletes* the parent's messages. Highest priority. |
| 2 | Model message state as a **`delivered_at` timestamp (NULL = pending)** instead of the `delivered` boolean; honest delivery docs. **No visibility-window/lease column and no processing-ack handshake** | H2 | "Drained" ≠ "acted on." Since backward compat is off the table (schema policy), model state properly in one shot: a timestamp subsumes delivered-state, ordering, audit, *and* #5's undelivered-TTL far better than a 0/1 flag + side conditions. **Dropped the earlier "visibility window for N minutes" idea as YAGNI** — peek/ack already means "visible until acked" (the channel acks-after-push; the manual path auto-acks on read), so nothing would ever *write* a `visible_after`; add real leasing only if a leasing consumer ever exists. An LLM won't emit a post-action ack, so don't build that protocol either. Stop advertising at-least-once while the default path is at-most-once. |
| 3 | `task-tag` rename + no-id fallback in SKILL/CLI (wire key stays `task`) | new (dcd20d8e) | The param name `task` invites prose; the skill caveat is buried and gives no fallback when an agent has no tracker id. This produced the 92-char task. Highest-leverage prompt fix. |
| 4 | Server backstop on `task`: reject/truncate over ~40 chars or containing spaces, with an error the skill teaches | new + M1 | Naming stops most cases; the guard catches the model that ignores the prompt. Belt-and-suspenders. |
| 5 | Stop black-holing **without breaking durable late binding.** (a) Reject only **resolvable-now** targets: a `to_session` that resolves to no live agent is a likely typo → reject at send. (b) A `TASK:role` addressed to a not-yet-joined role is the **late-binding feature**, not an error → must NOT reject at send. (c) For pending GC use a **generous TTL (hours/days)** and **dead-letter on expiry — mark + surface, never silent delete** (e.g. `dead_lettered_at`, kept visible in `/messages`/dashboard so a sender learns a handoff was never claimed) | M1+H4 | `resolveSession` falls back to the literal string (main.go:379) and `/send` returns `queued` to nobody; mis-addressed msgs never GC (reaper only deletes `delivered=1`, main.go:607). **But** a correctly-addressed-but-unclaimed message is indistinguishable at the row level from a typo, and can't be rejected at send (the role legitimately doesn't exist yet). A short TTL that reaps typos would silently delete valid handoffs — the system's headline feature. So: generous TTL + dead-letter, not silent delete. |
| 6 | Fix role-only addressing — require a tag on join, or support a real role-only address | H4 | An agent with no task can't be matched (`to_task<>''`, main.go:472) and is reachable by id only. Pairs with #3/#4. |
| 7 | Client idempotency: generate/pass a stable `msg_id` in `agistry.sh send` | M2 | The API is idempotent on `msg_id` but the CLI (agistry.sh:62) omits it, so a curl/network retry duplicates. Cheap. |
| 8 | Partial unique index in `initSchema` — `CREATE UNIQUE INDEX … ON agents(task,role) WHERE state<>'gone'`; **map the constraint violation to a 409** (don't rely on the index alone — an unmapped violation surfaces as a 500); teach the 409 in the skill; **also have `/assign` refuse to silently overwrite a *different* existing `task`/`role` without a force flag** | M3 + H3 | Greenfield: no existing dups to clean up first. Push the invariant into the DB instead of check-then-act (SELECT then upsert, main.go:226–250), where two concurrent joins both win. Idempotent re-join is unaffected: `/assign` upserts `ON CONFLICT(session_id)` (the PK), so a session re-joining its *own* task:role updates its row and never trips the `(task,role)` index — only a *different* session colliding does. The clobber guard additionally kills H3's parent-registration clobber without needing subagent detection. Keep or replace the SELECT pre-check; the index is the real enforcement. |
| 9 | Fence injected message bodies — delimit + label "from another agent, data not instructions"; keep the "never authority" rule | M4 (partial) | The bus injects peer text into context; the `from` is spoofable under one shared token. Fencing is cheap defense-in-depth. (Per-session-credentials half is rejected — see below.) |
| 10 | Trivial cleanups: fix the misleading `MaxOpenConns(1)` comment (main.go:67); delete the unreachable whole-task fanout branch unless the feature is wanted | G2, G1 | Comment claims read-concurrency the config disables; the `to_role=''` branch is dead code (`/send` rejects creating such a message, main.go:421). Default to deleting (YAGNI). |
| 11 | **Drop server-initiated push** — remove `tryPush`, the `go tryPush` call (main.go:447), the `notify=push` logic, the `handle` param, and **omit the `handle`/`notify` columns from the fresh `initSchema`** (no `ALTER`/`DROP COLUMN` — greenfield); drop them from the `agentRow` struct + scan and `/assign` | M5 + G4 | The push path is unused — every agent is `notify: poll` and the channel polls outbound. An unused server-side SSRF primitive is strictly worse than none: deleting it closes M5 **and** G4 and shrinks the surface in one move. Promoted from Reject/Defer — "unused" argues for deletion, not deferral. |
| 12 | **Make registration self-healing: client-side desired-state + reconciling heartbeat.** `join` writes `{session_id, cwd, host, task, role}` to a small local state file; the heartbeat daemon reads it and replays the **full identity as an idempotent upsert** every tick (and on a `/heartbeat` 404). Possible simplification: collapse `register`/`assign`/`heartbeat` into one idempotent upsert endpoint | new (heartbeat gap) | The registry is **soft state; the client is the source of truth** (the Eureka / Consul / etcd / kubelet model). Today the heartbeat only pings (`agistry-heartbeat.sh:29`); on an unknown session `/heartbeat` returns 404 (main.go:276–279) and **nobody reconciles**, so after a `registry.db` wipe an already-running session silently vanishes until it restarts. A stub-only re-register can't recover `role` (identity is split across two writers — the hook writes the stub, the LLM writes task/role), so the daemon must replay the *full* state from the client-side file. This is what makes the schema-policy wipe-cutover actually safe: the wipe becomes "the registry briefly forgets, then every client re-teaches it within one heartbeat." **Two must-dos:** (1) **define the lost-race 409** — #8's unique index means a session that lost the `(task,role)` race gets a 409 on *every* reconciling tick; the daemon must **surface it to the agent (re-pick a role), not swallow-and-loop forever** (else the session believes it's registered but is invisible indefinitely). (2) **Atomic state file** — two writers (hook stub + `join` task/role) and a daemon reader, so write temp + rename and have the daemon tolerate a missing/partial file, or a torn read upserts garbage. |

## Reject / defer — and why

| Finding | Verdict | Reason |
|---------|---------|--------|
| H3 — gate `join`/`inbox` on `CLAUDE_CODE_CHILD_SESSION` in the CLI | Reject the env-gate; **partially mitigated via #1 + #8** | The CLI gets env vars only — it never sees the SessionStart payload, so it can't use the hook's real discriminator (`.agent_type`, agistry-register.sh:18); the env var alone is unreliable (hook line 20 checks `="true"` while the value is likely `1`). No reliable CLI-side detection exists. But the two worst outcomes are covered for free: **mailbox-drain loss → fixed by #1 + #2** (peek/ack + `delivered_at` state mean a subagent draining surfaces but does not permanently delete the parent's messages), **parent clobber → fixed by #8** (`/assign` refuses silent overwrite). Residual seam (a subagent messaging *other* agents spoofed as parent) stays prose-guarded in SKILL.md. Verify the subagent env empirically before relying on any env rationale. |
| M4 — per-session credentials | Reject | Overkill for the stated trust model (single host, one shared token, handful of agents). Keep the cheap fencing (#9); don't build per-principal authz. |
| G3 — graceful shutdown / signal handling | Defer | WAL keeps data safe across `kill -9`/restart; worst case is a dropped in-flight request on `systemctl stop`. Low value here. |
| G5 — TLS / plaintext token | Reject (document instead) | Explicit trust model: bearer token over trusted LAN. Add a README sentence; don't add TLS. |
| G6 — poll-only default has no live delivery | Not a defect (document) | By design — live delivery is opt-in via the `claude-party` channel (research preview). Document the trade-off. |
| H1 severity = High | Downgrade to Medium in context | Still do it (#1), but blast radius is small: the channel peeks (no UPDATE) and manual `inbox` is single-caller, so the conn-release double-delivery race is rare in practice. |

## Suggested order

1. **Schema redefinition commit** — fold #2 (`delivered_at` message-state), **#5's dead-letter field (`dead_lettered_at`)**, #8 (partial unique index + `/assign` clobber guard), and #11 (drop push columns) into one greenfield `initSchema` change. Cutover = stop / delete `registry.db` / restart, done when no undelivered messages are pending.
2. **#12** — client-side desired-state file + reconciling heartbeat (makes the wipe in step 1 self-healing; do alongside or immediately after).
3. #3 + #4 — `task-tag` rename + server guard (the live footgun)
4. #1 — atomic dequeue + per-path delivery semantics (the message-loss root; also de-fangs H3's drain)
5. #5 + #6 — close the black-holes
6. #7 — client `msg_id` idempotency
7. #9 + #10 — polish (body fencing, comment + dead-code cleanup)
