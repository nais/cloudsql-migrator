package promote

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"

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

	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to create metric client: %v", err))
	}
	defer client.Close()

	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)

	// TODO: Resolve the region dynamically
	req := &monpb.ListTimeSeriesRequest{
		Name:   "projects/" + gcpProject.Id,
		Filter: fmt.Sprintf(`metric.type="cloudsql.googleapis.com/database/postgresql/external_sync/max_replica_byte_lag" AND resource.labels.region="europe-north1" AND resource.labels.project_id="%s" AND resource.labels.database_id="%s:%s"`, gcpProject.Id, gcpProject.Id, target.Name),
		Interval: &monpb.TimeInterval{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
		View: monpb.ListTimeSeriesRequest_FULL,
		Aggregation: &monpb.Aggregation{
			AlignmentPeriod:  durationpb.New(60 * time.Second), // 5 min
			PerSeriesAligner: monpb.Aggregation_ALIGN_MAX,
		},
	}

	for {
		mgr.Logger.Info("checking replication lag")
		it := client.ListTimeSeries(ctx, req)
		replicationLagZero := true
		data, err := it.Next()
		if err != nil {
			if !errors.Is(err, iterator.Done) {
				return fmt.Errorf("failed to fetch time series data: %w", err)
			}
			mgr.Logger.Info("no more data in iterator")
		} else {
			for _, point := range data.Points {
				switch value := point.GetValue().GetValue().(type) {
				case *monpb.TypedValue_Int64Value:
					if value.Int64Value != 0 {
						replicationLagZero = false
						time.Sleep(30 * time.Second)
					}
				default:
					return fmt.Errorf("unknown type from cloud monitoring: %T", value)
				}
			}
			if replicationLagZero {
				mgr.Logger.Info("replication lag reached zero")
				return nil
			}
		}
	}
}
