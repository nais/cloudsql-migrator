package migration

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"time"

	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func SetupMigration(ctx context.Context, cfg *config.CommonConfig, mgr *common_main.Manager) error {
	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		return err
	}

	err = deleteMigrationJob(ctx, migrationName, mgr)
	if err != nil {
	}

	err = instance.CreateConnectionProfiles(ctx, cfg, mgr)
	if err != nil {
		return err
	}

	migrationJob, err := createMigrationJob(ctx, migrationName, cfg, mgr)
	if err != nil {
		return err
	}

	err = demoteTargetInstance(ctx, migrationJob, mgr)
	if err != nil {
		return err
	}

	err = startMigrationJob(ctx, migrationJob, mgr)
	if err != nil {
		return err
	}

	return nil
}

func deleteMigrationJob(ctx context.Context, migrationName string, mgr *common_main.Manager) error {
	mgr.Logger.Info("deleting previous migration job", "name", migrationName)

	op, err := mgr.DBMigrationClient.DeleteMigrationJob(ctx, &clouddmspb.DeleteMigrationJobRequest{
		Name: mgr.Resolved.GcpComponentURI("migrationJobs", migrationName),
	})
	if err != nil {
		if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
			return fmt.Errorf("unable to delete previous migration job: %w", err)
		}
	} else {
		err = op.Wait(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for migration job deletion: %w", err)
		}
	}

	return nil
}

func demoteTargetInstance(ctx context.Context, migrationJob *clouddmspb.MigrationJob, mgr *common_main.Manager) error {
	mgr.Logger.Info("demoting target instance")

	op, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.DemoteDestination(migrationJob.Name, &datamigration.DemoteDestinationRequest{}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to demote target instance: %w", err)
	}

	for !op.Done {
		time.Sleep(1 * time.Second)
		mgr.Logger.Info("waiting for demote operation to complete")
		op, err = mgr.DatamigrationService.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get demote operation status: %w", err)
		}
	}

	return nil
}

func createMigrationJob(ctx context.Context, migrationName string, cfg *config.CommonConfig, mgr *common_main.Manager) (*clouddmspb.MigrationJob, error) {
	migrationJob, err := mgr.DBMigrationClient.GetMigrationJob(ctx, &clouddmspb.GetMigrationJobRequest{
		Name: mgr.Resolved.GcpComponentURI("migrationJobs", migrationName),
	})
	if err != nil {
		if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
			return nil, fmt.Errorf("unable to get any existing migration job: %w", err)
		}
	}

	if migrationJob != nil {
		mgr.Logger.Info("migration job already exists", "name", migrationJob.Name)
		return migrationJob, nil
	}

	req := &clouddmspb.CreateMigrationJobRequest{
		Parent:         mgr.Resolved.GcpParentURI(),
		MigrationJobId: migrationName,
		MigrationJob: &clouddmspb.MigrationJob{
			DisplayName: migrationName,
			Labels: map[string]string{
				"app":  cfg.ApplicationName,
				"team": cfg.Namespace,
			},
			Type:         clouddmspb.MigrationJob_CONTINUOUS,
			Source:       mgr.Resolved.GcpComponentURI("connectionProfiles", fmt.Sprintf("source-%s", cfg.ApplicationName)),
			Destination:  mgr.Resolved.GcpComponentURI("connectionProfiles", fmt.Sprintf("target-%s", cfg.ApplicationName)),
			Connectivity: &clouddmspb.MigrationJob_StaticIpConnectivity{},
		},
		RequestId: "",
	}
	mgr.Logger.Info("creating new migration job", "name", migrationName)
	createOperation, err := mgr.DBMigrationClient.CreateMigrationJob(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("unable to create new migration job: %w", err)
	}

	migrationJob, err = createOperation.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed waiting for migration job creation: %w", err)
	}

	mgr.Logger.Info("migration job created", "name", migrationJob.Name)
	return migrationJob, nil
}

func startMigrationJob(ctx context.Context, migrationJob *clouddmspb.MigrationJob, mgr *common_main.Manager) error {
	logger := mgr.Logger.With("migrationJob", migrationJob.Name)
	logger.Info("starting migration job")
	startOperation, err := mgr.DBMigrationClient.StartMigrationJob(ctx, &clouddmspb.StartMigrationJobRequest{
		Name: migrationJob.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to start migration job: %w", err)
	}

	logger.Info("waiting for migration job to start")
	migrationJob, err = startOperation.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed waiting for migration job to start: %w", err)
	}

	logger.Info("migration job started")

	return nil
}
