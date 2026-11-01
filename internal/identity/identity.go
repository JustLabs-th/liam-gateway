package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"llm-gateway/internal/config"
	"llm-gateway/internal/store"
)

type Principal struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	CanReadPayload bool   `json:"can_read_payload"`
	AuthMode       string `json:"auth_mode"`
}
type contextKey struct{}

func From(ctx context.Context) Principal { p, _ := ctx.Value(contextKey{}).(Principal); return p }

type Auth struct {
	Config   config.Config
	Store    *store.Store
	Provider *oidc.Provider
	OAuth    oauth2.Config
}

func New(ctx context.Context, c config.Config, s *store.Store) (*Auth, error) {
	a := &Auth{Config: c, Store: s}
	if c.Auth.Mode == "disabled" {
		return a, nil
	}
	p, e := oidc.NewProvider(ctx, c.Auth.Issuer)
	if e != nil {
		return nil, e
	}
	a.Provider = p
	a.OAuth = oauth2.Config{ClientID: c.Auth.ClientID, ClientSecret: c.Auth.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: c.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	return a, nil
}
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p := Principal{ID: "anonymous", Role: "admin", CanReadPayload: true, AuthMode: a.Config.Auth.Mode}
		if a.Config.Auth.Mode == "oidc" {
			c, e := r.Cookie("gateway_session")
			if e != nil {
				http.Error(w, "authentication required", 401)
				return
			}
			e = a.Store.DB.QueryRow(r.Context(), `SELECT u.id,u.email,u.role,u.can_read_payload FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, store.Hash(c.Value)).Scan(&p.ID, &p.Email, &p.Role, &p.CanReadPayload)
			if e != nil {
				http.Error(w, "invalid or expired session", 401)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, p)))
	})
}
func Admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if From(r.Context()).Role != "admin" {
			http.Error(w, "admin permission required", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *Auth) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			origin := r.Header.Get("Origin")
			if (origin != "" && origin != a.Config.PublicURL) || r.Header.Get("X-Gateway-Admin") != "1" {
				http.Error(w, "same-origin admin request required", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *Auth) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: len(a.Config.PublicURL) >= 8 && a.Config.PublicURL[:8] == "https://", SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

type loginState struct {
	State, Nonce, Verifier string
	Expires                int64
}

func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.Config.Auth.Mode == "disabled" {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	v := loginState{store.ID(), store.ID(), oauth2.GenerateVerifier(), time.Now().Add(10 * time.Minute).Unix()}
	b, _ := json.Marshal(v)
	sealed, e := a.Store.Cipher.Seal(b, "oidc-login")
	if e != nil {
		http.Error(w, "login unavailable", 503)
		return
	}
	http.SetCookie(w, a.cookie("gateway_oidc", base64.RawURLEncoding.EncodeToString(sealed), 600))
	http.Redirect(w, r, a.OAuth.AuthCodeURL(v.State, oidc.Nonce(v.Nonce), oauth2.S256ChallengeOption(v.Verifier)), http.StatusFound)
}
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if a.Config.Auth.Mode != "oidc" {
		http.NotFound(w, r)
		return
	}
	cookie, e := r.Cookie("gateway_oidc")
	if e != nil {
		http.Error(w, "missing login state", 400)
		return
	}
	http.SetCookie(w, a.cookie("gateway_oidc", "", -1))
	b, e := base64.RawURLEncoding.DecodeString(cookie.Value)
	if e != nil {
		http.Error(w, "invalid login state", 400)
		return
	}
	b, e = a.Store.Cipher.Open(b, "oidc-login")
	if e != nil {
		http.Error(w, "invalid login state", 400)
		return
	}
	var v loginState
	if json.Unmarshal(b, &v) != nil || v.Expires < time.Now().Unix() || v.State == "" || v.State != r.URL.Query().Get("state") {
		http.Error(w, "invalid login state", 400)
		return
	}
	tok, e := a.OAuth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(v.Verifier))
	if e != nil {
		http.Error(w, "SSO exchange failed", 401)
		return
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "ID token missing", 401)
		return
	}
	id, e := a.Provider.Verifier(&oidc.Config{ClientID: a.Config.Auth.ClientID}).Verify(r.Context(), raw)
	if e != nil || id.Nonce != v.Nonce {
		http.Error(w, "invalid ID token", 401)
		return
	}
	var claims struct {
		Email string `json:"email"`
	}
	if id.Claims(&claims) != nil {
		http.Error(w, "invalid claims", 401)
		return
	}
	role := "viewer"
	if slices.Contains(a.Config.Auth.AdminSubjects, id.Subject) {
		role = "admin"
	}
	payload := slices.Contains(a.Config.Auth.PayloadSubjects, id.Subject) || role == "admin"
	var uid string
	e = a.Store.DB.QueryRow(r.Context(), `INSERT INTO users(id,issuer,subject,email,role,can_read_payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(issuer,subject) DO UPDATE SET email=EXCLUDED.email RETURNING id`, store.ID(), id.Issuer, id.Subject, claims.Email, role, payload).Scan(&uid)
	if e != nil {
		http.Error(w, "session unavailable", 503)
		return
	}
	session := store.ID() + store.ID()
	_, e = a.Store.DB.Exec(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '8 hours')`, store.Hash(session), uid)
	if e != nil {
		http.Error(w, "session unavailable", 503)
		return
	}
	if a.Store.Audit(r.Context(), uid, "auth.login", uid, store.ID()) != nil {
		_, _ = a.Store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, store.Hash(session))
		http.Error(w, "audit unavailable", 503)
		return
	}
	http.SetCookie(w, a.cookie("gateway_session", session, 8*3600))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	c, e := r.Cookie("gateway_session")
	if e == nil {
		if _, e = a.Store.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, store.Hash(c.Value)); e != nil {
			http.Error(w, "logout unavailable", 503)
			return
		}
	}
	http.SetCookie(w, a.cookie("gateway_session", "", -1))
	w.WriteHeader(http.StatusNoContent)
}
