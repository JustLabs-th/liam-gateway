"use client";
import { useEffect, useRef, useState } from "react";
import { api, money, milliseconds, type Catalog, type RequestDetail } from "@/lib/api";
import {
  chatRequest,
  consumeCompletion,
  emptyCompletion,
  type ChatRequest,
  type Completion,
  type PlaygroundDraft,
  type ChatMessage,
} from "@/lib/inference";
import { Conversation } from "./conversation";
function initialRequest(catalog: Catalog): ChatRequest {
  return {
    model: Object.keys(catalog.models)[0] ?? "",
    messages: [
      { role: "system", content: "You are a helpful assistant." },
      { role: "user", content: "" },
    ],
    max_tokens: 256,
    temperature: 0.7,
    stream: true,
  };
}
export function PlaygroundPanel({
  catalog,
  draft,
  onInspect,
}: {
  catalog: Catalog;
  draft: PlaygroundDraft | null;
  onInspect: (id: string) => void;
}) {
  const [body, setBody] = useState<ChatRequest>(() => draft?.body ?? initialRequest(catalog));
  const [token, setToken] = useState("");
  const [result, setResult] = useState<Completion>(emptyCompletion);
  const [status, setStatus] = useState<"idle" | "running" | "completed" | "error" | "cancelled">(
    "idle",
  );
  const [error, setError] = useState("");
  const [requestID, setRequestID] = useState("");
  const [record, setRecord] = useState<RequestDetail | null>(null);
  const [firstOutput, setFirstOutput] = useState<number | null>(null);
  const [duration, setDuration] = useState<number | null>(null);
  const [copyStatus, setCopyStatus] = useState("");
  const [json, setJSON] = useState("");
  const mounted = useRef(true);
  const controller = useRef<AbortController | null>(null);
  const settlementController = useRef<AbortController | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.current?.abort();
      settlementController.current?.abort();
    };
  }, []);
  const running = status === "running";
  const messages = body.messages;
  const lastUser = messages.findLastIndex((m) => m.role === "user");
  const user = lastUser >= 0 ? (messages[lastUser].content ?? "") : "";
  const system = messages
    .filter((m) => m.role === "system")
    .map((m) => m.content ?? "")
    .join("\n\n");
  function patch(v: Partial<ChatRequest>) {
    setBody((b) => ({ ...b, ...v }));
    setJSON("");
  }
  function setUser(value: string) {
    patch({
      messages:
        lastUser < 0
          ? [...messages, { role: "user", content: value }]
          : messages.map((m, i) => (i === lastUser ? { ...m, content: value } : m)),
    });
  }
  function setSystem(value: string) {
    patch({
      messages: [
        ...(value ? [{ role: "system", content: value }] : []),
        ...messages.filter((m) => m.role !== "system"),
      ],
    });
  }
  async function loadSettlement(id: string) {
    settlementController.current?.abort();
    const c = new AbortController();
    settlementController.current = c;
    try {
      for (let i = 0; i < 15; i++) {
        const v = await api<RequestDetail>(`/requests/${id}`, { signal: c.signal });
        if (c.signal.aborted) return;
        setRecord(v);
        if (v.request.finished_at) return;
        await new Promise<void>((resolve) => {
          const timer = setTimeout(resolve, 200);
          c.signal.addEventListener(
            "abort",
            () => {
              clearTimeout(timer);
              resolve();
            },
            { once: true },
          );
        });
        if (c.signal.aborted) return;
      }
    } catch (e) {
      if (!c.signal.aborted)
        setError(
          (previous) =>
            previous || `Request sent, but usage settlement could not be loaded: ${String(e)}`,
        );
    }
  }
  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (running) return;
    settlementController.current?.abort();
    setError("");
    setRecord(null);
    setResult(emptyCompletion());
    setFirstOutput(null);
    setDuration(null);
    setRequestID("");
    const c = new AbortController();
    controller.current = c;
    setStatus("running");
    const started = performance.now();
    let first = false,
      id = "";
    try {
      const validated = chatRequest(JSON.stringify(body));
      if (!validated) throw new Error("Enter a valid model and conversation");
      const payload = {
        ...validated,
        ...(validated.stream ? { stream_options: { include_usage: true } } : {}),
      };
      if (!validated.stream) delete payload.stream_options;
      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        signal: c.signal,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token.trim()}` },
        body: JSON.stringify(payload),
      });
      id = response.headers.get("X-Request-ID") ?? "";
      setRequestID(id);
      await consumeCompletion(response, (v) => {
        if (c.signal.aborted) return;
        setResult(v);
        if (!first && (v.content || v.tools.length)) {
          first = true;
          setFirstOutput(performance.now() - started);
        }
      });
      if (!c.signal.aborted) setStatus("completed");
    } catch (e) {
      if (c.signal.aborted) {
        setStatus("cancelled");
        setError(
          "Request cancelled. Any partial answer is retained below; usage may still be charged.",
        );
      } else {
        setStatus("error");
        setError(String(e));
      }
    } finally {
      setDuration(performance.now() - started);
      if (controller.current === c) controller.current = null;
      if (id && mounted.current) void loadSettlement(id);
    }
  }
  const requestForCurl = {
    ...body,
    ...(body.stream ? { stream_options: { include_usage: true } } : {}),
  };
  if (!body.stream) delete requestForCurl.stream_options;
  const curl = `curl '${typeof window !== "undefined" ? window.location.origin : "http://localhost:8080"}/v1/chat/completions' \\
  -H "Authorization: Bearer $GATEWAY_API_KEY" \\
  -H 'Content-Type: application/json' \\
  ${body.stream ? "-N " : ""}-d '${JSON.stringify(requestForCurl, null, 2).replace(/'/g, "'\\''")}'`;
  const responseMessage: ChatMessage = {
    role: "assistant",
    content: result.content || null,
    ...(result.tools.length ? { tool_calls: result.tools } : {}),
  };
  return (
    <div className="playground">
      <section className="panel playground-config">
        <div className="panel-heading">
          <div>
            <h2>Compose a request</h2>
            <p>Uses the same gateway, quotas and durable logging as your applications.</p>
          </div>
          <span className="badge">Chat Completions</span>
        </div>
        {draft ? (
          <div className="notice">
            Draft from request <code>{draft.requestID.slice(0, 14)}…</code>. Sending creates a new
            request.
          </div>
        ) : null}
        <form onSubmit={run}>
          <fieldset disabled={running} className="playground-fields">
            <label>
              Gateway API key
              <input
                type="password"
                aria-label="Gateway API key"
                aria-describedby="playground-key-help"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="gw_…"
                autoComplete="off"
                required
              />
              <small id="playground-key-help">
                Held in component memory only. Not saved or included in copied cURL.
              </small>
            </label>
            <div className="form-pair">
              <label>
                Model alias
                <select
                  value={body.model}
                  onChange={(e) => patch({ model: e.target.value })}
                  required
                >
                  {!catalog.models[body.model] && body.model ? (
                    <option value={body.model}>{body.model} (not in catalog)</option>
                  ) : null}
                  {Object.keys(catalog.models).map((n) => (
                    <option key={n}>{n}</option>
                  ))}
                </select>
              </label>
              <label>
                Max output tokens
                <input
                  type="number"
                  min="1"
                  max={catalog.models[body.model]?.max_output_tokens ?? 1000000}
                  value={body.max_tokens ?? 256}
                  onChange={(e) => patch({ max_tokens: Number(e.target.value) })}
                  required
                />
              </label>
            </div>
            <div className="form-pair">
              <label>
                Temperature
                <input
                  type="number"
                  min="0"
                  max={
                    catalog.providers[catalog.models[body.model]?.provider]?.type === "anthropic"
                      ? "1"
                      : "2"
                  }
                  step="0.1"
                  value={body.temperature ?? 0.7}
                  onChange={(e) => patch({ temperature: Number(e.target.value) })}
                />
              </label>
              <label className="checkbox stream-toggle">
                <input
                  type="checkbox"
                  checked={body.stream ?? false}
                  onChange={(e) => patch({ stream: e.target.checked })}
                />
                Stream response
              </label>
            </div>
            <label>
              System instructions
              <textarea
                rows={3}
                value={system}
                onChange={(e) => setSystem(e.target.value)}
                placeholder="Optional system instructions"
              />
            </label>
            <label>
              User message
              <textarea
                rows={5}
                value={user}
                onChange={(e) => setUser(e.target.value)}
                placeholder="Ask something, then run through your gateway…"
                required
              />
            </label>
            <details className="advanced-request">
              <summary>Advanced request JSON · tools & conversation history</summary>
              <textarea
                aria-label="Advanced request JSON"
                className="mono"
                rows={14}
                value={json || JSON.stringify(body, null, 2)}
                onChange={(e) => setJSON(e.target.value)}
              />
              <button
                type="button"
                onClick={() => {
                  const parsed = chatRequest(json || JSON.stringify(body));
                  if (!parsed) {
                    setError("Invalid request JSON: model and messages are required");
                    return;
                  }
                  setBody(parsed);
                  setJSON("");
                  setError("");
                }}
              >
                Apply JSON
              </button>
              <small>
                Apply changes before running. The gateway validates the supported parameters.
              </small>
            </details>
          </fieldset>
          <div className="playground-actions">
            <button className="primary" disabled={running || !token.trim()}>
              {running ? "Generating…" : "Run request ↗"}
            </button>
            {running ? (
              <button type="button" onClick={() => controller.current?.abort()}>
                Stop generation
              </button>
            ) : (
              <button
                type="button"
                onClick={() => {
                  settlementController.current?.abort();
                  setBody(initialRequest(catalog));
                  setJSON("");
                  setResult(emptyCompletion());
                  setRecord(null);
                  setRequestID("");
                  setStatus("idle");
                  setError("");
                  setDuration(null);
                  setFirstOutput(null);
                }}
              >
                New conversation
              </button>
            )}
          </div>
        </form>
        <details className="curl-example">
          <summary>cURL request</summary>
          <pre>{curl}</pre>
          <button
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(curl);
                setCopyStatus("Copied without credentials");
              } catch {
                setCopyStatus("Clipboard unavailable. Select the command above to copy.");
              }
            }}
          >
            Copy cURL
          </button>
          <small role="status">{copyStatus}</small>
        </details>
      </section>
      <section className="panel playground-output">
        <div className="panel-heading">
          <div>
            <h2>Response</h2>
            <p>
              {status === "idle"
                ? "Your model’s answer will appear here."
                : status === "running"
                  ? "Streaming through the gateway…"
                  : `Request ${status}`}
            </p>
          </div>
          <span className={`status ${status === "completed" ? "success" : ""}`}>{status}</span>
        </div>
        {error ? (
          <p role="alert" className="error">
            {error}
          </p>
        ) : null}
        <div className="playground-result-metrics">
          <div>
            <small>Elapsed</small>
            <strong>{milliseconds(duration)}</strong>
          </div>
          <div>
            <small>Browser first output</small>
            <strong>{milliseconds(firstOutput)}</strong>
          </div>
          <div>
            <small>Input / output</small>
            <strong>
              {result.usage
                ? `${result.usage.prompt_tokens} / ${result.usage.completion_tokens}`
                : "—"}
            </strong>
          </div>
          <div>
            <small>Estimated cost</small>
            <strong>
              {record?.request.finished_at ? money(record.request.cost_microusd) : "—"}
            </strong>
          </div>
        </div>
        {result.content || result.tools.length ? (
          <Conversation messages={[responseMessage]} streaming={running} />
        ) : (
          <div className="empty">
            <span>↗</span>
            <h3>
              {running ? "Waiting for the first output…" : "Try a model. Follow every request."}
            </h3>
            <p>
              Use a gateway API key with access to the selected alias.
              <br />
              Requests and responses are captured automatically.
            </p>
          </div>
        )}
        {record ? (
          <p className="usage-note">
            Usage: {record.request.usage_status} · Gateway first output:{" "}
            {milliseconds(
              record.request.first_output_at
                ? Date.parse(record.request.first_output_at) - Date.parse(record.request.started_at)
                : null,
            )}
            {!record.request.finished_at ? " · Settling…" : ""}
          </p>
        ) : null}
        {requestID ? (
          <div className="result-request">
            <code>{requestID}</code>
            <button disabled={running} onClick={() => onInspect(requestID)}>
              Inspect request →
            </button>
          </div>
        ) : null}
        {status === "completed" && (result.content || result.tools.length) ? (
          <div className="playground-actions">
            <button
              onClick={() => {
                patch({
                  messages: [...body.messages, responseMessage, { role: "user", content: "" }],
                });
                settlementController.current?.abort();
                setDuration(null);
                setFirstOutput(null);
                setResult(emptyCompletion());
                setStatus("idle");
                setRequestID("");
                setRecord(null);
                setError("");
              }}
            >
              Continue conversation
            </button>
          </div>
        ) : null}
        {messages.length > 2 ? (
          <details className="history-preview">
            <summary>Conversation sent · {messages.length} messages</summary>
            <Conversation messages={messages} />
          </details>
        ) : null}
      </section>
    </div>
  );
}
