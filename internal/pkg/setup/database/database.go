package database

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"k8s.io/apimachinery/pkg/util/rand"
	"strconv"
)

func PrepareSourceDatabase(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	databasePassword := rand.String(14)
	err := setDatabasePassword(ctx, mgr, mgr.Resolved.SourceInstanceName, databasePassword, &mgr.Resolved.SourcePostgresUserPassword)
	if err != nil {
		return err
	}

	err = instance.CreateSslCert(ctx, cfg, mgr, mgr.Resolved.SourceInstanceName, &mgr.Resolved.SourceSslCert)
	if err != nil {
		return err
	}

	err = installExtension(ctx, mgr)
	if err != nil {
		return err
	}

	return nil
}

func PrepareTargetDatabase(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	databasePassword := rand.String(14)
	err := setDatabasePassword(ctx, mgr, cfg.CommonConfig.TargetInstance.Name, databasePassword, &mgr.Resolved.TargetPostgresUserPassword)
	if err != nil {
		return err
	}

	err = instance.CreateSslCert(ctx, cfg, mgr, cfg.CommonConfig.TargetInstance.Name, &mgr.Resolved.TargetSslCert)

	return nil
}

func setDatabasePassword(ctx context.Context, mgr *common_main.Manager, instance string, password string, resolved *string) error {
	usersService := mgr.SqlAdminService.Users
	user, err := usersService.Get(mgr.Resolved.GcpProjectId, instance, config.PostgresDatabaseUser).Context(ctx).Do()
	if err != nil {
		return err
	}
	user.Password = password

	// Using insert to update the password, as update doesn't work as it should
	_, err = usersService.Insert(mgr.Resolved.GcpProjectId, instance, user).Context(ctx).Do()
	if err != nil {
		mgr.Logger.Error("failed to update Cloud SQL user password", "error", err)
		return err
	}

	*resolved = password

	return nil
}

func installExtension(ctx context.Context, mgr *common_main.Manager) error {
	mgr.Logger.Info("preparing database for migration")

	dbInfos := []struct {
		DatabaseName string
		Username     string
		Password     string
	}{
		{
			DatabaseName: config.PostgresDatabaseName,
			Username:     config.PostgresDatabaseUser,
			Password:     mgr.Resolved.SourcePostgresUserPassword,
		},
		{
			DatabaseName: mgr.Resolved.DatabaseName,
			Username:     mgr.Resolved.SourceAppUsername,
			Password:     mgr.Resolved.SourceAppPassword,
		},
	}

	for _, dbInfo := range dbInfos {
		connection := fmt.Sprint(
			" host="+mgr.Resolved.SourceInstanceIp,
			" port="+strconv.Itoa(config.DatabasePort),
			" user="+dbInfo.Username,
			" password="+dbInfo.Password,
			" dbname="+dbInfo.DatabaseName,
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
			mgr.Logger.Error("failed to connect to database", "error", err)
			return err
		}

		mgr.Logger.Info("Granting permissions to postgres user", "database", dbInfo.DatabaseName)

		_, err = dbConn.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS pglogical; "+
			"GRANT USAGE on SCHEMA pglogical to \"postgres\";"+
			"GRANT SELECT on ALL TABLES in SCHEMA pglogical to \"postgres\";"+
			"GRANT SELECT on ALL SEQUENCES in SCHEMA pglogical to \"postgres\";"+
			"GRANT USAGE on SCHEMA public to \"postgres\";"+
			"GRANT SELECT on ALL TABLES in SCHEMA public to \"postgres\";"+
			"GRANT SELECT on ALL SEQUENCES in SCHEMA public to \"postgres\";"+
			"ALTER USER \"postgres\" with REPLICATION;")
		if err != nil {
			return err
		}
	}

	return nil
}
