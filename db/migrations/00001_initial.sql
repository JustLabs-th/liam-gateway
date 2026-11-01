-- +goose Up
CREATE TABLE users (
 id text PRIMARY KEY,
 issuer text NOT NULL,
 subject text NOT NULL,
 email text NOT NULL DEFAULT '',
 role text NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','viewer')),
 can_read_payload boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(issuer,subject)
);
CREATE TABLE sessions (
 token_hash text PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE TABLE api_keys (
 id text PRIMARY KEY,
 name text NOT NULL,
 prefix text NOT NULL,
 token_hash text NOT NULL UNIQUE,
 owner_user_id text REFERENCES users(id),
 allowed_models text[] NOT NULL,
 rpm integer NOT NULL CHECK (rpm>0),
 concurrency integer NOT NULL CHECK (concurrency>0),
 monthly_budget_microusd bigint NOT NULL DEFAULT 0 CHECK (monthly_budget_microusd>=0),
 revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE budget_periods (
 key_id text NOT NULL REFERENCES api_keys(id),
 period date NOT NULL,
 spent_microusd bigint NOT NULL DEFAULT 0,
 reserved_microusd bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(key_id,period)
);
CREATE TABLE requests (
 id text PRIMARY KEY,
 key_id text REFERENCES api_keys(id),
 owner_user_id text REFERENCES users(id),
 model text NOT NULL DEFAULT '',
 provider text NOT NULL DEFAULT '',
 stream boolean NOT NULL DEFAULT false,
 state text NOT NULL DEFAULT 'running',
 http_status integer NOT NULL DEFAULT 0,
 input_tokens bigint NOT NULL DEFAULT 0,
 output_tokens bigint NOT NULL DEFAULT 0,
 cost_microusd bigint NOT NULL DEFAULT 0,
 usage_status text NOT NULL DEFAULT 'unknown',
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 error_code text NOT NULL DEFAULT '',
 archive_key text,
 expires_at timestamptz NOT NULL,
 last_activity_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX requests_time_idx ON requests(started_at DESC);
CREATE INDEX requests_key_time_idx ON requests(key_id,started_at DESC);
CREATE TABLE request_attempts (
 id text PRIMARY KEY,
 request_id text NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
 ordinal integer NOT NULL,
 provider text NOT NULL,
 model text NOT NULL,
 http_status integer NOT NULL DEFAULT 0,
 state text NOT NULL DEFAULT 'running',
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(request_id,ordinal)
);
CREATE TABLE capture_events (
 id bigserial PRIMARY KEY,
 request_id text NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
 attempt_id text REFERENCES request_attempts(id) ON DELETE CASCADE,
 stage text NOT NULL,
 payload bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX capture_events_request_idx ON capture_events(request_id,id);
CREATE TABLE budget_reservations (
 request_id text PRIMARY KEY REFERENCES requests(id) ON DELETE CASCADE,
 key_id text NOT NULL,
 period date NOT NULL,
 amount_microusd bigint NOT NULL CHECK (amount_microusd>=0),
 settled_at timestamptz,
 FOREIGN KEY(key_id,period) REFERENCES budget_periods(key_id,period)
);
CREATE TABLE usage_ledger (
 request_id text PRIMARY KEY REFERENCES requests(id) ON DELETE CASCADE,
 cost_microusd bigint NOT NULL,
 status text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE audit_events (
 id bigserial PRIMARY KEY,
 actor text NOT NULL,
 action text NOT NULL,
 target text NOT NULL,
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE audit_events,usage_ledger,budget_reservations,capture_events,request_attempts,requests,budget_periods,api_keys,sessions,users;
