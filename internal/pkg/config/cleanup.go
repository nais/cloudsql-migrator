package config

type CleanupConfig struct {
	Config

	// Source instance name
	SourceInstanceName string `env:"SOURCE_INSTANCE_NAME, required"`
}
