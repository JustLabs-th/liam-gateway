package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"llm-gateway/internal/config"
)

type oidcMock struct {
	server           *httptest.Server
	signer           jose.Signer
	public           jose.JSONWebKey
	mu               sync.Mutex
	nonce, challenge string
	badNonce         bool
}

func newOIDCMock(t *testing.T) *oidcMock {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
	if e != nil {
		t.Fatal(e)
	}
	m := &oidcMock{signer: signer, public: jose.JSONWebKey{Key: &key.PublicKey, KeyID: "test", Algorithm: "ES256", Use: "sig"}}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": m.server.URL, "authorization_endpoint": m.server.URL + "/authorize", "token_endpoint": m.server.URL + "/token", "jwks_uri": m.server.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"ES256"}, "response_types_supported": []string{"code"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{m.public}})
		case "/token":
			_ = r.ParseForm()
			m.mu.Lock()
			nonce, challenge, bad := m.nonce, m.challenge, m.badNonce
			m.mu.Unlock()
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
				http.Error(w, "bad PKCE verifier", 400)
				return
			}
			if bad {
				nonce = "wrong"
			}
			token, e := jwt.Signed(m.signer).Claims(map[string]any{"iss": m.server.URL, "sub": "user-1", "aud": "gateway-test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "user@example.test"}).Serialize()
			if e != nil {
				http.Error(w, "signing failed", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "mock-access", "token_type": "Bearer", "id_token": token})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.server.Close)
	return m
}
func oidcLogin(t *testing.T, f *fixture, m *oidcMock) (*http.Client, int) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Get(f.server.URL + "/auth/login")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatal(resp.StatusCode)
	}
	authorization, e := url.Parse(resp.Header.Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := authorization.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
		t.Fatal("missing OIDC protection")
	}
	m.mu.Lock()
	m.nonce = q.Get("nonce")
	m.challenge = q.Get("code_challenge")
	m.mu.Unlock()
	resp, e = client.Get(f.server.URL + "/auth/callback?code=test-code&state=" + url.QueryEscape(q.Get("state")))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	return client, resp.StatusCode
}
func TestOIDCLoginPermissionsAndLogout(t *testing.T) {
	m := newOIDCMock(t)
	f := newFixture(t, func(c *config.Config) {
		c.Auth = config.Auth{Mode: "oidc", Issuer: m.server.URL, ClientID: "gateway-test", ClientSecret: "secret"}
	})
	status, _, _ := f.call("GET", "/admin/api/me", "", "", false)
	if status != 401 {
		t.Fatal("anonymous SSO access", status)
	}
	client, status := oidcLogin(t, f, m)
	if status != 303 {
		t.Fatal("login failed", status)
	}
	resp, e := client.Get(f.server.URL + "/admin/api/me")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"role":"viewer"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp, e = client.Get(f.server.URL + "/admin/api/keys")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("viewer can read keys")
	}
	resp, e = client.Get(f.server.URL + "/admin/api/requests/anything/payload")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("viewer can read payload")
	}
	req, _ := http.NewRequest("POST", f.server.URL+"/auth/logout", nil)
	req.Header.Set("X-Gateway-Admin", "1")
	resp, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	resp, e = client.Get(f.server.URL + "/admin/api/me")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("session survived logout")
	}
	var n int
	if e = f.rt.Store.DB.QueryRow(context.Background(), `SELECT count(*) FROM sessions`).Scan(&n); e != nil || n != 0 {
		t.Fatal("session not invalidated", n, e)
	}
}
func TestOIDCRejectsStateNonceAndBootstrapsAdmin(t *testing.T) {
	m := newOIDCMock(t)
	f := newFixture(t, func(c *config.Config) {
		c.Auth = config.Auth{Mode: "oidc", Issuer: m.server.URL, ClientID: "gateway-test", ClientSecret: "secret", AdminSubjects: []string{"user-1"}}
	})
	status, _, _ := f.call("GET", "/auth/callback?code=x&state=x", "", "", false)
	if status != 400 {
		t.Fatal("state bypass")
	}
	m.mu.Lock()
	m.badNonce = true
	m.mu.Unlock()
	_, status = oidcLogin(t, f, m)
	if status != 401 {
		t.Fatal("nonce bypass", status)
	}
	m.mu.Lock()
	m.badNonce = false
	m.mu.Unlock()
	client, status := oidcLogin(t, f, m)
	if status != 303 {
		t.Fatal(status)
	}
	resp, e := client.Get(f.server.URL + "/admin/api/me")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"role":"admin"`) || !strings.Contains(string(b), `"can_read_payload":true`) {
		t.Fatal(string(b))
	}
	// Revoking payload permission takes effect on an existing session.
	if _, e = f.rt.Store.DB.Exec(context.Background(), `UPDATE users SET can_read_payload=false WHERE subject='user-1'`); e != nil {
		t.Fatal(e)
	}
	resp, e = client.Get(f.server.URL + "/admin/api/requests/anything/payload")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("stale session permissions")
	}
}
