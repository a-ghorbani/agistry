// pi extension: join the agistry registry and receive agistry mail in this session.
// Installed into ~/.pi/agent/extensions/ by clients/pi/install.sh. The registry work is
// in the shared client (~/.config/agistry/lib/agistry-session.mjs); this file only maps
// pi's session lifecycle onto it.
//
// pi runs this factory once per session (startup, /new, /resume, /fork, /reload), so all
// state lives in this closure.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const CORE = `${process.env.HOME}/.config/agistry/lib/agistry-session.mjs`;

export default function (pi: ExtensionAPI) {
	let session: { sid: string; url: string; start(): Promise<void>; stop(o?: object): Promise<void> } | undefined;
	let hint = "";

	pi.on("session_start", async (event, ctx) => {
		const { createAgistrySession, joinHint } = await import(CORE);
		const sid = ctx.sessionManager.getSessionId();
		session = createAgistrySession({
			kind: "pi",
			sessionId: sid,
			cwd: ctx.cwd,
			// a /new replaces the previous session in this process, like Claude's /clear
			inherit: event.reason === "new",
			// Idle: starts a turn so the agent acts on it. Busy: queued after the current run.
			deliver: async (text: string) => {
				pi.sendMessage({ customType: "agistry", content: text, display: true }, { triggerTurn: true, deliverAs: "followUp" });
				return true;
			},
		});
		await session.start();
		// /reload keeps the same session, whose agent was already told
		if (event.reason !== "reload") hint = joinHint(sid, session.url);
	});

	// Tell the agent how to join, once, with its first prompt.
	pi.on("before_agent_start", () => {
		if (!hint) return;
		const content = hint;
		hint = "";
		return { message: { customType: "agistry", content, display: false } };
	});

	pi.on("session_shutdown", async (event) => {
		// /reload brings the same session straight back: keep it registered
		await session?.stop({ deregister: event.reason !== "reload", final: event.reason === "quit" });
		session = undefined;
	});
}
