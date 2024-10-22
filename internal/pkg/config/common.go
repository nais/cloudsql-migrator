package config

const (
	PostgresDatabaseName = "postgres"
	PostgresDatabaseUser = "postgres"
	DatabasePort         = 5432
	DatabaseDriver       = "postgres"
)

type InstanceSettings struct {
	Name           string `env:"NAME, required"`
	Type           string `env:"TYPE"`
	Tier           string `env:"TIER"`
	DiskSize       int    `env:"DISK_SIZE"`
	DiskAutoresize *bool  `env:"DISK_AUTORESIZE, noinit"`
}

type Config struct {
	// The name of the application
	ApplicationName string `env:"APP_NAME, required"`
	// The namespace to work in
	Namespace string `env:"NAMESPACE, required"`
	// New instance configuration
	TargetInstance InstanceSettings `env:", prefix=TARGET_INSTANCE_"`

	// Logging configuration
	Logging

	// Development mode config
	Development Development `env:", prefix=DEVELOPMENT_MODE_"`
}
