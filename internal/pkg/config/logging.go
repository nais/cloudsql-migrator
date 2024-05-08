package config

import (
	"log/slog"
	"os"
)

func SetupLogging(conf *CommonConfig) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: conf.Logging.Level,
	}
	var handler slog.Handler
	if conf.Logging.Format == "JSON" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}
