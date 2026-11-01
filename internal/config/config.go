package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Listen          string              `yaml:"listen" json:"listen"`
	PublicURL       string              `yaml:"public_url" json:"public_url"`
	DatabaseURL     string              `yaml:"database_url" json:"database_url"`
	RedisURL        string              `yaml:"redis_url" json:"redis_url"`
	EncryptionKey   string              `yaml:"encryption_key" json:"encryption_key"`
	WebURL          string              `yaml:"web_url" json:"web_url"`
	Auth            Auth                `yaml:"auth" json:"auth"`
	Providers       map[string]Provider `yaml:"providers" json:"providers"`
	Models          map[string]Model    `yaml:"models" json:"models"`
	Capture         Capture             `yaml:"capture" json:"capture"`
	MaxRequestBytes int64               `yaml:"max_request_bytes" json:"max_request_bytes"`
	RequestTimeout  time.Duration       `yaml:"request_timeout" json:"request_timeout"`
	HeaderTimeout   time.Duration       `yaml:"upstream_header_timeout" json:"upstream_header_timeout"`
	MaxRetries      int                 `yaml:"max_retries" json:"max_retries"`
}
type Auth struct {
	Mode            string   `yaml:"mode" json:"mode"`
	Issuer          string   `yaml:"issuer" json:"issuer"`
	ClientID        string   `yaml:"client_id" json:"client_id"`
	ClientSecret    string   `yaml:"client_secret" json:"client_secret"`
	AdminSubjects   []string `yaml:"admin_subjects" json:"admin_subjects"`
	PayloadSubjects []string `yaml:"payload_subjects" json:"payload_subjects"`
}
type Provider struct {
	Type    string `yaml:"type" json:"type"`
	BaseURL string `yaml:"base_url" json:"base_url"`
	APIKey  string `yaml:"api_key" json:"api_key"`
}
type Model struct {
	Provider            string  `yaml:"provider" json:"provider"`
	Model               string  `yaml:"model" json:"model"`
	MaxInputTokens      int64   `yaml:"max_input_tokens" json:"max_input_tokens"`
	MaxOutputTokens     int64   `yaml:"max_output_tokens" json:"max_output_tokens"`
	InputUSDPerMillion  float64 `yaml:"input_usd_per_million" json:"input_usd_per_million"`
	OutputUSDPerMillion float64 `yaml:"output_usd_per_million" json:"output_usd_per_million"`
}
type Capture struct {
	RetentionDays int           `yaml:"retention_days" json:"retention_days"`
	ArchiveAfter  time.Duration `yaml:"archive_after" json:"archive_after"`
	Storage       string        `yaml:"storage" json:"storage"`
	Directory     string        `yaml:"directory" json:"directory"`
	Endpoint      string        `yaml:"endpoint" json:"endpoint"`
	Bucket        string        `yaml:"bucket" json:"bucket"`
	AccessKey     string        `yaml:"access_key" json:"access_key"`
	SecretKey     string        `yaml:"secret_key" json:"secret_key"`
	Secure        bool          `yaml:"secure" json:"secure"`
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	// Config is trusted operator input; environment references are expanded before YAML decoding.
	b = []byte(os.ExpandEnv(string(b)))
	if err = yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if value := os.Getenv("GATEWAY_LISTEN"); value != "" {
		c.Listen = value
	}
	if value := os.Getenv("GATEWAY_WEB_URL"); value != "" {
		c.WebURL = value
	}
	if value := os.Getenv("GATEWAY_PUBLIC_URL"); value != "" {
		c.PublicURL = value
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.Auth.Mode == "" {
		c.Auth.Mode = "disabled"
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = 2 << 20
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 10 * time.Minute
	}
	if c.HeaderTimeout == 0 {
		c.HeaderTimeout = 60 * time.Second
	}
	if c.Capture.RetentionDays == 0 {
		c.Capture.RetentionDays = 30
	}
	if c.Capture.ArchiveAfter == 0 {
		c.Capture.ArchiveAfter = time.Minute
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Auth.Mode != "disabled" && c.Auth.Mode != "oidc" {
		return errors.New("auth.mode must be disabled or oidc")
	}
	u, e := url.Parse(c.PublicURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" {
		return errors.New("public_url must be an HTTP origin without path")
	}
	if c.WebURL != "" {
		web, e := url.Parse(c.WebURL)
		if e != nil || web.Host == "" || (web.Scheme != "http" && web.Scheme != "https") || web.User != nil || web.RawQuery != "" || web.Fragment != "" {
			return errors.New("invalid web_url")
		}
	}
	if c.Auth.Mode == "oidc" && (c.Auth.Issuer == "" || c.Auth.ClientID == "" || c.Auth.ClientSecret == "") {
		return errors.New("OIDC issuer, client_id and client_secret required")
	}
	if c.DatabaseURL == "" || c.RedisURL == "" {
		return errors.New("database_url and redis_url required")
	}
	k, e := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if e != nil || len(k) != 32 {
		return errors.New("encryption_key must be base64 encoded 32 bytes")
	}
	if c.MaxRequestBytes < 1 || c.RequestTimeout < time.Second || c.HeaderTimeout < time.Second || c.MaxRetries < 0 || c.MaxRetries > 2 {
		return errors.New("invalid request limits")
	}
	if c.Capture.RetentionDays < 1 || c.Capture.ArchiveAfter < 0 {
		return errors.New("invalid capture retention")
	}
	if c.Capture.Storage != "filesystem" && c.Capture.Storage != "s3" {
		return errors.New("capture.storage must be filesystem or s3")
	}
	if c.Capture.Storage == "filesystem" && c.Capture.Directory == "" {
		return errors.New("capture.directory required")
	}
	if c.Capture.Storage == "s3" && (c.Capture.Endpoint == "" || c.Capture.Bucket == "") {
		return errors.New("S3 endpoint and bucket required")
	}
	if len(c.Models) == 0 {
		return errors.New("at least one model alias required")
	}
	for name, m := range c.Models {
		p, ok := c.Providers[m.Provider]
		if !ok {
			return fmt.Errorf("model %s references missing provider", name)
		}
		if p.Type != "openai" && p.Type != "anthropic" {
			return fmt.Errorf("unknown provider type for %s", name)
		}
		u, e := url.Parse(p.BaseURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid provider URL for %s", name)
		}
		if m.Model == "" || m.MaxInputTokens < 1 || m.MaxOutputTokens < 1 || m.InputUSDPerMillion < 0 || m.OutputUSDPerMillion < 0 || math.IsNaN(m.InputUSDPerMillion) || math.IsNaN(m.OutputUSDPerMillion) || math.IsInf(m.InputUSDPerMillion, 0) || math.IsInf(m.OutputUSDPerMillion, 0) || m.MaxInputTokens > 10000000 || m.MaxOutputTokens > 1000000 || m.InputUSDPerMillion > 1000000 || m.OutputUSDPerMillion > 1000000 {
			return fmt.Errorf("invalid model %s", name)
		}
	}
	return nil
}
