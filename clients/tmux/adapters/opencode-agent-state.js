// opencode plugin: mirror the session's state onto its tmux pane via agent-state.
// Installed into ~/.config/opencode/plugins/. No-op outside tmux.
//
// Subagent (task) sessions emit their own busy/idle, so state is derived from the
// set of busy sessions and pending prompts rather than from the last event seen.
import { spawn } from "node:child_process"

const BIN = process.env.AGENT_STATE_BIN || `${process.env.HOME}/bin/agent-state`

export const AgentState = async () => {
  if (!process.env.TMUX_PANE) return {}

  const busy = new Set()
  const pending = new Set()
  let last = ""
  let chain = Promise.resolve()

  // Serialize the writes so a quick working→done can't land out of order.
  const set = (state) => {
    if (state === last) return
    last = state
    chain = chain.then(
      () =>
        new Promise((resolve) => {
          const p = spawn(BIN, ["opencode", state], { stdio: "ignore" })
          p.on("exit", resolve)
          p.on("error", resolve)
        }),
    )
  }

  return {
    event: async ({ event }) => {
      const p = event.properties || {}
      switch (event.type) {
        case "session.status":
          if (p.status?.type === "idle") busy.delete(p.sessionID)
          else busy.add(p.sessionID)
          break
        case "session.idle":
        case "session.error":
          busy.delete(p.sessionID)
          break
        case "permission.asked":
        case "question.asked":
          pending.add(p.id)
          break
        case "permission.replied":
        case "question.replied":
        case "question.rejected":
          pending.delete(p.requestID)
          break
        default:
          return
      }
      set(pending.size ? "waiting" : busy.size ? "working" : "done")
    },
  }
}
