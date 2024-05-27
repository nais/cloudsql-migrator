package promote

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
)

func Promote(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		return err
	}

	migrationJob, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Get(migrationName).Context(ctx).Do()
	if err != nil {
		return err
	}

	// Should verify migration lag is zero

	mgr.Logger.Info("start promoting destination", "job", migrationJob)
	return nil

}
