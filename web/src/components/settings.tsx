"use client";
import { useCallback, useEffect, useState } from "react";
import { api, money, type Catalog, type Key, type Model } from "@/lib/api";
export function KeysPanel({ catalog, generation }: { catalog: Catalog; generation: number }) {
  const [keys, setKeys] = useState<Key[]>([]);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => setKeys(await api<Key[]>("/keys")), []);
  useEffect(() => {
    const c = new AbortController();
    api<Key[]>("/keys", { signal: c.signal })
      .then(setKeys)
      .catch((e) => {
        if (!c.signal.aborted) setError(String(e));
      });
    return () => c.abort();
  }, [generation]);
  async function create(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const form = e.currentTarget;
    const d = new FormData(form);
    try {
      const result = await api<{ token: string }>("/keys", {
        method: "POST",
        body: JSON.stringify({
          name: d.get("name"),
          allowed_models: d.getAll("models"),
          rpm: Number(d.get("rpm")),
          concurrency: Number(d.get("concurrency")),
          monthly_budget_microusd: Math.round(Number(d.get("budget")) * 1e6),
        }),
      });
      setSecret(result.token);
      form.reset();
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>API keys</h2>
          <p>Credentials appear once. Zero monthly budget means unlimited.</p>
        </div>
      </div>
      {error ? (
        <p role="alert" className="error">
          {error}
        </p>
      ) : null}
      {secret ? (
        <div role="status" className="secret">
          <strong>Copy your API key now</strong>
          <code>{secret}</code>
          <button onClick={() => setSecret("")}>Dismiss</button>
        </div>
      ) : null}
      <form className="form-grid" onSubmit={create}>
        <label>
          Key name
          <input name="name" required maxLength={120} placeholder="e.g. production-chat" />
        </label>
        <label>
          Requests / minute
          <input name="rpm" type="number" min="1" max="100000" defaultValue="60" required />
        </label>
        <label>
          Concurrent requests
          <input name="concurrency" type="number" min="1" max="10000" defaultValue="5" required />
        </label>
        <label>
          Monthly budget (USD)
          <input name="budget" type="number" min="0" step="0.01" defaultValue="0" required />
        </label>
        <fieldset>
          <legend>Allowed models</legend>
          {Object.keys(catalog.models).map((n) => (
            <label className="checkbox" key={n}>
              <input name="models" type="checkbox" value={n} defaultChecked />
              {n}
            </label>
          ))}
        </fieldset>
        <button className="primary" disabled={busy}>
          {busy ? "Creating…" : "Create API key"}
        </button>
      </form>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Name / ID</th>
              <th>Models</th>
              <th>Limits</th>
              <th>Monthly budget</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id}>
                <td>
                  <strong>{k.name}</strong>
                  <small className="mono">{k.id}</small>
                  <small>
                    {k.prefix}… {k.revoked_at ? "· revoked" : ""}
                  </small>
                </td>
                <td>{k.allowed_models.join(", ")}</td>
                <td>
                  {k.rpm} RPM / {k.concurrency} concurrent
                </td>
                <td>
                  {k.monthly_budget_microusd ? money(k.monthly_budget_microusd) : "Unlimited"}
                </td>
                <td>
                  <div className="actions">
                    <button
                      disabled={!!k.revoked_at}
                      onClick={async () => {
                        if (
                          !window.confirm(
                            `Rotate ${k.name}? Its current credential will stop working.`,
                          )
                        )
                          return;
                        try {
                          const v = await api<{ token: string }>(`/keys/${k.id}/rotate`, {
                            method: "POST",
                          });
                          setSecret(v.token);
                          await load();
                        } catch (e) {
                          setError(String(e));
                        }
                      }}
                    >
                      Rotate
                    </button>
                    <button
                      disabled={!!k.revoked_at}
                      onClick={async () => {
                        if (!window.confirm(`Revoke ${k.name}?`)) return;
                        try {
                          await api(`/keys/${k.id}`, { method: "DELETE" });
                          await load();
                        } catch (e) {
                          setError(String(e));
                        }
                      }}
                    >
                      Revoke
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
export function ModelsPanel({
  catalog,
  canEdit,
  onChange,
}: {
  catalog: Catalog;
  canEdit: boolean;
  onChange: () => void;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState("");
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>Model aliases</h2>
          <p>Applications use aliases. Provider credentials stay on the server.</p>
        </div>
        {canEdit ? <button onClick={() => setEditing("")}>+ Add alias</button> : null}
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
              <th>Alias</th>
              <th>Provider model</th>
              <th>Input / Output per 1M</th>
              <th>Provider</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {Object.entries(catalog.models).map(([name, m]) => (
              <tr key={name}>
                <td>
                  <strong>{name}</strong>
                </td>
                <td className="mono">
                  {m.model}
                  <small>
                    Input bound {m.max_input_tokens} / output {m.max_output_tokens}
                  </small>
                </td>
                <td>
                  ${m.input_usd_per_million} / ${m.output_usd_per_million}
                </td>
                <td>
                  <span className="badge">{m.provider}</span>
                  <small>
                    {catalog.providers[m.provider]?.configured
                      ? "Credentials configured"
                      : "Credentials needed"}
                  </small>
                </td>
                <td>{canEdit ? <button onClick={() => setEditing(name)}>Edit</button> : null}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {editing !== null ? (
        <ModelEditor
          key={editing}
          name={editing}
          model={catalog.models[editing]}
          providers={Object.keys(catalog.providers)}
          onCancel={() => setEditing(null)}
          onSave={async (name, model) => {
            try {
              await api(`/models/${encodeURIComponent(name)}`, {
                method: "PUT",
                body: JSON.stringify(model),
              });
              setEditing(null);
              setError("");
              onChange();
            } catch (e) {
              setError(String(e));
            }
          }}
        />
      ) : null}
    </section>
  );
}
function ModelEditor({
  name,
  model,
  providers,
  onCancel,
  onSave,
}: {
  name: string;
  model?: Model;
  providers: string[];
  onCancel: () => void;
  onSave: (name: string, m: Model) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="form-grid"
      onSubmit={async (e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        setBusy(true);
        try {
          await onSave(String(d.get("name")), {
            provider: String(d.get("provider")),
            model: String(d.get("model")),
            max_input_tokens: Number(d.get("input")),
            max_output_tokens: Number(d.get("output")),
            input_usd_per_million: Number(d.get("input_price")),
            output_usd_per_million: Number(d.get("output_price")),
          });
        } finally {
          setBusy(false);
        }
      }}
    >
      <label>
        Alias
        <input name="name" required maxLength={120} defaultValue={name} readOnly={!!name} />
      </label>
      <label>
        Provider
        <select name="provider" defaultValue={model?.provider}>
          {providers.map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
      </label>
      <label>
        Provider model ID
        <input name="model" required defaultValue={model?.model} />
      </label>
      <label>
        Input token reservation bound
        <input
          name="input"
          type="number"
          min="1"
          required
          defaultValue={model?.max_input_tokens ?? 32768}
        />
      </label>
      <label>
        Max output tokens
        <input
          name="output"
          type="number"
          min="1"
          required
          defaultValue={model?.max_output_tokens ?? 4096}
        />
      </label>
      <label>
        Input USD / million tokens
        <input
          name="input_price"
          type="number"
          min="0"
          step="any"
          required
          defaultValue={model?.input_usd_per_million ?? 0}
        />
      </label>
      <label>
        Output USD / million tokens
        <input
          name="output_price"
          type="number"
          min="0"
          step="any"
          required
          defaultValue={model?.output_usd_per_million ?? 0}
        />
      </label>
      <div className="actions">
        <button className="primary" disabled={busy}>
          Save alias
        </button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
