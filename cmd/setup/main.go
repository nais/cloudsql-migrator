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

	// The migrationStepsTotal must be updated if the number of steps in the setup process changes
	// Used by nais-cli to show progressbar
	mgr.Logger.Info("Setup started", "config", cfg, "migrationStepsTotal", 20)

	mgr.Logger.Info("Resolving GCP project ID", "migrationStep", 1)
	gcpProject, err := resolved.ResolveGcpProject(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(3)
	}

	mgr.Logger.Info("Getting application", "migrationStep", 2)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get application", "error", err)
		os.Exit(4)
	}

	mgr.Logger.Info("Resolving source instance", "migrationStep", 3)
	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve source", "error", err)
		os.Exit(5)
	}

	if source.Name == cfg.TargetInstance.Name {
		mgr.Logger.Error("source and target instance cannot be the same")
		os.Exit(6)
	}

	mgr.Logger.Info("Resolving database name", "migrationStep", 4)
	databaseName, err := resolved.ResolveDatabaseName(app)
	if err != nil {
		mgr.Logger.Error("failed to resolve database name", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("Validating source instance eligibility", "migrationStep", 5)
	err = instance.ValidateSourceInstance(ctx, source, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("source instance is not eligible for migration", "error", err)
		os.Exit(8)
	}

	mgr.Logger.Info("Creating target instance", "migrationStep", 6)
	target, err := instance.CreateInstance(ctx, cfg, source, gcpProject, databaseName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to create target instance", "error", err)
		os.Exit(9)
	}

	mgr.Logger.Info("Deleting database from intended target instance", "migrationStep", 7)
	err = database.DeleteHelperTargetDatabase(ctx, cfg, target, databaseName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete database from intended target instance", "error", err)
		os.Exit(10)
	}

	mgr.Logger.Info("Creating backup", "migrationStep", 8)
	err = backup.CreateBackup(ctx, cfg, source.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(11)
	}

	mgr.Logger.Info("Disabling cascading delete", "migrationStep", 9)
	err = application.DisableCascadingDelete(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to disable cascading delete", "error", err)
		os.Exit(12)
	}

	mgr.Logger.Info("Creating network policy", "migrationStep", 10)
	err = netpol.CreateNetworkPolicy(ctx, cfg, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to create network policy", "error", err)
		os.Exit(13)
	}

	mgr.Logger.Info("Preparing source instance", "migrationStep", 11)
	err = instance.PrepareSourceInstance(ctx, source, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source instance", "error", err)
		os.Exit(14)
	}

	mgr.Logger.Info("Preparing source database", "migrationStep", 12)
	err = database.PrepareSourceDatabase(ctx, cfg, source, databaseName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source database", "error", err)
		os.Exit(15)
	}

	mgr.Logger.Info("Preparing target instance", "migrationStep", 13)
	err = instance.PrepareTargetInstance(ctx, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare target instance", "error", err)
		os.Exit(16)
	}

	mgr.Logger.Info("Preparing target database", "migrationStep", 14)
	_, err = database.PrepareTargetDatabase(ctx, cfg, target, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare target database", "error", err)
		os.Exit(17)
	}

	mgr.Logger.Info("Setting up migration", "migrationStep", 15)
	migrationJobName, err := migration.PrepareMigrationJob(ctx, cfg, gcpProject, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare migration", "error", err)
		os.Exit(18)
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("Failed to get helper name", "error", err)
		os.Exit(19)
	}

	mgr.Logger.Info("Getting helper application", "name", helperName, "migrationStep", 16)
	helperApp, err := mgr.AppClient.Get(ctx, helperName)
	if err != nil {
		mgr.Logger.Error("Failed to get helper application", "error", err)
		os.Exit(20)
	}

	mgr.Logger.Info("Resolving target instance after preparations", "migrationStep", 17)
	target, err = resolved.ResolveInstance(ctx, helperApp, mgr, resolved.RequireOutgoingIp)
	if err != nil {
		mgr.Logger.Error("Failed to resolve target", "error", err)
		os.Exit(21)
	}

	mgr.Logger.Info("Updating authorized networks for source instance", "migrationStep", 18)
	err = instance.AddTargetOutgoingIpsToSourceAuthNetworks(ctx, source, target, mgr)
	if err != nil {
		mgr.Logger.Error("failed to prepare source instance", "error", err)
		os.Exit(22)
	}

	mgr.Logger.Info("Starting migration", "migrationStep", 19)
	err = migration.StartMigrationJob(ctx, migrationJobName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to start migration", "error", err)
		os.Exit(23)
	}

	mgr.Logger.Info("Setup completed", "migrationStep", 20)
}
