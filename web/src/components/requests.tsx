"use client";
import { useEffect, useState } from "react";
import { api, money, milliseconds, type Principal, type Catalog, type RequestLog } from "@/lib/api";
import { RequestInspector } from "./inspector";
import type { PlaygroundDraft } from "@/lib/inference";
export function RequestsPanel({
  principal,
  catalog,
  generation,
  initialID,
  initialFilters,
  onReplay,
}: {
  principal: Principal;
  catalog: Catalog;
  generation: number;
  initialID?: string | null;
  initialFilters?: { model: string; keyID: string } | null;
  onReplay: (draft: PlaygroundDraft) => void;
}) {
  const [requests, setRequests] = useState<RequestLog[]>([]);
  const [model, setModel] = useState(initialFilters?.model ?? "");
  const [state, setState] = useState("");
  const [keyID, setKeyID] = useState(initialFilters?.keyID ?? "");
  const [live, setLive] = useState(true);
  const [owner, setOwner] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [before, setBefore] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>(initialID ?? null);
  useEffect(() => {
    if (initialFilters) {
      setModel(initialFilters.model);
      setKeyID(initialFilters.keyID);
      setBefore("");
    }
  }, [initialFilters]);
  useEffect(() => {
    if (initialID) setSelected(initialID);
  }, [initialID]);

  useEffect(() => {
    const c = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const query = new URLSearchParams({
      model,
      state,
      key_id: keyID,
      owner_user_id: owner,
      after: since ? new Date(since).toISOString() : "",
      before: before || (until ? new Date(until).toISOString() : ""),
      limit: "50",
    });
    async function load() {
      setLoading(true);
      try {
        const v = await api<RequestLog[]>(`/requests?${query}`, { signal: c.signal });
        if (!c.signal.aborted) {
          setRequests(v);
          setError("");
        }
      } catch (e) {
        if (!c.signal.aborted) setError(String(e));
      } finally {
        if (!c.signal.aborted) {
          setLoading(false);
          if (live && !before)
            timer = setTimeout(() => {
              if (document.visibilityState === "visible") void load();
              else timer = setTimeout(load, 3000);
            }, 3000);
        }
      }
    }
    void load();
    return () => {
      c.abort();
      clearTimeout(timer);
    };
  }, [model, state, keyID, owner, since, until, before, generation, live]);
  return (
    <>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Request history</h2>
            <p>Full request and response capture, including every upstream attempt.</p>
          </div>
          <div className="actions">
            <button aria-pressed={live} onClick={() => setLive((v) => !v)}>
              {live ? "● Live · Pause" : "▶ Resume live"}
            </button>
            <span className="count">{requests.length} results</span>
          </div>
        </div>
        <div className="filters">
          <label>
            Model
            <select
              value={model}
              onChange={(e) => {
                setModel(e.target.value);
                setBefore("");
              }}
            >
              <option value="">All models</option>
              {Object.keys(catalog.models).map((n) => (
                <option key={n}>{n}</option>
              ))}
            </select>
          </label>
          <label>
            Status
            <select
              value={state}
              onChange={(e) => {
                setState(e.target.value);
                setBefore("");
              }}
            >
              <option value="">All statuses</option>
              {["completed", "rejected", "failed", "incomplete", "interrupted", "running"].map(
                (n) => (
                  <option key={n}>{n}</option>
                ),
              )}
            </select>
          </label>
          <label>
            API key ID
            <input
              placeholder="Filter by key ID"
              value={keyID}
              onChange={(e) => {
                setKeyID(e.target.value);
                setBefore("");
              }}
            />
          </label>
          <label>
            User ID
            <input
              placeholder="Filter by owner"
              value={owner}
              onChange={(e) => {
                setOwner(e.target.value);
                setBefore("");
              }}
            />
          </label>
          <label>
            From
            <input
              type="datetime-local"
              value={since}
              onChange={(e) => {
                setSince(e.target.value);
                setBefore("");
              }}
            />
          </label>
          <label>
            Until
            <input
              type="datetime-local"
              value={until}
              onChange={(e) => {
                setUntil(e.target.value);
                setBefore("");
              }}
            />
          </label>
        </div>
        {error ? (
          <p role="alert" className="error">
            {error}
          </p>
        ) : null}
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Request / Time</th>
                <th>Model</th>
                <th>Status</th>
                <th>Latency / first output</th>
                <th>Tokens</th>
                <th>Est. cost</th>
                <th>Format</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((r) => (
                <tr key={r.id}>
                  <td>
                    <button className="text-link mono" onClick={() => setSelected(r.id)}>
                      {r.id.slice(0, 14)}…
                    </button>
                    <small>{new Date(r.started_at).toLocaleString()}</small>
                  </td>
                  <td>
                    <strong>{r.model || "—"}</strong>
                    <small>{r.provider || "Not routed"}</small>
                  </td>
                  <td>
                    <span className={`status ${r.state === "completed" ? "success" : ""}`}>
                      {r.state}
                    </span>
                    <small>HTTP {r.http_status || "pending"}</small>
                  </td>
                  <td className="mono">
                    {milliseconds(
                      r.finished_at ? Date.parse(r.finished_at) - Date.parse(r.started_at) : null,
                    )}
                    <small>
                      First output{" "}
                      {milliseconds(
                        r.first_output_at
                          ? Date.parse(r.first_output_at) - Date.parse(r.started_at)
                          : null,
                      )}
                    </small>
                  </td>
                  <td className="mono">{(r.input_tokens + r.output_tokens).toLocaleString()}</td>
                  <td className="mono">
                    {money(r.cost_microusd)}
                    <small>{r.usage_status}</small>
                  </td>
                  <td>
                    <span className="format">{r.stream ? "SSE" : "JSON"}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {requests.length === 0 ? (
          <div className="empty">
            <span>↗</span>
            <h3>{loading ? "Loading requests…" : "Your first request starts here."}</h3>
            <p>
              Send a chat completion using a gateway API key.
              <br />
              The complete request history will appear here.
            </p>
            <code>POST /v1/chat/completions</code>
          </div>
        ) : null}
        <div className="pagination">
          <button disabled={!before} onClick={() => setBefore("")}>
            Newest requests
          </button>
          <button
            disabled={requests.length < 50}
            onClick={() => setBefore(requests[requests.length - 1].started_at)}
          >
            Older requests →
          </button>
        </div>
      </section>
      {selected ? (
        <RequestInspector
          key={selected}
          onReplay={onReplay}
          id={selected}
          canRead={principal.can_read_payload}
          onClose={() => setSelected(null)}
        />
      ) : null}
    </>
  );
}
