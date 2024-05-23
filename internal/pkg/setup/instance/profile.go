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
			Name: mgr.Resolved.GcpComponentURI("connectionProfiles", profileName),
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
			Parent:              mgr.Resolved.GcpParentURI(),
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
				Host:     mgr.Resolved.Source.Ip,
				Port:     config.DatabasePort,
				Username: config.PostgresDatabaseUser,
				Password: mgr.Resolved.Source.PostgresPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              clouddmspb.SslConfig_SERVER_CLIENT,
					ClientKey:         mgr.Resolved.Source.SslCert.SslClientKey,
					ClientCertificate: mgr.Resolved.Source.SslCert.SslClientCert,
					CaCertificate:     mgr.Resolved.Source.SslCert.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.Source.Name,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: clouddmspb.DatabaseProvider_CLOUDSQL,
	}
	cps["target"] = &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     mgr.Resolved.Target.Ip,
				Port:     config.DatabasePort,
				Username: config.PostgresDatabaseUser,
				Password: mgr.Resolved.Target.PostgresPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              clouddmspb.SslConfig_SERVER_CLIENT,
					ClientKey:         mgr.Resolved.Target.SslCert.SslClientKey,
					ClientCertificate: mgr.Resolved.Target.SslCert.SslClientCert,
					CaCertificate:     mgr.Resolved.Target.SslCert.SslCaCert,
				},
				CloudSqlId:   mgr.Resolved.Target.Name,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: clouddmspb.DatabaseProvider_CLOUDSQL,
	}

	return cps
}
