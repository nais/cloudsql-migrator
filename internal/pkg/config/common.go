package config

// Resolved is configuration that is resolved by looking up in the cluster
type Resolved struct {
	GcpProjectId string
	InstanceName string
}

type NewInstance struct {
	Name     string `env:"NAME, required"`
	Type     string `env:"TYPE"`
	Tier     string `env:"TIER"`
	DiskSize int
}

type CommonConfig struct {
	// The name of the application
	ApplicationName string `env:"APP_NAME, required"`
	// The namespace to work in
	Namespace string `env:"NAMESPACE, required"`
	// New instance configuration
	NewInstance NewInstance `env:", prefix=NEW_INSTANCE_"`

	// Logging configuration
	Logging

	// Resolved configuration
	Resolved
}
