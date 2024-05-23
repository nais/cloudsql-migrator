package promote

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	autoscaling "k8s.io/client-go/applyconfigurations/autoscaling/v1"
	"k8s.io/utils/ptr"
)

func Promote(ctx context.Context, cfg *config.CommonConfig, mgr *common_main.Manager) error {
	migrationName := fmt.Sprintf("%s-%s", mgr.Resolved.Source.Name, mgr.Resolved.Target.Name)

	// Scale down application
	client := mgr.K8sClient
	_, err := client.AppsV1().Deployments(cfg.Namespace).ApplyScale(ctx, cfg.ApplicationName, &autoscaling.ScaleApplyConfiguration{
		Spec: &autoscaling.ScaleSpecApplyConfiguration{Replicas: ptr.To(int32(0))},
	}, v1.ApplyOptions{})
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
