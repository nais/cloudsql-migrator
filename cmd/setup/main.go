package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nais/cloudsql-migrator/internal/pkg/application"
	"github.com/nais/cloudsql-migrator/internal/pkg/backup"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/netpol"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
)

func main() {
	cfg := &config.Config{}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := envconfig.Process(ctx, cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(cfg)
	mgr, err := common_main.Main(ctx, cfg, "setup", logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("setup started", "config", cfg)

	gcpProject, err := resolved.ResolveGcpProject(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(3)
	}

	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get application", "error", err)
		os.Exit(4)
	}

	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve source", "error", err)
		os.Exit(5)
	}

	if source.Name == cfg.TargetInstance.Name {
		mgr.Logger.Error("source and target instance cannot be the same")
		os.Exit(6)
	}

	databaseName, err := resolved.ResolveDatabaseName(app)
	if err != nil {
		mgr.Logger.Error("failed to resolve database name", "error", err)
		os.Exit(7)
	}

	target, err := instance.CreateInstance(ctx, cfg, source, gcpProject, databaseName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to create target instance", "error", err)
		os.Exit(8)
	}

	err = database.DeleteHelperTargetDatabase(ctx, cfg, target, databaseName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete database from intended target instance", "error", err)
		os.Exit(9)
	}

	err = backup.CreateBackup(ctx, cfg, source.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(10)
	}

	err = application.DisableCascadingDelete(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to disable cascading delete", "error", err)
		os.Exit(11)
	}

	err = netpol.CreateNetworkPolicy(ctx, cfg, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to create network policy", "error", err)
		os.Exit(12)
	}

	err = instance.PrepareSourceInstance(ctx, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source instance", "error", err)
		os.Exit(13)
	}

	err = database.PrepareSourceDatabase(ctx, cfg, source, databaseName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source database", "error", err)
		os.Exit(14)
	}

	err = instance.PrepareTargetInstance(ctx, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare target instance", "error", err)
		os.Exit(15)
	}

	err = database.PrepareTargetDatabase(ctx, cfg, target, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare target database", "error", err)
		os.Exit(16)
	}

	err = migration.SetupMigration(ctx, cfg, gcpProject, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to setup migration", "error", err)
		os.Exit(17)
	}

	mgr.Logger.Info("setup completed")
}
