// Mastra Code plugin: register the Mastra Code session with agistry and deliver agistry
// mail into it. Installed into ~/.mastracode/plugins/agistry/ by
// clients/mastracode/install.sh. The registry work is in the shared client
// (~/.config/agistry/lib/agistry-session.mjs); this file maps Mastra Code onto it.
//
// Mastra Code has no session-lifecycle API for plugins, but it runs a plugin's signal
// providers for as long as the controller lives: it connects them to the coding agent,
// calls startPolling(), and stop() when the plugin is disabled, reloaded or removed.
// So the whole client is one provider:
//
//   startPolling()  wait for the TUI's session, then register it under an id of its
//                   own: Mastra Code's session id is per project, shared by every TUI
//                   open there, and its thread id changes on /new and resume. One id
//                   per process survives /new and thread switches, so a /new keeps the
//                   task and role, as Claude Code does on /clear.
//   deliver         each message becomes a high-priority notification signal on the
//                   session's current thread: an idle thread wakes and acts on it, a
//                   busy one gets it after the current step. Before the first prompt
//                   there is no thread, and mail waits for one.
//   stop() / exit   deregister
//
// The agent's shell commands inherit AGISTRY_SESSION_ID from this process, so the
// agistry CLI needs no flags. No imports from mastracode, so nothing to npm install;
// the provider is accepted structurally (id, connect, startPolling, stop,
// __registerMastra). Erasable TypeScript only: Mastra Code loads .ts entries directly.
import { execFileSync } from "node:child_process"
import { randomUUID } from "node:crypto"
import { homedir } from "node:os"
import { join } from "node:path"
import { pathToFileURL } from "node:url"

const CORE = pathToFileURL(join(homedir(), ".config/agistry/lib/agistry-session.mjs")).href
const KIND = "mastracode"
const SKILL = "~/.agents/skills/agistry/agistry.sh"

type Ctx = { getActiveSession?: () => any }

function agistryProvider(ctx: Ctx) {
  let agent: any
  let client: any
  let timer: ReturnType<typeof setInterval> | undefined
  let attaching = false
  let stopped = false

  // Deregister synchronously: Mastra Code exits without stopping plugin providers, and
  // an exit handler can't await. The token goes through curl's stdin config, not argv.
  function deregisterNow() {
    if (!client) return
    const { loadConfig } = cfgModule
    const cfg = loadConfig()
    const q = (s: string) => JSON.stringify(s)
    try {
      execFileSync("curl", ["-sS", "-m", "2", "-o", "/dev/null", "-K", "-"], {
        input: [
          `url = ${q(client.url + "/deregister")}`,
          `header = ${q("X-Registry-Token: " + (cfg.AGISTRY_TOKEN || ""))}`,
          `header = "Content-Type: application/json"`,
          `data = ${q(JSON.stringify({ session_id: client.sid }))}`,
        ].join("\n"),
        stdio: ["pipe", "ignore", "ignore"],
        timeout: 3000,
      })
    } catch {}
  }
  let cfgModule: any
  const onExit = () => { if (!stopped) { stopped = true; deregisterNow() } }

  async function deliver(text: string, m: any): Promise<boolean> {
    const session = ctx.getActiveSession?.()
    if (!agent?.sendNotificationSignal || !session) return false
    // A fresh TUI (or one right after /new) has no thread until the user's first
    // prompt. Creating one here would strand the mail: the TUI doesn't adopt it and
    // starts another on that prompt. So hold it pending until the thread exists.
    const threadId = session.thread.getId()
    if (!threadId) return false
    const result = await agent.sendNotificationSignal(
      {
        source: "agistry",
        kind: "peer-message",
        priority: "high",
        summary: text,
        sourceId: m.from || "agistry",
        dedupeKey: `agistry:${m.msg_id}`,
        attributes: { msgId: m.msg_id, from: m.from || "" },
      },
      { resourceId: session.identity.getResourceId(), threadId },
    )
    await result?.persisted // stored in the thread's notifications before we ack
    return true
  }

  async function attach() {
    if (client || attaching || stopped) return
    const session = ctx.getActiveSession?.()
    if (!session) return // the controller exists before the TUI's session does
    attaching = true
    try {
      cfgModule = await import(CORE)
      const sid = `mastracode-${randomUUID()}`
      process.env.AGISTRY_SESSION_ID = sid
      process.env.AGISTRY_AGENT_KIND = KIND
      const c = cfgModule.createAgistrySession({ kind: KIND, sessionId: sid, cwd: process.cwd(), deliver })
      await c.start()
      if (stopped) { await c.stop({ final: true }); return }
      client = c
      if (timer) clearInterval(timer)
      timer = undefined
      process.once("exit", onExit)
    } catch (e: any) {
      console.warn(`agistry: could not register (${e?.name || e})`)
    } finally {
      attaching = false
    }
  }

  return {
    id: "agistry",
    name: "agistry",
    __registerMastra() {},
    connect(a: any) { agent = a },
    startPolling() {
      if (timer || client) return
      timer = setInterval(() => void attach(), 1000)
      timer.unref?.()
      void attach()
    },
    stop() {
      if (timer) clearInterval(timer)
      timer = undefined
      process.off("exit", onExit)
      if (stopped) return
      stopped = true
      void client?.stop({ final: true })
    },
  }
}

export default {
  id: "agistry",
  name: "agistry",
  description: "Registers this session with the agistry agent registry and delivers agistry mail into it.",
  instructions:
    "[agistry] This session is registered in the agistry agent registry. As soon as it is " +
    `clear what your task is, declare it: ${SKILL} join <role> <task-tag> (your session id ` +
    "is already in the shell environment). Use its who/send/inbox commands to coordinate " +
    "with other agents. Peer messages arrive as agistry notifications in this session.",
  signalProviders: (ctx: Ctx) => [agistryProvider(ctx)],
}
