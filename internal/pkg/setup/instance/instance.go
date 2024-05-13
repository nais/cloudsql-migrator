package instance

import (
	"context"
	db "database/sql"
	"fmt"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/k8s/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-config-connector/pkg/clients/generated/apis/sql/v1beta1"
	_ "github.com/lib/pq"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	sql_cnrm_cloud_google_com_v1beta1 "github.com/nais/liberator/pkg/apis/sql.cnrm.cloud.google.com/v1beta1"
	"github.com/nais/liberator/pkg/namegen"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"time"
)

const (
	dummyAppImage = "europe-north1-docker.pkg.dev/nais-io/nais/images/kafka-debug:latest"
	certPath      = "/tmp/client.crt"
	keyPath       = "/tmp/client.key"
	rootCertPath  = "/tmp/root.crt"
)

func CreateInstance(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("Starting creation of target instance")

	app, err := mgr.AppClient.Get(ctx, cfg.ApplicationName)
	if err != nil {
		return err
	}

	newInstance, err := defineNewInstance(cfg, app)
	if err != nil {
		return err
	}

	helperName := namegen.PrefixedRandShortName("migrator", app.Name, 63)
	dummyApp := &nais_io_v1alpha1.Application{
		TypeMeta: app.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:      helperName,
			Namespace: cfg.Namespace,
			Labels: map[string]string{
				"app":                      app.Name,
				"team":                     cfg.Namespace,
				"migrator.nais.io/cleanup": app.Name,
			},
			Annotations: map[string]string{
				"migrator.nais.io/old-instance": cfg.InstanceName,
				"migrator.nais.io/new-instance": cfg.NewInstance.Name,
			},
		},
		Spec: nais_io_v1alpha1.ApplicationSpec{
			GCP: &nais_io_v1.GCP{
				SqlInstances: []nais_io_v1.CloudSqlInstance{*newInstance},
			},
			Image: dummyAppImage,
		},
	}

	_, err = mgr.AppClient.Create(ctx, dummyApp)
	if err != nil {
		return err
	}

	mgr.Logger.Info("Started creation of target instance", "helperApp", helperName)

	return nil
}

func defineNewInstance(cfg *setup.Config, app *nais_io_v1alpha1.Application) (*nais_io_v1.CloudSqlInstance, error) {
	oldInstance := app.Spec.GCP.SqlInstances[0]
	newInstance := oldInstance.DeepCopy()

	newInstance.Name = cfg.NewInstance.Name
	newInstance.CascadingDelete = false
	if cfg.NewInstance.Tier != "" {
		newInstance.Tier = cfg.NewInstance.Tier
	}
	if cfg.NewInstance.DiskSize != 0 {
		newInstance.DiskSize = cfg.NewInstance.DiskSize
	}
	if cfg.NewInstance.Type != "" {
		newInstance.Type = nais_io_v1.CloudSqlInstanceType(cfg.NewInstance.Type)
	} else {
		switch oldInstance.Type {
		case nais_io_v1.CloudSqlInstanceTypePostgres11:
			newInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres12
		case nais_io_v1.CloudSqlInstanceTypePostgres12:
			newInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres13
		case nais_io_v1.CloudSqlInstanceTypePostgres13:
			newInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres14
		case nais_io_v1.CloudSqlInstanceTypePostgres14:
			newInstance.Type = nais_io_v1.CloudSqlInstanceTypePostgres15
		default:
			return nil, fmt.Errorf("no valid target type for instance of type %v", oldInstance.Type)

		}
	}

	return newInstance, nil
}

func PrepareOldInstance(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("Preparing old instance for migration")

	sqlInstance, err := mgr.SqlInstanceClient.Get(ctx, cfg.InstanceName)
	if err != nil {
		return err
	}

	setFlag(sqlInstance, "cloudsql.enable_pglogical")
	setFlag(sqlInstance, "cloudsql.logical_decoding")

	_, err = mgr.SqlInstanceClient.Update(ctx, sqlInstance)
	if err != nil {
		return err
	}

	err = prepareOldDatabase(ctx, cfg, mgr)
	if err != nil {
		return err
	}
	mgr.Logger.Info("Old instance prepared for migration")
	return nil
}

type sqlDatabase struct {
	Name     string
	Port     int
	User     string
	Password string
}

func createSslCert(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) (*v1beta1.SQLSSLCert, error) {
	sqlSslCert, err := mgr.SqlSslCertClient.Create(ctx, &v1beta1.SQLSSLCert{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "sql.cnrm.cloud.google.com/v1beta1",
			Kind:       "SQLSSLCert",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "migrator",
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
				Name:      cfg.InstanceName,
				Namespace: cfg.Namespace,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return sqlSslCert, nil
}
func prepareOldDatabase(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	mgr.Logger.Info("Preparing old database for migration")

	sqlSslCert, err := createSslCert(ctx, cfg, mgr)

	for sqlSslCert.Status.Cert == nil || sqlSslCert.Status.PrivateKey == nil || sqlSslCert.Status.ServerCaCert == nil {
		time.Sleep(3 * time.Second)
		sqlSslCert, err = mgr.SqlSslCertClient.Get(ctx, sqlSslCert.Name)
		if err != nil {
			return err
		}
		mgr.Logger.Info("Waiting for SQLSSLCert to be ready")
	}

	err = createTempFiles(sqlSslCert.Status.Cert, sqlSslCert.Status.PrivateKey, sqlSslCert.Status.ServerCaCert)
	if err != nil {
		return err
	}
	connection := fmt.Sprint(
		" host=35.228.136.127",
		" port=5432",
		" user=postgres",
		" password=testpassword",
		" dbname=postgres",
		" sslmode=verify-ca",
		" sslrootcert="+rootCertPath,
		" sslkey="+keyPath,
		" sslcert="+certPath,
	)

	dbConn, err := db.Open("postgres", connection)
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
func setFlag(sqlInstance *sql_cnrm_cloud_google_com_v1beta1.SQLInstance, flagName string) {
	actualFlag := findFlag(sqlInstance.Spec.Settings.DatabaseFlags, flagName)
	if actualFlag == nil {
		sqlInstance.Spec.Settings.DatabaseFlags = append(sqlInstance.Spec.Settings.DatabaseFlags, sql_cnrm_cloud_google_com_v1beta1.SQLDatabaseFlag{
			Name:  flagName,
			Value: "on",
		})
	} else if actualFlag.Value != "on" {
		actualFlag.Value = "on"
	}
}

func findFlag(flags []sql_cnrm_cloud_google_com_v1beta1.SQLDatabaseFlag, key string) *sql_cnrm_cloud_google_com_v1beta1.SQLDatabaseFlag {
	for _, flag := range flags {
		if flag.Name == key {
			return &flag
		}
	}
	return nil
}
