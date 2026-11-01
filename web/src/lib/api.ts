export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/admin/api${path}`, {
    ...options,
    cache: "no-store",
    headers: { "Content-Type": "application/json", "X-Gateway-Admin": "1", ...options.headers },
  });
  if (!response.ok) throw new APIError(response.status, await response.text());
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
export type Principal = {
  id: string;
  email: string;
  role: "admin" | "viewer";
  can_read_payload: boolean;
  auth_mode: "disabled" | "oidc";
};
export type Key = {
  id: string;
  name: string;
  prefix: string;
  allowed_models: string[];
  rpm: number;
  concurrency: number;
  monthly_budget_microusd: number;
  revoked_at: string | null;
};
export type Model = {
  provider: string;
  model: string;
  max_input_tokens: number;
  max_output_tokens: number;
  input_usd_per_million: number;
  output_usd_per_million: number;
};
export type Catalog = {
  models: Record<string, Model>;
  providers: Record<string, { type: string; configured: boolean }>;
};
export type RequestLog = {
  id: string;
  key_id: string | null;
  model: string;
  provider: string;
  stream: boolean;
  state: string;
  http_status: number;
  input_tokens: number;
  output_tokens: number;
  cost_microusd: number;
  usage_status: string;
  started_at: string;
  finished_at: string | null;
  first_output_at: string | null;
  error_code: string;
};
export type CaptureEvent = {
  id: number;
  stage: string;
  attempt_id: string | null;
  payload_text: string;
  payload_base64: string;
  created_at: string;
};
export type Overview = {
  requests: number;
  completed: number;
  tokens: number;
  cost_microusd: number;
  estimated_requests: number;
};
export const money = (micro: number) =>
  new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 6,
  }).format(micro / 1_000_000);

export type Attempt = {
  id: string;
  request_id: string;
  ordinal: number;
  provider: string;
  model: string;
  state: string;
  http_status: number;
  started_at: string;
  finished_at: string | null;
};
export type RequestDetail = { request: RequestLog; attempts: Attempt[] };
export type AnalyticsRow = {
  name: string;
  id?: string;
  requests: number;
  errors: number;
  tokens: number;
  cost_microusd: number;
  p95_latency_ms: number | null;
};
export type AnalyticsPoint = {
  time: string;
  requests: number;
  errors: number;
  tokens: number;
  cost_microusd: number;
  p95_latency_ms: number | null;
};
export type Analytics = {
  since: string;
  until: string;
  summary: {
    requests: number;
    completed: number;
    running: number;
    errors: number;
    error_rate: number;
    tokens: number;
    cost_microusd: number;
    estimated_requests: number;
    p50_latency_ms: number | null;
    p95_latency_ms: number | null;
    avg_ttft_ms: number | null;
  };
  series: AnalyticsPoint[];
  by_model: AnalyticsRow[];
  by_key: AnalyticsRow[];
};
export const milliseconds = (n: number | null | undefined) =>
  n == null ? "—" : n >= 1000 ? `${(n / 1000).toFixed(2)} s` : `${Math.round(n)} ms`;
