package instance

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"google.golang.org/api/sqladmin/v1"
	"time"
)

func CreateBackup(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager, name string) error {
	if cfg.Development.SkipBackup {
		mgr.Logger.Warn("skipping backup creation because of development mode setting")
		return nil
	}
	mgr.Logger.Info("creating backup")

	backupRunsService := mgr.SqlAdminService.BackupRuns
	operationsService := mgr.SqlAdminService.Operations

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	backupRun := &sqladmin.BackupRun{
		Description: "Pre-migration backup",
	}
	op, err := backupRunsService.Insert(mgr.Resolved.GcpProjectId, name, backupRun).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = operationsService.Get(mgr.Resolved.GcpProjectId, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get backup operation status: %w", err)
		}
	}

	mgr.Logger.Info("backup creation complete")

	return nil
}
