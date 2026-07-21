package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options selects the log level and output format. Values follow
// config.Log; New validates them again so the package stands alone.
type Options struct {
	Level  string
	Format string
	// Output overrides os.Stdout, primarily for tests.
	Output io.Writer
}

// New builds a structured slog.Logger. JSON is the production format; text
// is for humans during development.
func New(opts Options) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(opts.Level) {
	case "debug":
		level = slog.LevelDebug
	case "", "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", opts.Level)
	}

	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	var handler slog.Handler
	switch strings.ToLower(opts.Format) {
	case "", "json":
		handler = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	case "text":
		handler = slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	default:
		return nil, fmt.Errorf("unknown log format %q", opts.Format)
	}
	return slog.New(handler), nil
}
