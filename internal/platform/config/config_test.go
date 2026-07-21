package config

import (
	"strings"
	"testing"
	"time"
)

func look(m map[string]string) lookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// prodSecrets is the minimum a production deployment must provide.
var prodSecrets = map[string]string{
	"RUNIX_JWT_SECRET":     "0123456789abcdef0123456789abcdef",
	"RUNIX_ENCRYPTION_KEY": "0123456789abcdef",
}

func withSecrets(extra map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range prodSecrets {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestServerDefaults(t *testing.T) {
	cfg, err := serverFrom(look(withSecrets(nil)))
	if err != nil {
		t.Fatalf("serverFrom: %v", err)
	}
	if cfg.Env != EnvProduction {
		t.Errorf("Env = %q, want production", cfg.Env)
	}
	if cfg.Auth.AccessTokenTTL != 15*time.Minute || cfg.Auth.GeneratedSecrets {
		t.Errorf("Auth defaults wrong: %+v", cfg.Auth)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", cfg.ShutdownTimeout)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v, want info/json", cfg.Log)
	}
}

func TestProductionRequiresSecrets(t *testing.T) {
	if _, err := serverFrom(look(nil)); err == nil {
		t.Fatal("production without secrets accepted")
	}
}

func TestDevelopmentGeneratesSecrets(t *testing.T) {
	cfg, err := serverFrom(look(map[string]string{"RUNIX_ENV": "development"}))
	if err != nil {
		t.Fatalf("serverFrom: %v", err)
	}
	if !cfg.Auth.GeneratedSecrets || len(cfg.Auth.JWTSecret) < 32 || len(cfg.Auth.EncryptionKey) < 16 {
		t.Errorf("dev secrets not generated: %+v", cfg.Auth)
	}
}

func TestServerOverrides(t *testing.T) {
	cfg, err := serverFrom(look(map[string]string{
		"RUNIX_ENV":              "development",
		"RUNIX_HTTP_ADDR":        "127.0.0.1:9090",
		"RUNIX_SHUTDOWN_TIMEOUT": "5s",
		"RUNIX_LOG_LEVEL":        "debug",
		"RUNIX_LOG_FORMAT":       "text",
		"RUNIX_DATABASE_DSN":     "postgres://runix@localhost/runix",
		"RUNIX_REDIS_ADDR":       "localhost:6379",
		"RUNIX_REDIS_DB":         "2",
	}))
	if err != nil {
		t.Fatalf("serverFrom: %v", err)
	}
	if cfg.Env != EnvDevelopment || cfg.HTTPAddr != "127.0.0.1:9090" ||
		cfg.ShutdownTimeout != 5*time.Second || cfg.Redis.DB != 2 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestServerInvalidValues(t *testing.T) {
	_, err := serverFrom(look(map[string]string{
		"RUNIX_SHUTDOWN_TIMEOUT": "soon",
		"RUNIX_REDIS_DB":         "two",
	}))
	if err == nil {
		t.Fatal("expected error for unparseable values")
	}
	for _, key := range []string{"RUNIX_SHUTDOWN_TIMEOUT", "RUNIX_REDIS_DB"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestServerInvalidLogLevel(t *testing.T) {
	if _, err := serverFrom(look(map[string]string{"RUNIX_LOG_LEVEL": "verbose"})); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestAgentRequiresServerURL(t *testing.T) {
	if _, err := agentFrom(look(nil)); err == nil {
		t.Fatal("expected error when RUNIX_AGENT_SERVER_URL is missing")
	}
}

func TestAgentValid(t *testing.T) {
	cfg, err := agentFrom(look(map[string]string{
		"RUNIX_AGENT_SERVER_URL": "wss://runix.example.com/agent",
		"RUNIX_AGENT_TOKEN":      "secret",
	}))
	if err != nil {
		t.Fatalf("agentFrom: %v", err)
	}
	if cfg.HeartbeatInterval != 30*time.Second {
		t.Errorf("HeartbeatInterval = %v, want 30s", cfg.HeartbeatInterval)
	}
}

func TestAgentRejectsBadURLAndInterval(t *testing.T) {
	if _, err := agentFrom(look(map[string]string{
		"RUNIX_AGENT_SERVER_URL": "ftp://example.com",
	})); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if _, err := agentFrom(look(map[string]string{
		"RUNIX_AGENT_SERVER_URL":         "https://example.com",
		"RUNIX_AGENT_HEARTBEAT_INTERVAL": "100ms",
	})); err == nil {
		t.Fatal("expected error for sub-second heartbeat interval")
	}
}
