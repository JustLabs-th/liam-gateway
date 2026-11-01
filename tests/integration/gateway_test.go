package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/db"
	"llm-gateway/internal/app"
	"llm-gateway/internal/capture"
	"llm-gateway/internal/config"
	"llm-gateway/internal/identity"
	"llm-gateway/internal/store"
	httptransport "llm-gateway/internal/transport/http"
)

type fixture struct {
	t      *testing.T
	rt     *app.Runtime
	c      config.Config
	server *httptest.Server
	up     *httptest.Server
	redis  *miniredis.Miniredis
	token  string
	key    store.Key
	calls  atomic.Int32
	mode   atomic.Value
}

func newFixture(t *testing.T, adjust func(*config.Config)) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + strings.ToLower(store.ID())
	persistent := t.Name() == "TestBrowserFixture" && os.Getenv("BROWSER_FIXTURE_STOP") != "" && os.Getenv("BROWSER_FIXTURE_SCHEMA") != ""
	if persistent {
		schema = os.Getenv("BROWSER_FIXTURE_SCHEMA")
		if !regexp.MustCompile(`^preview_[a-z0-9_]{1,48}$`).MatchString(schema) {
			t.Fatal("preview schema must start with preview_ and contain lowercase letters, digits or underscores")
		}
	}
	if _, e = admin.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if !persistent {
			_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		}
		admin.Close()
	})
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn = u.String()
	if e = db.Migrate(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	f := &fixture{t: t}
	f.mode.Store("normal")
	f.redis = miniredis.RunT(t)
	f.up = httptest.NewServer(http.HandlerFunc(f.upstream))
	t.Cleanup(f.up.Close)
	f.c = config.Config{Listen: "127.0.0.1:0", PublicURL: "http://gateway.test", DatabaseURL: dsn, RedisURL: "redis://" + f.redis.Addr(), EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), Auth: config.Auth{Mode: "disabled"}, MaxRequestBytes: 2 << 20, RequestTimeout: 3 * time.Second, HeaderTimeout: time.Second, Providers: map[string]config.Provider{"openai": {Type: "openai", BaseURL: f.up.URL + "/v1", APIKey: "upstream-secret"}, "anthropic": {Type: "anthropic", BaseURL: f.up.URL + "/v1", APIKey: "anthropic-secret"}}, Models: map[string]config.Model{"fast": {Provider: "openai", Model: "mock-openai", MaxInputTokens: 10000, MaxOutputTokens: 1000, InputUSDPerMillion: 1, OutputUSDPerMillion: 2}, "quality": {Provider: "anthropic", Model: "mock-claude", MaxInputTokens: 10000, MaxOutputTokens: 1000, InputUSDPerMillion: 1, OutputUSDPerMillion: 2}}, Capture: config.Capture{Storage: "filesystem", Directory: t.TempDir(), RetentionDays: 30, ArchiveAfter: time.Nanosecond}}
	if adjust != nil {
		adjust(&f.c)
	}
	f.rt, e = app.Open(ctx, f.c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(f.rt.Close)
	auth, e := identity.New(ctx, f.c, f.rt.Store)
	if e != nil {
		t.Fatal(e)
	}
	f.server = httptest.NewServer(httptransport.Router(f.c, f.rt.Store, f.rt.Quota, auth, f.rt.Archive))
	t.Cleanup(f.server.Close)
	f.key, f.token, e = f.rt.Store.CreateKey(ctx, store.Key{Name: "test", Models: []string{"fast", "quality"}, RPM: 1000, Concurrency: 20})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) upstream(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	var in struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &in)
	if f.mode.Load().(string) == "retry" && f.calls.Load() == 1 {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"busy"}`))
		return
	}
	if f.mode.Load().(string) == "block" {
		<-r.Context().Done()
		return
	}
	if r.URL.Path == "/v1/messages" {
		if r.Header.Get("x-api-key") != "anthropic-secret" {
			w.WriteHeader(401)
			return
		}
		if in.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":8}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello Claude\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_test","content":[{"type":"text","text":"Hello Claude"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":3}}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer upstream-secret" {
		w.WriteHeader(401)
		return
	}
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat_test\",\"model\":\"mock-openai\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello OpenAI\"}}]}\n\n")
		w.(http.Flusher).Flush()
		if f.mode.Load().(string) == "cancel" {
			<-r.Context().Done()
			return
		}
		if f.mode.Load().(string) == "incomplete" {
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"chat_test\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"chat_test","object":"chat.completion","model":"mock-openai","choices":[{"index":0,"message":{"role":"assistant","content":"Hello OpenAI"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`)
}
func (f *fixture) call(method, path, token, body string, admin bool) (int, []byte, http.Header) {
	f.t.Helper()
	req, e := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if e != nil {
		f.t.Fatal(e)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if admin {
		req.Header.Set("X-Gateway-Admin", "1")
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		f.t.Fatal(e)
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	if e != nil {
		f.t.Fatal(e)
	}
	return resp.StatusCode, b, resp.Header
}
func chat(model string, stream bool) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"private prompt"}],"stream":%t,"max_tokens":100,"stream_options":{"include_usage":true}}`, model, stream)
}
func (f *fixture) waitRequest(id, state string) store.Request {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, e := f.rt.Store.Request(context.Background(), id)
		if e == nil && r.State == state && r.FinishedAt != nil {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, e := f.rt.Store.Request(context.Background(), id)
	f.t.Fatalf("request did not reach %s: %+v %v", state, r, e)
	return r
}
func TestInferenceCaptureArchiveAndOptionalAuth(t *testing.T) {
	f := newFixture(t, nil)
	for _, model := range []string{"fast", "quality"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				status, b, h := f.call("POST", "/v1/chat/completions", f.token, chat(model, stream), false)
				if status != 200 || !bytes.Contains(b, []byte("Hello")) {
					t.Fatalf("%d %s", status, b)
				}
				if stream && !bytes.Contains(b, []byte("[DONE]")) {
					t.Fatal(string(b))
				}
				rid := h.Get("X-Request-ID")
				record := f.waitRequest(rid, "completed")
				if record.UsageStatus != "reported" || record.Cost == 0 {
					t.Fatalf("missing usage %+v", record)
				}
				var payload []byte
				if e := f.rt.Store.DB.QueryRow(context.Background(), `SELECT payload FROM capture_events WHERE request_id=$1 ORDER BY id LIMIT 1`, rid).Scan(&payload); e != nil {
					t.Fatal(e)
				}
				if bytes.Contains(payload, []byte("private prompt")) {
					t.Fatal("plaintext stored")
				}
				status, b, _ = f.call("GET", "/admin/api/requests/"+rid+"/payload", "", "", false)
				if status != 200 || !bytes.Contains(b, []byte("private prompt")) || !bytes.Contains(b, []byte("upstream_request")) {
					t.Fatalf("%d %s", status, b)
				}
				worker := app.Worker{Store: f.rt.Store, Archive: f.rt.Archive, Config: f.c}
				if e := worker.Tick(context.Background()); e != nil {
					t.Fatal(e)
				}
				record, e := f.rt.Store.Request(context.Background(), rid)
				if e != nil || record.ArchiveKey == nil {
					t.Fatal("not archived", e)
				}
				status, b, _ = f.call("GET", "/admin/api/requests/"+rid+"/payload", "", "", false)
				if status != 200 || !bytes.Contains(b, []byte("private prompt")) {
					t.Fatalf("archive read %d %s", status, b)
				}
			})
		}
	}
	status, b, _ := f.call("GET", "/admin/api/me", "", "", false)
	if status != 200 || !bytes.Contains(b, []byte(`"id":"anonymous"`)) {
		t.Fatalf("%d %s", status, b)
	}
}
func TestAuthorizationLimitsAndCaptureFailures(t *testing.T) {
	f := newFixture(t, nil)
	status, _, _ := f.call("POST", "/v1/chat/completions", "wrong", chat("fast", false), false)
	if status != 401 {
		t.Fatal(status)
	}
	status, _, _ = f.call("POST", "/admin/api/keys", "", `{}`, false)
	if status != 403 {
		t.Fatal("CSRF bypass", status)
	}
	req, _ := http.NewRequest("POST", f.server.URL+"/admin/api/keys", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("X-Gateway-Admin", "1")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin bypass")
	}
	status, _, _ = f.call("POST", "/v1/chat/completions", f.token, chat("unknown", false), false)
	if status != 403 {
		t.Fatal(status)
	}
	status, _, _ = f.call("POST", "/v1/chat/completions", f.token, `{"model":"fast","messages":[{"role":"user","content":"hi"}],"unknown":1}`, false)
	if status != 400 {
		t.Fatal(status)
	}
	status, _, _ = f.call("POST", "/v1/chat/completions", f.token, strings.Repeat("x", int(f.c.MaxRequestBytes)+1), false)
	if status != 413 {
		t.Fatal(status)
	}
	_, e = f.rt.Store.DB.Exec(context.Background(), `UPDATE api_keys SET rpm=1 WHERE id=$1`, f.key.ID)
	if e != nil {
		t.Fatal(e)
	}
	status, _, _ = f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	if status != 200 {
		t.Fatal(status)
	}
	status, _, _ = f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	if status != 429 {
		t.Fatal(status)
	}
	if e = f.rt.Store.Revoke(context.Background(), f.key.ID); e != nil {
		t.Fatal(e)
	}
	status, _, _ = f.call("GET", "/v1/models", f.token, "", false)
	if status != 401 {
		t.Fatal("revoked key allowed")
	}
	// Redis failure is closed, rather than bypassing quotas.
	k, tok, e := f.rt.Store.CreateKey(context.Background(), store.Key{Name: "redis", Models: []string{"fast"}, RPM: 60, Concurrency: 1})
	_ = k
	if e != nil {
		t.Fatal(e)
	}
	f.redis.Close()
	status, _, _ = f.call("POST", "/v1/chat/completions", tok, chat("fast", false), false)
	if status != 503 {
		t.Fatal("Redis fail-open", status)
	}
}
func TestAtomicBudgetAndSettlement(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	_, e := f.rt.Store.DB.Exec(ctx, `UPDATE api_keys SET monthly_budget_microusd=10000 WHERE id=$1`, f.key.ID)
	if e != nil {
		t.Fatal(e)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := store.ID()
			if e := f.rt.Store.Start(ctx, store.Request{ID: id, KeyID: &f.key.ID}, []byte(`{}`), 30); e != nil {
				t.Error(e)
				return
			}
			if e := f.rt.Store.Reserve(ctx, id, f.key.ID, 6000); e == nil {
				admitted.Add(1)
			} else if e != store.ErrBudget {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatal("budget race", admitted.Load())
	}
}
func TestRetryIncompleteAndCancellation(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		f := newFixture(t, func(c *config.Config) { c.MaxRetries = 1 })
		f.mode.Store("retry")
		status, b, h := f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
		if status != 200 {
			t.Fatalf("%d %s", status, b)
		}
		r := f.waitRequest(h.Get("X-Request-ID"), "completed")
		if f.calls.Load() != 2 || r.UsageStatus != "partially_estimated" {
			t.Fatalf("retry not recorded %+v", r)
		}
		var n int
		if e := f.rt.Store.DB.QueryRow(context.Background(), `SELECT count(*) FROM request_attempts WHERE request_id=$1`, r.ID).Scan(&n); e != nil || n != 2 {
			t.Fatal(n, e)
		}
	})
	t.Run("incomplete", func(t *testing.T) {
		f := newFixture(t, nil)
		f.mode.Store("incomplete")
		_, _, h := f.call("POST", "/v1/chat/completions", f.token, chat("fast", true), false)
		r := f.waitRequest(h.Get("X-Request-ID"), "incomplete")
		if r.UsageStatus != "estimated_unknown" || r.Cost <= 0 {
			t.Fatal(r)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		f := newFixture(t, nil)
		f.mode.Store("cancel")
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/v1/chat/completions", strings.NewReader(chat("fast", true)))
		req.Header.Set("Authorization", "Bearer "+f.token)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		rid := resp.Header.Get("X-Request-ID")
		buf := make([]byte, 1)
		_, _ = resp.Body.Read(buf)
		cancel()
		resp.Body.Close()
		r := f.waitRequest(rid, "incomplete")
		if r.Error != "response_interrupted" {
			t.Fatal(r)
		}
	})
}
func TestRotationCatalogAndRetention(t *testing.T) {
	f := newFixture(t, nil)
	status, b, _ := f.call("POST", "/admin/api/keys/"+f.key.ID+"/rotate", "", "", true)
	if status != 200 {
		t.Fatalf("%d %s", status, b)
	}
	var result struct {
		Token string `json:"token"`
	}
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	status, _, _ = f.call("GET", "/v1/models", f.token, "", false)
	if status != 401 {
		t.Fatal("old credential accepted")
	}
	f.token = result.Token
	status, b, _ = f.call("PUT", "/admin/api/models/fast", "", `{"provider":"openai","model":"updated","max_input_tokens":10000,"max_output_tokens":1000,"input_usd_per_million":1,"output_usd_per_million":2}`, true)
	if status != 200 {
		t.Fatalf("%d %s", status, b)
	}
	_, _, h := f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	id := h.Get("X-Request-ID")
	f.waitRequest(id, "completed")
	worker := app.Worker{Store: f.rt.Store, Archive: f.rt.Archive, Config: f.c}
	if e := worker.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, e := f.rt.Store.Request(context.Background(), id)
	if e != nil || r.ArchiveKey == nil {
		t.Fatal(e)
	}
	if _, e = f.rt.Store.DB.Exec(context.Background(), `UPDATE requests SET expires_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = worker.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.rt.Archive.Get(context.Background(), *r.ArchiveKey); e == nil {
		t.Fatal("expired archive exists")
	}
	status, _, _ = f.call("GET", "/admin/api/requests/"+id, "", "", false)
	if status != 404 {
		t.Fatal("expired metadata exists", status)
	}
}
func TestCaptureUnavailableBlocksUpstream(t *testing.T) {
	f := newFixture(t, nil)
	if _, e := f.rt.Store.DB.Exec(context.Background(), `ALTER TABLE capture_events RENAME TO capture_events_unavailable`); e != nil {
		t.Fatal(e)
	}
	status, _, _ := f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	if status != 503 || f.calls.Load() != 0 {
		t.Fatalf("unlogged request forwarded: %d / %d", status, f.calls.Load())
	}
}
func TestArchiveFailureKeepsJournal(t *testing.T) {
	f := newFixture(t, nil)
	_, _, h := f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	id := h.Get("X-Request-ID")
	f.waitRequest(id, "completed")
	w := app.Worker{Store: f.rt.Store, Config: f.c, Archive: brokenArchive{}}
	if e := w.Tick(context.Background()); e == nil {
		t.Fatal("expected archive error")
	}
	events, e := store.Events(context.Background(), f.rt.Store.DB, id)
	if e != nil || len(events) == 0 {
		t.Fatal("journal lost", e)
	}
}

