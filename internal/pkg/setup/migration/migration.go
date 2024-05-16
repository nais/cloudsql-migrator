package migration

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
)

func SetupMigration(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	err := instance.CreateConnectionProfiles(ctx, cfg, mgr)
	if err != nil {
		return err
	}

	//createMigrationJob()
	return nil
}

func createMigrationJob() {
	// Migrate the database
}
