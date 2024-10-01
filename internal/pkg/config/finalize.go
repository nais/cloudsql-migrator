package config

type FinalizeConfig struct {
	Config

	// Source instance name
	SourceInstanceName string `env:"SOURCE_INSTANCE_NAME, required"`
}
