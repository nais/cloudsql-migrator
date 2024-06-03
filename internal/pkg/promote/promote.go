package promote

import (
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"context"
	"errors"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/iterator"
	"time"
)

func CheckReadyForPromotion(ctx context.Context, mgr *common_main.Manager) error {
	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		return err
	}

	mgr.Logger.Info("checking if migration job is ready for promotion", "migrationName", migrationName)

	migrationJob, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Get(mgr.Resolved.GcpComponentURI("migrationJobs", migrationName)).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get migration job: %w", err)
	}

	if migrationJob.State != "RUNNING" {
		return fmt.Errorf("migration job is not running: %s", migrationJob.State)
	}

	if migrationJob.Phase != "CDC" && migrationJob.Phase != "READY_FOR_PROMOTE" {
		return fmt.Errorf("migration job is not ready for promotion: %s", migrationJob.Phase)
	}

	err = waitForReplicationLagToReachZero(ctx, mgr)
	if err != nil {
		return err
	}

	return nil
}

func Promote(ctx context.Context, mgr *common_main.Manager) error {
	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		return err
	}

	mgr.Logger.Info("start promoting destination", "migrationName", migrationName)

	op, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Promote(mgr.Resolved.GcpComponentURI("migrationJobs", migrationName), &datamigration.PromoteMigrationJobRequest{}).Context(ctx).Do()
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

	err = instance.UpdateTargetInstanceAfterPromotion(ctx, mgr)
	if err != nil {
		return err
	}

	return nil
}

func waitForReplicationLagToReachZero(ctx context.Context, mgr *common_main.Manager) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	queryClient, err := monitoring.NewQueryClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create query client: %w", err)
	}
	defer queryClient.Close()

	req := &monitoringpb.QueryTimeSeriesRequest{
		Name: mgr.Resolved.GcpParentURI(),
		Query: "fetch cloudsql_database\n" +
			"| metric\n" +
			"    'cloudsql.googleapis.com/database/postgresql/external_sync/max_replica_byte_lag'\n" +
			"| filter\n" +
			"    resource.region == 'europe-north1' && \n" +
			fmt.Sprintf("    resource.project_id == '%s' &&\n", mgr.Resolved.GcpProjectId) +
			fmt.Sprintf("    resource.database_id == '%s:%s'\n", mgr.Resolved.GcpProjectId, mgr.Resolved.Target.Name) +
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
			mgr.Logger.Debug("no more data in iterator")
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
