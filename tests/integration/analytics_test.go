package integration

import (
	"context"
	"encoding/json"
	"llm-gateway/internal/store"
	"testing"
	"time"
)

func TestAnalyticsUsesInferenceDataAndFirstOutput(t *testing.T) {
	f := newFixture(t, nil)
	for _, c := range []struct {
		model  string
		stream bool
	}{{"fast", false}, {"quality", true}} {
		status, b, h := f.call("POST", "/v1/chat/completions", f.token, chat(c.model, c.stream), false)
		if status != 200 {
			t.Fatalf("%d %s", status, b)
		}
		r := f.waitRequest(h.Get("X-Request-ID"), "completed")
		if c.stream && (r.FirstOutputAt == nil || r.FirstOutputAt.Before(r.StartedAt)) {
			t.Fatalf("first output not captured %+v", r)
		}
	}
	_, _, h := f.call("POST", "/v1/chat/completions", "bad-key", chat("fast", false), false)
	f.waitRequest(h.Get("X-Request-ID"), "rejected")
	status, _, h := f.call("GET", "/v1/models", f.token, "", false)
	if status != 200 {
		t.Fatal(status)
	}
	f.waitRequest(h.Get("X-Request-ID"), "completed")
	status, b, _ := f.call("GET", "/admin/api/analytics?range=24h", "", "", false)
	if status != 200 {
		t.Fatalf("%d %s", status, b)
	}
	var result struct {
		Summary struct {
			Requests, Completed, Errors, Tokens int
			Cost                                int64    `json:"cost_microusd"`
			TTFT                                *float64 `json:"avg_ttft_ms"`
		}
		Series []struct{ Requests int }
		Models []struct{ Name string } `json:"by_model"`
	}
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	if result.Summary.Requests != 3 || result.Summary.Completed != 2 || result.Summary.Errors != 1 || result.Summary.Tokens != 25 || result.Summary.Cost != 32 || result.Summary.TTFT == nil {
		t.Fatalf("wrong summary %s", b)
	}
	total := 0
	for _, p := range result.Series {
		total += p.Requests
	}
	if total != 3 || len(result.Models) != 2 {
		t.Fatalf("wrong groups %s", b)
	}
	status, b, _ = f.call("GET", "/admin/api/analytics?range=7d&model=quality&key_id="+f.key.ID, "", "", false)
	if status != 200 {
		t.Fatalf("%d %s", status, b)
	}
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	if result.Summary.Requests != 1 || result.Summary.Errors != 0 {
		t.Fatalf("filters ignored %s", b)
	}
	status, _, _ = f.call("GET", "/admin/api/analytics?range=year", "", "", false)
	if status != 400 {
		t.Fatal("invalid range accepted", status)
	}
}
func TestEmptyAnalyticsAndTimeWindow(t *testing.T) {
	f := newFixture(t, nil)
	status, b, _ := f.call("GET", "/admin/api/analytics", "", "", false)
	if status != 200 {
		t.Fatalf("%d %s", status, b)
	}
	var data struct {
		Summary struct {
			Requests int
			Latency  *float64 `json:"p95_latency_ms"`
		}
		Series []struct {
			Requests, Errors int
			Latency          *float64 `json:"p95_latency_ms"`
		}
	}
	if e := json.Unmarshal(b, &data); e != nil {
		t.Fatal(e)
	}
	if data.Summary.Requests != 0 || data.Summary.Latency != nil || len(data.Series) < 24 {
		t.Fatalf("wrong empty analytics %s", b)
	}
	for _, p := range data.Series {
		if p.Requests != 0 || p.Errors != 0 || p.Latency != nil {
			t.Fatal("fabricated data")
		}
	}
	ctx := context.Background()
	id := store.ID()
	if e := f.rt.Store.Start(ctx, store.Request{ID: id, Model: "old"}, nil, 30); e != nil {
		t.Fatal(e)
	}
	if e := f.rt.Store.Finish(ctx, store.Request{ID: id, State: "completed", UsageStatus: "reported"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.rt.Store.DB.Exec(ctx, `UPDATE requests SET started_at=$2,finished_at=$3 WHERE id=$1`, id, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour+time.Second)); e != nil {
		t.Fatal(e)
	}
	for _, window := range []struct {
		rangeName string
		count     int
	}{{"24h", 0}, {"7d", 1}, {"30d", 1}} {
		status, b, _ = f.call("GET", "/admin/api/analytics?range="+window.rangeName, "", "", false)
		if status != 200 {
			t.Fatalf("%d %s", status, b)
		}
		if e := json.Unmarshal(b, &data); e != nil {
			t.Fatal(e)
		}
		if data.Summary.Requests != window.count {
			t.Fatalf("range %s %s", window.rangeName, b)
		}
	}
}
