import type { ChatMessage } from "@/lib/inference";
import { pretty } from "@/lib/inference";
export function Conversation({
  messages,
  streaming = false,
}: {
  messages: ChatMessage[];
  streaming?: boolean;
}) {
  if (!messages.length)
    return (
      <div className="empty">
        <h3>No conversation content.</h3>
        <p>This request may have been rejected before inference.</p>
      </div>
    );
  return (
    <div className="conversation">
      {messages.map((m, i) => (
        <article key={i} className={`message message-${m.role}`}>
          <div className="message-role">
            <span>
              {m.role === "assistant"
                ? "↗"
                : m.role === "user"
                  ? "U"
                  : m.role === "tool"
                    ? "⌘"
                    : "S"}
            </span>
            {m.role}
            {m.tool_call_id ? <code>{m.tool_call_id}</code> : null}
            {streaming && i === messages.length - 1 ? (
              <span className="stream-indicator">Generating…</span>
            ) : null}
          </div>
          {m.content ? <div className="message-text">{m.content}</div> : null}
          {m.tool_calls?.map((tool, n) => (
            <div className="tool-block" key={`${tool.id}-${n}`}>
              <strong>⌘ {tool.function?.name || "Tool call"}</strong>
              <small>{tool.id}</small>
              <pre>{pretty(tool.function?.arguments || "{}")}</pre>
            </div>
          ))}
        </article>
      ))}
    </div>
  );
}
