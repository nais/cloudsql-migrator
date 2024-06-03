package config

type CleanupConfig struct {
	Config

	// Old instance name
	OldInstanceName string `env:"OLD_INSTANCE_NAME, required"`
}
