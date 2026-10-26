// MATOMERU_SETUP_ID=omp-activity
// Managed by mtm setup; mtm setup uninstall removes only this owned file.
// It records each delivered request and the agent's final answer in mtm's
// history journal (mtm history capture-turn). State reporting stays with
// the TUIOS integration (tuios integration install omp).
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";

const capture = { bin: "mtm", data: "" };

type Turn = {
  id: string;
  session: string;
  window: string;
  pane: string;
  harness: "omp";
  conversation_id: string;
  transcript_path?: string;
  prompt: string;
  started_at: number;
};

async function run(bin: string, args: string[], input?: string): Promise<void> {
  const { promise, resolve, reject } = Promise.withResolvers<void>();
  const child = spawn(bin, args, {
    stdio: [input === undefined ? "ignore" : "pipe", "ignore", "pipe"],
    windowsHide: true, timeout: 5000, killSignal: "SIGKILL",
  });
  let error = "";
  child.stderr?.on("data", chunk => { error = (error + chunk.toString()).slice(-2000); });
  child.once("error", reject);
  child.once("close", code => code === 0 ? resolve() : reject(new Error(error || `${bin} exited ${code}`)));
  child.stdin?.on("error", reject);
  child.stdin?.end(input);
  return promise;
}

export default function (pi: ExtensionAPI) {
  if (process.env.TUIOS_ENV !== "1" || !process.env.TUIOS_PANE_ID || !process.env.TUIOS_SESSION || !process.env.TUIOS_SOCKET) return;
  const session = process.env.TUIOS_SESSION;
  const window = process.env.TUIOS_PANE_ID;
  const socket = process.env.TUIOS_SOCKET;
  const main = (ctx: ExtensionContext) => ctx.mode === "tui" && ctx.agent.kind === "main";
  let active: { start: Turn; prompts: string[]; saved: boolean } | undefined;

  async function save(ctx: ExtensionContext, value: object): Promise<boolean> {
    try {
      await run(capture.bin, ["--data", capture.data, "--socket", socket, "history", "capture-turn", "--stdin"], JSON.stringify(value));
      return true;
    } catch (error) {
      ctx.ui.notify(`Matomeru turn capture failed: ${error instanceof Error ? error.message : String(error)}`, "warning");
      return false;
    }
  }

  pi.on("session_start", (_event, ctx) => { if (main(ctx)) active = undefined; });
  pi.on("session_switch", (_event, ctx) => { if (main(ctx)) active = undefined; });
  // Unlike input, this fires for delivered requests, not queued or handled commands.
  pi.on("before_agent_start", async (event, ctx) => {
    if (!main(ctx)) return;
    if (active?.start.conversation_id !== ctx.sessionManager.getSessionId()) active = undefined;
    if (active) {
      active.prompts.push(event.prompt);
      return;
    }
    const start: Turn = {
      id: randomUUID(), session, window, pane: process.env.TUIOS_WINDOW_NAME || window,
      harness: "omp", conversation_id: ctx.sessionManager.getSessionId(),
      transcript_path: ctx.sessionManager.getSessionFile(), prompt: event.prompt, started_at: Date.now(),
    };
    active = { start, prompts: [event.prompt], saved: await save(ctx, start) };
  });
  pi.on("agent_end", async (event, ctx) => {
    if (!main(ctx) || event.willContinue) return;
    const turn = active;
    active = undefined;
    const message = event.messages.findLast(message => message.role === "assistant");
    if (!message || message.stopReason === "error" || message.stopReason === "aborted") return;
    const response = message.content.filter(part => part.type === "text").map(part => part.text).join("\n");
    if (turn?.saved && turn.start.conversation_id === ctx.sessionManager.getSessionId()) {
      await save(ctx, {
        ...turn.start, prompt: turn.prompts.join("\n\n"), response,
        completed_at: Date.now(), response_message: message,
      });
    }
  });
}
