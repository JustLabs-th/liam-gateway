package httptransport

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"llm-gateway/internal/capture"
	"llm-gateway/internal/config"
	"llm-gateway/internal/gateway"
	"llm-gateway/internal/identity"
	"llm-gateway/internal/quota"
	"llm-gateway/internal/store"
)

func Router(c config.Config, s *store.Store, q *quota.Limiter, auth *identity.Auth, archive capture.Archive) http.Handler {
	r := chi.NewRouter()
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	g := gateway.New(c, s, q, reg)
	a := Admin{s, c, archive}
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id := store.ID()
			req.Header.Set("X-Request-ID", id)
			w.Header().Set("X-Request-ID", id)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "same-origin")
			started := time.Now()
			next.ServeHTTP(w, req)
			slog.Info("http request", "request_id", w.Header().Get("X-Request-ID"), "method", req.Method, "route", chi.RouteContext(req.Context()).RoutePattern(), "duration_ms", time.Since(started).Milliseconds())
		})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, map[string]string{"status": "ok"}) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.DB.Ping(r.Context()) != nil || q.Client.Ping(r.Context()).Err() != nil {
			http.Error(w, "not ready", 503)
			return
		}
		respond(w, map[string]string{"status": "ready"})
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", http.StatusSeeOther) })
	r.Get("/auth/login", auth.Login)
	r.Get("/auth/callback", auth.Callback)
	r.With(auth.Middleware, auth.CSRF).Post("/auth/logout", auth.Logout)
	r.Get("/v1/models", g.Models)
	r.Post("/v1/chat/completions", g.Chat)
	r.With(auth.Middleware, identity.Admin).Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	r.Route("/admin/api", func(r chi.Router) {
		r.Use(auth.Middleware, auth.CSRF)
		r.Get("/me", func(w http.ResponseWriter, r *http.Request) { respond(w, identity.From(r.Context())) })
		r.Get("/overview", a.Overview)
		r.Get("/analytics", a.Analytics)
		r.Get("/models", a.Models)
		r.Get("/requests", a.Requests)
		r.Get("/requests/{id}", a.Request)
		r.Get("/requests/{id}/payload", a.Payload)
		r.Group(func(r chi.Router) {
			r.Use(identity.Admin)
			r.Get("/keys", a.Keys)
			r.Post("/keys", a.CreateKey)
			r.Delete("/keys/{id}", a.RevokeKey)
			r.Post("/keys/{id}/rotate", a.RotateKey)
			r.Put("/models/{name}", a.PutModel)
			r.Get("/audit", a.Audit)
			r.Get("/users", a.Users)
			r.Patch("/users/{id}", a.UpdateUser)
		})
	})
	if c.WebURL != "" {
		target, _ := url.Parse(c.WebURL)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			http.Error(w, "Admin UI unavailable; start the web service", 503)
		}
		r.Handle("/admin", proxy)
		r.Handle("/admin/*", proxy)
	}
	return otelhttp.NewHandler(r, "gateway.http")
}
