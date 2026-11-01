package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"llm-gateway/internal/config"
	"llm-gateway/internal/provider"
	"llm-gateway/internal/quota"
	"llm-gateway/internal/store"
)

type Gateway struct {
	TTFT     prometheus.Histogram
	Config   config.Config
	Store    *store.Store
	Quota    *quota.Limiter
	Client   *http.Client
	Requests *prometheus.CounterVec
	Latency  prometheus.Histogram
}

func New(c config.Config, s *store.Store, q *quota.Limiter, reg *prometheus.Registry) *Gateway {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = c.HeaderTimeout
	t.MaxIdleConns = 200
	t.MaxIdleConnsPerHost = 100
	t.MaxConnsPerHost = 200
	g := &Gateway{Config: c, Store: s, Quota: q, Client: &http.Client{Transport: otelhttp.NewTransport(t), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	g.Requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gateway_requests_total", Help: "Inference requests by outcome."}, []string{"state"})
	g.Latency = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "gateway_request_duration_seconds", Help: "Total inference request duration.", Buckets: []float64{.1, .5, 1, 5, 15, 60, 180, 600}})
	g.TTFT = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "gateway_time_to_first_write_seconds", Help: "Time to first successful streamed write (includes role/metadata events).", Buckets: []float64{.1, .5, 1, 2, 5, 15, 60}})
	reg.MustRegister(g.Requests, g.Latency, g.TTFT)
	return g
}
func Cost(m config.Model, input, output int64) int64 {
	return int64(math.Ceil(float64(input)*m.InputUSDPerMillion + float64(output)*m.OutputUSDPerMillion))
}
func token(r *http.Request) string {
	p := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(p) == 2 && strings.EqualFold(p[0], "Bearer") {
		return p[1]
	}
	return ""
}

