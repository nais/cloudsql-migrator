package config

type Development struct {
	// Skips taking backups
	SkipBackup bool `env:"SKIP_BACKUP"`

	// Sets an unsafe, pre-defined password for postgres user
	UnsafePassword bool `env:"UNSAFE_PASSWORD"`

	// Looks up outgoing PrimaryIp via https://api.ipify.org and adds it to the authorized networks for instances
	AddAuthNetwork bool `env:"ADD_AUTH_NETWORK"`
}
