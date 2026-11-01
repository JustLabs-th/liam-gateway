package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/capture"
	"llm-gateway/internal/config"
	"llm-gateway/internal/identity"
	"llm-gateway/internal/store"
)

type Admin struct {
	Store   *store.Store
	Config  config.Config
	Archive capture.Archive
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid JSON body", 400)
		return false
	}
	return true
}
func (a *Admin) audit(w http.ResponseWriter, r *http.Request, action, target string) bool {
	if e := a.Store.Audit(r.Context(), identity.From(r.Context()).ID, action, target, r.Header.Get("X-Request-ID")); e != nil {
		http.Error(w, "audit unavailable", 503)
		return false
	}
	return true
}
func (a *Admin) Keys(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Keys(r.Context())
	if e != nil {
		http.Error(w, "keys unavailable", 503)
		return
	}
	respond(w, v)
}
func (a *Admin) CreateKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Models      []string `json:"allowed_models"`
		RPM         int      `json:"rpm"`
		Concurrency int      `json:"concurrency"`
		Budget      int64    `json:"monthly_budget_microusd"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || len(in.Name) > 120 || len(in.Models) == 0 || in.RPM < 1 || in.RPM > 100000 || in.Concurrency < 1 || in.Concurrency > 10000 || in.Budget < 0 {
		http.Error(w, "invalid key settings", 400)
		return
	}
	for _, m := range in.Models {
		if _, e := a.Store.Model(r.Context(), m); e != nil {
			http.Error(w, "unknown model alias", 400)
			return
		}
	}
	p := identity.From(r.Context())
	var owner *string
	if p.ID != "anonymous" {
		owner = &p.ID
	}
	if !a.audit(w, r, "key.create_attempt", in.Name) {
		return
	}
	k, token, e := a.Store.CreateKey(r.Context(), store.Key{Name: in.Name, Models: in.Models, RPM: in.RPM, Concurrency: in.Concurrency, Budget: in.Budget, Owner: owner})
	if e != nil {
		http.Error(w, "key creation failed", 503)
		return
	}
	respond(w, map[string]any{"key": k, "token": token})
}
func (a *Admin) RevokeKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !a.audit(w, r, "key.revoke_attempt", id) {
		return
	}
	e := a.Store.Revoke(r.Context(), id)
	if errors.Is(e, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "revoke failed", 503)
		return
	}
	w.WriteHeader(204)
}
func (a *Admin) RotateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !a.audit(w, r, "key.rotate_attempt", id) {
		return
	}
	token, e := a.Store.Rotate(r.Context(), id)
	if errors.Is(e, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "rotation failed", 503)
		return
	}
	respond(w, map[string]string{"token": token})
}
func (a *Admin) Models(w http.ResponseWriter, r *http.Request) {
	m, e := a.Store.Models(r.Context())
	if e != nil {
		http.Error(w, "catalog unavailable", 503)
		return
	}
	providers := map[string]any{}
	for n, p := range a.Config.Providers {
		providers[n] = map[string]any{"type": p.Type, "configured": p.APIKey != ""}
	}
	respond(w, map[string]any{"models": m, "providers": providers})
}
func (a *Admin) PutModel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if len(name) < 1 || len(name) > 120 {
		http.Error(w, "invalid alias", 400)
		return
	}
	var m config.Model
	if !decode(w, r, &m) {
		return
	}
	c := a.Config
	c.Models = map[string]config.Model{name: m}
	if c.Validate() != nil || m.MaxInputTokens > 10000000 || m.MaxOutputTokens > 1000000 || m.InputUSDPerMillion > 1000000 || m.OutputUSDPerMillion > 1000000 {
		http.Error(w, "invalid model configuration", 400)
		return
	}
	if !a.audit(w, r, "model.update_attempt", name) {
		return
	}
	if e := a.Store.SetModel(r.Context(), name, m); e != nil {
		http.Error(w, "model update failed", 503)
		return
	}
	respond(w, m)
}
func (a *Admin) Requests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	status, _ := strconv.Atoi(q.Get("status"))
	if q.Get("after") != "" {
		if _, e := time.Parse(time.RFC3339Nano, q.Get("after")); e != nil {
			http.Error(w, "invalid after timestamp", 400)
			return
		}
	}
	if q.Get("before") != "" {
		if _, e := time.Parse(time.RFC3339Nano, q.Get("before")); e != nil {
			http.Error(w, "invalid before timestamp", 400)
			return
		}
	}
	v, e := a.Store.Requests(r.Context(), store.Filter{Model: q.Get("model"), KeyID: q.Get("key_id"), Owner: q.Get("owner_user_id"), State: q.Get("state"), Status: status, Before: q.Get("before"), After: q.Get("after"), Limit: limit})
	if e != nil {
		http.Error(w, "requests unavailable", 503)
		return
	}
	respond(w, v)
}
func (a *Admin) Request(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	meta, e := a.Store.Request(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "request unavailable", 503)
		return
	}
	attempts, e := a.Store.JSONRows(r.Context(), `SELECT row_to_json(t) FROM (SELECT * FROM request_attempts WHERE request_id=$1 ORDER BY ordinal) t`, id)
	if e != nil {
		http.Error(w, "attempts unavailable", 503)
		return
	}
	respond(w, map[string]any{"request": meta, "attempts": attempts})
}
func (a *Admin) Payload(w http.ResponseWriter, r *http.Request) {
	if !identity.From(r.Context()).CanReadPayload {
		http.Error(w, "payload permission required", 403)
		return
	}
	id := chi.URLParam(r, "id")
	if !a.audit(w, r, "request.payload_read", id) {
		return
	}
	// Consistent snapshot prevents a concurrent archive from appearing as an empty payload.
	tx, e := a.Store.DB.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		http.Error(w, "payload unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	var archive *string
	e = tx.QueryRow(r.Context(), `SELECT archive_key FROM requests WHERE id=$1`, id).Scan(&archive)
	if errors.Is(e, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "payload unavailable", 503)
		return
	}
	var events []store.Event
	if archive == nil {
		events, e = store.Events(r.Context(), tx, id)
	} else {
		var b []byte
		b, e = a.Archive.Get(r.Context(), *archive)
		if e == nil {
			e = json.Unmarshal(b, &events)
		}
	}
	if e != nil {
		http.Error(w, "payload unavailable", 503)
		return
	}
	out := []any{}
	for _, v := range events {
		b, e := a.Store.Cipher.Open(v.Payload, id)
		if e != nil {
			http.Error(w, "payload decryption failed", 503)
			return
		}
		out = append(out, map[string]any{"id": v.ID, "attempt_id": v.AttemptID, "stage": v.Stage, "created_at": v.CreatedAt, "payload_base64": b, "payload_text": string(b)})
	}
	respond(w, out)
}
func (a *Admin) Overview(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.JSONRows(r.Context(), `SELECT row_to_json(t) FROM (SELECT count(*) AS requests,count(*) FILTER(WHERE state='completed') AS completed,COALESCE(sum(input_tokens+output_tokens),0) AS tokens,COALESCE(sum(cost_microusd),0) AS cost_microusd,count(*) FILTER(WHERE usage_status IN ('estimated_unknown','partially_estimated')) AS estimated_requests FROM requests WHERE started_at>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') t`)
	if e != nil {
		http.Error(w, "overview unavailable", 503)
		return
	}
	respond(w, v[0])
}
func (a *Admin) Audit(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.JSONRows(r.Context(), `SELECT row_to_json(t) FROM (SELECT * FROM audit_events ORDER BY id DESC LIMIT 100) t`)
	if e != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	respond(w, v)
}
func (a *Admin) Users(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.JSONRows(r.Context(), `SELECT row_to_json(t) FROM (SELECT id,email,subject,role,can_read_payload,created_at FROM users ORDER BY created_at DESC) t`)
	if e != nil {
		http.Error(w, "users unavailable", 503)
		return
	}
	respond(w, v)
}
func (a *Admin) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role    string `json:"role"`
		Payload bool   `json:"can_read_payload"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "admin" && in.Role != "viewer" {
		http.Error(w, "invalid role", 400)
		return
	}
	id := chi.URLParam(r, "id")
	if id == identity.From(r.Context()).ID {
		http.Error(w, "cannot modify own permissions", 400)
		return
	}
	if !a.audit(w, r, "user.update_attempt", id) {
		return
	}
	tag, e := a.Store.DB.Exec(r.Context(), `UPDATE users SET role=$2,can_read_payload=$3 WHERE id=$1`, id, in.Role, in.Payload)
	if e != nil {
		http.Error(w, "user update failed", 503)
		return
	}
	if tag.RowsAffected() == 0 {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(204)
}
