package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/migration"
	"github.com/sethvargo/go-envconfig"
)

func main() {
	cfg := &setup.Config{}

	ctx := context.Background()

	if err := envconfig.Process(ctx, cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg.CommonConfig)
	mgr, err := common_main.Main(ctx, &cfg.CommonConfig, logger)
	if err != nil {
		logger.Error("Failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("Setup started", "config", cfg)

	err = instance.CreateInstance(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create new instance", "error", err)
		os.Exit(3)
	}

	//err = instance.CreateBackup(ctx, mgr)
	//if err != nil {
	//	mgr.Logger.Error("Failed to create backup", "error", err)
	//	os.Exit(4)
	//}

	err = instance.PrepareOldInstance(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to prepare old instance", "error", err)
		os.Exit(5)
	}

	err = database.PrepareOldDatabase(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to prepare old database", "error", err)
		os.Exit(6)
	}

	err = database.PrepareNewDatabase(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to prepare new database", "error", err)
		os.Exit(7)
	}

	err = migration.SetupMigration(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to setup migration", "error", err)
		os.Exit(8)
	}

}
