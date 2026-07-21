package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const envPrefix = "RUNIX_"

const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
	EnvTest        = "test"
)

// Log configures structured logging for any Runix binary.
type Log struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

func (l Log) validate() error {
	switch l.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log level %q: must be debug, info, warn or error", l.Level)
	}
	switch l.Format {
	case "json", "text":
	default:
		return fmt.Errorf("log format %q: must be json or text", l.Format)
	}
	return nil
}

// Database configures the PostgreSQL connection of the control plane.
type Database struct {
	DSN string
}

// Redis configures the Redis connection of the control plane.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// Auth configures token signing and secret encryption. In production both
// secrets are mandatory; development and test generate ephemeral ones so a
// fresh checkout runs, at the cost of sessions not surviving restarts.
type Auth struct {
	JWTSecret        string
	EncryptionKey    string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	RememberTokenTTL time.Duration
	AdminPassword    string
	// GeneratedSecrets is set when non-production secrets were generated;
	// the app logs a warning so nobody ships this to production unnoticed.
	GeneratedSecrets bool
}

// Server is the control-plane configuration, loaded from RUNIX_* variables.
type Server struct {
	Env             string
	HTTPAddr        string
	ShutdownTimeout time.Duration
	// CORSOrigins are browser origins allowed to call the API and open
	// WebSockets. Empty means same-origin only (production default behind
	// a reverse proxy); development defaults to the Next.js dev server.
	CORSOrigins []string
	Log         Log
	Database    Database
	Redis       Redis
	Auth        Auth
}

func (c Server) Validate() error {
	switch c.Env {
	case EnvDevelopment, EnvProduction, EnvTest:
	default:
		return fmt.Errorf("env %q: must be development, production or test", c.Env)
	}
	if c.HTTPAddr == "" {
		return errors.New("http addr must not be empty")
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	if c.Env == EnvProduction {
		if len(c.Auth.JWTSecret) < 32 {
			return errors.New("RUNIX_JWT_SECRET must be set (min 32 chars) in production")
		}
		if len(c.Auth.EncryptionKey) < 16 {
			return errors.New("RUNIX_ENCRYPTION_KEY must be set (min 16 chars) in production")
		}
	}
	if c.Auth.AccessTokenTTL < time.Minute || c.Auth.AccessTokenTTL > 24*time.Hour {
		return errors.New("access token ttl must be between 1m and 24h")
	}
	if c.Auth.RefreshTokenTTL < time.Hour {
		return errors.New("refresh token ttl must be at least 1h")
	}
	if c.Auth.RememberTokenTTL < c.Auth.RefreshTokenTTL {
		return errors.New("remember token ttl must not be shorter than refresh token ttl")
	}
	return c.Log.validate()
}

// Agent is the agent configuration, loaded from RUNIX_AGENT_* variables.
type Agent struct {
	ServerURL         string
	Token             string
	HeartbeatInterval time.Duration
	DataDir           string
	Log               Log
}

func (c Agent) Validate() error {
	if c.ServerURL == "" {
		return errors.New("agent server url must not be empty (RUNIX_AGENT_SERVER_URL)")
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("agent server url: %w", err)
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return fmt.Errorf("agent server url %q: scheme must be http(s) or ws(s)", c.ServerURL)
	}
	if c.HeartbeatInterval < time.Second {
		return errors.New("heartbeat interval must be at least 1s")
	}
	return c.Log.validate()
}

// ServerFromEnv loads and validates the control-plane configuration from the
// process environment.
func ServerFromEnv() (Server, error) {
	return serverFrom(os.LookupEnv)
}

// AgentFromEnv loads and validates the agent configuration from the process
// environment.
func AgentFromEnv() (Agent, error) {
	return agentFrom(os.LookupEnv)
}

type lookupFunc func(string) (string, bool)

func serverFrom(look lookupFunc) (Server, error) {
	p := parser{look: look}
	cfg := Server{
		Env:             p.str("ENV", EnvProduction),
		HTTPAddr:        p.str("HTTP_ADDR", ":8080"),
		ShutdownTimeout: p.dur("SHUTDOWN_TIMEOUT", 15*time.Second),
		CORSOrigins:     p.list("CORS_ORIGINS", nil),
		Log: Log{
			Level:  p.str("LOG_LEVEL", "info"),
			Format: p.str("LOG_FORMAT", "json"),
		},
		Database: Database{
			DSN: p.str("DATABASE_DSN", ""),
		},
		Redis: Redis{
			Addr:     p.str("REDIS_ADDR", ""),
			Password: p.str("REDIS_PASSWORD", ""),
			DB:       p.num("REDIS_DB", 0),
		},
		Auth: Auth{
			JWTSecret:        p.str("JWT_SECRET", ""),
			EncryptionKey:    p.str("ENCRYPTION_KEY", ""),
			AccessTokenTTL:   p.dur("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL:  p.dur("REFRESH_TOKEN_TTL", 7*24*time.Hour),
			RememberTokenTTL: p.dur("REMEMBER_TOKEN_TTL", 30*24*time.Hour),
			AdminPassword:    p.str("ADMIN_PASSWORD", ""),
		},
	}
	if cfg.Env != EnvProduction {
		if cfg.Auth.JWTSecret == "" {
			cfg.Auth.JWTSecret = randomSecret(&p)
			cfg.Auth.GeneratedSecrets = true
		}
		if cfg.Auth.EncryptionKey == "" {
			cfg.Auth.EncryptionKey = randomSecret(&p)
			cfg.Auth.GeneratedSecrets = true
		}
		if cfg.CORSOrigins == nil {
			cfg.CORSOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
		}
	}
	if err := p.error(); err != nil {
		return Server{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Server{}, err
	}
	return cfg, nil
}

func randomSecret(p *parser) string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		p.errs = append(p.errs, fmt.Errorf("generate dev secret: %w", err))
		return ""
	}
	return hex.EncodeToString(raw)
}

func agentFrom(look lookupFunc) (Agent, error) {
	p := parser{look: look}
	cfg := Agent{
		ServerURL:         p.str("AGENT_SERVER_URL", ""),
		Token:             p.str("AGENT_TOKEN", ""),
		HeartbeatInterval: p.dur("AGENT_HEARTBEAT_INTERVAL", 30*time.Second),
		DataDir:           p.str("AGENT_DATA_DIR", defaultAgentDataDir()),
		Log: Log{
			Level:  p.str("AGENT_LOG_LEVEL", "info"),
			Format: p.str("AGENT_LOG_FORMAT", "json"),
		},
	}
	if err := p.error(); err != nil {
		return Agent{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Agent{}, err
	}
	return cfg, nil
}

// parser reads typed environment values and accumulates every parse error so
// a misconfigured deployment reports all problems at once.
type parser struct {
	look lookupFunc
	errs []error
}

func (p *parser) str(key, def string) string {
	if v, ok := p.look(envPrefix + key); ok {
		return v
	}
	return def
}

func (p *parser) dur(key string, def time.Duration) time.Duration {
	v, ok := p.look(envPrefix + key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s%s: %w", envPrefix, key, err))
		return def
	}
	return d
}

func (p *parser) list(key string, def []string) []string {
	v, ok := p.look(envPrefix + key)
	if !ok {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (p *parser) num(key string, def int) int {
	v, ok := p.look(envPrefix + key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s%s: %w", envPrefix, key, err))
		return def
	}
	return n
}

func (p *parser) error() error {
	return errors.Join(p.errs...)
}

func defaultAgentDataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "runix-agent")
		}
		return `C:\ProgramData\runix-agent`
	}
	return "/var/lib/runix-agent"
}
