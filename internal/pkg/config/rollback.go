package config

type RollbackConfig struct {
	Config

	// Source instance configuration
	SourceInstance InstanceSettings `env:", prefix=SOURCE_INSTANCE_"`
}
