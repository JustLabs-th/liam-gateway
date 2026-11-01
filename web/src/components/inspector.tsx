"use client";
import { useEffect, useRef, useState } from "react";
import { api, money, milliseconds, type CaptureEvent, type RequestDetail } from "@/lib/api";
import { capturedConversation, pretty, type PlaygroundDraft } from "@/lib/inference";
import { Conversation } from "./conversation";
type Tab = "Conversation" | "Raw JSON" | "Tools" | "Attempts" | "Timing";
const tabs: Tab[] = ["Conversation", "Raw JSON", "Tools", "Attempts", "Timing"];
export function RequestInspector({
  id,
  canRead,
  onClose,
  onReplay,
}: {
  id: string;
  canRead: boolean;
  onClose: () => void;
  onReplay: (draft: PlaygroundDraft) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [detail, setDetail] = useState<RequestDetail | null>(null);
  const [events, setEvents] = useState<CaptureEvent[] | null>(null);
  const [tab, setTab] = useState<Tab>("Conversation");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const payloadController = useRef<AbortController | null>(null);
  useEffect(() => {
    const el = dialog.current;
    el?.showModal();
    return () => {
      el?.close();
      payloadController.current?.abort();
    };
  }, []);
  useEffect(() => {
    const c = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const v = await api<RequestDetail>(`/requests/${id}`, { signal: c.signal });
        if (c.signal.aborted) return;
        setDetail(v);
        if (v.request.state === "running") timer = setTimeout(load, 1500);
      } catch (e) {
        if (!c.signal.aborted) setError(String(e));
      }
    }
    void load();
    return () => {
      c.abort();
      clearTimeout(timer);
    };
  }, [id]);
  async function loadPayload() {
    payloadController.current?.abort();
    const c = new AbortController();
    payloadController.current = c;
    setLoading(true);
    setError("");
    try {
      setEvents(await api<CaptureEvent[]>(`/requests/${id}/payload`, { signal: c.signal }));
    } catch (e) {
      if (!c.signal.aborted) setError(String(e));
    } finally {
      if (!c.signal.aborted) setLoading(false);
    }
  }
  const conversation = events ? capturedConversation(events) : null;
  const r = detail?.request;
  const latency = r?.finished_at
    ? Math.max(0, Date.parse(r.finished_at) - Date.parse(r.started_at))
    : null;
  const ttft = r?.first_output_at ? Date.parse(r.first_output_at) - Date.parse(r.started_at) : null;
  return (
    <dialog
      ref={dialog}
      className="inspector-dialog"
      aria-labelledby="inspector-title"
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === dialog.current) onClose();
      }}
    >
      <section className="inspector-content">
        <div className="panel-heading">
          <div>
            <div className="eyebrow">REQUEST INSPECTOR</div>
            <h2 id="inspector-title" className="mono">
              {id}
            </h2>
          </div>
          <button onClick={onClose} aria-label="Close inspector">
            ✕ Close
          </button>
        </div>
        {error ? (
          <p role="alert" className="error">
            {error}
          </p>
        ) : null}
        {!r ? (
          <p className="padded" role="status">
            Loading request…
          </p>
        ) : (
          <>
            <div className="inspector-summary">
              <span className={`status ${r.state === "completed" ? "success" : ""}`}>
                {r.state}
              </span>
              <strong>{r.model || "Unrouted"}</strong>
              <span>{r.provider || "No provider"}</span>
              <span className="format">{r.stream ? "SSE" : "JSON"}</span>
              <small>{new Date(r.started_at).toLocaleString()}</small>
            </div>
            <div className="inspector-metrics">
              <div>
                <small>Total latency</small>
                <strong>{milliseconds(latency)}</strong>
              </div>
              <div>
                <small>First output · streaming</small>
                <strong>{milliseconds(ttft)}</strong>
              </div>
              <div>
                <small>Input / output tokens</small>
                <strong>
                  {r.input_tokens} / {r.output_tokens}
                </strong>
              </div>
              <div>
                <small>Estimated cost</small>
                <strong>{money(r.cost_microusd)}</strong>
                <small>{r.usage_status}</small>
              </div>
            </div>
            <div className="inspector-toolbar">
              <div>
                {canRead ? (
                  <button className="primary" disabled={loading} onClick={loadPayload}>
                    {loading
                      ? "Loading payload…"
                      : events
                        ? "Reload payload"
                        : "Load full request & response"}
                  </button>
                ) : (
                  <p>Payload access requires additional permission.</p>
                )}
              </div>
              {conversation?.request ? (
                <button onClick={() => onReplay({ requestID: id, body: conversation.request! })}>
                  Open in Playground ↗
                </button>
              ) : null}
            </div>
            <div className="tab-bar" role="tablist" aria-label="Request inspector views">
              {tabs.map((name, index) => (
                <button
                  key={name}
                  id={`inspector-tab-${index}`}
                  role="tab"
                  aria-selected={tab === name}
                  aria-controls="inspector-view"
                  tabIndex={tab === name ? 0 : -1}
                  className={tab === name ? "selected" : ""}
                  onClick={() => setTab(name)}
                  onKeyDown={(e) => {
                    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) return;
                    e.preventDefault();
                    const next =
                      e.key === "Home"
                        ? 0
                        : e.key === "End"
                          ? tabs.length - 1
                          : (index + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
                    setTab(tabs[next]);
                    document.getElementById(`inspector-tab-${next}`)?.focus();
                  }}
                >
                  {name}
                </button>
              ))}
            </div>
            <div
              id="inspector-view"
              role="tabpanel"
              aria-labelledby={`inspector-tab-${tabs.indexOf(tab)}`}
              tabIndex={0}
              className="inspector-view"
            >
              {tab === "Conversation" ? (
                events ? (
                  <>
                    {r.state !== "completed" ? (
                      <p className="notice">
                        This request is {r.state}. Any answer below may be partial.
                      </p>
                    ) : null}
                    {conversation?.error ? (
                      <p role="alert" className="error">
                        {conversation.error}
                      </p>
                    ) : null}
                    <Conversation messages={conversation?.messages ?? []} />
                  </>
                ) : (
                  <PayloadPlaceholder canRead={canRead} />
                )
              ) : null}
              {tab === "Raw JSON" ? (
                <>
                  <details>
                    <summary>Request metadata & attempts</summary>
                    <pre>{JSON.stringify(detail, null, 2)}</pre>
                  </details>
                  {events?.map((e) => (
                    <details key={e.id}>
                      <summary>
                        <span className="badge">{e.stage}</span>{" "}
                        {e.attempt_id ? `Attempt ${e.attempt_id.slice(0, 10)}` : "Client"} ·{" "}
                        {new Date(e.created_at).toLocaleTimeString()}
                      </summary>
                      <pre>{pretty(e.payload_text)}</pre>
                    </details>
                  ))}
                  {!events ? <PayloadPlaceholder canRead={canRead} /> : null}
                </>
              ) : null}
              {tab === "Tools" ? (
                events ? (
                  <Tools messages={conversation?.messages ?? []} />
                ) : (
                  <PayloadPlaceholder canRead={canRead} />
                )
              ) : null}
              {tab === "Attempts" ? (
                <div className="attempt-list">
                  {detail?.attempts.length ? (
                    detail.attempts.map((a) => (
                      <article className="attempt-card" key={a.id}>
                        <div>
                          <strong>Attempt {a.ordinal}</strong>
                          <span className={`status ${a.state === "completed" ? "success" : ""}`}>
                            {a.state}
                          </span>
                        </div>
                        <p>
                          {a.provider} / {a.model}
                        </p>
                        <small className="mono">{a.id}</small>
                        <dl>
                          <dt>HTTP status</dt>
                          <dd>{a.http_status || "No upstream response"}</dd>
                          <dt>Duration</dt>
                          <dd>
                            {milliseconds(
                              a.finished_at
                                ? Date.parse(a.finished_at) - Date.parse(a.started_at)
                                : null,
                            )}
                          </dd>
                          <dt>Started</dt>
                          <dd>{new Date(a.started_at).toLocaleString()}</dd>
                        </dl>
                      </article>
                    ))
                  ) : (
                    <div className="empty">
                      <h3>No upstream attempts.</h3>
                      <p>The request was rejected before contacting a provider.</p>
                    </div>
                  )}
                </div>
              ) : null}
              {tab === "Timing" && detail ? <Timing detail={detail} events={events} /> : null}
            </div>
          </>
        )}
      </section>
    </dialog>
  );
}
function PayloadPlaceholder({ canRead }: { canRead: boolean }) {
  return (
    <div className="empty">
      <span>◷</span>
      <h3>{canRead ? "Load the captured conversation." : "Payload access is restricted."}</h3>
      <p>
        {canRead
          ? "Use “Load full request & response” above. Access is recorded in the audit trail."
          : "Metadata, attempts and timing remain available."}
      </p>
    </div>
  );
}
function Tools({ messages }: { messages: ReturnType<typeof capturedConversation>["messages"] }) {
  const calls = messages.flatMap((m, index) =>
    (m.tool_calls ?? []).map((t) => ({ ...t, turn: index })),
  );
  const results = messages.filter((m) => m.role === "tool");
  if (!calls.length && !results.length)
    return (
      <div className="empty">
        <h3>No tool calls in this request.</h3>
        <p>Function arguments and tool results appear here when tools are used.</p>
      </div>
    );
  return (
    <div className="tool-list">
      {calls.map((c, i) => (
        <article className="attempt-card" key={`${c.id}-${i}`}>
          <strong>⌘ {c.function?.name || "Unknown function"}</strong>
          <small className="mono">
            {c.id} · message {c.turn + 1}
          </small>
          <h3>Arguments</h3>
          <pre>{pretty(c.function?.arguments || "{}")}</pre>
          {results
            .filter((r) => r.tool_call_id === c.id)
            .map((r, n) => (
              <div key={n}>
                <h3>Tool result</h3>
                <pre>{r.content}</pre>
              </div>
            ))}
        </article>
      ))}
      {results
        .filter((r) => !calls.some((c) => c.id === r.tool_call_id))
        .map((r, i) => (
          <article className="attempt-card" key={i}>
            <strong>Tool result</strong>
            <small>{r.tool_call_id}</small>
            <pre>{r.content}</pre>
          </article>
        ))}
    </div>
  );
}
function Timing({ detail, events }: { detail: RequestDetail; events: CaptureEvent[] | null }) {
  const start = Date.parse(detail.request.started_at);
  const end = detail.request.finished_at ? Date.parse(detail.request.finished_at) : Date.now();
  const total = Math.max(1, end - start);
  const rows: { name: string; start: number; end: number; color: string }[] = [
    { name: "Entire request", start, end, color: "#bfd9d0" },
  ];
  detail.attempts.forEach((a) => {
    const begin = Date.parse(a.started_at),
      finish = a.finished_at ? Date.parse(a.finished_at) : end;
    rows.push({
      name: `Attempt ${a.ordinal} · ${a.provider}`,
      start: begin,
      end: finish,
      color: "#3d9a80",
    });
    const header = events?.find(
      (e) => e.attempt_id === a.id && e.stage === "upstream_response_metadata",
    );
    if (header)
      rows.push({
        name: `Attempt ${a.ordinal} · headers captured`,
        start: begin,
        end: Date.parse(header.created_at),
        color: "#7c9fc2",
      });
  });
  if (detail.request.first_output_at) {
    const first = Date.parse(detail.request.first_output_at);
    rows.push({ name: "First streamed output", start, end: first, color: "#b29bce" });
  }
  return (
    <div className="timing">
      <h3>Request waterfall</h3>
      <p>Measured timestamps. Streaming phases can overlap; durations are not additive.</p>
      <div className="timing-scale">
        <span>0 ms</span>
        <span>{milliseconds(total)}</span>
      </div>
      {rows.map((r, i) => (
        <div className="timing-row" key={i}>
          <div>
            <span>{r.name}</span>
            <strong>{milliseconds(Math.max(0, r.end - r.start))}</strong>
          </div>
          <div className="timing-track">
            <span
              style={{
                marginLeft: `${Math.max(0, Math.min(99, ((r.start - start) / total) * 100))}%`,
                width: `${Math.max(0.5, Math.min(100, ((r.end - r.start) / total) * 100))}%`,
                background: r.color,
              }}
            />
          </div>
        </div>
      ))}
      {!events ? (
        <p className="notice">Load the payload to see captured upstream-header timings.</p>
      ) : null}
      {!detail.request.first_output_at ? (
        <p className="notice">
          First-output timing is available for new streaming requests that emit content or tool
          calls.
        </p>
      ) : null}
    </div>
  );
}
