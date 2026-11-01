package config

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestExampleDefaultsToOptionalSSO(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://gateway@localhost/gateway")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("GATEWAY_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	c, e := Load("../../configs/example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if c.Auth.Mode != "disabled" || c.RequestTimeout != 10*time.Minute {
		t.Fatal("unexpected defaults")
	}
	c.Auth.Mode = "typo"
	if c.Validate() == nil {
		t.Fatal("unknown auth mode accepted")
	}
	c.Auth.Mode = "oidc"
	c.Auth.Issuer = ""
	if c.Validate() == nil {
		t.Fatal("incomplete SSO accepted")
	}
	c.Auth.Mode = "disabled"
	c.EncryptionKey = "short"
	if c.Validate() == nil {
		t.Fatal("invalid encryption key accepted")
	}
}
