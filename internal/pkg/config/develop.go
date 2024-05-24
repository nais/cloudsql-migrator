package config

type Development struct {
	SkipBackup bool `env:"SKIP_BACKUP"`
}
