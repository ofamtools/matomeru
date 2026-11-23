// Managed by `mtm setup hooks`: reports OMP turn, tool, and approval state
// through `mtm-hook omp`.
// @ts-nocheck
//
// OMP loads extensions from ~/.omp/agent/extensions/. Its agent_end event marks
// a settled turn unless an automatic continuation is scheduled; tool lifecycle
// and approval events report activity and permission prompts without
// interrupting OMP.

import { spawn } from "node:child_process";

const MTM = "mtm-hook";

function extractToolName(event) {
  return event?.toolName ?? event?.tool_name ?? event?.tool ?? "";
}

function extractToolInput(event) {
  const input = event?.toolInput ?? event?.tool_input ?? event?.input ?? event?.args;
  return input && typeof input === "object" ? input : undefined;
}

function report(event, ctx, extra = {}) {
  let sessionID = "";
  let sessionFile = "";
  try {
    const id = ctx?.sessionManager?.getSessionId?.();
    if (typeof id === "string") sessionID = id;
  } catch {}
  try {
    const file = ctx?.sessionManager?.getSessionFile?.();
    if (typeof file === "string") sessionFile = file;
  } catch {}
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionID,
    transcript_path: sessionFile,
    ...extra,
  });
  try {
    const child = spawn(MTM, ["omp"], {
      stdio: ["pipe", "ignore", "ignore"],
      windowsHide: true,
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
  } catch {
    // Reporting must not interrupt OMP.
  }
}

export default function (pi) {
  const env = process.env;
  if (
    env.MTM_ENV !== "1" &&
    !env.MTM_PANE_ID &&
    env.TUIOS_ENV !== "1" &&
    !env.TUIOS_AGENT
  ) {
    return;
  }
  const mainTUI = (ctx) => ctx?.mode === "tui" && ctx?.agent?.kind === "main";

  pi.on("session_start", (_event, ctx) => {
    if (mainTUI(ctx)) report("session_start", ctx, { busy: ctx?.isIdle?.() === false });
  });
  pi.on("agent_start", (event, ctx) => {
    if (mainTUI(ctx)) {
      const prompt = typeof event?.prompt === "string" ? event.prompt : undefined;
      report("agent_start", ctx, prompt ? { prompt } : {});
    }
  });
  pi.on("agent_end", (event, ctx) => {
    if (mainTUI(ctx) && !event?.willContinue) {
      const extra = {};
      if (typeof event?.summary === "string") extra.summary = event.summary;
      if (typeof event?.response === "string") extra.response = event.response;
      report("agent_end", ctx, extra);
    }
  });
  pi.on("tool_start", (event, ctx) => {
    if (mainTUI(ctx)) {
      const extra = { tool_name: extractToolName(event) };
      const toolInput = extractToolInput(event);
      if (toolInput) extra.tool_input = toolInput;
      report("tool_start", ctx, extra);
    }
  });
  pi.on("tool_end", (event, ctx) => {
    if (mainTUI(ctx)) {
      const extra = { tool_name: extractToolName(event) };
      const toolInput = extractToolInput(event);
      if (toolInput) extra.tool_input = toolInput;
      report("tool_end", ctx, extra);
    }
  });
  pi.on("tool_error", (event, ctx) => {
    if (mainTUI(ctx)) {
      const extra = { tool_name: extractToolName(event) };
      const toolInput = extractToolInput(event);
      if (toolInput) extra.tool_input = toolInput;
      const errMsg = event?.error?.message ?? (typeof event?.error === "string" ? event.error : undefined);
      if (errMsg) extra.error = errMsg;
      report("tool_error", ctx, extra);
    }
  });
  pi.on("tool_approval_requested", (event, ctx) => {
    if (mainTUI(ctx)) {
      const extra = {
        tool_name: extractToolName(event),
        reason: event?.reason,
      };
      const toolInput = extractToolInput(event);
      if (toolInput) extra.tool_input = toolInput;
      report("tool_approval_requested", ctx, extra);
    }
  });
  pi.on("tool_approval_resolved", (_event, ctx) => {
    if (mainTUI(ctx)) report("tool_approval_resolved", ctx);
  });
  pi.on("session_end", (_event, ctx) => {
    if (mainTUI(ctx)) report("session_end", ctx);
  });
}
