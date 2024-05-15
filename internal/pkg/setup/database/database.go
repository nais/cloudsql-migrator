package database

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/k8s/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"github.com/nais/liberator/pkg/namegen"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"os"
	"time"
)

const (
	certPath     = "/tmp/client.crt"
	keyPath      = "/tmp/client.key"
	rootCertPath = "/tmp/root.crt"
	databaseName = "postgres"
	databaseUser = "postgres"
	databasePort = "5432"
)

func PrepareOldDatabase(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	databasePassword := rand.String(14)
	err := setDatabasePassword(ctx, mgr, databasePassword)
	if err != nil {
		return err
	}

	err = installExtension(ctx, cfg, mgr)
	if err != nil {
		return err
	}
	return nil
}

func setDatabasePassword(ctx context.Context, mgr *common_main.Manager, password string) error {
	usersService := mgr.SqlAdminService.Users
	user, err := usersService.Get(mgr.Resolved.GcpProjectId, mgr.Resolved.InstanceName, databaseUser).Context(ctx).Do()
	if err != nil {
		return err
	}
	user.Password = password

	// Using insert to update the password, as update doesn't work as it should
	_, err = usersService.Insert(mgr.Resolved.GcpProjectId, mgr.Resolved.InstanceName, user).Context(ctx).Do()
	if err != nil {
		mgr.Logger.Error("failed to update Cloud SQL user password", "error", err)
		return err
	}

	mgr.Resolved.DbPassword = password

	return nil
}

func createSslCert(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) (*v1beta1.SQLSSLCert, error) {
	helperName, err := namegen.ShortName(fmt.Sprintf("migrator-%s", cfg.ApplicationName), 63)
	if err != nil {
		return nil, err
	}

	sqlSslCert, err := mgr.SqlSslCertClient.Get(ctx, helperName)
	if errors.IsNotFound(err) {
		sqlSslCert, err = mgr.SqlSslCertClient.Create(ctx, &v1beta1.SQLSSLCert{
			TypeMeta: v1.TypeMeta{
				APIVersion: "sql.cnrm.cloud.google.com/v1beta1",
				Kind:       "SQLSSLCert",
			},
			ObjectMeta: v1.ObjectMeta{
				Name:      helperName,
				Namespace: cfg.Namespace,
				Labels: map[string]string{
					"app":                      cfg.ApplicationName,
					"team":                     cfg.Namespace,
					"migrator.nais.io/cleanup": cfg.ApplicationName,
				},
			},
			Spec: v1beta1.SQLSSLCertSpec{
				CommonName: "test",
				InstanceRef: v1alpha1.ResourceRef{
					Name:      mgr.Resolved.InstanceName,
					Namespace: cfg.Namespace,
				},
			},
		})
	}

	if err != nil {
		return nil, err
	}

	return sqlSslCert, nil
}

func installExtension(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("Preparing old database for migration")

	sqlSslCert, err := createSslCert(ctx, cfg, mgr)
	if err != nil {
		return err
	}

	for sqlSslCert.Status.Cert == nil || sqlSslCert.Status.PrivateKey == nil || sqlSslCert.Status.ServerCaCert == nil {
		time.Sleep(3 * time.Second)
		sqlSslCert, err = mgr.SqlSslCertClient.Get(ctx, sqlSslCert.Name)
		if err != nil {
			return err
		}
		mgr.Logger.Info("Waiting for SQLSSLCert to be ready")
	}

	mgr.Resolved.SslCaCert = *sqlSslCert.Status.ServerCaCert
	mgr.Resolved.SslClientCert = *sqlSslCert.Status.Cert
	mgr.Resolved.SslClientKey = *sqlSslCert.Status.PrivateKey

	err = createTempFiles(sqlSslCert.Status.Cert, sqlSslCert.Status.PrivateKey, sqlSslCert.Status.ServerCaCert)
	if err != nil {
		return err
	}

	connection := fmt.Sprint(
		" host="+mgr.Resolved.InstanceIp,
		" port="+databasePort,
		" user="+databaseUser,
		" password="+mgr.Resolved.DbPassword,
		" dbname="+databaseName,
		" sslmode=verify-ca",
		" sslrootcert="+rootCertPath,
		" sslkey="+keyPath,
		" sslcert="+certPath,
	)

	dbConn, err := sql.Open("postgres", connection)
	if err != nil {
		return err
	}
	defer dbConn.Close()

	err = dbConn.Ping()
	if err != nil {
		mgr.Logger.Error("Failed to connect to old database", "error", err)
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

func createTempFiles(cert, key, rootCert *string) error {
	err := os.WriteFile(certPath, []byte(*cert), 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(keyPath, []byte(*key), 0600)
	if err != nil {
		return err
	}

	err = os.WriteFile(rootCertPath, []byte(*rootCert), 0644)
	if err != nil {
		return err
	}

	return nil
}
