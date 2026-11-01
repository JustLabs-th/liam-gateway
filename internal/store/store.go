package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/internal/capture"
)

var ErrBudget = errors.New("budget exceeded")
var ErrNotFound = errors.New("not found")

type Store struct {
	DB     *pgxpool.Pool
	Cipher *capture.Cipher
}

func New(ctx context.Context, dsn string, c *capture.Cipher) (*Store, error) {
	db, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return nil, e
	}
	if e = db.Ping(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db, c}, nil
}
func ID() string           { return rand.Text() }
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Key struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Owner       *string    `json:"owner_user_id"`
	Models      []string   `json:"allowed_models"`
	RPM         int        `json:"rpm"`
	Concurrency int        `json:"concurrency"`
	Budget      int64      `json:"monthly_budget_microusd"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

const keyColumns = `id,name,prefix,owner_user_id,allowed_models,rpm,concurrency,monthly_budget_microusd,revoked_at,created_at`

func scanKey(row pgx.Row) (Key, error) {
	var k Key
	e := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Owner, &k.Models, &k.RPM, &k.Concurrency, &k.Budget, &k.RevokedAt, &k.CreatedAt)
	return k, e
}
func (s *Store) FindKey(ctx context.Context, token string) (Key, error) {
	return scanKey(s.DB.QueryRow(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE token_hash=$1 AND revoked_at IS NULL`, Hash(token)))
}
func (s *Store) CreateKey(ctx context.Context, k Key) (Key, string, error) {
	token := "gw_" + ID() + ID()
	k.ID = ID()
	k.Prefix = token[:12]
	e := s.DB.QueryRow(ctx, `INSERT INTO api_keys(id,name,prefix,token_hash,owner_user_id,allowed_models,rpm,concurrency,monthly_budget_microusd) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`, k.ID, k.Name, k.Prefix, Hash(token), k.Owner, k.Models, k.RPM, k.Concurrency, k.Budget).Scan(&k.CreatedAt)
	return k, token, e
}
func (s *Store) Keys(ctx context.Context) ([]Key, error) {
	rows, e := s.DB.Query(ctx, `SELECT `+keyColumns+` FROM api_keys ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, e := scanKey(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	tag, e := s.DB.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
func (s *Store) Rotate(ctx context.Context, id string) (string, error) {
	token := "gw_" + ID() + ID()
	tag, e := s.DB.Exec(ctx, `UPDATE api_keys SET token_hash=$2,prefix=$3 WHERE id=$1 AND revoked_at IS NULL`, id, Hash(token), token[:12])
	if e == nil && tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return token, e
}

type Request struct {
	Operation     string     `json:"operation"`
	FirstOutputAt *time.Time `json:"first_output_at"`
	ID            string     `json:"id"`
	KeyID         *string    `json:"key_id"`
	Owner         *string    `json:"owner_user_id"`
	Model         string     `json:"model"`
	Provider      string     `json:"provider"`
	Stream        bool       `json:"stream"`
	State         string     `json:"state"`
	Status        int        `json:"http_status"`
	Input         int64      `json:"input_tokens"`
	Output        int64      `json:"output_tokens"`
	Cost          int64      `json:"cost_microusd"`
	UsageStatus   string     `json:"usage_status"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	Error         string     `json:"error_code"`
	ArchiveKey    *string    `json:"archive_key"`
}

const requestColumns = `id,key_id,owner_user_id,model,provider,stream,state,http_status,input_tokens,output_tokens,cost_microusd,usage_status,started_at,finished_at,error_code,archive_key,first_output_at,operation`

func scanRequest(row pgx.Row) (Request, error) {
	var r Request
	e := row.Scan(&r.ID, &r.KeyID, &r.Owner, &r.Model, &r.Provider, &r.Stream, &r.State, &r.Status, &r.Input, &r.Output, &r.Cost, &r.UsageStatus, &r.StartedAt, &r.FinishedAt, &r.Error, &r.ArchiveKey, &r.FirstOutputAt, &r.Operation)
	return r, e
}
func (s *Store) Start(ctx context.Context, r Request, body []byte, days int) error {
	if r.Operation == "" {
		r.Operation = "chat.completion"
	}
	sealed, e := s.Cipher.Seal(body, r.ID)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `INSERT INTO requests(id,key_id,owner_user_id,model,provider,stream,expires_at,operation) VALUES($1,$2,$3,$4,$5,$6,now()+make_interval(days=>$7),$8)`, r.ID, r.KeyID, r.Owner, r.Model, r.Provider, r.Stream, days, r.Operation)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO capture_events(request_id,stage,payload) VALUES($1,'client_request',$2)`, r.ID, sealed)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Capture(ctx context.Context, requestID, attemptID, stage string, body []byte) error {
	sealed, e := s.Cipher.Seal(body, requestID)
	if e != nil {
		return e
	}
	var aid *string
	if attemptID != "" {
		aid = &attemptID
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// The row lock also serializes capture against archival.
	tag, e := tx.Exec(ctx, `UPDATE requests SET last_activity_at=now() WHERE id=$1 AND archive_key IS NULL AND finished_at IS NULL`, requestID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return errors.New("request is not capturable")
	}
	_, e = tx.Exec(ctx, `INSERT INTO capture_events(request_id,attempt_id,stage,payload) VALUES($1,$2,$3,$4)`, requestID, aid, stage, sealed)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Attempt(ctx context.Context, rid, provider, model string, ordinal int) (string, error) {
	id := ID()
	_, e := s.DB.Exec(ctx, `INSERT INTO request_attempts(id,request_id,ordinal,provider,model) VALUES($1,$2,$3,$4,$5)`, id, rid, ordinal, provider, model)
	return id, e
}
func (s *Store) FinishAttempt(ctx context.Context, id, state string, status int) error {
	_, e := s.DB.Exec(ctx, `UPDATE request_attempts SET state=$2,http_status=$3,finished_at=now() WHERE id=$1`, id, state, status)
	return e
}
func (s *Store) Reserve(ctx context.Context, rid, key string, amount int64) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var budget int64
	e = tx.QueryRow(ctx, `SELECT monthly_budget_microusd FROM api_keys WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, key).Scan(&budget)
	if e != nil {
		return e
	}
	var period time.Time
	e = tx.QueryRow(ctx, `INSERT INTO budget_periods(key_id,period) VALUES($1,date_trunc('month',now() AT TIME ZONE 'UTC')::date) ON CONFLICT(key_id,period) DO UPDATE SET key_id=EXCLUDED.key_id RETURNING period`, key).Scan(&period)
	if e != nil {
		return e
	}
	var spent, reserved int64
	e = tx.QueryRow(ctx, `SELECT spent_microusd,reserved_microusd FROM budget_periods WHERE key_id=$1 AND period=$2 FOR UPDATE`, key, period).Scan(&spent, &reserved)
	if e != nil {
		return e
	}
	if budget > 0 && spent+reserved+amount > budget {
		return ErrBudget
	}
	_, e = tx.Exec(ctx, `UPDATE budget_periods SET reserved_microusd=reserved_microusd+$3 WHERE key_id=$1 AND period=$2`, key, period, amount)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO budget_reservations(request_id,key_id,period,amount_microusd) VALUES($1,$2,$3,$4)`, rid, key, period, amount)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Finish(ctx context.Context, r Request) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var finished *time.Time
	e = tx.QueryRow(ctx, `SELECT finished_at FROM requests WHERE id=$1 FOR UPDATE`, r.ID).Scan(&finished)
	if e != nil {
		return e
	}
	if finished != nil {
		return nil
	}
	var key string
	var period time.Time
	var amount int64
	e = tx.QueryRow(ctx, `UPDATE budget_reservations SET settled_at=now() WHERE request_id=$1 AND settled_at IS NULL RETURNING key_id,period,amount_microusd`, r.ID).Scan(&key, &period, &amount)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e == nil {
		if r.UsageStatus == "unknown" {
			r.Cost = amount
			r.UsageStatus = "estimated_unknown"
		}
		_, e = tx.Exec(ctx, `UPDATE budget_periods SET reserved_microusd=reserved_microusd-$3,spent_microusd=spent_microusd+$4 WHERE key_id=$1 AND period=$2`, key, period, amount, r.Cost)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO usage_ledger(request_id,cost_microusd,status) VALUES($1,$2,$3)`, r.ID, r.Cost, r.UsageStatus)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE requests SET state=$2,http_status=$3,input_tokens=$4,output_tokens=$5,cost_microusd=$6,usage_status=$7,error_code=$8,finished_at=now(),first_output_at=$9 WHERE id=$1`, r.ID, r.State, r.Status, r.Input, r.Output, r.Cost, r.UsageStatus, r.Error, r.FirstOutputAt)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type Filter struct {
	Model, KeyID, Owner, State, Before, After string
	Status                                    int
	Limit                                     int
}

func (s *Store) Requests(ctx context.Context, f Filter) ([]Request, error) {
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, e := s.DB.Query(ctx, `SELECT `+requestColumns+` FROM requests WHERE ($1='' OR model=$1) AND ($2='' OR key_id=$2) AND ($3='' OR owner_user_id=$3) AND ($4='' OR state=$4) AND ($5=0 OR http_status=$5) AND ($6='' OR started_at < NULLIF($6,'')::timestamptz) AND ($8='' OR started_at >= NULLIF($8,'')::timestamptz) ORDER BY started_at DESC LIMIT $7`, f.Model, f.KeyID, f.Owner, f.State, f.Status, f.Before, f.Limit, f.After)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Request(ctx context.Context, id string) (Request, error) {
	return scanRequest(s.DB.QueryRow(ctx, `SELECT `+requestColumns+` FROM requests WHERE id=$1`, id))
}

type Event struct {
	ID        int64     `json:"id"`
	AttemptID *string   `json:"attempt_id"`
	Stage     string    `json:"stage"`
	Payload   []byte    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

func Events(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, rid string) ([]Event, error) {
	rows, e := q.Query(ctx, `SELECT id,attempt_id,stage,payload,created_at FROM capture_events WHERE request_id=$1 ORDER BY id`, rid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.ID, &v.AttemptID, &v.Stage, &v.Payload, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Audit(ctx context.Context, actor, action, target, rid string) error {
	_, e := s.DB.Exec(ctx, `INSERT INTO audit_events(actor,action,target,request_id) VALUES($1,$2,$3,$4)`, actor, action, target, rid)
	return e
}
func (s *Store) JSONRows(ctx context.Context, query string, args ...any) ([]json.RawMessage, error) {
	rows, e := s.DB.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
