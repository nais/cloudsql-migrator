package config

import "log/slog"

type Logging struct {
	Level  slog.Level `env:"LOG_LEVEL, default=INFO"`
	Format string     `env:"LOG_FORMAT, default=TEXT"`
}

type CommonConfig struct {
	// The name of the application
	ApplicationName string `env:"APP_NAME"`
	// Logging configuration
	Logging
}
