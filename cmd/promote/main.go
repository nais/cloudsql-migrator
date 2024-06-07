package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/application"
	"github.com/nais/cloudsql-migrator/internal/pkg/backup"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/promote"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
	"os"
)

func main() {
	ctx := context.Background()

	cfg := &config.Config{}

	if err := envconfig.Process(ctx, cfg); err != nil {
		fmt.Printf("invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(cfg)
	mgr, err := common_main.Main(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("promote started", "config", cfg)

	gcpProject, err := resolved.ResolveGcpProject(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(1)
	}

	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get application", "error", err)
		os.Exit(2)
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get helper name", "error", err)
		os.Exit(3)
	}

	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve source", "error", err)
		os.Exit(2)
	}

	databaseName, err := resolved.ResolveDatabaseName(app)
	if err != nil {
		mgr.Logger.Error("failed to resolve database name", "error", err)
		os.Exit(3)
	}

	helperApp, err := mgr.AppClient.Get(ctx, helperName)
	if err != nil {
		mgr.Logger.Error("failed to get helper application", "error", err)
		os.Exit(4)
	}

	target, err := resolved.ResolveInstance(ctx, helperApp, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve target", "error", err)
		os.Exit(2)
	}

	err = promote.CheckReadyForPromotion(ctx, source, target, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("migration is not ready for promotion", "error", err)
		os.Exit(2)
	}

	err = application.ScaleApplication(ctx, cfg, mgr, 0)
	if err != nil {
		mgr.Logger.Error("failed to scale application", "error", err)
		os.Exit(3)
	}

	err = promote.Promote(ctx, source, target, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to promote", "error", err)
		os.Exit(4)
	}

	certPaths, err := instance.CreateSslCert(ctx, cfg, mgr, target.Name, &target.SslCert)
	if err != nil {
		mgr.Logger.Error("failed to create ssl certificate", "error", err)
		os.Exit(6)
	}

	err = database.ChangeOwnership(ctx, mgr, target, databaseName, certPaths)
	if err != nil {
		mgr.Logger.Error("failed to change ownership", "error", err)
		os.Exit(7)
	}

	err = application.DeleteHelperApplication(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete helper application", "error", err)
		os.Exit(8)
	}

	err = database.DeleteTargetDatabaseResource(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target database resource", "error", err)
		os.Exit(9)
	}

	app, err = application.UpdateApplicationInstance(ctx, cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application", "error", err)
		os.Exit(9)
	}

	target, err = resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve updated target", "error", err)
		os.Exit(2)
	}

	err = application.UpdateApplicationUser(ctx, target, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application user", "error", err)
		os.Exit(10)
	}

	err = backup.CreateBackup(ctx, cfg, target.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(12)
	}
}
