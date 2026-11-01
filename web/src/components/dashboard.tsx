"use client";
import { useCallback, useEffect, useState } from "react";
import { api, APIError, money, type Catalog, type Principal, type Overview } from "@/lib/api";
import type { PlaygroundDraft } from "@/lib/inference";
import { KeysPanel, ModelsPanel } from "./settings";
import { RequestsPanel } from "./requests";
import { UsersPanel, AuditPanel } from "./users";
import { OverviewPanel } from "./overview";
import { PlaygroundPanel } from "./playground";
type Tab = "overview" | "requests" | "playground" | "keys" | "models" | "users" | "audit";
const navigation: Record<Tab, { name: string; icon: string; title: string; description: string }> =
  {
    overview: {
      name: "Overview",
      icon: "▥",
      title: "Your gateway, at a glance.",
      description: "Follow traffic, reliability and spend across every model and API key.",
    },
    requests: {
      name: "Request history",
      icon: "◷",
      title: "Every request, in view.",
      description: "Read conversations, trace attempts, and understand where time is spent.",
    },
    playground: {
      name: "Playground",
      icon: "▷",
      title: "From prompt to production.",
      description: "Try models, stream answers, and follow the same request into your logs.",
    },
    keys: {
      name: "API keys",
      icon: "⌘",
      title: "Access, on your terms.",
      description: "Manage credentials, model permissions and usage limits.",
    },
    models: {
      name: "Models",
      icon: "◇",
      title: "One API. Your models.",
      description: "Configure model aliases and estimated token pricing.",
    },
    users: {
      name: "Users",
      icon: "♧",
      title: "People & permissions.",
      description: "Manage SSO accounts and access to captured content.",
    },
    audit: {
      name: "Audit trail",
      icon: "≡",
      title: "A record of every action.",
      description: "Inspect administrative actions and payload access.",
    },
  };
