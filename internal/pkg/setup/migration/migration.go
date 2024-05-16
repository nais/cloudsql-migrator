package migration

import (
	"context"
	"fmt"

	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func SetupMigration(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	err := createConnectionProfile(ctx, cfg, mgr)
	if err != nil {
		return err
	}
	//createMigrationJob()
	return nil
}

func createConnectionProfile(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	cp := getDmsConnectionProfile(cfg, mgr)
	op, err := mgr.DBMigrationClient.CreateConnectionProfile(ctx, &clouddmspb.CreateConnectionProfileRequest{
		Parent:              fmt.Sprintf("projects/%s/locations/europe-north1", mgr.Resolved.GcpProjectId),
		ConnectionProfileId: cp.Name,
		ConnectionProfile:   cp,
	})

	if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
		mgr.Logger.Info("connection profile already exists", "name", cp.Name)
		return nil
	}

	if err != nil {
		mgr.Logger.Error("failed to create connection profile", "error", err)
		return err
	}
	if op.Done() {
		mgr.Logger.Info("connection profile created", "name", cp.Name)
	}
	return nil
}

func createMigrationJob() {
	// Migrate the database
}

func getDmsConnectionProfile(cfg *setup.Config, mgr *common_main.Manager) *clouddmspb.ConnectionProfile {
	return &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     mgr.Resolved.SourceInstanceIp,
				Port:     5432,
				Username: "postgres",
				Password: mgr.Resolved.SourceDbPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              2,
					ClientKey:         mgr.Resolved.SourceSslCert.SslClientKey,
					ClientCertificate: mgr.Resolved.SourceSslCert.SslClientCert,
					CaCertificate:     mgr.Resolved.SourceSslCert.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.SourceInstanceName,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: 1,
	}
}
