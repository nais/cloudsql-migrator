package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"github.com/sethvargo/go-envconfig"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	cfg := config.FinalizeConfig{}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg.Config)
	mgr, err := common_main.Main(ctx, &cfg.Config, "finalize", logger)
	if err != nil {
		logger.Error("Failed to complete configuration", "error", err)
		os.Exit(2)
	}

	// The migrationStepsTotal must be updated if the number of steps in the finalize process changes
	// Used by nais-cli to show progressbar
	mgr.Logger.Info("Finalize started", "config", cfg, "migrationStepsTotal", 11)

	mgr.Logger.Info("Resolving GCP project ID", "migrationStep", 1)
	gcpProject, err := resolved.ResolveGcpProject(ctx, &cfg.Config, mgr)
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

	mgr.Logger.Info("Resolving target instance", "migrationStep", 3)
	target, err := resolved.ResolveInstance(ctx, app, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to resolve target", "error", err)
		os.Exit(5)
	}

	migrationName, err := resolved.MigrationName(cfg.SourceInstanceName, target.Name)
	if err != nil {
		mgr.Logger.Error("Failed to resolve migration name", "error", err)
		os.Exit(6)
	}

	mgr.Logger.Info("Deleting migration job", "migrationStep", 4)
	err = migration.DeleteMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete migration job", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("Cleaning up connection profiles", "migrationStep", 5)
	err = instance.CleanupConnectionProfiles(ctx, &cfg.Config, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to cleanup connection profiles", "error", err)
		os.Exit(8)
	}

	mgr.Logger.Info("Deleting master instance", "migrationStep", 6)
	masterInstanceName := fmt.Sprintf("%s-master", target.Name)
	err = instance.DeleteInstance(ctx, masterInstanceName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete master instance", "error", err)
		os.Exit(9)
	}

	mgr.Logger.Info("Deleting source instance", "migrationStep", 7)
	err = instance.DeleteInstance(ctx, cfg.SourceInstanceName, gcpProject, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to delete source instance", "error", err)
		os.Exit(10)
	}

	mgr.Logger.Info("Cleanup auth networks", "migrationStep", 8)
	err = instance.CleanupAuthNetworks(ctx, target, mgr)
	if err != nil {
		mgr.Logger.Error("Failed to cleanup authorized networks", "error", err)
		os.Exit(11)
	}

	mgr.Logger.Info("Deleting SQL SSL Certificates used during migration", "migrationStep", 9)
	err = mgr.SqlSslCertClient.DeleteCollection(ctx, v1.ListOptions{
		LabelSelector: "migrator.nais.io/finalize=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("Failed to delete SQL SSL Certificates", "error", err)
		os.Exit(12)
	}

	mgr.Logger.Info("Deleting Network Policy used during migration", "migrationStep", 10)
	err = mgr.K8sClient.NetworkingV1().NetworkPolicies(cfg.Namespace).DeleteCollection(ctx, v1.DeleteOptions{}, v1.ListOptions{
		LabelSelector: "migrator.nais.io/finalize=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("Failed to delete Network Policy", "error", err)
		os.Exit(13)
	}

	mgr.Logger.Info("Finalize completed", "migrationStep", 11)
}
