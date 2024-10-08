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
	"github.com/nais/cloudsql-migrator/internal/pkg/promote"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
	"k8s.io/apimachinery/pkg/api/errors"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg := &config.Config{}

	if err := envconfig.Process(ctx, cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(cfg)
	mgr, err := common_main.Main(ctx, cfg, "promote", logger)
	if err != nil {
		logger.Error("Failed to complete configuration", "error", err)
		os.Exit(2)
	}

	// The migrationStepsTotal must be updated if the number of steps in the promote process changes
	// Used by nais-cli to show progressbar
	mgr.Logger.Info("Promote started", "config", cfg, "migrationStepsTotal", 19)

	mgr.Logger.Info("Resolving GCP project ID", "migrationStep", 1)
	gcpProject, err := resolved.ResolveGcpProject(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve GCP project ID", "error", err)
		os.Exit(3)
	}

	mgr.Logger.Info("Getting application", "name", cfg.ApplicationName, "migrationStep", 2)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("Failed to get application", "error", err)
		os.Exit(4)
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("Failed to get helper name", "error", err)
		os.Exit(5)
	}

	mgr.Logger.Info("Resolving source instance", "migrationStep", 3)
	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve source", "error", err)
		os.Exit(6)
	}

	mgr.Logger.Info("Resolving database name", "migrationStep", 4)
	databaseName, err := resolved.ResolveDatabaseName(app)
	if err != nil {
		mgr.Logger.Error("Failed to resolve database name", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("Getting helper application", "name", helperName, "migrationStep", 5)
	helperApp, err := mgr.AppClient.Get(ctx, helperName)
	if err == nil {
		mgr.Logger.Info("Resolving target instance", "migrationStep", 6)
		target, err := resolved.ResolveInstance(ctx, helperApp, mgr)
		if err != nil {
			mgr.Logger.Error("Failed to resolve target", "error", err)
			os.Exit(8)
		}

		mgr.Logger.Info("Checking if migration is ready for promotion", "migrationStep", 7)
		err = promote.CheckReadyForPromotion(ctx, source, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("Migration is not ready for promotion", "error", err)
			os.Exit(9)
		}

		mgr.Logger.Info("Scaling down application", "migrationStep", 8)
		err = application.ScaleApplication(ctx, cfg, mgr, 0)
		if err != nil {
			mgr.Logger.Error("Failed to scale application", "error", err)
			os.Exit(10)
		}

		mgr.Logger.Info("Starting promote of target instance", "migrationStep", 9)
		err = promote.Promote(ctx, source, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("Failed to promote", "error", err)
			os.Exit(11)
		}

		mgr.Logger.Info("Preparing target database", "migrationStep", 10)
		certPaths, err := database.PrepareTargetDatabase(ctx, cfg, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("Failed to prepare target database", "error", err)
			os.Exit(12)
		}

		mgr.Logger.Info("Changing ownership for postgres database", "migrationStep", 11)
		err = database.ChangeOwnership(ctx, mgr, target, config.PostgresDatabaseName, certPaths)
		if err != nil {
			mgr.Logger.Error("Failed to change ownership for database", "databaseName", config.PostgresDatabaseName, "error", err)
			os.Exit(13)
		}

		mgr.Logger.Info("Changing ownership for application database", "migrationStep", 12)
		err = database.ChangeOwnership(ctx, mgr, target, databaseName, certPaths)
		if err != nil {
			mgr.Logger.Error("Failed to change ownership for database", "databaseName", databaseName, "error", err)
			os.Exit(14)
		}

		mgr.Logger.Info("Deleting helper application", "migrationStep", 13)
		err = application.DeleteHelperApplication(ctx, cfg, mgr)
		if err != nil {
			mgr.Logger.Error("Failed to delete helper application", "error", err)
			os.Exit(15)
		}
	} else if errors.IsNotFound(err) {
		mgr.Logger.Info("Helper application is gone, skipping previously completed steps")
	} else {
		mgr.Logger.Error("Failed to get helper application", "error", err)
		os.Exit(16)
	}

	mgr.Logger.Info("Deleting target database resource", "migrationStep", 14)
	err = database.DeleteTargetDatabaseResource(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete target database resource", "error", err)
		os.Exit(17)
	}

	mgr.Logger.Info("Waiting for cnrm resources to go away", "migrationStep", 15)
	err = instance.WaitForCnrmResourcesToGoAway(ctx, cfg.TargetInstance.Name, cfg.ApplicationName, mgr)
	if err != nil {
		mgr.Logger.Error("Helper instance definition is stuck", "error", err)
		os.Exit(18)
	}

	mgr.Logger.Info("Updating application", "migrationStep", 16)
	app, err = application.UpdateApplicationInstance(ctx, cfg, &cfg.TargetInstance, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to update application", "error", err)
		os.Exit(19)
	}

	mgr.Logger.Info("Resolving updated target", "migrationStep", 17)
	target, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve updated target", "error", err)
		os.Exit(20)
	}

	mgr.Logger.Info("Updating application user", "migrationStep", 18)
	err = application.UpdateApplicationUser(ctx, target, gcpProject, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to update application user", "error", err)
		os.Exit(21)
	}

	mgr.Logger.Info("Creating backup", "migrationStep", 19)
	err = backup.CreateBackup(ctx, cfg, target.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(22)
	}

	mgr.Logger.Info("Promote completed")
}
