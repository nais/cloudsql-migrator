package instance

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"google.golang.org/api/sqladmin/v1"
	"time"
)

func CreateBackup(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("Creating backup")

	backupRunsService := mgr.SqlAdminService.BackupRuns
	operationsService := mgr.SqlAdminService.Operations

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	backupRun := &sqladmin.BackupRun{
		Description: "Pre-migration backup",
	}
	op, err := backupRunsService.Insert(mgr.Resolved.GcpProjectId, mgr.Resolved.InstanceName, backupRun).Context(ctx).Do()
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

	mgr.Logger.Info("Backup creation complete")

	return nil
}
