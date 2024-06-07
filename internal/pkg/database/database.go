package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/sqladmin/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

func PrepareSourceDatabase(ctx context.Context, cfg *config.Config, source *resolved.Instance, databaseName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	databasePassword := makePassword(cfg, mgr.Logger)
	err := SetDatabasePassword(ctx, source.Name, config.PostgresDatabaseUser, databasePassword, gcpProject, mgr)
	if err != nil {
		return err
	}
	source.PostgresPassword = databasePassword

	certPaths, err := instance.CreateSslCert(ctx, cfg, mgr, source.Name, &source.SslCert)
	if err != nil {
		return err
	}

	err = installExtension(ctx, mgr, source, databaseName, certPaths)
	if err != nil {
		return err
	}

	return nil
}

func DeleteHelperTargetDatabase(ctx context.Context, cfg *config.Config, target *resolved.Instance, databaseName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	helperName, err := common_main.HelperName(cfg.ApplicationName)
	if err != nil {
		return err
	}

	mgr.Logger.Info("deleting kubernetes database resource for target instance")
	err = mgr.SqlDatabaseClient.DeleteCollection(ctx, metav1.ListOptions{
		LabelSelector: "app=" + helperName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete databases from target instance: %w", err)
	}

	mgr.Logger.Info("deleting database in target instance")
	op, err := mgr.SqlAdminService.Databases.Delete(gcpProject.Id, target.Name, databaseName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete database from target instance: %w", err)
	}

	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = mgr.SqlAdminService.Operations.Get(gcpProject.Id, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get delete operation status: %w", err)
		}
	}

	return nil
}

func DeleteTargetDatabaseResource(ctx context.Context, cfg *config.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("deleting kubernetes database resource for target instance")
	err := mgr.SqlDatabaseClient.DeleteCollection(ctx, metav1.ListOptions{
		LabelSelector: "app=" + cfg.ApplicationName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete databases from target instance: %w", err)
	}

	return nil
}

func PrepareTargetDatabase(ctx context.Context, cfg *config.Config, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	databasePassword := makePassword(cfg, mgr.Logger)
	err := SetDatabasePassword(ctx, cfg.TargetInstance.Name, config.PostgresDatabaseUser, databasePassword, gcpProject, mgr)
	if err != nil {
		return err
	}
	target.PostgresPassword = databasePassword

	_, err = instance.CreateSslCert(ctx, cfg, mgr, cfg.TargetInstance.Name, &target.SslCert)

	return nil
}

func makePassword(cfg *config.Config, logger *slog.Logger) string {
	if cfg.Development.UnsafePassword {
		logger.Warn("using unsafe password for database user because of development mode setting")
		return "testpassword"
	}
	return rand.String(14)
}

func SetDatabasePassword(ctx context.Context, instance string, userName string, password string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	mgr.Logger.Info("updating Cloud SQL user password", "instance", instance, "user", userName)

	usersService := mgr.SqlAdminService.Users

	updateCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var err error
	var op *sqladmin.Operation

	for {
		var user *sqladmin.User
		user, err = getSqlUser(updateCtx, instance, userName, gcpProject, mgr)
		if err != nil {
			return err
		}

		user.Password = password

		op, err = usersService.Update(gcpProject.Id, instance, user).Name(user.Name).Host(user.Host).Context(updateCtx).Do()
		if err != nil {
			var ae *googleapi.Error
			if errors.As(err, &ae) && ae.Code == http.StatusConflict {
				mgr.Logger.Info("user not found, retrying", "user", user.Name)
				time.Sleep(3 * time.Second)
				continue
			}
			return fmt.Errorf("failed to update Cloud SQL user password: %w", err)
		}
		break
	}

	operationsService := mgr.SqlAdminService.Operations
	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		op, err = operationsService.Get(gcpProject.Id, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get update operation status: %w", err)
		}
	}

	mgr.Logger.Info("updated Cloud SQL user password", "user", userName)

	return nil
}

func getSqlUser(ctx context.Context, instance string, userName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) (*sqladmin.User, error) {
	usersService := mgr.SqlAdminService.Users

	retries := 5
	for {
		user, err := usersService.Get(gcpProject.Id, instance, userName).Context(ctx).Do()
		if err != nil {
			var ae *googleapi.Error
			if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
				retries -= 1
				if retries > 0 {
					mgr.Logger.Info("user not found, retrying", "remaining_retries", retries)
					time.Sleep(3 * time.Second)
					continue
				}
				return nil, fmt.Errorf("failed to get Cloud SQL user, ran out of retries: %w", err)
			}
			return nil, err
		}
		return user, nil
	}
}

func installExtension(ctx context.Context, mgr *common_main.Manager, source *resolved.Instance, databaseName string, certPaths *instance.CertPaths) error {
	logger := mgr.Logger.With("instance", source.Name)
	logger.Info("installing pglogical extension and adding grants")

	dbInfos := []struct {
		DatabaseName string
		Username     string
		Password     string
	}{
		{
			DatabaseName: config.PostgresDatabaseName,
			Username:     config.PostgresDatabaseUser,
			Password:     source.PostgresPassword,
		},
		{
			DatabaseName: databaseName,
			Username:     source.AppUsername,
			Password:     source.AppPassword,
		},
	}

	for _, dbInfo := range dbInfos {
		logger.Info("connecting to database", "database", dbInfo.DatabaseName, "user", dbInfo.Username)
		dbConn, err := createConnection(
			source.PrimaryIp,
			dbInfo.Username,
			dbInfo.Password,
			dbInfo.DatabaseName,
			certPaths.RootCertPath,
			certPaths.KeyPath,
			certPaths.CertPath,
			logger,
		)
		if err != nil {
			return err
		}
		defer dbConn.Close()

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

func ChangeOwnership(ctx context.Context, mgr *common_main.Manager, target *resolved.Instance, databaseName string, certPaths *instance.CertPaths) error {
	logger := mgr.Logger

	dbConn, err := createConnection(
		target.PrimaryIp,
		target.AppUsername,
		target.AppPassword,
		databaseName,
		certPaths.RootCertPath,
		certPaths.KeyPath,
		certPaths.CertPath,
		logger,
	)
	if err != nil {
		return err
	}
	defer dbConn.Close()

	logger.Info("reassigning ownership from cloudsqlexternalsync to app user", "database", databaseName, "user", target.AppUsername)

	_, err = dbConn.ExecContext(ctx, "GRANT cloudsqlexternalsync to \""+target.AppUsername+"\";"+
		"REASSIGN OWNED BY cloudsqlexternalsync to \""+target.AppUsername+"\";")
	if err != nil {
		return err
	}

	return nil
}

func createConnection(instanceIp, username, password, databaseName, rootCertPath, keyPath, certPath string, logger *slog.Logger) (*sql.DB, error) {
	connection := fmt.Sprint(
		" host="+instanceIp,
		" port="+strconv.Itoa(config.DatabasePort),
		" user="+username,
		" password="+password,
		" dbname="+databaseName,
		" sslmode=verify-ca",
		" sslrootcert="+rootCertPath,
		" sslkey="+keyPath,
		" sslcert="+certPath,
	)

	dbConn, err := sql.Open(config.DatabaseDriver, connection)
	if err != nil {
		return nil, err
	}

	err = dbConn.Ping()
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		_ = dbConn.Close()
		return nil, err
	}
	return dbConn, err
}
