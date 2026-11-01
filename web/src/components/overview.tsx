"use client";
import { useEffect, useState } from "react";
import {
  api,
  money,
  milliseconds,
  type Analytics,
  type AnalyticsRow,
  type Catalog,
} from "@/lib/api";
type Metric = "requests" | "errors" | "p95_latency_ms" | "cost_microusd";
const metricLabels: Record<Metric, string> = {
  requests: "Traffic",
  errors: "Errors",
  p95_latency_ms: "P95 latency",
  cost_microusd: "Estimated spend",
};
export function OverviewPanel({
  catalog,
  generation,
  onInspectRequests,
}: {
  catalog: Catalog;
  generation: number;
  onInspectRequests: (model: string, keyID: string) => void;
}) {
  const [range, setRange] = useState("24h"),
    [model, setModel] = useState(""),
    [key, setKey] = useState("");
  const [data, setData] = useState<Analytics | null>(null),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true);
  const [metric, setMetric] = useState<Metric>("requests");
  useEffect(() => {
    const c = new AbortController();
    setLoading(true);
    setError("");
    const params = new URLSearchParams({ range, model, key_id: key });
    api<Analytics>(`/analytics?${params}`, { signal: c.signal })
      .then((v) => {
        if (!c.signal.aborted) setData(v);
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(String(e));
      })
      .finally(() => {
        if (!c.signal.aborted) setLoading(false);
      });
    return () => c.abort();
  }, [range, model, key, generation]);
  const s = data?.summary;
  return (
    <div className="overview">
      <div className="overview-filters">
        <div className="range-control" aria-label="Analytics time range">
          {[
            ["24h", "Last 24 hours"],
            ["7d", "Last 7 days"],
            ["30d", "Last 30 days"],
          ].map(([v, label]) => (
            <button
              key={v}
              aria-pressed={range === v}
              className={range === v ? "selected" : ""}
              onClick={() => setRange(v)}
            >
              {label}
            </button>
          ))}
        </div>
        <label>
          Model
          <select value={model} onChange={(e) => setModel(e.target.value)}>
            <option value="">All models</option>
            {Object.keys(catalog.models).map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </label>
        <label>
          API key ID
          <input value={key} onChange={(e) => setKey(e.target.value)} placeholder="All keys" />
        </label>
      </div>
      {error ? (
        <p role="alert" className="error">
          {error}
        </p>
      ) : null}
      <div className="stats analytics-stats">
        <MetricCard
          title="INFERENCE REQUESTS"
          value={s?.requests.toLocaleString() ?? "—"}
          detail={`${s?.running ?? 0} in progress · ${s?.completed ?? 0} completed`}
        />
        <MetricCard
          title="ERROR RATE"
          value={s ? `${(s.error_rate * 100).toFixed(1)}%` : "—"}
          detail={`${s?.errors ?? 0} failed or rejected requests`}
        />
        <MetricCard
          title="P95 LATENCY"
          value={milliseconds(s?.p95_latency_ms)}
          detail={`Median ${milliseconds(s?.p50_latency_ms)} · completed lifecycle`}
        />
        <MetricCard
          title="ESTIMATED SPEND"
          value={s ? money(s.cost_microusd) : "—"}
          detail={`${s?.estimated_requests ?? 0} requests with uncertain usage`}
        />
      </div>
      <section className="panel chart-panel" aria-busy={loading}>
        <div className="panel-heading">
          <div>
            <h2>Gateway activity</h2>
            <p>
              Actual inference traffic. Empty buckets show zero; missing latency stays unavailable.
            </p>
          </div>
          <div className="chart-summary">
            <strong>{s?.tokens.toLocaleString() ?? "—"}</strong>
            <small>total tokens · avg first output {milliseconds(s?.avg_ttft_ms)}</small>
          </div>
        </div>
        <div className="chart-tabs">
          {(Object.keys(metricLabels) as Metric[]).map((m) => (
            <button
              key={m}
              className={metric === m ? "selected" : ""}
              aria-pressed={metric === m}
              onClick={() => setMetric(m)}
            >
              {metricLabels[m]}
            </button>
          ))}
          {loading ? <span role="status">Updating…</span> : null}
        </div>
        {data ? (
          <ActivityChart
            key={`${range}-${model}-${key}-${metric}-${data.since}-${data.until}`}
            data={data}
            metric={metric}
          />
        ) : (
          <div className="empty" role="status">
            {error ? "Activity could not be loaded." : "Loading activity…"}
          </div>
        )}
      </section>
      <div className="breakdown-grid">
        <Breakdown
          title="Usage by model"
          rows={data?.by_model ?? []}
          onSelect={(r) => onInspectRequests(r.name, key)}
        />
        <Breakdown
          title="Usage by API key"
          rows={data?.by_key ?? []}
          onSelect={(r) => onInspectRequests(model, r.id ?? "")}
        />
      </div>
      <p className="analytics-note">
        {data
          ? `${new Date(data.since).toLocaleString()} — ${new Date(data.until).toLocaleString()}. `
          : ""}
        Inference requests only; /v1/models is excluded. Spend uses configured rates. Statistics
        cover retained request records.
      </p>
    </div>
  );
}
function MetricCard({ title, value, detail }: { title: string; value: string; detail: string }) {
  return (
    <div className="stat">
      <div>{title}</div>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}
function ActivityChart({ data, metric }: { data: Analytics; metric: Metric }) {
  const points = data.series;
  const [active, setActive] = useState(() => {
    const last = points.findLastIndex((p) => p[metric] != null);
    return Math.max(0, last < 0 ? points.length - 1 : last);
  });
  const values = points.map((p) => p[metric]);
  const max = Math.max(1, ...values.filter((v): v is number => v != null));
  const width = 1000,
    height = 220,
    pad = 12;
  const x = (i: number) => pad + (i / Math.max(1, points.length - 1)) * (width - 2 * pad),
    y = (v: number) => height - pad - (v / max) * (height - 2 * pad);
  const path = values
    .map((v, i) => (v == null ? null : { x: x(i), y: y(v) }))
    .reduce(
      (all, p, i, arr) => (p ? `${all} ${i === 0 || !arr[i - 1] ? "M" : "L"}${p.x},${p.y}` : all),
      "",
    );
  const current = points[active];
  const color =
    metric === "errors"
      ? "#bc6d45"
      : metric === "p95_latency_ms"
        ? "#637fbd"
        : metric === "cost_microusd"
          ? "#8a6dae"
          : "#22846b";
  const format = (v: number | null | undefined) =>
    v == null
      ? "—"
      : metric === "cost_microusd"
        ? money(v)
        : metric === "p95_latency_ms"
          ? milliseconds(v)
          : v.toLocaleString();
  return (
    <div className="activity-chart">
      <div className="chart-readout">
        <span>{current ? new Date(current.time).toLocaleString() : "No data"}</span>
        <strong>
          {format(current?.[metric])} <small>{metricLabels[metric]}</small>
        </strong>
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label={`${metricLabels[metric]} over the selected time range`}
      >
        <title>
          {metricLabels[metric]} for {points.length} time buckets
        </title>
        {[0, 0.25, 0.5, 0.75, 1].map((n) => (
          <line
            key={n}
            x1={pad}
            x2={width - pad}
            y1={y(max * n)}
            y2={y(max * n)}
            stroke="#e6eeea"
            strokeDasharray="4 5"
          />
        ))}
        <path
          d={path}
          stroke={color}
          strokeWidth="2.5"
          fill="none"
          vectorEffect="non-scaling-stroke"
        />
        {values.map((v, i) =>
          v == null ? null : (
            <circle
              data-sample="true"
              key={points[i].time}
              cx={x(i)}
              cy={y(v)}
              r="3"
              fill={color}
            />
          ),
        )}
        {current && current[metric] != null ? (
          <>
            <line
              x1={x(active)}
              x2={x(active)}
              y1={pad}
              y2={height - pad}
              stroke={color}
              opacity=".3"
            />
            <circle cx={x(active)} cy={y(current[metric]!)} r="5" fill={color} />
          </>
        ) : null}
        {points.map((p, i) => (
          <rect
            key={p.time}
            x={x(i) - width / points.length / 2}
            y={0}
            width={width / points.length}
            height={height}
            fill="transparent"
            onMouseEnter={() => setActive(i)}
          >
            <title>
              {new Date(p.time).toLocaleString()}: {format(p[metric])}
            </title>
          </rect>
        ))}
      </svg>
      <div className="chart-axis">
        <span>{points[0] ? new Date(points[0].time).toLocaleString() : ""}</span>
        <span>{points.at(-1) ? new Date(points.at(-1)!.time).toLocaleString() : ""}</span>
      </div>
      <label className="chart-scrubber">
        Inspect time bucket
        <input
          type="range"
          min="0"
          max={Math.max(0, points.length - 1)}
          value={active}
          onChange={(e) => setActive(Number(e.target.value))}
        />
      </label>
      <details className="chart-data">
        <summary>View chart data</summary>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Requests</th>
                <th>Errors</th>
                <th>P95 latency</th>
                <th>Est. spend</th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.time}>
                  <td>{new Date(p.time).toLocaleString()}</td>
                  <td>{p.requests}</td>
                  <td>{p.errors}</td>
                  <td>{milliseconds(p.p95_latency_ms)}</td>
                  <td>{money(p.cost_microusd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
function Breakdown({
  title,
  rows,
  onSelect,
}: {
  title: string;
  rows: AnalyticsRow[];
  onSelect: (row: AnalyticsRow) => void;
}) {
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>{title}</h2>
        <span className="count">{rows.length} groups</span>
      </div>
      {rows.length ? (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Traffic</th>
                <th>Tokens</th>
                <th>Est. spend</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id ?? r.name}>
                  <td>
                    <button
                      className="text-link"
                      disabled={r.id === undefined ? !r.name : !r.id}
                      onClick={() => onSelect(r)}
                    >
                      {r.name || "Unrouted"}
                    </button>
                    <small>P95 {milliseconds(r.p95_latency_ms)}</small>
                  </td>
                  <td>
                    {r.requests}
                    <small>{r.errors} errors</small>
                  </td>
                  <td>{r.tokens.toLocaleString()}</td>
                  <td>{money(r.cost_microusd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="empty">
          <h3>No inference traffic in this range.</h3>
          <p>Send a request from Playground to see usage here.</p>
        </div>
      )}
    </section>
  );
}
