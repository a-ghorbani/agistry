// opencode plugin: register opencode sessions with agistry and deliver agistry mail
// into them. Installed into ~/.config/opencode/plugins/ by clients/opencode/install.sh.
// The registry work is in the shared client (~/.config/agistry/lib/agistry-session.mjs).
//
// opencode creates a plugin once per project directory and sends it the events of
// every session there, so this tracks one agistry session per top-level opencode
// session: from its creation, or from its first activity (a resumed session fires no
// "created"). Subagent sessions (those with a parentID) never join.
const CORE = `${process.env.HOME}/.config/agistry/lib/agistry-session.mjs`

export const Agistry = async ({ client, directory }) => {
  const { createAgistrySession, joinHint } = await import(CORE)
  const sessions = new Map() // opencode session id -> agistry session
  const tracking = new Map() // opencode session id -> promise of the lookup/registration

  // Idempotent and safe to call concurrently: every caller awaits the same promise.
  function track(id, info) {
    if (!id) return Promise.resolve()
    if (!tracking.has(id)) tracking.set(id, register(id, info).catch(() => {}))
    return tracking.get(id)
  }

  async function register(id, info) {
    if (!info) info = (await client.session.get({ path: { id } }).catch(() => ({}))).data
    if (!info || info.parentID) return
    const s = createAgistrySession({
      kind: "opencode",
      sessionId: id,
      cwd: info.directory || directory,
      // Idle: starts a turn so the agent acts on it. Busy: queued behind the running turn.
      deliver: async (text) => {
        const r = await client.session.promptAsync({ path: { id }, body: { parts: [{ type: "text", text }] } })
        return !r.error
      },
    })
    sessions.set(id, s)
    await s.start()
  }

  async function untrack(id, final = false) {
    const s = sessions.get(id)
    sessions.delete(id)
    await s?.stop({ final })
  }

  return {
    event: async ({ event }) => {
      const p = event.properties || {}
      switch (event.type) {
        case "session.created":
          return track(p.info?.id, p.info)
        case "session.status":
        case "session.idle":
          return track(p.sessionID)
        case "session.deleted":
          return untrack(p.info?.id)
        case "server.instance.disposed":
          await Promise.all([...sessions.keys()].map((id) => untrack(id, true)))
      }
    },

    // Every command the agent runs knows which session it belongs to, so the agistry
    // CLI needs no flags. Subagent sessions get nothing and so cannot join.
    "shell.env": async (input, output) => {
      if (!sessions.has(input.sessionID)) return
      output.env.AGISTRY_SESSION_ID = input.sessionID
      output.env.AGISTRY_AGENT_KIND = "opencode"
    },

    // Tell the agent how to join. A first prompt is the first activity, so make sure
    // the session is tracked before its first LLM call.
    "experimental.chat.system.transform": async (input, output) => {
      await track(input.sessionID)
      const s = sessions.get(input.sessionID)
      if (s) output.system.push(joinHint(s.sid, s.url))
    },
  }
}
