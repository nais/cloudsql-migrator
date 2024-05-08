package config

type Logging struct {
	Level  string `env:"LOG_LEVEL, default=INFO"`
	Format string `env:"LOG_FORMAT, default=JSON"`
}

type CommonConfig struct {
	// The name of the application
	ApplicationName string `env:"APP_NAME"`
	// Logging configuration
	Logging
}
