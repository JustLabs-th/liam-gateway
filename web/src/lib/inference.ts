import type { CaptureEvent } from "./api";
export type ToolCall = { id: string; type: string; function: { name: string; arguments: string } };
export type ChatMessage = {
  role: string;
  content: string | null;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
};
export type ChatRequest = Record<string, unknown> & {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  max_tokens?: number;
  temperature?: number;
};
export type PlaygroundDraft = { requestID: string; body: ChatRequest };
export type Completion = {
  content: string;
  tools: ToolCall[];
  usage: { prompt_tokens: number; completion_tokens: number } | null;
  finishReason: string | null;
};
export const emptyCompletion = (): Completion => ({
  content: "",
  tools: [],
  usage: null,
  finishReason: null,
});
export function object(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}
export function parseJSON(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}
export function chatRequest(s: string): ChatRequest | null {
  const v = parseJSON(s);
  if (!object(v) || typeof v.model !== "string" || !Array.isArray(v.messages) || !v.messages.length)
    return null;
  if (
    !v.messages.every(
      (m) =>
        object(m) &&
        typeof m.role === "string" &&
        ["system", "user", "assistant", "tool"].includes(m.role) &&
        (m.tool_call_id == null || typeof m.tool_call_id === "string") &&
        (typeof m.content === "string" || m.content == null) &&
        (!m.tool_calls ||
          (Array.isArray(m.tool_calls) &&
            m.tool_calls.every(
              (t) =>
                object(t) &&
                typeof t.id === "string" &&
                object(t.function) &&
                typeof t.function.name === "string" &&
                typeof t.function.arguments === "string",
            ))),
    )
  )
    return null;
  if (
    v.max_tokens != null &&
    (typeof v.max_tokens !== "number" || !Number.isFinite(v.max_tokens) || v.max_tokens < 1)
  )
    return null;
  if (
    v.temperature != null &&
    (typeof v.temperature !== "number" || !Number.isFinite(v.temperature))
  )
    return null;
  if (v.stream != null && typeof v.stream !== "boolean") return null;
  return v as ChatRequest;
}
export function sseData(frame: string): string {
  return frame
    .split(/\r?\n/)
    .filter((l) => l.startsWith("data:"))
    .map((l) => l.slice(5).replace(/^ /, ""))
    .join("\n");
}
export function applyChunk(current: Completion, v: unknown): Completion {
  if (!object(v)) throw new Error("Invalid completion payload");
  if (object(v.error))
    throw new Error(
      typeof v.error.message === "string" ? v.error.message : "Provider stream failed",
    );
  let next = {
    ...current,
    tools: current.tools.map((t) => ({ ...t, function: { ...t.function } })),
  };
  if (
    object(v.usage) &&
    typeof v.usage.prompt_tokens === "number" &&
    typeof v.usage.completion_tokens === "number"
  )
    next.usage = {
      prompt_tokens: v.usage.prompt_tokens,
      completion_tokens: v.usage.completion_tokens,
    };
  const choice = Array.isArray(v.choices) ? v.choices[0] : null;
  if (!object(choice)) return next;
  if (typeof choice.finish_reason === "string") next.finishReason = choice.finish_reason;
  const delta = object(choice.delta)
    ? choice.delta
    : object(choice.message)
      ? choice.message
      : null;
  if (!delta) return next;
  if (typeof delta.content === "string") next.content += delta.content;
  if (Array.isArray(delta.tool_calls))
    for (const [i, raw] of delta.tool_calls.entries()) {
      if (!object(raw)) continue;
      const index = typeof raw.index === "number" ? raw.index : i;
      if (!Number.isInteger(index) || index < 0 || index > 128)
        throw new Error("Invalid tool call index");
      const tool = next.tools[index] ?? {
        id: "",
        type: "function",
        function: { name: "", arguments: "" },
      };
      if (typeof raw.id === "string") tool.id = raw.id;
      if (object(raw.function)) {
        if (typeof raw.function.name === "string") tool.function.name += raw.function.name;
        if (typeof raw.function.arguments === "string")
          tool.function.arguments += raw.function.arguments;
      }
      next.tools[index] = tool;
    }
  return next;
}
export async function consumeCompletion(
  response: Response,
  onUpdate: (v: Completion) => void,
): Promise<Completion> {
  if (!response.ok) {
    const text = await response.text();
    const parsed = parseJSON(text);
    throw new Error(
      object(parsed) && object(parsed.error) && typeof parsed.error.message === "string"
        ? parsed.error.message
        : `HTTP ${response.status}: ${text.slice(0, 300)}`,
    );
  }
  let state = emptyCompletion();
  if (!response.headers.get("content-type")?.includes("text/event-stream")) {
    state = applyChunk(state, await response.json());
    onUpdate(state);
    return state;
  }
  if (!response.body) throw new Error("Streaming response has no body");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "",
    done = false;
  function frame(raw: string) {
    const data = sseData(raw);
    if (!data) return;
    if (data === "[DONE]") {
      done = true;
      return;
    }
    if (done) throw new Error("Unexpected data after terminal event");
    state = applyChunk(state, JSON.parse(data));
    onUpdate(state);
  }
  try {
    for (;;) {
      const chunk = await reader.read();
      buffer += decoder.decode(chunk.value, { stream: !chunk.done });
      let match: RegExpExecArray | null;
      while ((match = /\r?\n\r?\n/.exec(buffer)) !== null) {
        frame(buffer.slice(0, match.index));
        buffer = buffer.slice(match.index + match[0].length);
      }
      if (buffer.length > 4 * 1024 * 1024) throw new Error("Stream event exceeds size limit");
      if (chunk.done) break;
    }
    if (buffer.trim()) frame(buffer);
    if (!done)
      throw new Error("Stream ended before completion. The partial answer is shown below.");
    return state;
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
// Decode bytes as a single stream so a UTF-8 character split between captures is preserved.
export function eventText(events: CaptureEvent[], stage: string): string {
  const decoder = new TextDecoder();
  let text = "";
  for (const e of events.filter((e) => e.stage === stage)) {
    try {
      const bytes = Uint8Array.from(atob(e.payload_base64), (c) => c.charCodeAt(0));
      text += decoder.decode(bytes, { stream: true });
    } catch {
      text += e.payload_text;
    }
  }
  return text + decoder.decode();
}
export function capturedConversation(events: CaptureEvent[]): {
  request: ChatRequest | null;
  messages: ChatMessage[];
  error: string | null;
} {
  const request = chatRequest(eventText(events, "client_request"));
  const messages = request ? [...request.messages] : [];
  let state = emptyCompletion(),
    error: string | null = null;
  try {
    const body = eventText(events, "client_response");
    if (body) state = applyChunk(state, parseJSON(body));
    for (const event of events.filter((e) => e.stage === "client_response_event")) {
      const data = sseData(event.payload_text);
      if (data && data !== "[DONE]") state = applyChunk(state, parseJSON(data));
    }
  } catch (e) {
    error = String(e);
  }
  if (state.content || state.tools.length)
    messages.push({
      role: "assistant",
      content: state.content || null,
      ...(state.tools.length ? { tool_calls: state.tools } : {}),
    });
  return { request, messages, error };
}
export function pretty(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