export function Dashboard() {
  const [me, setMe] = useState<Principal | null>(null),
    [overview, setOverview] = useState<Overview | null>(null),
    [catalog, setCatalog] = useState<Catalog | null>(null);
  const [tab, setTab] = useState<Tab>("overview"),
    [error, setError] = useState(""),
    [login, setLogin] = useState(false),
    [generation, setGeneration] = useState(0);
  const [draft, setDraft] = useState<PlaygroundDraft | null>(null),
    [selectedID, setSelectedID] = useState<string | null>(null),
    [filters, setFilters] = useState<{ model: string; keyID: string } | null>(null);
  const refresh = useCallback(() => setGeneration((n) => n + 1), []);
  useEffect(() => {
    const c = new AbortController();
    setError("");
    Promise.all([
      api<Principal>("/me", { signal: c.signal }),
      api<Overview>("/overview", { signal: c.signal }),
      api<Catalog>("/models", { signal: c.signal }),
    ])
      .then(([p, s, m]) => {
        if (c.signal.aborted) return;
        setMe(p);
        setOverview(s);
        setCatalog(m);
        setLogin(false);
      })
      .catch((e) => {
        if (c.signal.aborted) return;
        if (e instanceof APIError && e.status === 401) setLogin(true);
        else setError(String(e));
      });
    return () => c.abort();
  }, [generation]);
  function navigate(t: Tab) {
    setTab(t);
    setSelectedID(null);
    if (t === "requests") setFilters(null);
    if (t === "playground") setDraft(null);
  }
  function inspect(id: string) {
    setSelectedID(id);
    setFilters(null);
    setTab("requests");
    refresh();
  }
  function replay(v: PlaygroundDraft) {
    setDraft(v);
    setTab("playground");
  }
  function filteredRequests(model: string, keyID: string) {
    setFilters({ model, keyID });
    setSelectedID(null);
    setTab("requests");
  }
  if (login)
    return (
      <main className="login">
        <span className="brand">↗ relay</span>
        <h1>
          Your models.
          <br />
          One gateway.
        </h1>
        <p>Sign in with your organization to continue.</p>
        <a className="button primary" href="/auth/login">
          Continue with SSO →
        </a>
      </main>
    );
  const tabs: Tab[] =
    me?.role === "admin"
      ? ["overview", "requests", "playground", "keys", "models", "users", "audit"]
      : ["overview", "requests", "playground", "models"];
  return (
    <div className="shell">
      <aside>
        <a className="brand" href="/admin">
          ↗ relay<span>LLM GATEWAY</span>
        </a>
        <div className="workspace">
          <span className="workspace-icon">R</span>
          <div>
            Gateway workspace<small>Phase 1 · Self hosted</small>
          </div>
        </div>
        <div className="nav-label">WORKSPACE</div>
        <nav aria-label="Main navigation">
          {tabs.map((t) => (
            <button
              key={t}
              className={tab === t ? "active" : ""}
              aria-current={tab === t ? "page" : undefined}
              onClick={() => navigate(t)}
            >
              <span aria-hidden="true">{navigation[t].icon}</span>
              {navigation[t].name}
            </button>
          ))}
        </nav>
        <div className="aside-footer">
          <span className="dot" />
          {me?.auth_mode === "disabled" ? "SSO disabled · Open access" : "SSO connected"}
          <small>{me?.email || "Local administrator"}</small>
          {me?.auth_mode === "oidc" ? (
            <button
              onClick={async () => {
                try {
                  const r = await fetch("/auth/logout", {
                    method: "POST",
                    headers: { "X-Gateway-Admin": "1" },
                  });
                  if (!r.ok) throw new Error("Sign out failed");
                  window.location.assign("/auth/login");
                } catch (e) {
                  setError(String(e));
                }
              }}
            >
              Sign out
            </button>
          ) : null}
        </div>
      </aside>
      <main>
        <header>
          <span>
            Workspace <span className="separator">/</span>
            <strong>{navigation[tab].name}</strong>
          </span>
          <button onClick={refresh}>↻ Refresh</button>
        </header>
        <section className="page">
          <div className="eyebrow">OBSERVE · CONTROL · CONNECT</div>
          <div className="page-heading">
            <div>
              <h1>{navigation[tab].title}</h1>
              <p>{navigation[tab].description}</p>
            </div>
            <span className="badge">● {me ? "Gateway connected" : "Connecting"}</span>
          </div>
          {error ? (
            <div role="alert" className="error">
              {error}
            </div>
          ) : null}
          {tab !== "overview" && tab !== "playground" ? (
            <div className="stats">
              <Stat
                title="REQUESTS THIS MONTH"
                value={overview?.requests.toLocaleString() ?? "—"}
                detail="UTC calendar month"
              />
              <Stat
                title="TOKENS PROCESSED"
                value={overview?.tokens.toLocaleString() ?? "—"}
                detail="Provider reported usage"
              />
              <Stat
                title="ESTIMATED SPEND"
                value={overview ? money(overview.cost_microusd) : "—"}
                detail={`${overview?.estimated_requests ?? 0} requests with uncertain usage`}
              />
              <Stat
                title="MODEL ALIASES"
                value={catalog ? String(Object.keys(catalog.models).length) : "—"}
                detail="Across configured providers"
              />
            </div>
          ) : null}
          {!me || !catalog ? (
            <p role="status">Connecting to gateway…</p>
          ) : tab === "overview" ? (
            <OverviewPanel
              catalog={catalog}
              generation={generation}
              onInspectRequests={filteredRequests}
            />
          ) : tab === "requests" ? (
            <RequestsPanel
              principal={me}
              catalog={catalog}
              generation={generation}
              initialID={selectedID}
              initialFilters={filters}
              onReplay={replay}
            />
          ) : tab === "playground" ? (
            <PlaygroundPanel
              key={draft?.requestID ?? "new"}
              catalog={catalog}
              draft={draft}
              onInspect={inspect}
            />
          ) : tab === "keys" ? (
            <KeysPanel catalog={catalog} generation={generation} />
          ) : tab === "models" ? (
            <ModelsPanel catalog={catalog} canEdit={me.role === "admin"} onChange={refresh} />
          ) : tab === "users" ? (
            <UsersPanel key={generation} currentID={me.id} />
          ) : (
            <AuditPanel key={generation} />
          )}
          <footer>
            Relay gateway <span>Payloads encrypted at rest · API keys stored as hashes</span>
          </footer>
        </section>
      </main>
    </div>
  );
}
function Stat({ title, value, detail }: { title: string; value: string; detail: string }) {
  return (
    <div className="stat">
      <div>{title}</div>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}
