package promote

import (
	"context"
	"errors"
	"fmt"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/iterator"
)

func CheckReadyForPromotion(ctx context.Context, source, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	migrationName, err := resolved.MigrationName(source.Name, target.Name)
	if err != nil {
		return err
	}

	mgr.Logger.Info("checking if migration job is ready for promotion", "migrationName", migrationName)

	migrationJob, err := migration.GetMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		return err
	}

	if migrationJob.Phase == "PROMOTE_IN_PROGRESS" {
		mgr.Logger.Info("migration job is already under promotion, continuing...", "migrationName", migrationName)
		return nil
	}

	if migrationJob.State == "COMPLETED" {
		mgr.Logger.Info("migration job is already completed, continuing...", "migrationName", migrationName)
		return nil
	}

	if migrationJob.State != "RUNNING" {
		return fmt.Errorf("migration job is not running: %s", migrationJob.State)
	}

	if migrationJob.Phase != "CDC" && migrationJob.Phase != "READY_FOR_PROMOTE" {
		return fmt.Errorf("migration job is not ready for promotion: %s", migrationJob.Phase)
	}

	err = waitForReplicationLagToReachZero(ctx, target, gcpProject, mgr)
	if err != nil {
		return err
	}

	return nil
}

func Promote(ctx context.Context, source, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	migrationName, err := resolved.MigrationName(source.Name, target.Name)
	if err != nil {
		return err
	}

	mgr.Logger.Info("start promoting destination", "migrationName", migrationName)

	op, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Promote(gcpProject.GcpComponentURI("migrationJobs", migrationName), &datamigration.PromoteMigrationJobRequest{}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to promote target instance: %w", err)
	}

	for !op.Done {
		time.Sleep(10 * time.Second)
		mgr.Logger.Info("waiting for promote operation to complete")
		op, err = mgr.DatamigrationService.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get promote operation status: %w", err)
		}
	}

	err = instance.UpdateTargetInstanceAfterPromotion(ctx, target, mgr)
	if err != nil {
		return err
	}

	return nil
}

func waitForReplicationLagToReachZero(ctx context.Context, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	queryClient, err := monitoring.NewQueryClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create query client: %w", err)
	}
	defer queryClient.Close()

	req := &monitoringpb.QueryTimeSeriesRequest{
		Name: gcpProject.GcpParentURI(),
		Query: "fetch cloudsql_database\n" +
			"| metric\n" +
			"    'cloudsql.googleapis.com/database/postgresql/external_sync/max_replica_byte_lag'\n" +
			"| filter\n" +
			"    resource.region == 'europe-north1' && \n" +
			fmt.Sprintf("    resource.project_id == '%s' &&\n", gcpProject.Id) +
			fmt.Sprintf("    resource.database_id == '%s:%s'\n", gcpProject.Id, target.Name) +
			"| group_by [], mean(val())\n" +
			"| within 5m\n",
	}

	for {
		mgr.Logger.Info("checking replication lag")
		it := queryClient.QueryTimeSeries(ctx, req)

		var data *monitoringpb.TimeSeriesData
		data, err = it.Next()
		if err != nil {
			if !errors.Is(err, iterator.Done) {
				return fmt.Errorf("failed to fetch time series data: %w", err)
			}
			mgr.Logger.Info("no more data in iterator")
		} else {
			value := data.PointData[0].Values[0].GetInt64Value()
			if value == 0 {
				mgr.Logger.Info("replication lag reached zero")
				return nil
			}
			mgr.Logger.Info("replication lag still not zero", "value", value)
		}
		time.Sleep(30 * time.Second)
	}
}
