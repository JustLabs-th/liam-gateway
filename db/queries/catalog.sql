-- name: GetModel :one
SELECT config FROM model_aliases WHERE name=$1;
-- name: ListModels :many
SELECT name,config FROM model_aliases ORDER BY name;
-- name: SeedModel :exec
INSERT INTO model_aliases(name,config) VALUES($1,$2) ON CONFLICT(name) DO NOTHING;
-- name: SetModel :exec
INSERT INTO model_aliases(name,config) VALUES($1,$2) ON CONFLICT(name) DO UPDATE SET config=EXCLUDED.config,updated_at=now();
