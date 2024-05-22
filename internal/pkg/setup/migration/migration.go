package migration

import (
	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config/setup"
	"github.com/nais/cloudsql-migrator/internal/pkg/setup/instance"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

func SetupMigration(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) error {
	err := instance.CreateConnectionProfiles(ctx, cfg, mgr)
	if err != nil {
		return err
	}

	migrationJob, err := createMigrationJob(ctx, cfg, mgr)
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

func demoteTargetInstance(ctx context.Context, migrationJob *clouddmspb.MigrationJob, mgr *common_main.Manager) error {
	mgr.Logger.Info("Demoting target instance")

	op, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.DemoteDestination(migrationJob.Name, &datamigration.DemoteDestinationRequest{}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to demote target instance: %w", err)
	}

	for !op.Done {
		time.Sleep(1 * time.Second)
		op, err = mgr.DatamigrationService.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get demote operation status: %w", err)
		}
	}

	return nil
}

func createMigrationJob(ctx context.Context, cfg *setup.Config, mgr *common_main.Manager) (*clouddmspb.MigrationJob, error) {
	name := fmt.Sprintf("%s-%s", mgr.Resolved.SourceInstanceName, mgr.Resolved.TargetInstanceName)

	migrationJob, err := mgr.DBMigrationClient.GetMigrationJob(ctx, &clouddmspb.GetMigrationJobRequest{
		Name: fmt.Sprintf("projects/%s/locations/europe-north1/migrationJobs/%s", mgr.Resolved.GcpProjectId, name),
	})
	if err != nil {
		if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
			return nil, err
		}
	}

	if migrationJob != nil {
		mgr.Logger.Info("migration job already exists", "name", migrationJob.Name)
		return migrationJob, nil
	}

	req := &clouddmspb.CreateMigrationJobRequest{
		Parent:         fmt.Sprintf("projects/%s/locations/europe-north1", mgr.Resolved.GcpProjectId),
		MigrationJobId: name,
		MigrationJob: &clouddmspb.MigrationJob{
			DisplayName: name,
			Labels: map[string]string{
				"app":  cfg.ApplicationName,
				"team": cfg.Namespace,
			},
			Type:         clouddmspb.MigrationJob_CONTINUOUS,
			Source:       fmt.Sprintf("projects/%s/locations/europe-north1/connectionProfiles/source-%s", mgr.Resolved.GcpProjectId, cfg.ApplicationName),
			Destination:  fmt.Sprintf("projects/%s/locations/europe-north1/connectionProfiles/target-%s", mgr.Resolved.GcpProjectId, cfg.ApplicationName),
			Connectivity: &clouddmspb.MigrationJob_StaticIpConnectivity{},
		},
		RequestId: "",
	}
	createOperation, err := mgr.DBMigrationClient.CreateMigrationJob(ctx, req)
	if err != nil {
		return nil, err
	}

	migrationJob, err = createOperation.Wait(ctx)
	if err != nil {
		return nil, err
	}

	mgr.Logger.Info("migration job created", "name", migrationJob.Name)
	return migrationJob, err

}

func startMigrationJob(ctx context.Context, migrationJob *clouddmspb.MigrationJob, mgr *common_main.Manager) error {
	logger := mgr.Logger.With("migrationJob", migrationJob.Name)
	logger.Info("Starting migration job")
	startOperation, err := mgr.DBMigrationClient.StartMigrationJob(ctx, &clouddmspb.StartMigrationJobRequest{
		Name: migrationJob.Name,
	})
	if err != nil {
		return err
	}

	logger.Info("waiting for migration job to start")
	migrationJob, err = startOperation.Wait(ctx)
	if err != nil {
		return err
	}

	logger.Info("migration job started")

	return nil
}
