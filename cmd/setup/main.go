package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/backup"
	"os"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/migration"
	"github.com/sethvargo/go-envconfig"
)

func main() {
	cfg := &config.CommonConfig{}

	ctx := context.Background()

	if err := envconfig.Process(ctx, cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(cfg)
	mgr, err := common_main.Main(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("setup started", "config", cfg)

	err = instance.CreateInstance(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to create target instance", "error", err)
		os.Exit(3)
	}

	err = backup.CreateBackup(ctx, cfg, mgr, mgr.Resolved.Source.Name)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(4)
	}

	err = instance.PrepareSourceInstance(ctx, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source instance", "error", err)
		os.Exit(5)
	}

	err = database.PrepareSourceDatabase(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source database", "error", err)
		os.Exit(6)
	}

	err = database.PrepareTargetDatabase(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare target database", "error", err)
		os.Exit(7)
	}

	err = migration.SetupMigration(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to setup migration", "error", err)
		os.Exit(8)
	}

}