func (g *Gateway) Models(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rid := store.ID()
	w.Header().Set("X-Request-ID", rid)
	w.Header().Set("Cache-Control", "no-store")
	record := store.Request{ID: rid, Model: "catalog", Operation: "model.list", State: "rejected", UsageStatus: "not_started"}
	k, keyErr := g.Store.FindKey(ctx, token(r))
	if keyErr == nil {
		record.KeyID = &k.ID
		record.Owner = k.Owner
	}
	if e := g.Store.Start(ctx, record, nil, g.Config.Capture.RetentionDays); e != nil {
		writeError(w, 503, "capture_unavailable", "Request logging is unavailable")
		return
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := g.Store.Finish(c, record); e != nil {
			slog.Error("catalog request finalization failed", "request_id", rid)
		}
	}()
	status := 200
	var body []byte
	if keyErr != nil {
		status = 401
		record.Error = "invalid_api_key"
		body = errorBody(record.Error, "A valid gateway API key is required")
	} else {
		models, e := g.Store.Models(ctx)
		if e != nil {
			status = 503
			record.Error = "catalog_unavailable"
			body = errorBody(record.Error, "Model catalog unavailable")
		} else {
			names := []string{}
			for n := range models {
				names = append(names, n)
			}
			slices.Sort(names)
			list := []any{}
			for _, n := range names {
				if slices.Contains(k.Models, n) {
					list = append(list, map[string]any{"id": n, "object": "model", "owned_by": "gateway"})
				}
			}
			body, _ = json.Marshal(map[string]any{"object": "list", "data": list})
			record.State = "completed"
		}
	}
	meta, _ := json.Marshal(map[string]string{"method": r.Method, "path": r.URL.Path})
	if e := g.Store.Capture(ctx, rid, "", "client_request_metadata", meta); e != nil {
		record.State = "capture_failed"
		record.Status = 503
		writeError(w, 503, "capture_unavailable", "Logging unavailable")
		return
	}
	if e := g.Store.Capture(ctx, rid, "", "client_response", body); e != nil {
		record.State = "capture_failed"
		record.Status = 503
		writeError(w, 503, "capture_unavailable", "Logging unavailable")
		return
	}
	record.Status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
func errorBody(code, message string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"type": "gateway_error", "code": code, "message": message}})
	return b
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(errorBody(code, message))
}
func (g *Gateway) Chat(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), g.Config.RequestTimeout)
	defer cancel()
	rid := store.ID()
	w.Header().Set("X-Request-ID", rid)
	w.Header().Set("Cache-Control", "no-store")
	record := store.Request{ID: rid, State: "rejected", UsageStatus: "not_started"}
	// Body size is bounded at admission; rejected oversized bodies retain the received prefix.
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, g.Config.MaxRequestBytes+1))
	if int64(len(raw)) > g.Config.MaxRequestBytes {
		raw = raw[:g.Config.MaxRequestBytes]
		readErr = errors.New("body too large")
	}
	request, parseErr := provider.Decode(raw)
	k, keyErr := g.Store.FindKey(ctx, token(r))
	if keyErr == nil {
		record.KeyID = &k.ID
		record.Owner = k.Owner
	}
	if parseErr == nil {
		record.Model = request.Model
		record.Stream = request.Stream
	}
	model, modelErr := g.Store.Model(ctx, request.Model)
	found := modelErr == nil
	if found {
		record.Provider = model.Provider
	}
	if e := g.Store.Start(ctx, record, raw, g.Config.Capture.RetentionDays); e != nil {
		writeError(w, 503, "capture_unavailable", "Request logging is unavailable")
		return
	}
	defer func() {
		finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if e := g.Store.Finish(finishCtx, record); e != nil {
			slog.Error("request finalization failed", "request_id", rid)
			record.State = "finalization_failed"
		}
		g.Requests.WithLabelValues(record.State).Inc()
		g.Latency.Observe(time.Since(started).Seconds())
	}()
	fail := func(status int, code, message string) {
		record.Status = status
		record.Error = code
		b := errorBody(code, message)
		if e := g.Store.Capture(ctx, rid, "", "client_response", b); e != nil {
			record.State = "capture_failed"
			writeError(w, 503, "capture_unavailable", "Response logging is unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(b)
	}
	meta, _ := json.Marshal(map[string]string{"method": r.Method, "path": r.URL.Path})
	if e := g.Store.Capture(ctx, rid, "", "client_request_metadata", meta); e != nil {
		record.State = "capture_failed"
		record.Status = 503
		writeError(w, 503, "capture_unavailable", "Request logging is unavailable")
		return
	}
	if readErr != nil {
		fail(413, "request_body_rejected", "Body unreadable or exceeds configured size limit")
		return
	}
	if keyErr != nil {
		fail(401, "invalid_api_key", "A valid gateway API key is required")
		return
	}
	if parseErr != nil {
		fail(400, "invalid_request", parseErr.Error())
		return
	}
	if !found || !slices.Contains(k.Models, request.Model) {
		fail(403, "model_not_allowed", "Model alias is unavailable for this key")
		return
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = min(int64(1024), model.MaxOutputTokens)
	}
	if request.MaxTokens > model.MaxOutputTokens {
		fail(400, "output_limit", "max_tokens exceeds model output limit")
		return
	}
	// Raw byte count is a conservative admission heuristic, not a tokenizer.
	if int64(len(raw)) > model.MaxInputTokens {
		fail(400, "input_limit", "Request exceeds conservative input byte limit")
		return
	}
	p := g.Config.Providers[model.Provider]
	body, e := provider.Build(request, model, p)
	if e != nil {
		fail(400, "unsupported_request", e.Error())
		return
	}
	if p.APIKey == "" {
		fail(503, "provider_unconfigured", "Provider credentials have not been configured")
		return
	}
	release, e := g.Quota.Acquire(ctx, k.ID, rid, k.RPM, k.Concurrency, g.Config.RequestTimeout+30*time.Second)
	if e != nil {
		if errors.Is(e, quota.ErrLimit) {
			w.Header().Set("Retry-After", "60")
			fail(429, "rate_limit", "Rate or concurrency limit exceeded")
		} else {
			fail(503, "quota_unavailable", "Quota service is unavailable")
		}
		return
	}
	defer release()
	unit := Cost(model, model.MaxInputTokens, request.MaxTokens)
	if k.Budget > 0 && unit == 0 {
		fail(400, "pricing_required", "Configure model pricing before using a budget-limited key")
		return
	}
	reserve := unit * int64(g.Config.MaxRetries+1)
	if e = g.Store.Reserve(ctx, rid, k.ID, reserve); e != nil {
		if errors.Is(e, store.ErrBudget) {
			fail(402, "budget_exceeded", "Monthly budget cannot cover request reservation")
		} else {
			fail(503, "budget_unavailable", "Budget service is unavailable")
		}
		return
	}
	record.UsageStatus = "unknown"
	record.State = "failed"
	var resp *http.Response
	var aid string
	uncertainCost := int64(0)
	for attempt := 0; attempt <= g.Config.MaxRetries; attempt++ {
		aid, e = g.Store.Attempt(ctx, rid, model.Provider, model.Model, attempt+1)
		if e != nil {
			fail(503, "capture_unavailable", "Attempt logging is unavailable")
			return
		}
		if e = g.Store.Capture(ctx, rid, aid, "upstream_request", body); e != nil {
			fail(503, "capture_unavailable", "Attempt logging is unavailable")
			return
		}
		path := "/chat/completions"
		if p.Type == "anthropic" {
			path = "/messages"
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+path, bytes.NewReader(body))
		if e != nil {
			fail(500, "upstream_request_invalid", "Provider request could not be constructed")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Encoding", "identity")
		if p.Type == "anthropic" {
			req.Header.Set("x-api-key", p.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
		resp, e = g.Client.Do(req)
		if e != nil {
			attemptCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			_ = g.Store.FinishAttempt(attemptCtx, aid, "transport_error", 0)
			stop()
			fail(502, "upstream_unavailable", "Provider request failed")
			return
		}
		meta, _ := json.Marshal(map[string]any{"status": resp.StatusCode, "content_type": resp.Header.Get("Content-Type"), "request_id": resp.Header.Get("X-Request-ID")})
		if e = g.Store.Capture(ctx, rid, aid, "upstream_response_metadata", meta); e != nil {
			resp.Body.Close()
			fail(503, "capture_unavailable", "Response logging is unavailable")
			return
		}
		retryable := resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		// Bound and capture provider errors without forwarding their body to the caller.
		n, copyErr := io.Copy(&captureWriter{ctx: ctx, g: g, rid: rid, aid: aid, stage: "upstream_response"}, io.LimitReader(resp.Body, 32<<20+1))
		e = copyErr
		if e == nil && n > 32<<20 {
			e = errors.New("upstream error body exceeds limit")
		}
		resp.Body.Close()
		if e != nil {
			attemptCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			_ = g.Store.FinishAttempt(attemptCtx, aid, "capture_or_read_failed", resp.StatusCode)
			stop()
			fail(502, "upstream_read_failed", "Provider response could not be captured")
			return
		}
		if e = g.Store.FinishAttempt(ctx, aid, "upstream_error", resp.StatusCode); e != nil {
			fail(503, "capture_unavailable", "Attempt finalization is unavailable")
			return
		}
		if resp.StatusCode >= 500 {
			uncertainCost += unit
		}
		if !retryable || attempt == g.Config.MaxRetries {
			record.Cost = uncertainCost
			record.UsageStatus = "provider_rejected"
			if uncertainCost > 0 {
				record.UsageStatus = "estimated_unknown"
			}
			fail(502, "upstream_error", fmt.Sprintf("Provider returned HTTP %d", resp.StatusCode))
			return
		}
		select {
		case <-ctx.Done():
			fail(504, "request_timeout", "Request timed out")
			return
		case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
		}
	}
	defer resp.Body.Close()
	var u provider.Usage
	var streamDone bool
	if request.Stream {
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			fail(502, "invalid_upstream_response", "Provider did not return an event stream")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		record.Status = 200
		firstWrite := true
		converter := provider.AnthropicStream{Model: request.Model, IncludeUsage: request.StreamOptions != nil && request.StreamOptions.IncludeUsage}
		streamReader := &io.LimitedReader{R: resp.Body, N: 32<<20 + 1}
		e = provider.ReadSSE(streamReader, func(frame []byte) error {
			if e := g.Store.Capture(ctx, rid, aid, "upstream_response_event", frame); e != nil {
				return e
			}
			out := frame
			if p.Type == "anthropic" {
				var err error
				out, err = converter.Convert(frame)
				if err != nil {
					return err
				}
				u = provider.Usage{Input: converter.Input, Output: converter.Output, Known: converter.Known}
				streamDone = converter.Done
			} else {
				data := provider.SSEData(frame)
				if bytes.Equal(data, []byte("[DONE]")) {
					streamDone = true
				} else if len(data) > 0 {
					var v map[string]json.RawMessage
					if json.Unmarshal(data, &v) != nil {
						return errors.New("invalid upstream SSE JSON")
					}
					if _, ok := v["error"]; ok {
						return errors.New("upstream SSE error")
					}
					parsed := provider.ParseUsage(data, "openai")
					if parsed.Known {
						u = parsed
					}
					if _, ok := v["model"]; ok {
						v["model"], _ = json.Marshal(request.Model)
						normalized, err := json.Marshal(v)
						if err != nil {
							return err
						}
						out = append(append([]byte("data: "), normalized...), []byte("\n\n")...)
					}
				}
			}
			if len(out) == 0 {
				return nil
			}
			if e := g.Store.Capture(ctx, rid, aid, "client_response_event", out); e != nil {
				return e
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, e := w.Write(out); e != nil {
				return e
			}
			if record.FirstOutputAt == nil && provider.HasOutput(provider.SSEData(out)) {
				now := time.Now()
				record.FirstOutputAt = &now
			}
			if firstWrite {
				g.TTFT.Observe(time.Since(started).Seconds())
				firstWrite = false
			}
			return http.NewResponseController(w).Flush()
		})
		if e == nil && streamReader.N == 0 {
			e = errors.New("upstream stream exceeds 32 MiB limit")
		}
		if e == nil && !streamDone {
			e = errors.New("stream ended without terminal event")
		}
	} else {
		var b []byte
		// Journal the raw upstream in chunks before assembling the bounded compatibility response.
		b, e = io.ReadAll(io.LimitReader(io.TeeReader(resp.Body, &captureWriter{ctx: ctx, g: g, rid: rid, aid: aid, stage: "upstream_response"}), 32<<20+1))
		if e == nil && len(b) > 32<<20 {
			e = errors.New("upstream response exceeds 32 MiB limit")
		}
		if e == nil {
			u = provider.ParseUsage(b, p.Type)
			if p.Type == "anthropic" {
				b, e = provider.ConvertResponse(b, request.Model)
			} else {
				var v map[string]json.RawMessage
				if json.Unmarshal(b, &v) != nil {
					e = errors.New("invalid upstream JSON")
				} else {
					v["model"], _ = json.Marshal(request.Model)
					b, e = json.Marshal(v)
				}
			}
		}
		if e == nil {
			e = g.Store.Capture(ctx, rid, aid, "client_response", b)
		}
		if e == nil {
			record.Status = 200
			w.Header().Set("Content-Type", "application/json")
			_, e = w.Write(b)
		}
	}
	if e != nil {
		record.State = "incomplete"
		record.Error = "response_interrupted"
		record.UsageStatus = "unknown"
		if !request.Stream && record.Status == 0 {
			fail(502, "response_interrupted", "Provider response could not be completed")
		}
	} else {
		record.State = "completed"
		if u.Known {
			record.Input = u.Input
			record.Output = u.Output
			record.Cost = Cost(model, u.Input, u.Output) + uncertainCost
			record.UsageStatus = "reported"
			if uncertainCost > 0 {
				record.UsageStatus = "partially_estimated"
			}
		}
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := g.Store.FinishAttempt(finishCtx, aid, record.State, resp.StatusCode); err != nil {
		slog.Error("attempt finalization failed", "request_id", rid)
	}
}

type captureWriter struct {
	ctx             context.Context
	g               *Gateway
	rid, aid, stage string
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if e := w.g.Store.Capture(w.ctx, w.rid, w.aid, w.stage, b); e != nil {
		return 0, e
	}
	return len(b), nil
}
