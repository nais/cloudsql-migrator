package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/sethvargo/go-envconfig"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
)

func main() {
	cfg := config.CleanupConfig{}

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

	// TODO: Refactor resolvers to fit each phase
	// Must overwrite this, since the resolved value points to the new instance now
	mgr.Resolved.Source.Name = cfg.OldInstanceName
	masterInstanceName := fmt.Sprintf("%s-master", mgr.Resolved.Target.Name)

	mgr.Logger.Info("cleanup started", "config", cfg)

	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		mgr.Logger.Error("failed to resolve migration name", "error", err)
		os.Exit(3)
	}

	err = migration.DeleteMigrationJob(ctx, migrationName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete migration job", "error", err)
		os.Exit(4)
	}

	err = instance.CleanupConnectionProfiles(ctx, &cfg.Config, mgr)
	if err != nil {
		mgr.Logger.Error("failed to cleanup connection profiles", "error", err)
		os.Exit(5)
	}

	err = instance.DeleteInstance(ctx, masterInstanceName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete master instance", "error", err)
		os.Exit(6)
	}

	err = instance.DeleteInstance(ctx, cfg.OldInstanceName, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete old instance", "error", err)
		os.Exit(7)
	}

	mgr.Logger.Info("deleting SQL SSL Certificates used during migration")
	err = mgr.SqlSslCertClient.DeleteCollection(ctx, v1.ListOptions{
		LabelSelector: "migrator.nais.io/cleanup=" + cfg.ApplicationName,
	})
	if err != nil {
		mgr.Logger.Error("failed to delete SQL SSL Certificates", "error", err)
		os.Exit(8)
	}
}
