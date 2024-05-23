package promote

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	autoscaling_v1 "k8s.io/client-go/applyconfigurations/autoscaling/v1"
)

func Promote(ctx context.Context, cfg *config.CommonConfig, mgr *common_main.Manager) error {
	migrationName, err := mgr.Resolved.MigrationName()
	if err != nil {
		return err
	}

	// Scale down application
	scaleApplyConfiguration := autoscaling_v1.Scale().
		WithName(cfg.ApplicationName).
		WithNamespace(cfg.Namespace).
		WithSpec(autoscaling_v1.ScaleSpec().WithReplicas(0))
	_, err = mgr.K8sClient.AppsV1().Deployments(cfg.Namespace).ApplyScale(ctx, cfg.ApplicationName, scaleApplyConfiguration, meta_v1.ApplyOptions{})
	if err != nil {
		return err
	}

	migrationJob, err := mgr.DatamigrationService.Projects.Locations.MigrationJobs.Get(migrationName).Context(ctx).Do()
	if err != nil {
		return err
	}

	mgr.Logger.Info("start promoting destination", "job", migrationJob)
	return nil

}
