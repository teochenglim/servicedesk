package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// LevelFatal sits above slog.LevelError so Fatal() log lines are never
// filtered out regardless of the configured minimum level.
const LevelFatal = slog.Level(12)

// ParseLevel maps the five supported level names (fatal, error, warning/warn,
// info, debug) to a slog.Level, defaulting to info on anything unrecognized.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warning", "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "fatal":
		return LevelFatal
	default:
		return slog.LevelInfo
	}
}

// New builds the process-wide structured logger at the requested level.
func New(levelStr string) *slog.Logger {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       ParseLevel(levelStr),
		ReplaceAttr: replaceLevelName,
	})
	return slog.New(h)
}

func replaceLevelName(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.LevelKey {
		if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == LevelFatal {
			a.Value = slog.StringValue("FATAL")
		}
	}
	return a
}

// Fatal logs at the Fatal level (always emitted) and terminates the process,
// mirroring the log.Fatal convention stdlib "log" offers but slog does not.
func Fatal(logger *slog.Logger, msg string, args ...any) {
	logger.Log(context.Background(), LevelFatal, msg, args...)
	os.Exit(1)
}
