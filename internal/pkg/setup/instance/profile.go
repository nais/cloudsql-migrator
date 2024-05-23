package instance

import (
	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func CreateConnectionProfiles(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	cps := getDmsConnectionProfiles(cfg, mgr)
	for i, cp := range cps {
		profileName := fmt.Sprintf("%s-%s", i, cp.Name)

		mgr.Logger.Info("deleting previous connection profile", "name", profileName)
		deleteOperation, err := mgr.DBMigrationClient.DeleteConnectionProfile(ctx, &clouddmspb.DeleteConnectionProfileRequest{
			Name: fmt.Sprintf("projects/%s/locations/europe-north1/connectionProfiles/%s", mgr.Resolved.GcpProjectId, profileName),
		})
		if err != nil {
			if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
				return fmt.Errorf("unable to delete previous connection profile: %w", err)
			}
		} else {
			err = deleteOperation.Wait(ctx)
			if err != nil {
				return fmt.Errorf("failed to wait for connection profile deletion: %w", err)
			}
		}

		mgr.Logger.Info("creating connection profile", "name", profileName)
		createOperation, err := mgr.DBMigrationClient.CreateConnectionProfile(ctx, &clouddmspb.CreateConnectionProfileRequest{
			Parent:              fmt.Sprintf("projects/%s/locations/europe-north1", mgr.Resolved.GcpProjectId),
			ConnectionProfileId: profileName,
			ConnectionProfile:   cp,
		})

		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			mgr.Logger.Info("connection profile already exists, updating", "name", profileName)
			continue
		}

		if err != nil {
			mgr.Logger.Error("failed to create connection profile", "error", err)
			return err
		}

		_, err = createOperation.Wait(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for connection profile creation: %w", err)
		}

		mgr.Logger.Info("connection profile created", "name", profileName)
	}
	return nil
}

func getDmsConnectionProfiles(cfg *setup.Config, mgr *common_main.Manager) map[string]*clouddmspb.ConnectionProfile {
	cps := make(map[string]*clouddmspb.ConnectionProfile, 2)
	cps["source"] = &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     mgr.Resolved.SourceInstanceIp,
				Port:     config.DatabasePort,
				Username: config.PostgresDatabaseUser,
				Password: mgr.Resolved.SourcePostgresUserPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              clouddmspb.SslConfig_SERVER_CLIENT,
					ClientKey:         mgr.Resolved.SourceSslCert.SslClientKey,
					ClientCertificate: mgr.Resolved.SourceSslCert.SslClientCert,
					CaCertificate:     mgr.Resolved.SourceSslCert.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.SourceInstanceName,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: clouddmspb.DatabaseProvider_CLOUDSQL,
	}
	cps["target"] = &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     mgr.Resolved.TargetInstanceIp,
				Port:     config.DatabasePort,
				Username: config.PostgresDatabaseUser,
				Password: mgr.Resolved.TargetPostgresUserPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              clouddmspb.SslConfig_SERVER_CLIENT,
					ClientKey:         mgr.Resolved.TargetSslCert.SslClientKey,
					ClientCertificate: mgr.Resolved.TargetSslCert.SslClientCert,
					CaCertificate:     mgr.Resolved.TargetSslCert.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.TargetInstanceName,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: clouddmspb.DatabaseProvider_CLOUDSQL,
	}

	return cps
}
