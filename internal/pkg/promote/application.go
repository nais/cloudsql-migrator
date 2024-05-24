package promote

import (
	"context"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	autoscaling_v1 "k8s.io/client-go/applyconfigurations/autoscaling/v1"
)

func ScaleApplication(ctx context.Context, cfg *config.CommonConfig, mgr *common_main.Manager, replicas int32) error {
	// Scale down application
	scaleApplyConfiguration := autoscaling_v1.Scale().
		WithName(cfg.ApplicationName).
		WithNamespace(cfg.Namespace).
		WithSpec(autoscaling_v1.ScaleSpec().WithReplicas(replicas))
	_, err := mgr.K8sClient.AppsV1().Deployments(cfg.Namespace).ApplyScale(ctx, cfg.ApplicationName, scaleApplyConfiguration, meta_v1.ApplyOptions{})
	if err != nil {
		return err
	}
	return nil
}
