-- +goose Up
ALTER TABLE requests ADD COLUMN first_output_at timestamptz;
ALTER TABLE requests ADD COLUMN operation text NOT NULL DEFAULT 'chat.completion';
UPDATE requests SET operation='model.list' WHERE model='catalog' AND provider='';
CREATE INDEX requests_model_time_idx ON requests(model,started_at DESC);
-- +goose Down
DROP INDEX requests_model_time_idx;
ALTER TABLE requests DROP COLUMN first_output_at;
ALTER TABLE requests DROP COLUMN operation;
