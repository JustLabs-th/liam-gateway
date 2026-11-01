package store

import (
	"context"
	"encoding/json"
)

// Analytics aggregates only inference traffic. Catalog requests are not model usage.
func (s *Store) Analytics(ctx context.Context, windowSeconds, bucketSeconds int64, model, keyID string) (json.RawMessage, error) {
	var b []byte
	err := s.DB.QueryRow(ctx, `
WITH bounds AS (
 SELECT now() AS until, now()-make_interval(secs=>$1::double precision) AS since
), filtered AS (
 SELECT r.* FROM requests r,bounds b
 WHERE r.started_at>=b.since AND r.started_at<b.until AND r.operation='chat.completion'
 AND ($3='' OR r.model=$3) AND ($4='' OR r.key_id=$4)
), measured AS (
 SELECT *,CASE WHEN finished_at IS NOT NULL THEN greatest(0,extract(epoch FROM finished_at-started_at)*1000) END AS latency_ms,
 CASE WHEN stream AND first_output_at IS NOT NULL THEN greatest(0,extract(epoch FROM first_output_at-started_at)*1000) END AS ttft_ms,
 (state NOT IN ('completed','running')) AS is_error FROM filtered
), bucketed AS (
 SELECT date_bin(make_interval(secs=>$2::double precision),started_at,'1970-01-01'::timestamptz) AS bucket,
 count(*) AS requests,count(*) FILTER(WHERE is_error) AS errors,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY latency_ms) AS p95_latency_ms,
 coalesce(sum(cost_microusd),0) AS cost_microusd,coalesce(sum(input_tokens+output_tokens),0) AS tokens
 FROM measured GROUP BY bucket
), series AS (
 SELECT ticks AS time,coalesce(d.requests,0) AS requests,coalesce(d.errors,0) AS errors,d.p95_latency_ms,
 coalesce(d.cost_microusd,0) AS cost_microusd,coalesce(d.tokens,0) AS tokens
 FROM bounds b CROSS JOIN LATERAL generate_series(
 date_bin(make_interval(secs=>$2::double precision),b.since,'1970-01-01'::timestamptz),
 date_bin(make_interval(secs=>$2::double precision),b.until,'1970-01-01'::timestamptz),make_interval(secs=>$2::double precision)) ticks
 LEFT JOIN bucketed d ON d.bucket=ticks ORDER BY ticks
), model_usage AS (
 SELECT model AS name,count(*) AS requests,count(*) FILTER(WHERE is_error) AS errors,
 coalesce(sum(input_tokens+output_tokens),0) AS tokens,coalesce(sum(cost_microusd),0) AS cost_microusd,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY latency_ms) AS p95_latency_ms
 FROM measured GROUP BY model ORDER BY cost_microusd DESC,requests DESC
), key_usage AS (
 SELECT coalesce(m.key_id,'') AS id,coalesce(k.name,'Unattributed') AS name,count(*) AS requests,
 count(*) FILTER(WHERE m.is_error) AS errors,coalesce(sum(m.input_tokens+m.output_tokens),0) AS tokens,
 coalesce(sum(m.cost_microusd),0) AS cost_microusd,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY m.latency_ms) AS p95_latency_ms
 FROM measured m LEFT JOIN api_keys k ON k.id=m.key_id GROUP BY m.key_id,k.name
 ORDER BY cost_microusd DESC,requests DESC LIMIT 100
)
SELECT jsonb_build_object(
 'since',(SELECT since FROM bounds),'until',(SELECT until FROM bounds),
 'summary',(SELECT jsonb_build_object('requests',count(*),'completed',count(*) FILTER(WHERE state='completed'),
 'running',count(*) FILTER(WHERE state='running'),'errors',count(*) FILTER(WHERE is_error),
 'error_rate',CASE WHEN count(*) FILTER(WHERE state<>'running')>0 THEN count(*) FILTER(WHERE is_error)::double precision/count(*) FILTER(WHERE state<>'running') ELSE 0 END,
 'tokens',coalesce(sum(input_tokens+output_tokens),0),'cost_microusd',coalesce(sum(cost_microusd),0),
 'estimated_requests',count(*) FILTER(WHERE usage_status IN ('estimated_unknown','partially_estimated')),
 'p50_latency_ms',percentile_cont(0.5) WITHIN GROUP(ORDER BY latency_ms),
 'p95_latency_ms',percentile_cont(0.95) WITHIN GROUP(ORDER BY latency_ms),'avg_ttft_ms',avg(ttft_ms)) FROM measured),
 'series',coalesce((SELECT jsonb_agg(to_jsonb(series) ORDER BY time) FROM series),'[]'::jsonb),
 'by_model',coalesce((SELECT jsonb_agg(to_jsonb(model_usage)) FROM model_usage),'[]'::jsonb),
 'by_key',coalesce((SELECT jsonb_agg(to_jsonb(key_usage)) FROM key_usage),'[]'::jsonb)
)`, windowSeconds, bucketSeconds, model, keyID).Scan(&b)
	return b, err
}
