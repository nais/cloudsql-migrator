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
	"github.com/sethvargo/go-envconfig"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
)

func main() {
	ctx := context.Background()

	cfg := config.Config{}

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg)
	mgr, err := common_main.Main(ctx, &cfg, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("promote started", "config", cfg)

	// TODO: Put this somewhere sensible
	targetSqlInstance, err := mgr.SqlInstanceClient.Get(ctx, mgr.Resolved.Target.Name)
	if err != nil {
		if !errors.IsNotFound(err) {
			mgr.Logger.Error("unable to resolve target instance", "error", err)
			os.Exit(1)
		}
	}

	mgr.Resolved.Target.Ip = *targetSqlInstance.Status.PublicIpAddress

	err = application.ScaleApplication(ctx, &cfg, mgr, 0)
	if err != nil {
		mgr.Logger.Error("failed to scale application", "error", err)
		os.Exit(3)
	}

	// TODO: Check migration job phase

	err = promote.Promote(ctx, &cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to promote", "error", err)
		os.Exit(5)
	}

	err = setAppCredentials(ctx, mgr, &cfg)
	if err != nil {
		mgr.Logger.Error("failed to set application password", "error", err)
		os.Exit(6)
	}

	certPaths, err := instance.CreateSslCert(ctx, &cfg, mgr, mgr.Resolved.Target.Name, &mgr.Resolved.Target.SslCert)

	err = application.UpdateApplicationUser(ctx, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application user", "error", err)
		os.Exit(10)
	}

	err = database.ChangeOwnership(ctx, mgr, certPaths)
	if err != nil {
		mgr.Logger.Error("failed to change ownership", "error", err)
		os.Exit(7)
	}

	err = application.DeleteHelperApplication(ctx, &cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to delete helper application", "error", err)
		os.Exit(8)
	}

	err = application.UpdateApplicationInstance(ctx, &cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to update application", "error", err)
		os.Exit(9)
	}

	err = application.ScaleApplication(ctx, &cfg, mgr, 1)
	if err != nil {
		mgr.Logger.Error("failed to scale application", "error", err)
		os.Exit(11)
	}

	err = backup.CreateBackup(ctx, &cfg, mgr, mgr.Resolved.Target.Name)
	if err != nil {
		mgr.Logger.Error("Failed to create backup", "error", err)
		os.Exit(12)
	}
}

func setAppCredentials(ctx context.Context, mgr *common_main.Manager, cfg *config.Config) error {
	clientSet := mgr.K8sClient
	helperName, err := common_main.HelperAppName(cfg.ApplicationName)
	if err != nil {
		return err
	}
	secret, err := clientSet.CoreV1().Secrets(cfg.Namespace).Get(ctx, "google-sql-"+helperName, v1.GetOptions{})
	if err != nil {
		return err
	}

	err = mgr.Resolved.Target.ResolveAppPassword(secret)
	if err != nil {
		return err
	}

	err = mgr.Resolved.Target.ResolveAppUsername(secret)
	if err != nil {
		return err
	}

	return nil
}
