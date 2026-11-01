-- +goose Up
CREATE TABLE model_aliases (
 name text PRIMARY KEY,
 config jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE model_aliases;