type brokenArchive struct{}

func (brokenArchive) Put(context.Context, string, []byte) error {
	return fmt.Errorf("storage unavailable")
}
func (brokenArchive) Get(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("unavailable")
}
func (brokenArchive) Delete(context.Context, string) error { return fmt.Errorf("unavailable") }

var _ capture.Archive = brokenArchive{}

func TestSettlementIsIdempotentAndRecoveryChargesUnknown(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	status, _, h := f.call("POST", "/v1/chat/completions", f.token, chat("fast", false), false)
	if status != 200 {
		t.Fatal(status)
	}
	r := f.waitRequest(h.Get("X-Request-ID"), "completed")
	if e := f.rt.Store.Finish(ctx, r); e != nil {
		t.Fatal(e)
	}
	var spent, reserved int64
	if e := f.rt.Store.DB.QueryRow(ctx, `SELECT spent_microusd,reserved_microusd FROM budget_periods WHERE key_id=$1`, f.key.ID).Scan(&spent, &reserved); e != nil {
		t.Fatal(e)
	}
	if spent != r.Cost || reserved != 0 {
		t.Fatalf("duplicate settlement spent=%d reserved=%d cost=%d", spent, reserved, r.Cost)
	}
	id := store.ID()
	if e := f.rt.Store.Start(ctx, store.Request{ID: id, KeyID: &f.key.ID}, []byte(`{}`), 30); e != nil {
		t.Fatal(e)
	}
	if e := f.rt.Store.Reserve(ctx, id, f.key.ID, 1000); e != nil {
		t.Fatal(e)
	}
	if _, e := f.rt.Store.DB.Exec(ctx, `UPDATE requests SET last_activity_at=now()-interval '1 hour' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	w := app.Worker{Store: f.rt.Store, Archive: f.rt.Archive, Config: f.c}
	if e := w.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	recovered := f.waitRequest(id, "interrupted")
	if recovered.Cost != 1000 || recovered.UsageStatus != "estimated_unknown" {
		t.Fatal(recovered)
	}
}
