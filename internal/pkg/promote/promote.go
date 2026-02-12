package promote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sethvargo/go-retry"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"

	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/instance"
	"github.com/nais/cloudsql-migrator/internal/pkg/migration"
	"github.com/nais/cloudsql-migrator/internal/pkg/resolved"
	"google.golang.org/api/datamigration/v1"
	"google.golang.org/api/iterator"
)

const (
	numberOfZeroPointsForLagToBeConsideredZero = 3
	acceptableLagBytesForPromotion             = 16 * 1024 * 1024
)

type ReplicationLagPredicate func([]*monpb.Point, *slog.Logger) (bool, error)

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

	return waitForReplicationLagToBeAcceptablyLow(ctx, target, gcpProject, mgr)
}

func Promote(ctx context.Context, source, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	migrationName, err := resolved.MigrationName(source.Name, target.Name)
	if err != nil {
		return err
	}

	mgr.Logger.Info("checking status of migration job", "migrationName", migrationName)

	migrationJob, err := migration.GetMigrationJob(ctx, migrationName, gcpProject, mgr)
	if err != nil {
		return err
	}

	var op *datamigration.Operation
	if migrationJob.Phase == "PROMOTE_IN_PROGRESS" {
		mgr.Logger.Info("migration job is already under promotion, continuing...", "migrationName", migrationName)

		listOp, err := mgr.DatamigrationService.Projects.Locations.Operations.List(gcpProject.GcpComponentURI("migrationJobs", migrationName)).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to list promotion operations: %w", err)
		}
		if len(listOp.Operations) == 0 {
			return fmt.Errorf("failed to find current promotion operation")
		} else if len(listOp.Operations) > 1 {
			return fmt.Errorf("too many promotion operations")
		}
		op = listOp.Operations[0]
	} else if migrationJob.State == "COMPLETED" {
		mgr.Logger.Info("migration job is already completed, continuing...", "migrationName", migrationName)
		op = &datamigration.Operation{
			Done: true,
		}
	} else {
		mgr.Logger.Info("migration job is ready for promotion, continuing...", "migrationName", migrationName)
		err = waitForReplicationLagToReachZero(ctx, target, gcpProject, mgr)
		if err != nil {
			return err
		}

		op, err = mgr.DatamigrationService.Projects.Locations.MigrationJobs.Promote(gcpProject.GcpComponentURI("migrationJobs", migrationName), &datamigration.PromoteMigrationJobRequest{}).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to promote target instance: %w", err)
		}
	}

	for !op.Done {
		time.Sleep(10 * time.Second)
		mgr.Logger.Info("waiting for promote operation to complete")
		op, err = mgr.DatamigrationService.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get promote operation status: %w", err)
		}
	}
	err = instance.UpdateTargetInstanceAfterPromotion(ctx, source, target, mgr)
	if err != nil {
		return err
	}

	return nil
}

func waitForReplicationLagToBeAcceptablyLow(ctx context.Context, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	return waitForReplicationLag(ctx, target, lagAcceptablyLow, gcpProject, mgr)
}

func waitForReplicationLagToReachZero(ctx context.Context, target *resolved.Instance, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	return waitForReplicationLag(ctx, target, lagReachedZero, gcpProject, mgr)
}

func waitForReplicationLag(ctx context.Context, target *resolved.Instance, predicate ReplicationLagPredicate, gcpProject *resolved.GcpProject, mgr *common_main.Manager) error {
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to create metric client: %v", err))
	}
	defer client.Close()

	b := retry.NewConstant(30 * time.Second)
	b = retry.WithMaxDuration(10*time.Minute, b)

	return retry.Do(ctx, b, func(ctx context.Context) error {
		req := makeMetricsRequest(gcpProject, target)

		mgr.Logger.Info("checking replication lag")
		it := client.ListTimeSeries(ctx, req)
		data, err := it.Next()
		if err != nil {
			if !errors.Is(err, iterator.Done) {
				return retry.RetryableError(fmt.Errorf("failed to fetch time series data: %w", err))
			}
			mgr.Logger.Info("no more data in iterator, retrying")
			return retry.RetryableError(fmt.Errorf("no more data in iterator"))
		}

		mgr.Logger.Debug("fetched time series data", "number_of_points", len(data.Points))
		result, err := predicate(data.Points, mgr.Logger)
		if err != nil {
			return retry.RetryableError(fmt.Errorf("failed to evaluate predicate: %w", err))
		}
		if !result {
			return retry.RetryableError(fmt.Errorf("predicate not satisfied, retrying"))
		}
		return nil
	})
}

func lagAcceptablyLow(points []*monpb.Point, logger *slog.Logger) (bool, error) {
	if len(points) == 0 {
		logger.Debug("no data points available to determine lag")
		return false, nil
	}

	point := points[0]

	value, err := getPointValue(point)
	if err != nil {
		logger.Warn("failed to get point value", "error", err)
		return false, err
	}
	logger.Debug("lag", "value", value, "point", formatPoint(point))

	if value > acceptableLagBytesForPromotion {
		logger.Debug("lag is too high for promotion", "lag_bytes", value, "acceptable_lag_bytes", acceptableLagBytesForPromotion)
		return false, nil
	}

	logger.Info("lag is acceptably low for promotion", "lag_bytes", value)
	return true, nil
}

func lagReachedZero(points []*monpb.Point, logger *slog.Logger) (bool, error) {
	if len(points) < numberOfZeroPointsForLagToBeConsideredZero {
		logger.Debug("not enough data points to determine if lag has reached zero", "points_available", len(points), "points_required", numberOfZeroPointsForLagToBeConsideredZero)
		return false, nil
	}

	for _, point := range points[0:numberOfZeroPointsForLagToBeConsideredZero] {
		value, err := getPointValue(point)
		if err != nil {
			logger.Warn("failed to get point value", "error", err)
			return false, err
		}
		logger.Debug("lag", "value", value, "point", formatPoint(point))

		if value != 0 {
			return false, nil
		}
	}

	logger.Info("lag has reached zero")
	return true, nil
}

func getPointValue(point *monpb.Point) (int64, error) {
	value := point.GetValue()
	if value == nil {
		return 0, fmt.Errorf("point has no value")
	}
	intValue, ok := value.GetValue().(*monpb.TypedValue_Int64Value)
	if !ok {
		return 0, fmt.Errorf("point value is not int64")
	}
	return intValue.Int64Value, nil
}

func makeMetricsRequest(gcpProject *resolved.GcpProject, target *resolved.Instance) *monpb.ListTimeSeriesRequest {
	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)
	req := &monpb.ListTimeSeriesRequest{
		Name:   "projects/" + gcpProject.Id,
		Filter: fmt.Sprintf(`metric.type="cloudsql.googleapis.com/database/postgresql/external_sync/max_replica_byte_lag" AND resource.labels.region="%s" AND resource.labels.project_id="%s" AND resource.labels.database_id="%s:%s"`, target.Region, gcpProject.Id, gcpProject.Id, target.Name),
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
	return req
}

func formatPoint(point *monpb.Point) string {
	interval := point.GetInterval()
	if interval == nil {
		return "no interval"
	}
	start := formatTimestamp(interval.GetStartTime())
	end := formatTimestamp(interval.GetEndTime())
	return fmt.Sprintf("start: %s, end: %s", start, end)
}

func formatTimestamp(t *timestamppb.Timestamp) string {
	return t.AsTime().Format(time.RFC3339)
}
