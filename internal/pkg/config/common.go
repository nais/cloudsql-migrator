package config

import (
	"log/slog"
)

type Logging struct {
	Level  slog.Level `env:"LOG_LEVEL, default=INFO"`
	Format string     `env:"LOG_FORMAT, default=TEXT"`
}

// Resolved is configuration that is resolved by looking up in the cluster
type Resolved struct {
	GcpProjectId string
	InstanceName string
}

type CommonConfig struct {
	// The name of the application
	ApplicationName string `env:"APP_NAME"`
	// The namespace to work in
	Namespace string `env:"NAMESPACE"`
	// The new name of the instance
	NewInstanceName string `env:"NEW_INSTANCE_NAME"`

	// Logging configuration
	Logging

	// Resolved configuration
	Resolved
}
