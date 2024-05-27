package promote

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"strconv"
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

func ChangeOwnership(ctx context.Context, mgr *common_main.Manager) error {
	logger := mgr.Logger

	connection := fmt.Sprint(
		" host="+mgr.Resolved.Source.Ip,
		" port="+strconv.Itoa(config.DatabasePort),
		" user="+mgr.Resolved.Target.AppUsername,
		" password="+mgr.Resolved.Target.AppPassword,
		" dbname="+mgr.Resolved.DatabaseName,
		" sslmode=verify-ca",
		" sslrootcert="+instance.RootCertPath,
		" sslkey="+instance.KeyPath,
		" sslcert="+instance.CertPath,
	)

	dbConn, err := sql.Open(config.DatabaseDriver, connection)
	if err != nil {
		return err
	}
	defer dbConn.Close()

	err = dbConn.Ping()
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		return err
	}

	logger.Info("installing extension and granting permissions to postgres user", "database", mgr.Resolved.DatabaseName)

	_, err = dbConn.ExecContext(ctx, "GRANT cloudsqlexternalsync to \""+mgr.Resolved.Target.AppUsername+"\""+
		"REASSIGN OWNED BY cloudsqlexternalsync to \""+mgr.Resolved.Target.AppUsername+"\";"+
		"DROP ROLE cloudsqlexternalsync;")
	if err != nil {
		return err
	}

	return nil
}
