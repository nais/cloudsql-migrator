package instance

import (
	"context"
	"fmt"
	"github.com/sethvargo/go-retry"
	"time"

	clouddms "cloud.google.com/go/clouddms/apiv1"
	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func CreateConnectionProfiles(ctx context.Context, cfg *config.Config, gcpProject *resolved.GcpProject, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) error {
	cps := getDmsConnectionProfiles(cfg, source, target)

	err := deleteOldConnectionProfiles(ctx, cps, gcpProject, mgr)
	if err != nil {
		return err
	}

	err = createConnectionProfiles(ctx, cps, gcpProject, mgr)
	if err != nil {
		return err
	}

	return nil
}

func CleanupConnectionProfiles(ctx context.Context, cfg *config.Config, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	for _, prefix := range []string{"source", "target"} {
		profileName := fmt.Sprintf("%s-%s", prefix, cfg.ApplicationName)
		_, err := deleteConnectionProfile(ctx, profileName, gcpProject, mgr)
		if err != nil {
			return err
		}
	}

	return nil
}

func createConnectionProfiles(ctx context.Context, cps map[string]*clouddmspb.ConnectionProfile, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	createOperations := make([]*clouddms.CreateConnectionProfileOperation, 0, 2)
	for i, cp := range cps {
		profileName := fmt.Sprintf("%s-%s", i, cp.Name)

		mgr.Logger.Info("creating connection profile", "name", profileName)
		createOperation, err := mgr.DBMigrationClient.CreateConnectionProfile(ctx, &clouddmspb.CreateConnectionProfileRequest{
			Parent:              gcpProject.GcpParentURI(),
			ConnectionProfileId: profileName,
			ConnectionProfile:   cp,
		})

		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			mgr.Logger.Info("connection profile already exists, updating", "name", profileName)
			continue
		}

		if err != nil {
			mgr.Logger.Error("failed to create connection profile", "name", profileName, "error", err)
			return err
		}

		createOperations = append(createOperations, createOperation)
	}

	for _, createOperation := range createOperations {
		cp, err := createOperation.Wait(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for connection profile creation: %w", err)
		}
		mgr.Logger.Info("connection profile created", "name", cp.Name)
	}
	return nil
}

func deleteOldConnectionProfiles(ctx context.Context, cps map[string]*clouddmspb.ConnectionProfile, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	deleteOperations := make([]*clouddms.DeleteConnectionProfileOperation, 0, 2)
	for i, cp := range cps {
		profileName := fmt.Sprintf("%s-%s", i, cp.Name)

		deleteOperation, err := deleteConnectionProfile(ctx, profileName, gcpProject, mgr)
		if err != nil {
			return fmt.Errorf("failed to delete connection profile: %w", err)
		}

		if deleteOperation != nil {
			deleteOperations = append(deleteOperations, deleteOperation)
		}
	}

	for _, deleteOperation := range deleteOperations {
		mgr.Logger.Info("waiting for connection profile deletion...", "name", deleteOperation.Name())
		err := deleteOperation.Wait(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for connection profile deletion: %w", err)
		}
	}
	return nil
}

func deleteConnectionProfile(ctx context.Context, profileName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) (*clouddms.DeleteConnectionProfileOperation, error) {
	mgr.Logger.Info("deleting connection profile", "name", profileName)

	b := retry.NewConstant(5 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	deleteOperation, err := retry.DoValue(ctx, b, func(ctx context.Context) (*clouddms.DeleteConnectionProfileOperation, error) {
		deleteOperation, err := mgr.DBMigrationClient.DeleteConnectionProfile(ctx, &clouddmspb.DeleteConnectionProfileRequest{
			Name: gcpProject.GcpComponentURI("connectionProfiles", profileName),
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				return nil, nil
			}
			return nil, retry.RetryableError(fmt.Errorf("unable to delete connection profile, retrying: %w", err))
		}
		return deleteOperation, nil
	})

	if err != nil {
		mgr.Logger.Error("failed to delete connection profile", "error", err)
	} else {
		mgr.Logger.Info("connection profile deleted", "name", profileName)
	}
	return deleteOperation, err
}

func getDmsConnectionProfiles(cfg *config.Config, source *resolved.Instance, target *resolved.Instance) map[string]*clouddmspb.ConnectionProfile {
	cps := make(map[string]*clouddmspb.ConnectionProfile, 2)
	cps["source"] = connectionProfile(cfg, source)
	cps["target"] = connectionProfile(cfg, target)

	return cps
}

func connectionProfile(cfg *config.Config, instance *resolved.Instance) *clouddmspb.ConnectionProfile {
	return &clouddmspb.ConnectionProfile{
		Name: cfg.ApplicationName,
		ConnectionProfile: &clouddmspb.ConnectionProfile_Postgresql{
			Postgresql: &clouddmspb.PostgreSqlConnectionProfile{
				Host:     instance.PrimaryIp,
				Port:     config.DatabasePort,
				Username: config.PostgresDatabaseUser,
				Password: instance.PostgresPassword,
				Ssl: &clouddmspb.SslConfig{
					Type:              clouddmspb.SslConfig_SERVER_CLIENT,
					ClientKey:         instance.SslCert.SslClientKey,
					ClientCertificate: instance.SslCert.SslClientCert,
					CaCertificate:     instance.SslCert.SslCaCert,
				},
				CloudSqlId:   instance.Name,
				Connectivity: &clouddmspb.PostgreSqlConnectionProfile_StaticIpConnectivity{},
			},
		},
		Provider: clouddmspb.DatabaseProvider_CLOUDSQL,
	}
}
