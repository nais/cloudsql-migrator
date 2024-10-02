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
		fmt.Printf("invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(cfg)
	mgr, err := common_main.Main(ctx, cfg, "promote", logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("promote started", "config", cfg)

	gcpProject, err := resolved.ResolveGcpProject(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(3)
	}

	mgr.Logger.Info("getting application", "name", cfg.ApplicationName)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get application", "error", err)
		os.Exit(4)
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get helper name", "error", err)
		os.Exit(5)
	}

	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve source", "error", err)
		os.Exit(6)
	}

	databaseName, err := resolved.ResolveDatabaseName(app)
	if err != nil {
		mgr.Logger.Error("failed to resolve database name", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("getting helper application", "name", helperName)
	helperApp, err := mgr.AppClient.Get(ctx, helperName)
	if err == nil {
		target, err := resolved.ResolveInstance(ctx, helperApp, mgr)
		if err != nil {
			mgr.Logger.Error("failed to resolve target", "error", err)
			os.Exit(8)
		}

		err = promote.CheckReadyForPromotion(ctx, source, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("migration is not ready for promotion", "error", err)
			os.Exit(9)
		}

		err = application.ScaleApplication(ctx, cfg, mgr, 0)
		if err != nil {
			mgr.Logger.Error("failed to scale application", "error", err)
			os.Exit(10)
		}

		err = promote.Promote(ctx, source, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("failed to promote", "error", err)
			os.Exit(11)
		}

		certPaths, err := database.PrepareTargetDatabase(ctx, cfg, target, gcpProject, mgr)
		if err != nil {
			mgr.Logger.Error("failed to prepare target database", "error", err)
			os.Exit(12)
		}

		err = database.ChangeOwnership(ctx, mgr, target, config.PostgresDatabaseName, certPaths)
		if err != nil {
			mgr.Logger.Error("failed to change ownership for database", "databaseName", config.PostgresDatabaseName, "error", err)
			os.Exit(13)
		}

		err = database.ChangeOwnership(ctx, mgr, target, databaseName, certPaths)
		if err != nil {
			mgr.Logger.Error("failed to change ownership for database", "databaseName", databaseName, "error", err)
			os.Exit(14)
		}

		err = application.DeleteHelperApplication(ctx, cfg, mgr)
		if err != nil {
			mgr.Logger.Error("failed to delete helper application", "error", err)
			os.Exit(15)
		}
	} else if !errors.IsNotFound(err) {
		mgr.Logger.Error("failed to get helper application", "error", err)
		os.Exit(16)
	}

	err = database.DeleteTargetDatabaseResource(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target database resource", "error", err)
		os.Exit(17)
	}

	err = instance.WaitForCnrmResourcesToGoAway(ctx, cfg.TargetInstance.Name, mgr)
	if err != nil {
		mgr.Logger.Error("helper instance definition is stuck", "error", err)
		os.Exit(18)
	}

	app, err = application.UpdateApplicationInstance(ctx, cfg, &cfg.TargetInstance, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application", "error", err)
		os.Exit(19)
	}

	target, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve updated target", "error", err)
		os.Exit(20)
	}

	err = application.UpdateApplicationUser(ctx, target, gcpProject, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application user", "error", err)
		os.Exit(21)
	}

	err = backup.CreateBackup(ctx, cfg, target.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(22)
	}

	mgr.Logger.Info("promote completed")
}
