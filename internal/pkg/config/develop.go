package config

type Development struct {
	// Skips taking backups
	SkipBackup bool `env:"SKIP_BACKUP"`

	// Sets an unsafe, pre-defined password for postgres user
	UnsafePassword bool `env:"UNSAFE_PASSWORD"`
}
