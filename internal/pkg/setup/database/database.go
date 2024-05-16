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
	err := setDatabasePassword(ctx, mgr, mgr.Resolved.SourceInstanceName, databasePassword, &mgr.Resolved.SourceDbPassword)
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
	err := setDatabasePassword(ctx, mgr, cfg.CommonConfig.TargetInstance.Name, databasePassword, &mgr.Resolved.TargetDbPassword)
	if err != nil {
		return err
	}

	err = instance.CreateSslCert(ctx, cfg, mgr, cfg.CommonConfig.TargetInstance.Name, &mgr.Resolved.TargetSslCert)

	return nil
}

func setDatabasePassword(ctx context.Context, mgr *common_main.Manager, instance string, password string, resolved *string) error {
	usersService := mgr.SqlAdminService.Users
	user, err := usersService.Get(mgr.Resolved.GcpProjectId, instance, config.DatabaseUser).Context(ctx).Do()
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

	connection := fmt.Sprint(
		" host="+mgr.Resolved.SourceInstanceIp,
		" port="+strconv.Itoa(config.DatabasePort),
		" user="+config.DatabaseUser,
		" password="+mgr.Resolved.SourceDbPassword,
		" dbname="+config.DatabaseName,
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

	return nil
}
