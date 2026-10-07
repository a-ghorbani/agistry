// pi extension: mirror the agent's state onto its tmux pane via agent-state.
// Installed into ~/.pi/agent/extensions/. No-op outside tmux.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { spawn, spawnSync } from "node:child_process";

const BIN = process.env.AGENT_STATE_BIN || `${process.env.HOME}/bin/agent-state`;

export default function (pi: ExtensionAPI) {
	if (!process.env.TMUX_PANE) return;

	let running = false;
	let last = "";
	let chain: Promise<unknown> = Promise.resolve();

	// Serialize the writes so a quick working→done can't land out of order.
	const set = (state: string) => {
		if (state === last) return;
		last = state;
		chain = chain.then(
			() =>
				new Promise((resolve) => {
					const p = spawn(BIN, ["pi", state], { stdio: "ignore" });
					p.on("exit", resolve);
					p.on("error", resolve);
				}),
		);
	};

	pi.on("agent_start", () => {
		running = true;
		set("working");
	});
	pi.on("agent_settled", () => {
		running = false;
		set("done");
	});
	// pi has no built-in approvals; only extension prompts (confirm/select/input) block.
	pi.on("ui_prompt_start", () => set("waiting"));
	pi.on("ui_prompt_end", () => set(running ? "working" : "done"));
	// The process is about to exit, so this one can't be fire-and-forget.
	pi.on("session_shutdown", () => {
		spawnSync(BIN, ["pi", "off"], { stdio: "ignore", timeout: 2000 });
	});
}
