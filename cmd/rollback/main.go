package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/application"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
)

func main() {
	cfg := config.RollbackConfig{}

	ctx := context.Background()

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg.Config)
	mgr, err := common_main.Main(ctx, &cfg.Config, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("rollback started", "config", cfg)

	err = application.ScaleApplication(ctx, &cfg.Config, mgr, 0)
	if err != nil {
		mgr.Logger.Error("failed to scale application", "error", err)
		os.Exit(3)
	}

	gcpProject, err := resolved.ResolveGcpProject(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(3)
	}

	migrationName, err := resolved.MigrationName(cfg.SourceInstance.Name, cfg.TargetInstance.Name)
	if err != nil {
		mgr.Logger.Error("failed to resolve migration name", "error", err)
		os.Exit(5)
	}

	err = migration.DeleteMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete migration job", "error", err)
		os.Exit(6)
	}

	err = instance.CleanupConnectionProfiles(ctx, &cfg.Config, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to cleanup connection profiles", "error", err)
		os.Exit(7)
	}

	err = instance.DeleteInstance(ctx, cfg.TargetInstance.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target instance", "error", err)
		os.Exit(8)
	}

	masterInstanceName := fmt.Sprintf("%s-master", cfg.TargetInstance.Name)
	err = instance.DeleteInstance(ctx, masterInstanceName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete master instance", "error", err)
		os.Exit(6)
	}

	err = database.DeleteTargetDatabaseResource(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target database resource", "error", err)
		os.Exit(9)
	}

	err = instance.DeleteSslCertByCommonName(ctx, cfg.SourceInstance.Name, cfg.ApplicationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete old ssl certificate", "error", err)
		os.Exit(4)
	}

	app, err := application.UpdateApplicationInstance(ctx, &cfg.Config, &cfg.SourceInstance, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application", "error", err)
		os.Exit(4)
	}

	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve updated target", "error", err)
		os.Exit(2)
	}

	err = application.UpdateApplicationUser(ctx, source, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application user", "error", err)
		os.Exit(10)
	}

	mgr.Logger.Info("deleting SQL SSL Certificates used during migration")
	err = mgr.SqlSslCertClient.DeleteCollection(ctx, v1.ListOptions{
		LabelSelector: "migrator.nais.io/cleanup=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("failed to delete SQL SSL Certificates", "error", err)
		os.Exit(8)
	}

	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get helper name", "error", err)
		os.Exit(9)
	}

	mgr.Logger.Info("deleting Network Policy used during migration")
	err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).Delete(ctx, helperName, v1.DeleteOptions{})
	if err != nil {
		mgr.Logger.Error("failed to delete Network Policy", "error", err)
		os.Exit(10)
	}
}
