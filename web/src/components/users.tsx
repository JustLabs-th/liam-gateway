"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
type User = {
  id: string;
  email: string;
  subject: string;
  role: "admin" | "viewer";
  can_read_payload: boolean;
};
export function UsersPanel({ currentID }: { currentID: string }) {
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    const c = new AbortController();
    api<User[]>("/users", { signal: c.signal })
      .then(setUsers)
      .catch((e) => {
        if (!c.signal.aborted) setError(String(e));
      });
    return () => c.abort();
  }, []);
  async function update(u: User, patch: Partial<User>) {
    try {
      const next = { ...u, ...patch };
      await api(`/users/${u.id}`, {
        method: "PATCH",
        body: JSON.stringify({ role: next.role, can_read_payload: next.can_read_payload }),
      });
      setUsers((list) => list.map((v) => (v.id === u.id ? next : v)));
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>People & permissions</h2>
          <p>
            SSO accounts appear after their first sign in. Open access uses an anonymous
            administrator.
          </p>
        </div>
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
              <th>User</th>
              <th>Role</th>
              <th>Read request / response bodies</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>
                  {u.email || u.subject}
                  <small className="mono">{u.id}</small>
                </td>
                <td>
                  <select
                    aria-label={`Role for ${u.email || u.subject}`}
                    disabled={u.id === currentID}
                    value={u.role}
                    onChange={(e) => update(u, { role: e.target.value as User["role"] })}
                  >
                    <option value="viewer">Viewer</option>
                    <option value="admin">Admin</option>
                  </select>
                </td>
                <td>
                  <input
                    aria-label={`Payload access for ${u.email || u.subject}`}
                    type="checkbox"
                    checked={u.can_read_payload}
                    disabled={u.id === currentID}
                    onChange={(e) => update(u, { can_read_payload: e.target.checked })}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {users.length === 0 ? (
        <div className="empty">
          <h3>No SSO accounts yet.</h3>
          <p>SSO is optional. You can continue using the gateway without signing in.</p>
        </div>
      ) : null}
    </section>
  );
}
export function AuditPanel() {
  const [rows, setRows] = useState<
    { id: number; actor: string; action: string; target: string; created_at: string }[]
  >([]);
  const [error, setError] = useState("");
  useEffect(() => {
    const c = new AbortController();
    api<typeof rows>("/audit", { signal: c.signal })
      .then(setRows)
      .catch((e) => {
        if (!c.signal.aborted) setError(String(e));
      });
    return () => c.abort();
  }, []);
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>Audit trail</h2>
          <p>Latest 100 actions. Mutation attempts are recorded before execution.</p>
        </div>
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
              <th>Time</th>
              <th>Actor</th>
              <th>Action</th>
              <th>Target</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((v) => (
              <tr key={v.id}>
                <td>{new Date(v.created_at).toLocaleString()}</td>
                <td className="mono">{v.actor}</td>
                <td>{v.action}</td>
                <td className="mono">{v.target}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
