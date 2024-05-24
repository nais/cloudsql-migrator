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
	"time"
)

func PrepareSourceDatabase(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	databasePassword := makePassword(cfg)
	err := setDatabasePassword(ctx, mgr, mgr.Resolved.Source.Name, databasePassword, &mgr.Resolved.Source.PostgresPassword)
	if err != nil {
		return err
	}

	err = instance.CreateSslCert(ctx, cfg, mgr, mgr.Resolved.Source.Name, &mgr.Resolved.Source.SslCert)
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
	databasePassword := makePassword(cfg)
	err := setDatabasePassword(ctx, mgr, cfg.CommonConfig.TargetInstance.Name, databasePassword, &mgr.Resolved.Target.PostgresPassword)
	if err != nil {
		return err
	}

	err = instance.CreateSslCert(ctx, cfg, mgr, cfg.CommonConfig.TargetInstance.Name, &mgr.Resolved.Target.SslCert)

	return nil
}

func makePassword(cfg *setup.Config) string {
	if cfg.UnsafePassword {
		return "testpassword"
	}
	return rand.String(14)
}

func setDatabasePassword(ctx context.Context, mgr *common_main.Manager, instance string, password string, resolved *string) error {
	mgr.Logger.Info("updating Cloud SQL user password", "instance", instance)

	usersService := mgr.SqlAdminService.Users
	user, err := usersService.Get(mgr.Resolved.GcpProjectId, instance, config.PostgresDatabaseUser).Context(ctx).Do()
	if err != nil {
		return err
	}

	user.Password = password

	op, err := usersService.Update(mgr.Resolved.GcpProjectId, instance, user).Name(user.Name).Host(user.Host).Context(ctx).Do()
	if err != nil {
		mgr.Logger.Error("failed to update Cloud SQL user password", "error", err)
		return err
	}

	operationsService := mgr.SqlAdminService.Operations
	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = operationsService.Get(mgr.Resolved.GcpProjectId, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get update operation status: %w", err)
		}
	}

	*resolved = password

	return nil
}

func installExtension(ctx context.Context, mgr *common_main.Manager) error {
	logger := mgr.Logger.With("instance", mgr.Resolved.Source.Name)
	logger.Info("installing pglogical extension and adding grants")

	dbInfos := []struct {
		DatabaseName string
		Username     string
		Password     string
	}{
		{
			DatabaseName: config.PostgresDatabaseName,
			Username:     config.PostgresDatabaseUser,
			Password:     mgr.Resolved.Source.PostgresPassword,
		},
		{
			DatabaseName: mgr.Resolved.DatabaseName,
			Username:     mgr.Resolved.Source.AppUsername,
			Password:     mgr.Resolved.Source.AppPassword,
		},
	}

	for _, dbInfo := range dbInfos {
		connection := fmt.Sprint(
			" host="+mgr.Resolved.Source.Ip,
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
			logger.Error("failed to connect to database", "error", err)
			return err
		}

		logger.Info("installing extension and granting permissions to postgres user", "database", dbInfo.DatabaseName)

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
