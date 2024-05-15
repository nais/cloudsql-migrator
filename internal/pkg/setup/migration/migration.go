package migration

import (
	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
)

func SetupMigration(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	cp := &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     mgr.Resolved.InstanceIp,
				Port:     5432,
				Username: "postgres",
				Password: mgr.Resolved.DbPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              2,
					ClientKey:         mgr.Resolved.SslClientKey,
					ClientCertificate: mgr.Resolved.SslClientCert,
					CaCertificate:     mgr.Resolved.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.InstanceName,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: 1,
	}
	err := createConnectionProfile(ctx, mgr, cp)
	if err != nil {
		return err
	}
	//createMigrationJob()
	return nil
}

func createConnectionProfile(ctx context.Context, mgr *common_main.Manager, cp *clouddmspb.ConnectionProfile) error {
	op, err := mgr.DBMigrationClient.CreateConnectionProfile(ctx, &clouddmspb.CreateConnectionProfileRequest{
		Parent:              fmt.Sprintf("projects/%s/locations/europe-north1", mgr.Resolved.GcpProjectId),
		ConnectionProfileId: cp.Name,
		ConnectionProfile:   cp,
	})
	if err != nil {
		mgr.Logger.Error("Failed to create connection profile", "error", err)
		return err
	}
	if op.Done() {
		mgr.Logger.Info("Connection profile created", "name", cp.Name)
	}
	return nil
}

func createMigrationJob() {
	// Migrate the database
}
