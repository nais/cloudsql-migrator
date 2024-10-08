package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nais/cloudsql-migrator/internal/pkg/application"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/database"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		logger.Error("Failed to complete configuration", "error", err)
		os.Exit(2)
	}

	// The migrationStepsTotal must be updated if the number of steps in the rollback process changes
	// Used by nais-cli to show progressbar
	mgr.Logger.Info("Rollback started", "config", cfg, "migrationStepsTotal", 17)

	mgr.Logger.Info("Getting application", "name", cfg.ApplicationName, "migrationStep", 1)
	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		mgr.Logger.Error("Failed to get application", "error", err)
		os.Exit(3)
	}

	if app.Spec.GCP.SqlInstances[0].Name != cfg.SourceInstance.Name {
		// We only need to scale down if we are making changes to the instance the application currently uses
		mgr.Logger.Info("Scaling down application", "migrationStep", 2)
		err = application.ScaleApplication(ctx, &cfg.Config, mgr, 0)
		if err != nil {
			mgr.Logger.Error("Failed to scale application", "error", err)
			os.Exit(4)
		}
	}

	mgr.Logger.Info("Deleting helper application", "migrationStep", 3)
	err = application.DeleteHelperApplication(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete helper application", "error", err)
		os.Exit(5)
	}

	mgr.Logger.Info("Resolving GCP project ID", "migrationStep", 4)
	gcpProject, err := resolved.ResolveGcpProject(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve GCP project ID", "error", err)
		os.Exit(6)
	}

	migrationName, err := resolved.MigrationName(cfg.SourceInstance.Name, cfg.TargetInstance.Name)
	if err != nil {
		mgr.Logger.Error("Failed to resolve migration name", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("Deleting migration job", "migrationStep", 5)
	err = migration.DeleteMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete migration job", "error", err)
		os.Exit(8)
	}

	mgr.Logger.Info("Cleaning up connection profiles", "migrationStep", 6)
	err = instance.CleanupConnectionProfiles(ctx, &cfg.Config, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to cleanup connection profiles", "error", err)
		os.Exit(9)
	}

	mgr.Logger.Info("Deleting target instance", "migrationStep", 7)
	err = instance.DeleteInstance(ctx, cfg.TargetInstance.Name, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete target instance", "error", err)
		os.Exit(10)
	}

	mgr.Logger.Info("Deleting master instance", "migrationStep", 8)
	masterInstanceName := fmt.Sprintf("%s-master", cfg.TargetInstance.Name)
	err = instance.DeleteInstance(ctx, masterInstanceName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete master instance", "error", err)
		os.Exit(11)
	}

	mgr.Logger.Info("Deleting target database resource", "migrationStep", 9)
	err = database.DeleteTargetDatabaseResource(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete target database resource", "error", err)
		os.Exit(12)
	}

	mgr.Logger.Info("Deleting old ssl certificate", "migrationStep", 10)
	err = instance.DeleteSslCertByCommonName(ctx, cfg.SourceInstance.Name, cfg.ApplicationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete old ssl certificate", "error", err)
		os.Exit(13)
	}

	mgr.Logger.Info("Waiting for sqldatabase resource to go away", "migrationStep", 11)
	err = instance.WaitForSQLDatabaseResourceToGoAway(ctx, cfg.ApplicationName, mgr)
	if err != nil {
		mgr.Logger.Error("Sqldatabase is stuck", "error", err)
		os.Exit(14)
	}

	mgr.Logger.Info("Updating application", "migrationStep", 12)
	app, err = application.UpdateApplicationInstance(ctx, &cfg.Config, &cfg.SourceInstance, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to update application", "error", err)
		os.Exit(15)
	}

	mgr.Logger.Info("Resolving updated target", "migrationStep", 13)
	source, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve updated target", "error", err)
		os.Exit(16)
	}

	mgr.Logger.Info("Updating application user", "migrationStep", 14)
	err = application.UpdateApplicationUser(ctx, source, gcpProject, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to update application user", "error", err)
		os.Exit(17)
	}

	mgr.Logger.Info("Deleting SQL SSL Certificates used during migration", "migrationStep", 15)
	err = mgr.SqlSslCertClient.DeleteCollection(ctx, v1.ListOptions{
		LabelSelector: "migrator.nais.io/finalize=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("Failed to delete SQL SSL Certificates", "error", err)
		os.Exit(18)
	}

	mgr.Logger.Info("Deleting Network Policy used during migration", "migrationStep", 16)
	err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).DeleteCollection(ctx, v1.DeleteOptions{}, v1.ListOptions{
		LabelSelector: "migrator.nais.io/finalize=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("Failed to delete Network Policy", "error", err)
		os.Exit(19)
	}

	mgr.Logger.Info("Rollback completed", "migrationStep", 17)
}
