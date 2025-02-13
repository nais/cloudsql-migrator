package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/sethvargo/go-retry"

	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"

	"cloud.google.com/go/clouddms/apiv1/clouddmspb"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func PrepareMigrationJob(ctx context.Context, cfg *config.Config, gcpProject *resolved.GcpProject, source *resolved.Instance, target *resolved.Instance, mgr *common_main.Manager) (string, error) {
	migrationName, err := resolved.MigrationName(source.Name, target.Name)
	if err != nil {
		return "", err
	}

	err = DeleteMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		return "", err
	}

	err = instance.CreateConnectionProfiles(ctx, cfg, gcpProject, source, target, mgr)
	if err != nil {
		return "", err
	}

	migrationJob, err := createMigrationJob(ctx, migrationName, cfg, gcpProject, mgr)
	if err != nil {
		return "", err
	}

	migrationJobName := migrationJob.Name
	err = demoteTargetInstance(ctx, migrationJobName, mgr)
	if err != nil {
		return "", err
	}

	return migrationJobName, err
}

func DeleteMigrationJob(ctx context.Context, migrationName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	mgr.Logger.Info("deleting previous migration job", "name", migrationName)

	b := retry.NewConstant(20 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		op, err := mgr.DBMigrationClient.DeleteMigrationJob(ctx, &clouddmspb.DeleteMigrationJobRequest{
			Name: gcpProject.GcpComponentURI("migrationJobs", migrationName),
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				return nil
			}
			mgr.Logger.Warn("failed to delete previous migration job, retrying", "error", err)
			return retry.RetryableError(fmt.Errorf("unable to delete previous migration job: %w", err))
		} else {
			err = op.Wait(ctx)
			if err != nil {
				mgr.Logger.Warn("failed to wait for deletion of previous migration job, retrying", "error", err)
				return retry.RetryableError(fmt.Errorf("failed to wait for migration job deletion: %w", err))
			}
		}

		mgr.Logger.Info("migration job deleted", "name", migrationName)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete migration job: %w", err)
	}
	return nil
}

func demoteTargetInstance(ctx context.Context, migrationJobName string, mgr *common_main.Manager) error {
	mgr.Logger.Info("demoting target instance")

	op, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.DemoteDestination(migrationJobName, &datamigration.DemoteDestinationRequest{}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to demote target instance: %w", err)
	}

	for !op.Done {
		time.Sleep(10 * time.Second)
		mgr.Logger.Info("waiting for demote operation to complete")
		op, err = mgr.DatamigrationService.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get demote operation status: %w", err)
		}
	}

	return nil
}

func createMigrationJob(ctx context.Context, migrationName string, cfg *config.Config, gcpProject *resolved.GcpProject, mgr *common_main.Manager) (*clouddmspb.MigrationJob, error) {
	mgr.Logger.Info("looking for existing migration job", "name", migrationName)
	migrationJob, err := mgr.DBMigrationClient.GetMigrationJob(ctx, &clouddmspb.GetMigrationJobRequest{
		Name: gcpProject.GcpComponentURI("migrationJobs", migrationName),
	})
	if err != nil {
		if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
			return nil, fmt.Errorf("error while trying to look for existing migration job: %w", err)
		}
	}

	if migrationJob != nil {
		mgr.Logger.Info("migration job already exists", "name", migrationJob.Name)
		return migrationJob, nil
	}

	req := &clouddmspb.CreateMigrationJobRequest{
		Parent:         gcpProject.GcpParentURI(),
		MigrationJobId: migrationName,
		MigrationJob: &clouddmspb.MigrationJob{
			DisplayName: migrationName,
			Labels: map[string]string{
				"app":  cfg.ApplicationName,
				"team": cfg.Namespace,
			},
			Type:         clouddmspb.MigrationJob_CONTINUOUS,
			Source:       gcpProject.GcpComponentURI("connectionProfiles", fmt.Sprintf("source-%s", cfg.ApplicationName)),
			Destination:  gcpProject.GcpComponentURI("connectionProfiles", fmt.Sprintf("target-%s", cfg.ApplicationName)),
			Connectivity: &clouddmspb.MigrationJob_StaticIpConnectivity{},
		},
		RequestId: "",
	}
	mgr.Logger.Info("creating new migration job", "name", migrationName)
	createOperation, err := mgr.DBMigrationClient.CreateMigrationJob(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("unable to create new migration job: %w", err)
	}

	mgr.Logger.Info("waiting for migration job creation", "name", migrationName)
	migrationJob, err = createOperation.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed waiting for migration job creation: %w", err)
	}

	mgr.Logger.Info("migration job created", "name", migrationJob.Name)
	return migrationJob, nil
}

func StartMigrationJob(ctx context.Context, migrationJobName string, mgr *common_main.Manager) error {
	logger := mgr.Logger.With("migrationJobName", migrationJobName)
	logger.Info("starting migration job")

	b := retry.NewConstant(20 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		startOperation, err := mgr.DBMigrationClient.StartMigrationJob(ctx, &clouddmspb.StartMigrationJobRequest{
			Name: migrationJobName,
		})
		if err != nil {
			return fmt.Errorf("failed to start migration job: %w", err)
		}

		logger.Info("waiting for migration job to start")
		_, err = startOperation.Wait(ctx)
		if err != nil {
			return retry.RetryableError(fmt.Errorf("failed waiting for migration job to start, retrying: %w", err))
		}

		return nil
	})
	if err != nil {
		return err
	}

	logger.Info("migration job started")
	return nil
}

func GetMigrationJob(ctx context.Context, migrationName string, gcpProject *resolved.GcpProject, mgr *common_main.Manager) (*datamigration.MigrationJob, error) {
	b := retry.NewConstant(20 * time.Second)
	b = retry.WithMaxDuration(5*time.Minute, b)

	migrationJob, err := retry.DoValue(ctx, b, func(ctx context.Context) (*datamigration.MigrationJob, error) {
		migrationJob, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Get(gcpProject.GcpComponentURI("migrationJobs", migrationName)).Context(ctx).Do()
		if err != nil {
			return nil, retry.RetryableError(fmt.Errorf("failed to get migration job: %w", err))
		}

		mgr.Logger.Info("got migration job", "name", migrationJob.Name)
		return migrationJob, err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get migration job: %w", err)
	}

	return migrationJob, nil
}
