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
	"time"
)

func main() {
	cfg := config.RollbackConfig{}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg.Config)
	mgr, err := common_main.Main(ctx, &cfg.Config, "rollback", logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("rollback started", "config", cfg)

	mgr.Logger.Info("getting application", "name", cfg.ApplicationName)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("failed to get application", "error", err)
		os.Exit(3)
	}

	if app.Spec.GCP.SqlInstances[0].Name != cfg.SourceInstance.Name {
		// We only need to scale down if we are making changes to the instance the application currently uses
		err = application.ScaleApplication(ctx, &cfg.Config, mgr, 0)
		if err != nil {
			mgr.Logger.Error("failed to scale application", "error", err)
			os.Exit(4)
		}
	}

	err = application.DeleteHelperApplication(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete helper application", "error", err)
		os.Exit(5)
	}

	gcpProject, err := resolved.ResolveGcpProject(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve GCP project ID", "error", err)
		os.Exit(6)
	}

	migrationName, err := resolved.MigrationName(cfg.SourceInstance.Name, cfg.TargetInstance.Name)
	if err != nil {
		mgr.Logger.Error("failed to resolve migration name", "error", err)
		os.Exit(7)
	}

	err = migration.DeleteMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete migration job", "error", err)
		os.Exit(8)
	}

	err = instance.CleanupConnectionProfiles(ctx, &cfg.Config, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to cleanup connection profiles", "error", err)
		os.Exit(9)
	}

	err = instance.DeleteInstance(ctx, cfg.TargetInstance.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target instance", "error", err)
		os.Exit(10)
	}

	masterInstanceName := fmt.Sprintf("%s-master", cfg.TargetInstance.Name)
	err = instance.DeleteInstance(ctx, masterInstanceName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete master instance", "error", err)
		os.Exit(11)
	}

	err = database.DeleteTargetDatabaseResource(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete target database resource", "error", err)
		os.Exit(12)
	}

	err = instance.DeleteSslCertByCommonName(ctx, cfg.SourceInstance.Name, cfg.ApplicationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete old ssl certificate", "error", err)
		os.Exit(13)
	}

	err = instance.WaitForSQLDatabaseResourceToGoAway(ctx, cfg.ApplicationName, mgr)
	if err != nil {
		mgr.Logger.Error("sqldatabase is stuck", "error", err)
		os.Exit(14)
	}

	app, err = application.UpdateApplicationInstance(ctx, &cfg.Config, &cfg.SourceInstance, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application", "error", err)
		os.Exit(15)
	}

	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("failed to resolve updated target", "error", err)
		os.Exit(16)
	}

	err = application.UpdateApplicationUser(ctx, source, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application user", "error", err)
		os.Exit(17)
	}

	mgr.Logger.Info("deleting SQL SSL Certificates used during migration")
	err = mgr.SqlSslCertClient.DeleteCollection(ctx, v1.ListOptions{
		LabelSelector: "migrator.nais.io/cleanup=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("failed to delete SQL SSL Certificates", "error", err)
		os.Exit(18)
	}

	mgr.Logger.Info("deleting Network Policy used during migration")
	err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).DeleteCollection(ctx, v1.DeleteOptions{}, v1.ListOptions{
		LabelSelector: "migrator.nais.io/cleanup=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("failed to delete Network Policy", "error", err)
		os.Exit(19)
	}
}
