package backup

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"google.golang.org/api/sqladmin/v1"
	"time"
)

func CreateBackup(ctx context.Context, cfg *setup.Config, app *common_main.App) error {
	logger := app.Logger.With("instance", cfg.InstanceName)
	logger.Info("Creating backup")

	sqladminService, err := sqladmin.NewService(ctx)
	if err != nil {
		return err
	}

	backupRunsService := sqladminService.BackupRuns
	operationsService := sqladminService.Operations

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	backupRun := &sqladmin.BackupRun{
		Description: "Pre-migration backup",
	}
	op, err := backupRunsService.Insert(cfg.GcpProjectId, cfg.InstanceName, backupRun).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = operationsService.Get(cfg.GcpProjectId, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get backup operation status: %w", err)
		}
	}

	logger.Info("Backup creation complete")

	return nil
}
